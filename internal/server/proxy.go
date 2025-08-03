package server

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

type BandwidthConfig struct {
	DefaultLocal  int64                     `yaml:"default_local"`
	DefaultGlobal int64                     `yaml:"default_global"`
	Users         map[string]*UserBandwidth `yaml:"users"`
}

type UserBandwidth struct {
	Local  int64 `yaml:"local"`
	Global int64 `yaml:"global,omitempty"` // Optional, defaults to Local if not specified
}

type Config struct {
	Bandwidth *BandwidthConfig         `yaml:"bandwidth"`
	Redis     *RedisCoordinationConfig `yaml:"redis"` // Redis config for global mode
	
	// Legacy fields for backward compatibility
	DefaultBandwidth int64            `yaml:"default_bandwidth,omitempty"`
	Users            map[string]int64 `yaml:"users,omitempty"`
}

// MetricsCollector interface for collecting proxy metrics
type MetricsCollector interface {
	RecordBytesReceived(user string, bytes int64)
	RecordBytesSent(user string, bytes int64)
	RecordMessageReceived(user string)
	RecordMessageSent(user string)
	RecordConnection(user string)
	RecordDisconnection(user string)
	RecordAuthentication(user string)
	RecordAuthFailure(user string)
}

type Proxy struct {
	upstreamHost string
	upstreamPort int
	config       *Config
	rateLimiter  *CombinedRateLimiter // Combined local + global rate limiting
	metrics      MetricsCollector     // Metrics collector
}

type SwapReader struct {
	mu     sync.RWMutex
	reader io.Reader
}

func (s *SwapReader) Read(p []byte) (int, error) {
	s.mu.RLock()
	r := s.reader
	s.mu.RUnlock()
	return r.Read(p)
}

func (s *SwapReader) Swap(r io.Reader) {
	s.mu.Lock()
	s.reader = r
	s.mu.Unlock()
}

func LoadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var cfg Config
	decoder := yaml.NewDecoder(f)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, err
	}
	// Handle backward compatibility and set defaults
	if err := cfg.normalizeBandwidthConfig(); err != nil {
		return nil, fmt.Errorf("invalid bandwidth configuration: %w", err)
	}
	
	// Set default Redis coordination config if not specified
	if cfg.Redis == nil {
		cfg.Redis = &RedisCoordinationConfig{
			Enabled:           false,
			Sentinels:         []string{"redis-sentinel:26379"},
			MasterName:        "mymaster",
			Password:          "",
			Database:          0,
			SyncInterval:      5 * time.Second,
			RebalanceInterval: 30 * time.Second,
			CleanupInterval:   60 * time.Second,
			StartupTimeout:    30 * time.Second,
		}
	}
	
	return &cfg, nil
}

// normalizeBandwidthConfig handles backward compatibility and sets defaults
func (cfg *Config) normalizeBandwidthConfig() error {
	// If using new bandwidth structure, validate and set defaults
	if cfg.Bandwidth != nil {
		// Set default values if not specified
		if cfg.Bandwidth.DefaultLocal == 0 {
			cfg.Bandwidth.DefaultLocal = 10 * 1024 * 1024 // 10MB/s
		}
		if cfg.Bandwidth.DefaultGlobal == 0 {
			cfg.Bandwidth.DefaultGlobal = cfg.Bandwidth.DefaultLocal // Default global = local
		}
		
		// Ensure user global defaults to local if not specified
		for _, userBW := range cfg.Bandwidth.Users {
			if userBW.Global == 0 {
				userBW.Global = userBW.Local
			}
		}
		return nil
	}
	
	// Handle legacy configuration format
	if cfg.DefaultBandwidth == 0 {
		cfg.DefaultBandwidth = 10 * 1024 * 1024 // 10MB/s
	}
	
	// Migrate legacy format to new structure
	cfg.Bandwidth = &BandwidthConfig{
		DefaultLocal:  cfg.DefaultBandwidth,
		DefaultGlobal: cfg.DefaultBandwidth,
		Users:         make(map[string]*UserBandwidth),
	}
	
	// Migrate legacy user settings
	for username, bandwidth := range cfg.Users {
		cfg.Bandwidth.Users[username] = &UserBandwidth{
			Local:  bandwidth,
			Global: bandwidth, // Default global = local for legacy configs
		}
	}
	
	log.Info().Msg("Migrated legacy bandwidth configuration to new format")
	return nil
}

// getMyIP returns the current instance's IP address
func getMyIP() string {
	// Try to get IP from environment first (Kubernetes/Docker sets this)
	if podIP := os.Getenv("POD_IP"); podIP != "" {
		return podIP
	}
	
	// Fall back to detecting local IP
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Error().Err(err).Msg("Failed to detect local IP")
		return "127.0.0.1"
	}
	defer conn.Close()
	
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

func NewProxy(upstreamHost string, upstreamPort int, configPath string, metrics MetricsCollector) (*Proxy, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Always create local rate limiter (for enforcement)
	localRateLimiter := NewLocalRateLimiter(config)

	var globalRateLimiter *GlobalRateLimiter
	
	// Create global rate limiter if Redis is enabled
	if config.Redis.Enabled {
		peerID := GeneratePeerID()
		globalRateLimiter, err = NewGlobalRateLimiter(config, peerID)
		if err != nil {
			return nil, fmt.Errorf("failed to create global rate limiter: %w", err)
		}
		
		log.Info().Str("peer_id", peerID).Msg("Running in global rate limiting mode")
	} else {
		log.Info().Msg("Running in local rate limiting mode")
	}

	// Create combined rate limiter
	rateLimiter := NewCombinedRateLimiter(localRateLimiter, globalRateLimiter)

	return &Proxy{
		upstreamHost: upstreamHost,
		upstreamPort: upstreamPort,
		config:       config,
		rateLimiter:  rateLimiter,
		metrics:      metrics,
	}, nil
}

func (p *Proxy) getBandwidthForUser(user string) int64 {
	if user != "" && p.config.Bandwidth.Users != nil {
		if userBW, ok := p.config.Bandwidth.Users[user]; ok {
			return userBW.Local // Proxy uses local bandwidth for legacy compatibility
		}
	}
	return p.config.Bandwidth.DefaultLocal
}

func (p *Proxy) HandleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	// Record new connection (initially unauthenticated)
	p.metrics.RecordConnection("")

	upstreamConn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", p.upstreamHost, p.upstreamPort))
	if err != nil {
		log.Error().Err(err).Msg("Failed to connect to upstream")
		p.metrics.RecordDisconnection("")
		return
	}
	defer upstreamConn.Close()

	// Create parser for tracking connection state (client -> upstream)
	parser := NewClientMessageParser(
		clientConn,
		upstreamConn,
		p.rateLimiter,
		p.metrics,
	)

	// Client -> Upstream (with parsing and rate limiting)
	go func() {
		defer func() {
			parser.Disconnect()
			// Record disconnection when parser finishes
			user := parser.GetAuthenticatedUser()
			p.metrics.RecordDisconnection(user)
		}()
		parser.ParseAndForward()
	}()

	// Upstream -> Client (with metrics but no rate limiting on responses)
	p.copyWithMetrics(clientConn, upstreamConn, parser)
}

// copyWithMetrics copies data from src to dst while recording bytes_sent metrics
func (p *Proxy) copyWithMetrics(dst, src net.Conn, parser *ClientMessageParser) {
	buffer := make([]byte, 32*1024) // 32KB buffer
	for {
		n, err := src.Read(buffer)
		if n > 0 {
			// Record bytes sent TO client (proxy -> client)
			user := parser.GetAuthenticatedUser()
			p.metrics.RecordBytesSent(user, int64(n))
			
			_, writeErr := dst.Write(buffer[:n])
			if writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (p *Proxy) Start(port int) error {
	// Start rate limiters
	if err := p.rateLimiter.Start(); err != nil {
		return fmt.Errorf("failed to start rate limiters: %w", err)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", port, err)
	}
	log.Info().Int("port", port).Msg("NATS proxy listening")

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Error().Err(err).Msg("Accept error")
			continue
		}
		go p.HandleConnection(conn)
	}
}

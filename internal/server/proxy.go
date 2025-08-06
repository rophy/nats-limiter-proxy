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

// New simplified config structure
type LimitsConfig struct {
	Defaults *UserLimits  `yaml:"defaults"`
	Users    []*UserLimit `yaml:"users,omitempty"`
}

type UserLimits struct {
	BPSLocal  int64 `yaml:"bps_local"`
	BPSGlobal int64 `yaml:"bps_global"`
}

type UserLimit struct {
	User      string `yaml:"user"`
	BPSLocal  int64  `yaml:"bps_local"`
	BPSGlobal int64  `yaml:"bps_global"`
}

// Legacy bandwidth config for backward compatibility
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
	Limits    *LimitsConfig            `yaml:"limits"`    // New config format
	Bandwidth *BandwidthConfig         `yaml:"bandwidth"` // Legacy config for backward compatibility
	Redis     *RedisCoordinationConfig `yaml:"redis"`     // Redis config for global mode
	
	// Legacy fields for backward compatibility
	DefaultBandwidth int64            `yaml:"default_bandwidth,omitempty"`
	Users            map[string]int64 `yaml:"users,omitempty"`
}

// GetUserLimits returns the bandwidth limits for a given user
func (cfg *LimitsConfig) GetUserLimits(username string) *UserLimits {
	// Check specific users first
	for _, user := range cfg.Users {
		if user.User == username {
			return &UserLimits{
				BPSLocal:  user.BPSLocal,
				BPSGlobal: user.BPSGlobal,
			}
		}
	}
	
	// Return defaults if user not found
	return cfg.Defaults
}

// NormalizeLimits ensures the config has a valid Limits section by migrating from legacy formats
func (cfg *Config) NormalizeLimits() {
	// If new format already exists, use it
	if cfg.Limits != nil {
		return
	}
	
	// Create new format from legacy config
	cfg.Limits = &LimitsConfig{
		Users: []*UserLimit{},
	}
	
	// Set defaults from legacy config
	if cfg.Bandwidth != nil {
		cfg.Limits.Defaults = &UserLimits{
			BPSLocal:  cfg.Bandwidth.DefaultLocal,
			BPSGlobal: cfg.Bandwidth.DefaultGlobal,
		}
		
		// Convert legacy users
		for username, userBW := range cfg.Bandwidth.Users {
			globalBW := userBW.Global
			if globalBW == 0 {
				globalBW = userBW.Local // Default global to local if not specified
			}
			
			cfg.Limits.Users = append(cfg.Limits.Users, &UserLimit{
				User:      username,
				BPSLocal:  userBW.Local,
				BPSGlobal: globalBW,
			})
		}
	} else {
		// Legacy format with default_bandwidth and users map
		defaultBW := cfg.DefaultBandwidth
		if defaultBW == 0 {
			defaultBW = 102400 // 100KB/s default
		}
		
		cfg.Limits.Defaults = &UserLimits{
			BPSLocal:  defaultBW,
			BPSGlobal: defaultBW,
		}
		
		// Convert legacy users map
		for username, bandwidth := range cfg.Users {
			cfg.Limits.Users = append(cfg.Limits.Users, &UserLimit{
				User:      username,
				BPSLocal:  bandwidth,
				BPSGlobal: bandwidth,
			})
		}
	}
	
	// Ensure defaults exist
	if cfg.Limits.Defaults == nil {
		cfg.Limits.Defaults = &UserLimits{
			BPSLocal:  102400, // 100KB/s
			BPSGlobal: 102400, // 100KB/s
		}
	}
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
	// Normalize config by migrating all formats to new Limits structure
	cfg.NormalizeLimits()
	
	// Validate limits configuration and set reasonable defaults
	if cfg.Limits != nil && cfg.Limits.Defaults != nil {
		// Set reasonable defaults if not specified
		if cfg.Limits.Defaults.BPSLocal == 0 {
			cfg.Limits.Defaults.BPSLocal = 10 * 1024 * 1024 // 10MB/s
		}
		if cfg.Limits.Defaults.BPSGlobal == 0 {
			cfg.Limits.Defaults.BPSGlobal = cfg.Limits.Defaults.BPSLocal // Default global = local
		}
		
		// Ensure user limits are reasonable
		for _, userLimit := range cfg.Limits.Users {
			if userLimit.BPSLocal == 0 {
				userLimit.BPSLocal = cfg.Limits.Defaults.BPSLocal
			}
			if userLimit.BPSGlobal == 0 {
				userLimit.BPSGlobal = userLimit.BPSLocal // Default global = local
			}
		}
	}
	
	log.Info().Msg("Configuration normalized to limits format")
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

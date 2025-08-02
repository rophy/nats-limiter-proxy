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

type Config struct {
	DefaultBandwidth int64                     `yaml:"default_bandwidth"`
	Users            map[string]int64          `yaml:"users"`
	Coordination     *CoordinationConfig       `yaml:"coordination"`        // Legacy gossip config (deprecated)
	Redis            *RedisCoordinationConfig  `yaml:"redis"`               // New Redis coordination config
}

type Proxy struct {
	upstreamHost     string
	upstreamPort     int
	config           *Config
	rateLimiterMgr   RateLimiterManagerInterface
	peerCoordinator  *PeerCoordinator       // Legacy gossip coordinator
	redisCoordinator *RedisCoordinator      // New Redis coordinator
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
	if cfg.DefaultBandwidth == 0 {
		cfg.DefaultBandwidth = 10 * 1024 * 1024 // 10MB/s
	}
	
	// Set default coordination config if not specified
	if cfg.Coordination == nil {
		cfg.Coordination = &CoordinationConfig{
			Enabled:           false,
			Port:              8081,
			GossipInterval:    5 * time.Second,
			RebalanceInterval: 30 * time.Second,
			CleanupInterval:   60 * time.Second,
			DiscoveryMethod:   "kubernetes",
		}
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

func NewProxy(upstreamHost string, upstreamPort int, configPath string) (*Proxy, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	var rateLimiterMgr RateLimiterManagerInterface
	var redisCoordinator *RedisCoordinator
	
	// Redis coordination takes precedence over legacy gossip
	if config.Redis.Enabled {
		peerID := GeneratePeerID()
		redisCoordinator = NewRedisCoordinator(config.Redis, peerID)
		redisCoordinator.SetConfig(config)
		
		// Use distributed rate limiter manager with Redis coordinator
		distributedRLM := NewDistributedRateLimiterManagerWithRedis(config, redisCoordinator)
		distributedRLM.Start()
		rateLimiterMgr = distributedRLM
	} else {
		// Use simple rate limiter manager (no coordination)
		rateLimiterMgr = NewRateLimiterManager(config)
		log.Info().Msg("Running in standalone mode - no distributed coordination")
	}

	return &Proxy{
		upstreamHost:     upstreamHost,
		upstreamPort:     upstreamPort,
		config:           config,
		rateLimiterMgr:   rateLimiterMgr,
		redisCoordinator: redisCoordinator,
	}, nil
}

func (p *Proxy) getBandwidthForUser(user string) int64 {
	if user != "" && p.config.Users != nil {
		if bw, ok := p.config.Users[user]; ok {
			return bw
		}
	}
	return p.config.DefaultBandwidth
}

func (p *Proxy) HandleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	upstreamConn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", p.upstreamHost, p.upstreamPort))
	if err != nil {
		log.Error().Err(err).Msg("Failed to connect to upstream")
		return
	}
	defer upstreamConn.Close()

	// Client -> Upstream
	go func() {
		parser := NewClientMessageParser(
			clientConn,
			upstreamConn,
			p.rateLimiterMgr,
		)
		parser.ParseAndForward()
	}()

	io.Copy(clientConn, upstreamConn)
}

func (p *Proxy) Start(port int) error {
	// Start Redis coordination if enabled
	if p.redisCoordinator != nil {
		if err := p.redisCoordinator.Start(); err != nil {
			return fmt.Errorf("failed to start Redis coordination: %w", err)
		}
		log.Info().Msg("Redis coordination started")
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

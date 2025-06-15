package server

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

type Config struct {
	DefaultBandwidth int64            `yaml:"default_bandwidth"`
	Users            map[string]int64 `yaml:"users"`
}

type Proxy struct {
	upstreamHost   string
	upstreamPort   int
	config         *Config
	rateLimiterMgr *RateLimiterManager
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
	return &cfg, nil
}

func NewProxy(upstreamHost string, upstreamPort int, configPath string) (*Proxy, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return &Proxy{
		upstreamHost:   upstreamHost,
		upstreamPort:   upstreamPort,
		config:         config,
		rateLimiterMgr: NewRateLimiterManager(config),
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
		parser := ClientMessageParser{
			RateLimiterManager: p.rateLimiterMgr,
			OnUserAuthenticated: func(user string) {
				log.Info().Str("user", user).Msg("User authenticated")
			},
		}
		parser.ParseAndForward(clientConn, upstreamConn)
	}()

	io.Copy(clientConn, upstreamConn)
}

func (p *Proxy) Start(port int) error {
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

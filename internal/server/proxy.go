package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/golang-jwt/jwt/v5"
	"github.com/juju/ratelimit"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

type Config struct {
	DefaultBandwidth int64            `yaml:"default_bandwidth"`
	Users            map[string]int64 `yaml:"users"`
}

type Proxy struct {
	upstreamHost string
	upstreamPort int
	config       *Config
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
		upstreamHost: upstreamHost,
		upstreamPort: upstreamPort,
		config:       config,
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

func (p *Proxy) extractUsernameFromJWT(jwtToken string) string {
	// Parse JWT without verification since we just need to extract claims
	token, _ := jwt.ParseWithClaims(jwtToken, jwt.MapClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Return nil to skip signature verification - we just need the claims
		return nil, nil
	})

	// Even with signature verification errors, we can still extract claims
	if token != nil {
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			if name, exists := claims["name"]; exists {
				if nameStr, ok := name.(string); ok {
					return nameStr
				}
			}
			if sub, exists := claims["sub"]; exists {
				if subStr, ok := sub.(string); ok {
					return subStr
				}
			}
		}
	}

	return ""
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
		// Step 1: Read until CONNECT is parsed
		buffer := &bytes.Buffer{}
		reader := bufio.NewReader(io.TeeReader(clientConn, buffer))
		var user string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(strings.TrimSpace(line), "CONNECT ") {
				var obj map[string]interface{}
				jsonStr := strings.TrimSpace(line)[8:]
				if err := json.Unmarshal([]byte(jsonStr), &obj); err == nil {
					// Check for traditional username/password authentication
					if u, ok := obj["user"].(string); ok {
						user = u
						log.Info().Str("user", u).Str("auth_type", "password").Msg("User authenticated")
						break
					}
					// Check for JWT authentication
					if jwtToken, ok := obj["jwt"].(string); ok {
						user = p.extractUsernameFromJWT(jwtToken)
						if user != "" {
							log.Info().Str("user", user).Str("auth_type", "jwt").Msg("User authenticated")
							break
						}
					}
				}
			}
			// Stop after CONNECT, or keep reading if you want to support INFO before CONNECT
		}

		// Step 2: Use the correct limiter for this user
		limiter := ratelimit.NewBucketWithRate(float64(p.getBandwidthForUser(user)), p.getBandwidthForUser(user))
		limitedReader := ratelimit.Reader(io.MultiReader(buffer, clientConn), limiter)

		parser := NATSProxyParser{
			LogFunc: func(direction, line, contextUser string) {
				if contextUser != "" {
					log.Debug().Str("direction", direction).Str("user", contextUser).Msg("Protocol data")
				} else {
					log.Debug().Str("direction", direction).Msg("Protocol data")
				}
			},
		}
		parser.ParseAndForward(limitedReader, upstreamConn, "C->S")
	}()

	// Upstream -> Client (use default bandwidth)
	parser := NATSProxyParser{
		LogFunc: func(direction, line, contextUser string) {
			log.Debug().Str("direction", direction).Msg("Protocol data")
		},
	}
	limitedUpstreamReader := ratelimit.Reader(upstreamConn, ratelimit.NewBucketWithRate(
		float64(p.config.DefaultBandwidth),
		p.config.DefaultBandwidth,
	))
	parser.ParseAndForward(limitedUpstreamReader, clientConn, "S->C")
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

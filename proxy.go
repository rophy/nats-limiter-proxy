package main

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

	"nats-limiter-proxy/server"

	"github.com/golang-jwt/jwt/v5"
	"github.com/juju/ratelimit"
	"gopkg.in/yaml.v3"
)

const (
	localPort = 4223
)

var (
	upstreamHost string
	upstreamPort int
	config       *Config
)

type Config struct {
	DefaultBandwidth int64            `yaml:"default_bandwidth"`
	Users            map[string]int64 `yaml:"users"`
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

func init() {
	upstreamHost = os.Getenv("UPSTREAM_HOST")
	if upstreamHost == "" {
		fmt.Fprintln(os.Stderr, "Environment variable UPSTREAM_HOST is required")
		os.Exit(1)
	}
	portStr := os.Getenv("UPSTREAM_PORT")
	if portStr == "" {
		fmt.Fprintln(os.Stderr, "Environment variable UPSTREAM_PORT is required")
		os.Exit(1)
	}
	_, err := fmt.Sscanf(portStr, "%d", &upstreamPort)
	if err != nil || upstreamPort <= 0 || upstreamPort > 65535 {
		fmt.Fprintln(os.Stderr, "Invalid UPSTREAM_PORT value")
		os.Exit(1)
	}
	// Load config.yaml
	cfg, err := LoadConfig("config.yaml")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to load config.yaml:", err)
		os.Exit(1)
	}
	config = cfg
}

func getBandwidthForUser(user string) int64 {
	if user != "" && config.Users != nil {
		if bw, ok := config.Users[user]; ok {
			return bw
		}
	}
	return config.DefaultBandwidth
}

func extractUsernameFromJWT(jwtToken string) string {
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

func handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	upstreamConn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", upstreamHost, upstreamPort))
	if err != nil {
		fmt.Println("Failed to connect to upstream:", err)
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
						fmt.Printf("Authenticated user (password): %s\n", u)
						break
					}
					// Check for JWT authentication
					if jwtToken, ok := obj["jwt"].(string); ok {
						user = extractUsernameFromJWT(jwtToken)
						if user != "" {
							fmt.Printf("Authenticated user (JWT): %s\n", user)
							break
						}
					}
				}
			}
			// Stop after CONNECT, or keep reading if you want to support INFO before CONNECT
		}

		// Step 2: Use the correct limiter for this user
		limiter := ratelimit.NewBucketWithRate(float64(getBandwidthForUser(user)), getBandwidthForUser(user))
		limitedReader := ratelimit.Reader(io.MultiReader(buffer, clientConn), limiter)

		parser := server.NATSProxyParser{
			LogFunc: func(direction, line string) {
				fmt.Printf("C->S: %s", line)
			},
		}
		parser.ParseAndForward(limitedReader, upstreamConn, "C->S")
	}()

	// Upstream -> Client (use default bandwidth)
	parser := server.NATSProxyParser{
		LogFunc: func(direction, line string) {
			fmt.Printf("S->C: %s", line)
		},
	}
	limitedUpstreamReader := ratelimit.Reader(upstreamConn, ratelimit.NewBucketWithRate(
		float64(config.DefaultBandwidth),
		config.DefaultBandwidth,
	))
	parser.ParseAndForward(limitedUpstreamReader, clientConn, "S->C")
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

func main() {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", localPort))
	if err != nil {
		fmt.Println("Failed to listen:", err)
		return
	}
	fmt.Printf("NATS proxy (TCP) listening on port %d\n", localPort)
	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Accept error:", err)
			continue
		}
		go handleConnection(conn)
	}
}

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"nats-limiter-proxy/server"

	"github.com/fsnotify/fsnotify"
	"github.com/golang-jwt/jwt/v5"
	"github.com/juju/ratelimit"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

const (
	localPort = 4223
	metricsPort = 8080
	healthPort = 8081
	configFile = "config.yaml"
)

// ProductionProxy holds all the production-ready proxy components
type ProductionProxy struct {
	config       *Config
	configMutex  sync.RWMutex
	logger       *logrus.Logger
	metrics      *ProxyMetrics
	watcher      *fsnotify.Watcher
	upstreamHost string
	upstreamPort int
	activeConns  sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
}

// Config represents the proxy configuration
type Config struct {
	DefaultBandwidth int64            `yaml:"default_bandwidth"`
	Users            map[string]int64 `yaml:"users"`
	LogLevel         string           `yaml:"log_level,omitempty"`
	MetricsEnabled   bool             `yaml:"metrics_enabled,omitempty"`
}

// ProxyMetrics holds all Prometheus metrics
type ProxyMetrics struct {
	ConnectionsTotal       prometheus.Counter
	ActiveConnections      prometheus.Gauge
	BytesTransferred       *prometheus.CounterVec
	RateLimitingEvents     *prometheus.CounterVec
	AuthenticationAttempts *prometheus.CounterVec
	ConfigReloads          prometheus.Counter
	ErrorsTotal            *prometheus.CounterVec
}

// NewProxyMetrics creates and registers all metrics
func NewProxyMetrics() *ProxyMetrics {
	metrics := &ProxyMetrics{
		ConnectionsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nats_proxy_connections_total",
			Help: "Total number of connections handled",
		}),
		ActiveConnections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "nats_proxy_active_connections",
			Help: "Number of currently active connections",
		}),
		BytesTransferred: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nats_proxy_bytes_transferred_total",
			Help: "Total bytes transferred through the proxy",
		}, []string{"user", "direction"}),
		RateLimitingEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nats_proxy_rate_limiting_events_total",
			Help: "Total rate limiting events",
		}, []string{"user", "event_type"}),
		AuthenticationAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nats_proxy_authentication_attempts_total",
			Help: "Total authentication attempts",
		}, []string{"user", "method", "result"}),
		ConfigReloads: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nats_proxy_config_reloads_total",
			Help: "Total number of configuration reloads",
		}),
		ErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nats_proxy_errors_total",
			Help: "Total number of errors",
		}, []string{"type"}),
	}

	// Register all metrics
	prometheus.MustRegister(
		metrics.ConnectionsTotal,
		metrics.ActiveConnections,
		metrics.BytesTransferred,
		metrics.RateLimitingEvents,
		metrics.AuthenticationAttempts,
		metrics.ConfigReloads,
		metrics.ErrorsTotal,
	)

	return metrics
}

// NewProductionProxy creates a new production-ready proxy instance
func NewProductionProxy() (*ProductionProxy, error) {
	ctx, cancel := context.WithCancel(context.Background())
	
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})
	
	// Load initial configuration
	config, err := LoadConfig(configFile)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	
	// Set log level from config
	if config.LogLevel != "" {
		level, err := logrus.ParseLevel(config.LogLevel)
		if err == nil {
			logger.SetLevel(level)
		}
	}

	// Initialize metrics if enabled
	var metrics *ProxyMetrics
	if config.MetricsEnabled {
		metrics = NewProxyMetrics()
	}

	// Get upstream configuration from environment
	upstreamHost := os.Getenv("UPSTREAM_HOST")
	if upstreamHost == "" {
		cancel()
		return nil, fmt.Errorf("environment variable UPSTREAM_HOST is required")
	}
	
	upstreamPortStr := os.Getenv("UPSTREAM_PORT")
	if upstreamPortStr == "" {
		cancel()
		return nil, fmt.Errorf("environment variable UPSTREAM_PORT is required")
	}
	
	var upstreamPort int
	if _, err := fmt.Sscanf(upstreamPortStr, "%d", &upstreamPort); err != nil || upstreamPort <= 0 || upstreamPort > 65535 {
		cancel()
		return nil, fmt.Errorf("invalid UPSTREAM_PORT value: %s", upstreamPortStr)
	}

	// Setup file watcher for configuration hot-reload
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	proxy := &ProductionProxy{
		config:       config,
		logger:       logger,
		metrics:      metrics,
		watcher:      watcher,
		upstreamHost: upstreamHost,
		upstreamPort: upstreamPort,
		ctx:          ctx,
		cancel:       cancel,
	}

	// Start watching configuration file
	if err := proxy.startConfigWatcher(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start config watcher: %w", err)
	}

	return proxy, nil
}

// LoadConfig loads configuration from a YAML file with proper error handling
func LoadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file %s: %w", path, err)
	}
	defer f.Close()

	var cfg Config
	decoder := yaml.NewDecoder(f)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", path, err)
	}

	// Set defaults
	if cfg.DefaultBandwidth == 0 {
		cfg.DefaultBandwidth = 10 * 1024 * 1024 // 10MB/s
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	cfg.MetricsEnabled = true // Enable by default in production

	return &cfg, nil
}

// startConfigWatcher sets up file system watching for configuration changes
func (p *ProductionProxy) startConfigWatcher() error {
	configPath, err := filepath.Abs(configFile)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for config: %w", err)
	}

	if err := p.watcher.Add(filepath.Dir(configPath)); err != nil {
		return fmt.Errorf("failed to watch config directory: %w", err)
	}

	go p.configWatcherLoop()
	return nil
}

// configWatcherLoop handles configuration file change events
func (p *ProductionProxy) configWatcherLoop() {
	for {
		select {
		case event, ok := <-p.watcher.Events:
			if !ok {
				return
			}
			
			// Check if the config file was modified
			if event.Name == configFile && (event.Op&fsnotify.Write == fsnotify.Write) {
				p.logger.Info("Configuration file changed, reloading...")
				if err := p.reloadConfig(); err != nil {
					p.logger.WithError(err).Error("Failed to reload configuration")
					if p.metrics != nil {
						p.metrics.ErrorsTotal.WithLabelValues("config_reload").Inc()
					}
				} else {
					p.logger.Info("Configuration reloaded successfully")
					if p.metrics != nil {
						p.metrics.ConfigReloads.Inc()
					}
				}
			}

		case err, ok := <-p.watcher.Errors:
			if !ok {
				return
			}
			p.logger.WithError(err).Error("File watcher error")

		case <-p.ctx.Done():
			return
		}
	}
}

// reloadConfig safely reloads the configuration
func (p *ProductionProxy) reloadConfig() error {
	newConfig, err := LoadConfig(configFile)
	if err != nil {
		return err
	}

	p.configMutex.Lock()
	defer p.configMutex.Unlock()

	// Update log level if changed
	if newConfig.LogLevel != p.config.LogLevel {
		if level, err := logrus.ParseLevel(newConfig.LogLevel); err == nil {
			p.logger.SetLevel(level)
			p.logger.WithField("level", newConfig.LogLevel).Info("Log level updated")
		}
	}

	p.config = newConfig
	return nil
}

// getBandwidthForUser safely gets bandwidth limit for a user
func (p *ProductionProxy) getBandwidthForUser(user string) int64 {
	p.configMutex.RLock()
	defer p.configMutex.RUnlock()

	if user != "" && p.config.Users != nil {
		if bw, ok := p.config.Users[user]; ok {
			return bw
		}
	}
	return p.config.DefaultBandwidth
}

// extractUsernameFromJWT extracts username from JWT token
func (p *ProductionProxy) extractUsernameFromJWT(jwtToken string) string {
	token, _ := jwt.ParseWithClaims(jwtToken, jwt.MapClaims{}, func(token *jwt.Token) (interface{}, error) {
		return nil, nil
	})

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

// handleConnection processes a client connection with full production features
func (p *ProductionProxy) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()
	p.activeConns.Add(1)
	defer p.activeConns.Done()

	if p.metrics != nil {
		p.metrics.ConnectionsTotal.Inc()
		p.metrics.ActiveConnections.Inc()
		defer p.metrics.ActiveConnections.Dec()
	}

	clientAddr := clientConn.RemoteAddr().String()
	logger := p.logger.WithField("client", clientAddr)
	logger.Info("New client connection")

	// Connect to upstream NATS server
	upstreamConn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", p.upstreamHost, p.upstreamPort))
	if err != nil {
		logger.WithError(err).Error("Failed to connect to upstream NATS server")
		if p.metrics != nil {
			p.metrics.ErrorsTotal.WithLabelValues("upstream_connection").Inc()
		}
		return
	}
	defer upstreamConn.Close()

	logger.Info("Connected to upstream NATS server")

	// Client -> Upstream (with authentication parsing and rate limiting)
	go func() {
		defer upstreamConn.Close()
		
		// Parse CONNECT message for authentication
		buffer := &bytes.Buffer{}
		reader := bufio.NewReader(io.TeeReader(clientConn, buffer))
		var user string
		var authMethod string

		// Read until CONNECT message is found
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				logger.WithError(err).Error("Error reading from client")
				return
			}

			if strings.HasPrefix(strings.TrimSpace(line), "CONNECT ") {
				var obj map[string]interface{}
				jsonStr := strings.TrimSpace(line)[8:]
				
				if err := json.Unmarshal([]byte(jsonStr), &obj); err == nil {
					// Check for traditional username/password authentication first
					if u, ok := obj["user"].(string); ok && u != "" {
						user = u
						authMethod = "password"
						logger.WithFields(logrus.Fields{
							"user": user,
							"method": authMethod,
						}).Info("User authenticated")
						
						if p.metrics != nil {
							p.metrics.AuthenticationAttempts.WithLabelValues(user, authMethod, "success").Inc()
						}
						break
					}
					// Check for JWT authentication if no username provided
					if jwtToken, ok := obj["jwt"].(string); ok && jwtToken != "" {
						user = p.extractUsernameFromJWT(jwtToken)
						if user != "" {
							authMethod = "jwt"
							logger.WithFields(logrus.Fields{
								"user": user,
								"method": authMethod,
							}).Info("User authenticated")
							
							if p.metrics != nil {
								p.metrics.AuthenticationAttempts.WithLabelValues(user, authMethod, "success").Inc()
							}
							break
						}
					}
				}
				
				// If we reach here, authentication failed
				logger.Warn("Authentication failed - no valid credentials found")
				if p.metrics != nil {
					p.metrics.AuthenticationAttempts.WithLabelValues("unknown", "unknown", "failed").Inc()
				}
			}
		}

		// Create rate limiter for this user
		bandwidthLimit := p.getBandwidthForUser(user)
		// Use smaller bucket capacity to prevent excessive bursting
		// Allow burst of ~100ms worth of data (bandwidthLimit/10)
		burstCapacity := bandwidthLimit / 10
		if burstCapacity < 1024 {
			burstCapacity = 1024 // Minimum 1KB burst
		}
		limiter := ratelimit.NewBucketWithRate(float64(bandwidthLimit), burstCapacity)
		limitedReader := ratelimit.Reader(io.MultiReader(buffer, clientConn), limiter)

		logger.WithFields(logrus.Fields{
			"user": user,
			"bandwidth_limit": bandwidthLimit,
			"burst_capacity": burstCapacity,
		}).Info("Rate limiter configured")

		if p.metrics != nil {
			p.metrics.RateLimitingEvents.WithLabelValues(user, "limiter_created").Inc()
		}

		// Create parser for protocol-aware forwarding
		parser := server.NATSProxyParser{
			LogFunc: func(direction, line string) {
				logger.WithField("direction", direction).Debug(line)
			},
		}

		// Forward data with metrics tracking
		bytesRead, err := parser.ParseAndForward(limitedReader, upstreamConn, "C->S")
		if err != nil && err != io.EOF {
			logger.WithError(err).Error("Error forwarding client data to upstream")
			if p.metrics != nil {
				p.metrics.ErrorsTotal.WithLabelValues("client_to_upstream").Inc()
			}
		}

		if p.metrics != nil {
			p.metrics.BytesTransferred.WithLabelValues(user, "upstream").Add(float64(bytesRead))
		}

		logger.WithFields(logrus.Fields{
			"user": user,
			"bytes_transferred": bytesRead,
		}).Info("Client to upstream transfer completed")
	}()

	// Upstream -> Client
	go func() {
		defer clientConn.Close()
		
		parser := server.NATSProxyParser{
			LogFunc: func(direction, line string) {
				logger.WithField("direction", direction).Debug(line)
			},
		}

		bytesRead, err := parser.ParseAndForward(upstreamConn, clientConn, "S->C")
		if err != nil && err != io.EOF {
			logger.WithError(err).Error("Error forwarding upstream data to client")
			if p.metrics != nil {
				p.metrics.ErrorsTotal.WithLabelValues("upstream_to_client").Inc()
			}
		}

		if p.metrics != nil {
			p.metrics.BytesTransferred.WithLabelValues("unknown", "client").Add(float64(bytesRead))
		}

		logger.WithField("bytes_transferred", bytesRead).Info("Upstream to client transfer completed")
	}()

	// Wait for connection to close
	select {
	case <-p.ctx.Done():
		return
	}
}

// startMetricsServer starts the Prometheus metrics HTTP server
func (p *ProductionProxy) startMetricsServer() error {
	if p.metrics == nil {
		return nil // Metrics disabled
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", metricsPort),
		Handler: mux,
	}

	go func() {
		p.logger.WithField("port", metricsPort).Info("Starting metrics server")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			p.logger.WithError(err).Error("Metrics server error")
		}
	}()

	// Graceful shutdown
	go func() {
		<-p.ctx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	return nil
}

// startHealthServer starts the health check HTTP server
func (p *ProductionProxy) startHealthServer() error {
	mux := http.NewServeMux()
	
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "healthy",
			"timestamp": time.Now().Format(time.RFC3339),
		})
	})

	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		// Check if we can connect to upstream
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", p.upstreamHost, p.upstreamPort), 5*time.Second)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{
				"status": "not ready",
				"reason": "cannot connect to upstream NATS server",
			})
			return
		}
		conn.Close()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ready",
		})
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", healthPort),
		Handler: mux,
	}

	go func() {
		p.logger.WithField("port", healthPort).Info("Starting health check server")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			p.logger.WithError(err).Error("Health server error")
		}
	}()

	// Graceful shutdown
	go func() {
		<-p.ctx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	return nil
}

// Run starts the production proxy with all features
func (p *ProductionProxy) Run() error {
	// Start auxiliary servers
	if err := p.startMetricsServer(); err != nil {
		return fmt.Errorf("failed to start metrics server: %w", err)
	}

	if err := p.startHealthServer(); err != nil {
		return fmt.Errorf("failed to start health server: %w", err)
	}

	// Start main proxy server
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", localPort))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", localPort, err)
	}
	defer listener.Close()

	p.logger.WithField("port", localPort).Info("NATS proxy server started")

	// Setup graceful shutdown
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-signalChan
		p.logger.Info("Shutdown signal received, gracefully shutting down...")
		p.cancel()
		listener.Close()
	}()

	// Accept connections
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-p.ctx.Done():
				p.logger.Info("Server shutting down...")
				p.activeConns.Wait() // Wait for all connections to finish
				return nil
			default:
				p.logger.WithError(err).Error("Failed to accept connection")
				if p.metrics != nil {
					p.metrics.ErrorsTotal.WithLabelValues("accept_connection").Inc()
				}
				continue
			}
		}

		go p.handleConnection(conn)
	}
}

// Cleanup performs cleanup operations
func (p *ProductionProxy) Cleanup() {
	if p.watcher != nil {
		p.watcher.Close()
	}
	p.cancel()
}

func main() {
	proxy, err := NewProductionProxy()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create proxy: %v\n", err)
		os.Exit(1)
	}
	defer proxy.Cleanup()

	if err := proxy.Run(); err != nil {
		proxy.logger.WithError(err).Fatal("Proxy server failed")
	}
}
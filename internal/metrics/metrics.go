package metrics

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
)

const (
	// User label for unauthenticated connections
	UnauthenticatedUser = "unauthenticated"
)

// Metrics holds all Prometheus metrics for the NATS limiter proxy
type Metrics struct {
	// Per-user message and byte counters
	BytesReceived    *prometheus.CounterVec
	BytesSent        *prometheus.CounterVec
	MessagesReceived *prometheus.CounterVec
	MessagesSent     *prometheus.CounterVec

	// Connection metrics
	ActiveConnections     prometheus.Gauge
	ConnectionsTotal      *prometheus.CounterVec
	DisconnectionsTotal   *prometheus.CounterVec
	AuthenticationsTotal  *prometheus.CounterVec
	AuthFailuresTotal     *prometheus.CounterVec

	registry *prometheus.Registry
	server   *http.Server
	mutex    sync.RWMutex
}

// New creates a new Metrics instance with all required collectors
func New() *Metrics {
	registry := prometheus.NewRegistry()

	m := &Metrics{
		BytesReceived: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nats_proxy_bytes_received_total",
				Help: "Total bytes received from clients, by user",
			},
			[]string{"user"},
		),
		BytesSent: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nats_proxy_bytes_sent_total",
				Help: "Total bytes sent to clients, by user",
			},
			[]string{"user"},
		),
		MessagesReceived: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nats_proxy_messages_received_total",
				Help: "Total messages received from clients, by user",
			},
			[]string{"user"},
		),
		MessagesSent: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nats_proxy_messages_sent_total",
				Help: "Total messages sent to clients, by user",
			},
			[]string{"user"},
		),
		ActiveConnections: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "nats_proxy_active_connections",
				Help: "Current number of active connections",
			},
		),
		ConnectionsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nats_proxy_connections_total",
				Help: "Total number of connections established, by user",
			},
			[]string{"user"},
		),
		DisconnectionsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nats_proxy_disconnections_total",
				Help: "Total number of disconnections, by user",
			},
			[]string{"user"},
		),
		AuthenticationsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nats_proxy_authentications_total",
				Help: "Total number of successful authentications, by user",
			},
			[]string{"user"},
		),
		AuthFailuresTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nats_proxy_auth_failures_total",
				Help: "Total number of authentication failures, by user",
			},
			[]string{"user"},
		),
		registry: registry,
	}

	// Register all metrics with the registry
	registry.MustRegister(
		m.BytesReceived,
		m.BytesSent,
		m.MessagesReceived,
		m.MessagesSent,
		m.ActiveConnections,
		m.ConnectionsTotal,
		m.DisconnectionsTotal,
		m.AuthenticationsTotal,
		m.AuthFailuresTotal,
	)

	return m
}

// StartServer starts the HTTP metrics server on the specified port
func (m *Metrics) StartServer(port string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.server != nil {
		return nil // Already started
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))

	m.server = &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	go func() {
		log.Info().Str("port", port).Msg("Starting metrics server")
		if err := m.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("Metrics server failed")
		}
	}()

	return nil
}

// Stop gracefully shuts down the metrics server
func (m *Metrics) Stop() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.server == nil {
		return nil
	}

	err := m.server.Close()
	m.server = nil
	return err
}

// RecordBytesReceived increments the bytes received counter for a user
func (m *Metrics) RecordBytesReceived(user string, bytes int64) {
	if user == "" {
		user = UnauthenticatedUser
	}
	m.BytesReceived.WithLabelValues(user).Add(float64(bytes))
}

// RecordBytesSent increments the bytes sent counter for a user
func (m *Metrics) RecordBytesSent(user string, bytes int64) {
	if user == "" {
		user = UnauthenticatedUser
	}
	m.BytesSent.WithLabelValues(user).Add(float64(bytes))
}

// RecordMessageReceived increments the messages received counter for a user
func (m *Metrics) RecordMessageReceived(user string) {
	if user == "" {
		user = UnauthenticatedUser
	}
	m.MessagesReceived.WithLabelValues(user).Inc()
}

// RecordMessageSent increments the messages sent counter for a user
func (m *Metrics) RecordMessageSent(user string) {
	if user == "" {
		user = UnauthenticatedUser
	}
	m.MessagesSent.WithLabelValues(user).Inc()
}

// RecordConnection increments the connection counter and active connections gauge
func (m *Metrics) RecordConnection(user string) {
	if user == "" {
		user = UnauthenticatedUser
	}
	m.ConnectionsTotal.WithLabelValues(user).Inc()
	m.ActiveConnections.Inc()
}

// RecordDisconnection increments the disconnection counter and decrements active connections gauge
func (m *Metrics) RecordDisconnection(user string) {
	if user == "" {
		user = UnauthenticatedUser
	}
	m.DisconnectionsTotal.WithLabelValues(user).Inc()
	m.ActiveConnections.Dec()
}

// RecordAuthentication increments the successful authentication counter
func (m *Metrics) RecordAuthentication(user string) {
	if user == "" {
		user = UnauthenticatedUser
	}
	m.AuthenticationsTotal.WithLabelValues(user).Inc()
}

// RecordAuthFailure increments the authentication failure counter
func (m *Metrics) RecordAuthFailure(user string) {
	if user == "" {
		user = UnauthenticatedUser
	}
	m.AuthFailuresTotal.WithLabelValues(user).Inc()
}
package metrics

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

func TestMetricsNew(t *testing.T) {
	m := New()
	
	if m == nil {
		t.Fatal("New() returned nil")
	}
	
	if m.BytesReceived == nil {
		t.Error("BytesReceived metric not initialized")
	}
	
	if m.BytesSent == nil {
		t.Error("BytesSent metric not initialized")
	}
	
	if m.MessagesReceived == nil {
		t.Error("MessagesReceived metric not initialized")
	}
	
	if m.MessagesSent == nil {
		t.Error("MessagesSent metric not initialized")
	}
	
	if m.ActiveConnections == nil {
		t.Error("ActiveConnections metric not initialized")
	}
	
	if m.ConnectionsTotal == nil {
		t.Error("ConnectionsTotal metric not initialized")
	}
	
	if m.DisconnectionsTotal == nil {
		t.Error("DisconnectionsTotal metric not initialized")
	}
	
	if m.AuthenticationsTotal == nil {
		t.Error("AuthenticationsTotal metric not initialized")
	}
	
	if m.AuthFailuresTotal == nil {
		t.Error("AuthFailuresTotal metric not initialized")
	}
}

func TestMetricsRecordBytesReceived(t *testing.T) {
	m := New()
	
	// Test with authenticated user
	m.RecordBytesReceived("alice", 100)
	
	metric := &dto.Metric{}
	err := m.BytesReceived.WithLabelValues("alice").Write(metric)
	if err != nil {
		t.Fatalf("Failed to get metric value: %v", err)
	}
	
	if metric.Counter.GetValue() != 100 {
		t.Errorf("Expected 100, got %f", metric.Counter.GetValue())
	}
	
	// Test with unauthenticated user
	m.RecordBytesReceived("", 50)
	
	err = m.BytesReceived.WithLabelValues(UnauthenticatedUser).Write(metric)
	if err != nil {
		t.Fatalf("Failed to get metric value: %v", err)
	}
	
	if metric.Counter.GetValue() != 50 {
		t.Errorf("Expected 50, got %f", metric.Counter.GetValue())
	}
}

func TestMetricsRecordBytesSent(t *testing.T) {
	m := New()
	
	// Test with authenticated user
	m.RecordBytesSent("bob", 200)
	
	metric := &dto.Metric{}
	err := m.BytesSent.WithLabelValues("bob").Write(metric)
	if err != nil {
		t.Fatalf("Failed to get metric value: %v", err)
	}
	
	if metric.Counter.GetValue() != 200 {
		t.Errorf("Expected 200, got %f", metric.Counter.GetValue())
	}
}

func TestMetricsRecordMessages(t *testing.T) {
	m := New()
	
	// Test message received
	m.RecordMessageReceived("alice")
	m.RecordMessageReceived("alice")
	
	metric := &dto.Metric{}
	err := m.MessagesReceived.WithLabelValues("alice").Write(metric)
	if err != nil {
		t.Fatalf("Failed to get metric value: %v", err)
	}
	
	if metric.Counter.GetValue() != 2 {
		t.Errorf("Expected 2, got %f", metric.Counter.GetValue())
	}
	
	// Test message sent
	m.RecordMessageSent("alice")
	
	err = m.MessagesSent.WithLabelValues("alice").Write(metric)
	if err != nil {
		t.Fatalf("Failed to get metric value: %v", err)
	}
	
	if metric.Counter.GetValue() != 1 {
		t.Errorf("Expected 1, got %f", metric.Counter.GetValue())
	}
}

func TestMetricsRecordConnections(t *testing.T) {
	m := New()
	
	// Test connection
	m.RecordConnection("alice")
	m.RecordConnection("bob")
	
	// Check total connections for alice
	metric := &dto.Metric{}
	err := m.ConnectionsTotal.WithLabelValues("alice").Write(metric)
	if err != nil {
		t.Fatalf("Failed to get metric value: %v", err)
	}
	
	if metric.Counter.GetValue() != 1 {
		t.Errorf("Expected 1, got %f", metric.Counter.GetValue())
	}
	
	// Check active connections gauge
	gMetric := &dto.Metric{}
	err = m.ActiveConnections.Write(gMetric)
	if err != nil {
		t.Fatalf("Failed to get metric value: %v", err)
	}
	
	if gMetric.Gauge.GetValue() != 2 {
		t.Errorf("Expected 2 active connections, got %f", gMetric.Gauge.GetValue())
	}
	
	// Test disconnection
	m.RecordDisconnection("alice")
	
	// Check disconnections counter
	err = m.DisconnectionsTotal.WithLabelValues("alice").Write(metric)
	if err != nil {
		t.Fatalf("Failed to get metric value: %v", err)
	}
	
	if metric.Counter.GetValue() != 1 {
		t.Errorf("Expected 1, got %f", metric.Counter.GetValue())
	}
	
	// Check active connections decreased
	err = m.ActiveConnections.Write(gMetric)
	if err != nil {
		t.Fatalf("Failed to get metric value: %v", err)
	}
	
	if gMetric.Gauge.GetValue() != 1 {
		t.Errorf("Expected 1 active connection, got %f", gMetric.Gauge.GetValue())
	}
}

func TestMetricsRecordAuthentication(t *testing.T) {
	m := New()
	
	// Test successful authentication
	m.RecordAuthentication("alice")
	
	metric := &dto.Metric{}
	err := m.AuthenticationsTotal.WithLabelValues("alice").Write(metric)
	if err != nil {
		t.Fatalf("Failed to get metric value: %v", err)
	}
	
	if metric.Counter.GetValue() != 1 {
		t.Errorf("Expected 1, got %f", metric.Counter.GetValue())
	}
	
	// Test authentication failure
	m.RecordAuthFailure("bob")
	
	err = m.AuthFailuresTotal.WithLabelValues("bob").Write(metric)
	if err != nil {
		t.Fatalf("Failed to get metric value: %v", err)
	}
	
	if metric.Counter.GetValue() != 1 {
		t.Errorf("Expected 1, got %f", metric.Counter.GetValue())
	}
}

func TestMetricsStartServer(t *testing.T) {
	m := New()
	
	// Start server on a test port
	err := m.StartServer("0") // Use port 0 to get any available port
	if err != nil {
		t.Fatalf("Failed to start metrics server: %v", err)
	}
	
	// Give server a moment to start
	time.Sleep(100 * time.Millisecond)
	
	// Stop server
	err = m.Stop()
	if err != nil {
		t.Fatalf("Failed to stop metrics server: %v", err)
	}
}

func TestMetricsEndpointFormat(t *testing.T) {
	m := New()
	
	// Add some test data
	m.RecordBytesReceived("alice", 1000)
	m.RecordBytesSent("alice", 500)
	m.RecordMessageReceived("alice")
	m.RecordConnection("alice")
	m.RecordAuthentication("alice")
	
	// Create a test HTTP handler
	handler := promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
	
	// Create a test request
	req, err := http.NewRequest("GET", "/metrics", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	
	// Create a response recorder
	rr := &mockResponseWriter{
		headers: make(http.Header),
		body:    strings.Builder{},
	}
	
	// Call the handler
	handler.ServeHTTP(rr, req)
	
	if rr.statusCode != 200 && rr.statusCode != 0 {
		t.Errorf("Expected status 200, got %d", rr.statusCode)
	}
	
	body := rr.body.String()
	
	// Check that metrics are present in the output
	expectedMetrics := []string{
		"nats_proxy_bytes_received_total",
		"nats_proxy_bytes_sent_total",
		"nats_proxy_messages_received_total",
		"nats_proxy_active_connections",
		"nats_proxy_connections_total",
		"nats_proxy_authentications_total",
	}
	
	for _, metric := range expectedMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("Expected metric %s not found in output", metric)
		}
	}
	
	// Check that alice label is present
	if !strings.Contains(body, `user="alice"`) {
		t.Error("Expected alice user label not found in output")
	}
}

// mockResponseWriter is a simple mock for testing HTTP handlers
type mockResponseWriter struct {
	headers    http.Header
	body       strings.Builder
	statusCode int
}

func (m *mockResponseWriter) Header() http.Header {
	return m.headers
}

func (m *mockResponseWriter) Write(data []byte) (int, error) {
	return m.body.Write(data)
}

func (m *mockResponseWriter) WriteHeader(statusCode int) {
	m.statusCode = statusCode
}
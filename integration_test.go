package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// MockNATSServer simulates a basic NATS server for testing
type MockNATSServer struct {
	listener net.Listener
	port     int
	conns    []net.Conn
	mu       sync.Mutex
	messages []string
}

func NewMockNATSServer(port int) (*MockNATSServer, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, err
	}

	return &MockNATSServer{
		listener: listener,
		port:     port,
		conns:    make([]net.Conn, 0),
		messages: make([]string, 0),
	}, nil
}

func (m *MockNATSServer) Start() {
	go func() {
		for {
			conn, err := m.listener.Accept()
			if err != nil {
				return // Server stopped
			}

			m.mu.Lock()
			m.conns = append(m.conns, conn)
			m.mu.Unlock()

			go m.handleConnection(conn)
		}
	}()
}

func (m *MockNATSServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Send INFO message like real NATS server
	info := `INFO {"server_id":"mock_server","version":"2.9.0","go":"go1.19","host":"localhost","port":4222,"max_payload":1048576,"proto":1,"client_id":1,"auth_required":false,"tls_required":false,"tls_verify":false}` + "\r\n"
	conn.Write([]byte(info))

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		
		m.mu.Lock()
		m.messages = append(m.messages, line)
		m.mu.Unlock()

		// Echo back some responses for testing
		if strings.HasPrefix(line, "CONNECT") {
			conn.Write([]byte("+OK\r\n"))
		} else if strings.HasPrefix(line, "PING") {
			conn.Write([]byte("PONG\r\n"))
		} else if strings.HasPrefix(line, "SUB") {
			conn.Write([]byte("+OK\r\n"))
		} else if strings.HasPrefix(line, "PUB") || strings.HasPrefix(line, "HPUB") {
			conn.Write([]byte("+OK\r\n"))
		}
	}
}

func (m *MockNATSServer) Stop() {
	m.listener.Close()
	m.mu.Lock()
	for _, conn := range m.conns {
		conn.Close()
	}
	m.mu.Unlock()
}

func (m *MockNATSServer) GetMessages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.messages))
	copy(result, m.messages)
	return result
}

func TestProxyBasicConnectivity(t *testing.T) {
	// Start mock NATS server
	mockServer, err := NewMockNATSServer(0) // Use any available port
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}
	defer mockServer.Stop()

	// Get the actual port assigned
	addr := mockServer.listener.Addr().(*net.TCPAddr)
	mockPort := addr.Port

	mockServer.Start()

	// Test basic connection through proxy functionality
	// Note: This is a simplified test that doesn't start the full proxy
	// but tests the core connection handling logic

	// Create a test configuration
	testConfig := &Config{
		DefaultBandwidth: 1024 * 1024, // 1MB/s
		Users: map[string]int64{
			"testuser": 512 * 1024, // 512KB/s
		},
	}

	// Test getBandwidthForUser function with test config
	originalConfig := config
	config = testConfig
	defer func() { config = originalConfig }()

	bandwidth := getBandwidthForUser("testuser")
	expectedBandwidth := int64(512 * 1024)
	if bandwidth != expectedBandwidth {
		t.Errorf("Expected bandwidth %d, got %d", expectedBandwidth, bandwidth)
	}

	// Test connection to mock server
	conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", mockPort))
	if err != nil {
		t.Fatalf("Failed to connect to mock server: %v", err)
	}
	defer conn.Close()

	// Send CONNECT message
	connectMsg := `CONNECT {"user":"testuser","pass":"testpass"}` + "\r\n"
	_, err = conn.Write([]byte(connectMsg))
	if err != nil {
		t.Fatalf("Failed to send CONNECT: %v", err)
	}

	// Read response
	buffer := make([]byte, 1024)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buffer)
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	response := string(buffer[:n])
	if !strings.Contains(response, "INFO") {
		t.Errorf("Expected INFO message in response, got: %s", response)
	}

	t.Logf("Mock server received messages: %v", mockServer.GetMessages())
}

func TestJWTExtractionIntegration(t *testing.T) {
	tests := []struct {
		name           string
		connectMessage string
		expectedUser   string
	}{
		{
			name:           "Username/password authentication",
			connectMessage: `CONNECT {"user":"alice","pass":"alicepass","verbose":true}`,
			expectedUser:   "alice",
		},
		{
			name:           "JWT authentication",
			connectMessage: `CONNECT {"jwt":"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJuYW1lIjoiYm9iIiwiaWF0IjoxNTE2MjM5MDIyfQ.invalid","verbose":true}`,
			expectedUser:   "bob",
		},
		{
			name:           "No authentication",
			connectMessage: `CONNECT {"verbose":true}`,
			expectedUser:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse the CONNECT message like the proxy would
			var connectObj map[string]interface{}
			jsonStr := strings.TrimSpace(tt.connectMessage)[8:] // Remove "CONNECT "
			
			err := json.Unmarshal([]byte(jsonStr), &connectObj)
			if err != nil {
				t.Fatalf("Failed to parse CONNECT JSON: %v", err)
			}

			var extractedUser string

			// Test the authentication extraction logic
			if user, ok := connectObj["user"].(string); ok && user != "" {
				extractedUser = user
			} else if jwtToken, ok := connectObj["jwt"].(string); ok && jwtToken != "" {
				extractedUser = extractUsernameFromJWT(jwtToken)
			}

			if extractedUser != tt.expectedUser {
				t.Errorf("Expected user %q, got %q", tt.expectedUser, extractedUser)
			}
		})
	}
}

func TestRateLimitingIntegration(t *testing.T) {
	// Test the rate limiting logic that would be used in handleConnection
	tests := []struct {
		name           string
		user           string
		expectedLimit  int64
		expectedBurst  int64
	}{
		{
			name:          "Alice with 5MB limit",
			user:          "alice",
			expectedLimit: 5 * 1024 * 1024,
			expectedBurst: 524288, // 512KB
		},
		{
			name:          "Bob with 2MB limit", 
			user:          "bob",
			expectedLimit: 2 * 1024 * 1024,
			expectedBurst: 204800, // ~200KB
		},
		{
			name:          "Unknown user gets default",
			user:          "unknown",
			expectedLimit: 10 * 1024 * 1024, // Default
			expectedBurst: 1048576,          // 1MB
		},
	}

	// Set up test config that matches production
	testConfig := &Config{
		DefaultBandwidth: 10 * 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
			"bob":   2 * 1024 * 1024,
		},
	}

	originalConfig := config
	config = testConfig
	defer func() { config = originalConfig }()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the bandwidth calculation
			bandwidthLimit := getBandwidthForUser(tt.user)
			if bandwidthLimit != tt.expectedLimit {
				t.Errorf("Expected limit %d, got %d", tt.expectedLimit, bandwidthLimit)
			}

			// Test the burst capacity calculation (same logic as proxy.go)
			burstCapacity := bandwidthLimit / 10
			if burstCapacity < 1024 {
				burstCapacity = 1024
			}

			if burstCapacity != tt.expectedBurst {
				t.Errorf("Expected burst %d, got %d", tt.expectedBurst, burstCapacity)
			}
		})
	}
}

func TestConnectionStateTransitions(t *testing.T) {
	// Test simulating the connection handling state transitions
	
	// Create test data that simulates what handleConnection would process
	testData := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "CONNECT followed by PUB",
			input:    "CONNECT {\"user\":\"alice\"}\r\nPUB test.subject 5\r\nhello\r\n",
			expected: "alice", // Should extract alice as user
		},
		{
			name:     "CONNECT with JWT followed by messages",
			input:    "CONNECT {\"jwt\":\"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJuYW1lIjoiYm9iIn0.invalid\"}\r\nSUB test.* 1\r\n",
			expected: "bob",
		},
		{
			name:     "Multiple messages without auth",
			input:    "PING\r\nPONG\r\nSUB test 1\r\n",
			expected: "",
		},
	}

	for _, tt := range testData {
		t.Run(tt.name, func(t *testing.T) {
			reader := strings.NewReader(tt.input)
			buffer := bytes.NewBuffer(nil)
			
			// Simulate parsing like handleConnection does
			scanner := bufio.NewScanner(reader)
			var extractedUser string
			
			for scanner.Scan() {
				line := scanner.Text()
				
				if strings.HasPrefix(line, "CONNECT ") {
					var obj map[string]interface{}
					jsonStr := strings.TrimSpace(line)[8:]
					if err := json.Unmarshal([]byte(jsonStr), &obj); err == nil {
						if user, ok := obj["user"].(string); ok && user != "" {
							extractedUser = user
						} else if jwtToken, ok := obj["jwt"].(string); ok && jwtToken != "" {
							extractedUser = extractUsernameFromJWT(jwtToken)
						}
					}
				}
				
				// Write to buffer to verify all data is processed
				buffer.WriteString(line + "\r\n")
			}

			if extractedUser != tt.expected {
				t.Errorf("Expected user %q, got %q", tt.expected, extractedUser)
			}

			// Verify all input was processed
			if buffer.String() != tt.input {
				t.Errorf("Data processing mismatch")
			}
		})
	}
}

func TestConcurrentConnections(t *testing.T) {
	// Test configuration for concurrent connections
	testConfig := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"user1": 512 * 1024,
			"user2": 256 * 1024,
			"user3": 128 * 1024,
		},
	}

	originalConfig := config
	config = testConfig
	defer func() { config = originalConfig }()

	// Test that different users get different bandwidth limits concurrently
	users := []string{"user1", "user2", "user3", "unknown"}
	expectedLimits := []int64{512 * 1024, 256 * 1024, 128 * 1024, 1024 * 1024}

	var wg sync.WaitGroup
	results := make(chan struct {
		user  string
		limit int64
	}, len(users))

	for i, user := range users {
		wg.Add(1)
		go func(u string, expectedLimit int64) {
			defer wg.Done()
			
			// Simulate concurrent bandwidth lookups
			limit := getBandwidthForUser(u)
			results <- struct {
				user  string
				limit int64
			}{u, limit}
			
			if limit != expectedLimit {
				t.Errorf("User %s: expected limit %d, got %d", u, expectedLimit, limit)
			}
		}(user, expectedLimits[i])
	}

	wg.Wait()
	close(results)

	// Verify all results were collected
	resultCount := 0
	for range results {
		resultCount++
	}

	if resultCount != len(users) {
		t.Errorf("Expected %d results, got %d", len(users), resultCount)
	}
}

func TestProxyErrorHandling(t *testing.T) {
	// Test error conditions that handleConnection might encounter
	
	tests := []struct {
		name          string
		input         string
		expectError   bool
		description   string
	}{
		{
			name:        "Malformed CONNECT JSON",
			input:       "CONNECT {invalid json}\r\n",
			expectError: false, // Should be handled gracefully
			description: "Invalid JSON should not crash parser",
		},
		{
			name:        "Empty CONNECT",
			input:       "CONNECT\r\n",
			expectError: false,
			description: "Missing JSON should be handled",
		},
		{
			name:        "Very long line",
			input:       "CONNECT " + strings.Repeat("a", 10000) + "\r\n",
			expectError: false,
			description: "Very long input should be handled",
		},
		{
			name:        "Binary data in CONNECT",
			input:       "CONNECT \x00\x01\x02\r\n",
			expectError: false,
			description: "Binary data should not crash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := strings.NewReader(tt.input)
			
			// Test that the parsing logic doesn't panic
			defer func() {
				if r := recover(); r != nil {
					if !tt.expectError {
						t.Errorf("Unexpected panic: %v", r)
					}
				}
			}()

			scanner := bufio.NewScanner(reader)
			for scanner.Scan() {
				line := scanner.Text()
				
				if strings.HasPrefix(line, "CONNECT ") {
					var obj map[string]interface{}
					jsonStr := strings.TrimSpace(line)[8:]
					
					// This should not panic even with invalid JSON
					json.Unmarshal([]byte(jsonStr), &obj)
				}
			}
			
			// If we reach here without panic, the test passed
		})
	}
}
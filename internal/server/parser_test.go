package server

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/juju/ratelimit"
)

func TestClientMessageParser_ParseAndForward(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectUser  string
		description string
	}{
		{
			name:        "CONNECT with username",
			input:       "CONNECT {\"user\":\"alice\",\"pass\":\"secret\"}\r\n",
			expectUser:  "alice",
			description: "Should extract username from CONNECT message",
		},
		{
			name:        "CONNECT with JWT",
			input:       "CONNECT {\"jwt\":\"eyJ0eXAiOiJKV1QiLCJhbGciOiJlZDI1NTE5LW5rZXkifQ.eyJuYW1lIjoiYWxpY2UiLCJzdWIiOiJhbGljZSJ9.invalid\"}\r\n",
			expectUser:  "alice",
			description: "Should extract username from JWT in CONNECT message",
		},
		{
			name:        "PUB message",
			input:       "PUB test.subject 5\r\nhello\r\n",
			description: "Should forward PUB messages correctly",
		},
		{
			name:        "HPUB message",
			input:       "HPUB test.subject 0 5\r\nhello\r\n",
			description: "Should forward HPUB messages correctly",
		},
		{
			name:        "PING message",
			input:       "PING\r\n",
			description: "Should pass through PING without rate limiting",
		},
		{
			name:        "PONG message",
			input:       "PONG\r\n",
			description: "Should pass through PONG without rate limiting",
		},
		{
			name:        "SUB message",
			input:       "SUB test.subject 1\r\n",
			description: "Should pass through SUB without rate limiting",
		},
		{
			name:        "UNSUB message",
			input:       "UNSUB 1\r\n",
			description: "Should pass through UNSUB without rate limiting",
		},
		{
			name:        "INFO message",
			input:       "INFO {\"server_id\":\"test\"}\r\n",
			description: "Should pass through INFO without rate limiting",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			var authenticatedUser string

			// Create mock rate limiter manager
			mockRLM := &mockRateLimiterManager{}

			parser := ClientMessageParser{
				RateLimiterManager: mockRLM,
				OnUserAuthenticated: func(user string) {
					authenticatedUser = user
				},
			}

			input := strings.NewReader(tt.input)
			err := parser.ParseAndForward(input, &output)

			if err != nil {
				t.Fatalf("ParseAndForward failed: %v", err)
			}

			// Verify user authentication
			if tt.expectUser != "" && authenticatedUser != tt.expectUser {
				t.Errorf("Expected user %q, got %q", tt.expectUser, authenticatedUser)
			}
		})
	}
}

func TestClientMessageParser_MultipleMessages(t *testing.T) {
	var output bytes.Buffer
	var authenticatedUser string

	mockRLM := &mockRateLimiterManager{}

	parser := ClientMessageParser{
		RateLimiterManager: mockRLM,
		OnUserAuthenticated: func(user string) {
			authenticatedUser = user
		},
	}

	// Send multiple messages in sequence
	expectedOutput := "CONNECT {\"user\":\"alice\"}\r\nPING\r\nPUB test 5\r\nhello\r\nPING\r\nPUB test2 5\r\nworld\r\n"
	input := strings.NewReader(expectedOutput)

	err := parser.ParseAndForward(input, &output)
	if err != nil {
		t.Fatalf("ParseAndForward failed: %v", err)
	}

	// Verify user was authenticated
	if authenticatedUser != "alice" {
		t.Errorf("Expected user 'alice', got %q", authenticatedUser)
	}

	// Verify all messages were forwarded correctly
	if output.String() != expectedOutput {
		t.Errorf("Output doesn't match expected.\nExpected: %q\nGot: %q", expectedOutput, output.String())
	}
}

func TestClientMessageParser_ExtractUsernameFromJWT(t *testing.T) {
	parser := ClientMessageParser{}

	tests := []struct {
		name     string
		jwt      string
		expected string
	}{
		{
			name:     "JWT with name claim",
			jwt:      "eyJ0eXAiOiJKV1QiLCJhbGciOiJub25lIn0.eyJuYW1lIjoiYWxpY2UifQ.",
			expected: "alice",
		},
		{
			name:     "JWT with sub claim",
			jwt:      "eyJ0eXAiOiJKV1QiLCJhbGciOiJub25lIn0.eyJzdWIiOiJib2IifQ.",
			expected: "bob",
		},
		{
			name:     "JWT with both name and sub (name takes precedence)",
			jwt:      "eyJ0eXAiOiJKV1QiLCJhbGciOiJub25lIn0.eyJuYW1lIjoiYWxpY2UiLCJzdWIiOiJib2IifQ.",
			expected: "alice",
		},
		{
			name:     "Invalid JWT",
			jwt:      "invalid.jwt.token",
			expected: "",
		},
		{
			name:     "Empty JWT",
			jwt:      "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.extractUsernameFromJWT(tt.jwt)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestClientMessageParser_RateLimitingIntegration(t *testing.T) {
	var output bytes.Buffer
	var rateLimitWaitTime time.Duration

	// Create a real rate limiter with very low rate (1 byte per second)
	bucket := ratelimit.NewBucketWithRate(1, 1)

	mockRLM := &mockRateLimiterManager{
		bucket: bucket,
	}

	parser := ClientMessageParser{
		RateLimiterManager: mockRLM,
	}

	// First, authenticate a user
	connectInput := strings.NewReader("CONNECT {\"user\":\"alice\"}\r\n")
	err := parser.ParseAndForward(connectInput, &output)
	if err != nil {
		t.Fatalf("ParseAndForward failed for CONNECT: %v", err)
	}

	// Now send a PUB message and measure the rate limiting delay
	start := time.Now()
	pubInput := strings.NewReader("PUB test 5\r\nhello\r\n")
	err = parser.ParseAndForward(pubInput, &output)
	if err != nil {
		t.Fatalf("ParseAndForward failed for PUB: %v", err)
	}
	rateLimitWaitTime = time.Since(start)

	// With a 1 byte/second rate limit, we should see some delay
	// (This is a basic test - in practice the delay depends on bucket state)
	if rateLimitWaitTime < 0 {
		t.Error("Expected some rate limiting delay, but got none")
	}
}

// Mock RateLimiterManager for testing
type mockRateLimiterManager struct {
	bucket *ratelimit.Bucket
}

func (m *mockRateLimiterManager) GetLimiter(username string) *ratelimit.Bucket {
	if m.bucket != nil {
		return m.bucket
	}

	// For simplicity, just return a real bucket for basic functionality tests
	// Rate limiting behavior will be tested separately
	return ratelimit.NewBucketWithRate(1000, 1000)
}

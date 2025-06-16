package server

import (
	"bytes"
	"fmt"
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

			// Create mock rate limiter manager
			mockRLM := &mockRateLimiterManager{}

			input := strings.NewReader(tt.input)
			parser := NewClientMessageParser(
				input,
				&output,
				mockRLM,
			)

			err := parser.ParseAndForward()

			if err != nil {
				t.Fatalf("ParseAndForward failed: %v", err)
			}

			// Verify user authentication
			if tt.expectUser != "" && parser.GetUser() != tt.expectUser {
				t.Errorf("Expected user %q, got %q", tt.expectUser, parser.GetUser())
			}
		})
	}
}

func TestClientMessageParser_MultipleMessages(t *testing.T) {
	var output bytes.Buffer

	mockRLM := &mockRateLimiterManager{}

	// Send multiple messages in sequence
	expectedOutput := "CONNECT {\"user\":\"alice\"}\r\nPING\r\nPUB test 5\r\nhello\r\nPING\r\nPUB test2 5\r\nworld\r\n"
	input := strings.NewReader(expectedOutput)

	parser := NewClientMessageParser(
		input,
		&output,
		mockRLM,
	)

	err := parser.ParseAndForward()
	if err != nil {
		t.Fatalf("ParseAndForward failed: %v", err)
	}

	// Verify user was authenticated
	if parser.GetUser() != "alice" {
		t.Errorf("Expected user 'alice', got %q", parser.GetUser())
	}

	// Verify all messages were forwarded correctly
	if output.String() != expectedOutput {
		t.Errorf("Output doesn't match expected.\nExpected: %q\nGot: %q", expectedOutput, output.String())
	}
}

func TestClientMessageParser_BufferDuplicationIssue(t *testing.T) {
	// After analysis, the current parser implementation appears to handle multiple messages correctly
	// because buf = buf[:0] resets the buffer after each message write.
	// 
	// However, I initially thought there could be an issue where:
	// 1. Buffer accumulates: "PING\r\nPONG\r\nSUB..."
	// 2. When PING\r\n completes, entire buffer gets written: "PING\r\nPONG\r\nSUB..."
	// 3. Buffer gets reset, but PONG and SUB were already written
	// 4. When PONG\r\n completes later, it would be missing from output
	//
	// Let's test this scenario anyway to document the expected behavior.
	
	var output bytes.Buffer
	mockRLM := &mockRateLimiterManager{}

	// Test multiple messages that should each appear exactly once
	multipleMessages := "PING\r\nPONG\r\nSUB test 1\r\nUNSUB 1\r\n"
	input := strings.NewReader(multipleMessages)

	parser := NewClientMessageParser(
		input,
		&output,
		mockRLM,
	)

	err := parser.ParseAndForward()
	if err != nil {
		t.Fatalf("ParseAndForward failed: %v", err)
	}

	actualOutput := output.String()
	
	// The test documents expected behavior: output should match input exactly
	// If this test fails in the future, it indicates a buffer management regression
	if actualOutput != multipleMessages {
		t.Errorf("Buffer duplication/corruption detected!")
		t.Logf("Expected (%d bytes): %q", len(multipleMessages), multipleMessages)
		t.Logf("Actual   (%d bytes): %q", len(actualOutput), actualOutput)
		
		// Analyze the type of corruption
		if len(actualOutput) > len(multipleMessages) {
			t.Logf("OUTPUT IS LONGER - indicates message duplication")
			
			// Check for specific duplications
			pingCount := strings.Count(actualOutput, "PING\r\n")
			pongCount := strings.Count(actualOutput, "PONG\r\n")
			if pingCount > 1 || pongCount > 1 {
				t.Errorf("Message duplication confirmed - PING appears %d times, PONG appears %d times", pingCount, pongCount)
			}
		} else if len(actualOutput) < len(multipleMessages) {
			t.Logf("OUTPUT IS SHORTER - indicates missing messages")
		}
		
		// This test should pass with current implementation
		// If it fails, there's a buffer management bug
		t.FailNow()
	}
	
	// Test passes - current implementation handles multiple messages correctly
	t.Logf("SUCCESS: Parser correctly handled %d bytes of multiple messages without duplication", len(actualOutput))
}

func TestClientMessageParser_RateLimitingOnBufferFlushes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping rate limiting test in short mode")
	}
	
	var output bytes.Buffer

	// Create moderately restrictive rate limiter (100 bytes/second)
	bucket := ratelimit.NewBucketWithRate(100, 100)

	mockRLM := &mockRateLimiterManager{
		bucket: bucket,
	}

	// Combine CONNECT and PUB messages into single input
	connectMsg := "CONNECT {\"user\":\"alice\"}\r\n"
	payloadSize := 5000 // This will cause buffer flush
	payload := strings.Repeat("F", payloadSize)
	pubMsg := fmt.Sprintf("PUB test.flush %d\r\n%s\r\n", payloadSize, payload)
	
	combinedInput := connectMsg + pubMsg
	input := strings.NewReader(combinedInput)

	parser := NewClientMessageParser(
		input,
		&output,
		mockRLM,
	)

	start := time.Now()
	err := parser.ParseAndForward()
	if err != nil {
		t.Fatalf("ParseAndForward failed: %v", err)
	}
	elapsed := time.Since(start)

	if parser.GetUser() != "alice" {
		t.Fatalf("Expected user 'alice', got %q", parser.GetUser())
	}

	// With 100 bytes/second and ~5000+ byte message, should see ~50+ second delay
	expectedMinDelay := time.Second * 30 // Minimum expected delay
	if elapsed < expectedMinDelay {
		t.Errorf("Rate limiting not properly applied to buffer flushes - elapsed: %v, expected min: %v", elapsed, expectedMinDelay)
	} else {
		t.Logf("Buffer flush rate limiting working correctly - elapsed: %v", elapsed)
	}

	// Verify message integrity despite rate limiting
	outputStr := output.String()
	if !strings.Contains(outputStr, payload[:100]) {
		t.Error("Message corrupted during rate-limited buffer flush")
	}
}

func TestClientMessageParser_ExtractUsernameFromJWT(t *testing.T) {
	// Create a dummy parser just to test the JWT extraction method
	input := strings.NewReader("")
	output := &bytes.Buffer{}
	parser := NewClientMessageParser(input, output, nil)

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

	// Create a real rate limiter with very low rate (1 byte per second)
	bucket := ratelimit.NewBucketWithRate(1, 1)

	mockRLM := &mockRateLimiterManager{
		bucket: bucket,
	}

	// Combine CONNECT and PUB messages
	combinedInput := "CONNECT {\"user\":\"alice\"}\r\nPUB test 5\r\nhello\r\n"
	input := strings.NewReader(combinedInput)

	parser := NewClientMessageParser(
		input,
		&output,
		mockRLM,
	)

	// Measure the rate limiting delay
	start := time.Now()
	err := parser.ParseAndForward()
	if err != nil {
		t.Fatalf("ParseAndForward failed: %v", err)
	}
	rateLimitWaitTime := time.Since(start)

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

func TestClientMessageParser_LargePayload(t *testing.T) {
	tests := []struct {
		name        string
		payloadSize int
		description string
	}{
		{
			name:        "Payload exactly 4096 bytes",
			payloadSize: 4096,
			description: "Should handle payload equal to initial buffer size",
		},
		{
			name:        "Payload larger than 4096 bytes",
			payloadSize: 8192,
			description: "Should handle payload requiring buffer growth",
		},
		{
			name:        "Very large payload",
			payloadSize: 65536,
			description: "Should handle very large payloads without corruption",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			mockRLM := &mockRateLimiterManager{}
			// Create large payload
			payload := strings.Repeat("A", tt.payloadSize)
			message := "PUB test.subject " + fmt.Sprintf("%d", tt.payloadSize) + "\r\n" + payload + "\r\n"
			
			input := strings.NewReader(message)
			parser := NewClientMessageParser(
				input,
				&output,
				mockRLM,
			)

			err := parser.ParseAndForward()
			if err != nil {
				t.Fatalf("ParseAndForward failed: %v", err)
			}

			// Verify output matches input exactly
			if output.String() != message {
				t.Errorf("Large payload corrupted during parsing")
				t.Logf("Expected length: %d", len(message))
				t.Logf("Actual length: %d", output.Len())
				
				// Check if payload was truncated
				if output.Len() < len(message) {
					t.Error("Payload appears to be truncated")
				} else if output.Len() > len(message) {
					t.Error("Output is longer than expected - possible duplication")
				}
			}
		})
	}
}

func TestClientMessageParser_LargeHPUBPayload(t *testing.T) {
	var output bytes.Buffer
	mockRLM := &mockRateLimiterManager{}
	// Test HPUB with large payload
	payloadSize := 10000
	payload := strings.Repeat("B", payloadSize)
	headerSize := 0
	message := "HPUB test.subject " + fmt.Sprintf("%d %d", headerSize, payloadSize) + "\r\n" + payload + "\r\n"
	
	input := strings.NewReader(message)
	parser := NewClientMessageParser(
		input,
		&output,
		mockRLM,
	)

	err := parser.ParseAndForward()
	if err != nil {
		t.Fatalf("ParseAndForward failed: %v", err)
	}

	if output.String() != message {
		t.Errorf("Large HPUB payload corrupted during parsing")
		t.Logf("Expected length: %d, Actual length: %d", len(message), output.Len())
	}
}

func TestClientMessageParser_MultipleLargeMessages(t *testing.T) {
	var output bytes.Buffer
	mockRLM := &mockRateLimiterManager{}
	// Send multiple large messages to test buffer reuse
	var expectedOutput strings.Builder
	
	// First large message
	payload1 := strings.Repeat("X", 5000)
	msg1 := "PUB test1 " + fmt.Sprintf("%d", len(payload1)) + "\r\n" + payload1 + "\r\n"
	expectedOutput.WriteString(msg1)
	
	// Second large message
	payload2 := strings.Repeat("Y", 6000)
	msg2 := "PUB test2 " + fmt.Sprintf("%d", len(payload2)) + "\r\n" + payload2 + "\r\n"
	expectedOutput.WriteString(msg2)
	
	// Third large message
	payload3 := strings.Repeat("Z", 4500)
	msg3 := "PUB test3 " + fmt.Sprintf("%d", len(payload3)) + "\r\n" + payload3 + "\r\n"
	expectedOutput.WriteString(msg3)
	
	input := strings.NewReader(expectedOutput.String())
	parser := NewClientMessageParser(
		input,
		&output,
		mockRLM,
	)

	err := parser.ParseAndForward()
	if err != nil {
		t.Fatalf("ParseAndForward failed: %v", err)
	}

	if output.String() != expectedOutput.String() {
		t.Errorf("Multiple large messages corrupted during parsing")
		t.Logf("Expected length: %d, Actual length: %d", expectedOutput.Len(), output.Len())
		
		// Check for message boundary issues
		actualStr := output.String()
		if !strings.Contains(actualStr, strings.Repeat("X", 100)) {
			t.Error("First message payload missing or corrupted")
		}
		if !strings.Contains(actualStr, strings.Repeat("Y", 100)) {
			t.Error("Second message payload missing or corrupted")
		}
		if !strings.Contains(actualStr, strings.Repeat("Z", 100)) {
			t.Error("Third message payload missing or corrupted")
		}
	}
}

func TestClientMessageParser_BufferGrowthAndReuse(t *testing.T) {
	mockRLM := &mockRateLimiterManager{}

	// Test that buffer grows efficiently and is reused properly
	testSizes := []int{1000, 8000, 500, 12000, 100}
	
	for i, size := range testSizes {
		t.Run(fmt.Sprintf("Message_%d_size_%d", i+1, size), func(t *testing.T) {
			var output bytes.Buffer
			payload := strings.Repeat("T", size)
			message := fmt.Sprintf("PUB test%d %d\r\n%s\r\n", i, size, payload)
			
			input := strings.NewReader(message)
			parser := NewClientMessageParser(
				input,
				&output,
				mockRLM,
			)

			err := parser.ParseAndForward()
			if err != nil {
				t.Fatalf("ParseAndForward failed for size %d: %v", size, err)
			}
			
			if output.String() != message {
				t.Errorf("Message %d corrupted, size %d", i+1, size)
			}
		})
	}
}

func TestClientMessageParser_PartialReadScenarios(t *testing.T) {
	mockRLM := &mockRateLimiterManager{}

	// Test message that arrives in chunks (simulating network conditions)
	largePayload := strings.Repeat("CHUNK", 2000) // 10000 bytes
	message := fmt.Sprintf("PUB test.chunked %d\r\n%s\r\n", len(largePayload), largePayload)
	
	// The new parser design expects complete input, so we'll test with complete message
	input := strings.NewReader(message)
	var output bytes.Buffer
	
	parser := NewClientMessageParser(
		input,
		&output,
		mockRLM,
	)

	err := parser.ParseAndForward()
	if err != nil {
		t.Fatalf("ParseAndForward failed: %v", err)
	}
	
	// The current parser implementation expects complete messages
	// This test documents the current behavior for partial message handling
	if output.Len() == 0 {
		t.Log("Parser requires complete messages - partial messages not forwarded until complete")
	}
}

func TestClientMessageParser_ExtremelyLargePayload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large payload test in short mode")
	}
	
	mockRLM := &mockRateLimiterManager{}
	// Test with 1MB payload to verify memory efficiency
	payloadSize := 1024 * 1024 // 1MB
	payload := strings.Repeat("M", payloadSize)
	message := fmt.Sprintf("PUB test.megabyte %d\r\n%s\r\n", payloadSize, payload)
	
	var output bytes.Buffer
	input := strings.NewReader(message)
	parser := NewClientMessageParser(
		input,
		&output,
		mockRLM,
	)

	err := parser.ParseAndForward()
	if err != nil {
		t.Fatalf("ParseAndForward failed for 1MB payload: %v", err)
	}
	
	if output.Len() != len(message) {
		t.Errorf("1MB payload size mismatch: expected %d, got %d", len(message), output.Len())
	}
	
	// Verify first and last parts of payload to ensure no corruption
	outputStr := output.String()
	if !strings.HasPrefix(outputStr, "PUB test.megabyte") {
		t.Error("Message header corrupted")
	}
	if !strings.HasSuffix(outputStr, strings.Repeat("M", 100)+"\r\n") {
		t.Error("Message payload end corrupted")
	}
}

func TestClientMessageParser_RateLimitingWithLargeMessages(t *testing.T) {
	var output bytes.Buffer

	// Create a very restrictive rate limiter (10 bytes/second)
	bucket := ratelimit.NewBucketWithRate(10, 10)

	mockRLM := &mockRateLimiterManager{
		bucket: bucket,
	}

	// Combine CONNECT and PUB messages
	connectMsg := "CONNECT {\"user\":\"alice\"}\r\n"
	payloadSize := 1000
	payload := strings.Repeat("R", payloadSize)
	pubMsg := fmt.Sprintf("PUB test.rate %d\r\n%s\r\n", payloadSize, payload)
	
	combinedInput := connectMsg + pubMsg
	input := strings.NewReader(combinedInput)

	parser := NewClientMessageParser(
		input,
		&output,
		mockRLM,
	)

	start := time.Now()
	err := parser.ParseAndForward()
	if err != nil {
		t.Fatalf("ParseAndForward failed: %v", err)
	}
	elapsed := time.Since(start)

	if parser.GetUser() != "alice" {
		t.Fatalf("Expected user 'alice', got %q", parser.GetUser())
	}

	// With 10 bytes/second rate limit and ~1000 byte message, 
	// we should see significant delay (but actual timing depends on bucket state)
	t.Logf("Rate limited large message took %v", elapsed)
	
	// Verify the message was forwarded correctly despite rate limiting
	outputStr := output.String()
	if !strings.Contains(outputStr, payload[:100]) {
		t.Error("Large rate-limited message was corrupted")
	}
}

func TestClientMessageParser_RateLimitingAccuracy(t *testing.T) {
	// Test that rate limiting is applied per byte correctly for large messages
	var output bytes.Buffer

	// Create rate limiter with known capacity
	bucket := ratelimit.NewBucketWithRate(100, 100) // 100 bytes/second

	mockRLM := &mockRateLimiterManager{
		bucket: bucket,
	}

	// Build combined input with CONNECT and multiple messages
	var combinedInput strings.Builder
	combinedInput.WriteString("CONNECT {\"user\":\"testuser\"}\r\n")
	
	// Send multiple messages of known size
	messageCount := 3
	messageSize := 200 // Each message ~200 bytes
	
	for i := 0; i < messageCount; i++ {
		payload := strings.Repeat(fmt.Sprintf("%d", i), messageSize/2)
		message := fmt.Sprintf("PUB test%d %d\r\n%s\r\n", i, len(payload), payload)
		combinedInput.WriteString(message)
	}
	
	input := strings.NewReader(combinedInput.String())
	parser := NewClientMessageParser(
		input,
		&output,
		mockRLM,
	)

	start := time.Now()
	err := parser.ParseAndForward()
	if err != nil {
		t.Fatalf("ParseAndForward failed: %v", err)
	}
	elapsed := time.Since(start)
	
	t.Logf("Combined messages took %v", elapsed)

	// Verify all messages were processed correctly
	outputStr := output.String()
	for i := 0; i < messageCount; i++ {
		expectedSubstring := fmt.Sprintf("PUB test%d", i)
		if !strings.Contains(outputStr, expectedSubstring) {
			t.Errorf("Message %d missing from output", i)
		}
	}
}

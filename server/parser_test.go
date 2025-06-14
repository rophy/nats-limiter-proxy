package server

import (
	"bytes"
	"strings"
	"testing"
)

func TestNATSProxyParser_Reset(t *testing.T) {
	parser := &NATSProxyParser{
		state:   psPubArg,
		argBuf:  []byte("test"),
		msgBuf:  []byte("msg"),
		as:      5,
		drop:    10,
		payload: 100,
	}

	parser.Reset()

	if parser.state != psOpStart {
		t.Errorf("Expected state %d, got %d", psOpStart, parser.state)
	}
	if parser.argBuf != nil {
		t.Errorf("Expected argBuf to be nil, got %v", parser.argBuf)
	}
	if parser.msgBuf != nil {
		t.Errorf("Expected msgBuf to be nil, got %v", parser.msgBuf)
	}
	if parser.as != 0 {
		t.Errorf("Expected as to be 0, got %d", parser.as)
	}
	if parser.drop != 0 {
		t.Errorf("Expected drop to be 0, got %d", parser.drop)
	}
	if parser.payload != 0 {
		t.Errorf("Expected payload to be 0, got %d", parser.payload)
	}
}

func TestExtractUsernameFromJWT(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		expected string
	}{
		{
			name:     "Valid JWT with name claim",
			token:    "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJuYW1lIjoiYWxpY2UiLCJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyfQ.invalid_signature",
			expected: "alice",
		},
		{
			name:     "Valid JWT with sub claim only",
			token:    "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJib2IiLCJpYXQiOjE1MTYyMzkwMjJ9.invalid_signature",
			expected: "bob",
		},
		{
			name:     "Valid JWT with both name and sub (name takes priority)",
			token:    "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJuYW1lIjoiY2hhcmxpZSIsInN1YiI6ImRpYW5hIiwiaWF0IjoxNTE2MjM5MDIyfQ.invalid_signature",
			expected: "charlie",
		},
		{
			name:     "JWT without name or sub claims",
			token:    "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpYXQiOjE1MTYyMzkwMjJ9.invalid_signature",
			expected: "",
		},
		{
			name:     "Invalid JWT format",
			token:    "invalid.jwt.token",
			expected: "",
		},
		{
			name:     "Empty token",
			token:    "",
			expected: "",
		},
		{
			name:     "JWT with non-string name claim",
			token:    "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJuYW1lIjoxMjMsInN1YiI6ImJvYiJ9.invalid_signature",
			expected: "bob", // Falls back to sub claim
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractUsernameFromJWT(tt.token)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestNATSProxyParser_ParseAndForward_SimpleMessages(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple INFO message",
			input:    "INFO {\"server_id\":\"test\"}\r\n",
			expected: "INFO {\"server_id\":\"test\"}\r\n",
		},
		{
			name:     "PING message",
			input:    "PING\r\n",
			expected: "PING\r\n",
		},
		{
			name:     "PONG message",
			input:    "PONG\r\n",
			expected: "PONG\r\n",
		},
		{
			name:     "SUB message",
			input:    "SUB test.subject 1\r\n",
			expected: "SUB test.subject 1\r\n",
		},
		{
			name:     "Multiple messages",
			input:    "PING\r\nPONG\r\nSUB test 1\r\n",
			expected: "PING\r\nPONG\r\nSUB test 1\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := &NATSProxyParser{}
			input := strings.NewReader(tt.input)
			output := &bytes.Buffer{}

			bytesTransferred, err := parser.ParseAndForward(input, output, "test")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			result := output.String()
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}

			if bytesTransferred != int64(len(tt.expected)) {
				t.Errorf("Expected %d bytes transferred, got %d", len(tt.expected), bytesTransferred)
			}
		})
	}
}

func TestNATSProxyParser_ParseAndForward_PUBMessages(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple PUB message",
			input:    "PUB test.subject 5\r\nhello\r\n",
			expected: "PUB test.subject 5\r\nhello\r\n",
		},
		{
			name:     "PUB with reply subject",
			input:    "PUB test.subject reply.inbox 7\r\nworld!\r\n",
			expected: "PUB test.subject reply.inbox 7\r\nworld!\r\n",
		},
		{
			name:     "PUB with zero-length payload",
			input:    "PUB test.empty 0\r\n\r\n",
			expected: "PUB test.empty 0\r\n\r\n",
		},
		{
			name:     "Multiple PUB messages",
			input:    "PUB test1 3\r\nfoo\r\nPUB test2 3\r\nbar\r\n",
			expected: "PUB test1 3\r\nfoo\r\nPUB test2 3\r\nbar\r\n",
		},
		{
			name:     "PUB with binary payload",
			input:    "PUB test.binary 4\r\n\x00\x01\x02\x03\r\n",
			expected: "PUB test.binary 4\r\n\x00\x01\x02\x03\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := &NATSProxyParser{}
			input := strings.NewReader(tt.input)
			output := &bytes.Buffer{}

			bytesTransferred, err := parser.ParseAndForward(input, output, "test")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			result := output.String()
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}

			if bytesTransferred != int64(len(tt.expected)) {
				t.Errorf("Expected %d bytes transferred, got %d", len(tt.expected), bytesTransferred)
			}
		})
	}
}

func TestNATSProxyParser_ParseAndForward_HPUBMessages(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple HPUB message",
			input:    "HPUB test.subject 10 15\r\nheader1: a\r\n\r\nhello\r\n",
			expected: "HPUB test.subject 10 15\r\nheader1: a\r\n\r\nhello\r\n",
		},
		{
			name:     "HPUB with reply subject",
			input:    "HPUB test.subject reply.inbox 12 18\r\nheader2: b\r\n\r\nworld!\r\n",
			expected: "HPUB test.subject reply.inbox 12 18\r\nheader2: b\r\n\r\nworld!\r\n",
		},
		{
			name:     "HPUB with empty headers",
			input:    "HPUB test.empty 2 7\r\n\r\nhello\r\n",
			expected: "HPUB test.empty 2 7\r\n\r\nhello\r\n",
		},
		{
			name:     "HPUB with zero payload",
			input:    "HPUB test.zero 2 2\r\n\r\n\r\n",
			expected: "HPUB test.zero 2 2\r\n\r\n\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := &NATSProxyParser{}
			input := strings.NewReader(tt.input)
			output := &bytes.Buffer{}

			bytesTransferred, err := parser.ParseAndForward(input, output, "test")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			result := output.String()
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}

			if bytesTransferred != int64(len(tt.expected)) {
				t.Errorf("Expected %d bytes transferred, got %d", len(tt.expected), bytesTransferred)
			}
		})
	}
}

func TestNATSProxyParser_ParseAndForward_CONNECTMessages(t *testing.T) {
	var loggedMessages []string
	logFunc := func(direction, line string) {
		loggedMessages = append(loggedMessages, direction+": "+line)
	}

	tests := []struct {
		name            string
		input           string
		expected        string
		expectedLogs    []string
		expectedUserLog string
	}{
		{
			name:     "CONNECT with username/password",
			input:    "CONNECT {\"user\":\"alice\",\"pass\":\"alicepass\"}\r\n",
			expected: "CONNECT {\"user\":\"alice\",\"pass\":\"alicepass\"}\r\n",
			expectedLogs: []string{
				"test: CONNECT {\"user\":\"alice\",\"pass\":\"alicepass\"}\r\n",
				"test: Authenticated user (password): alice",
				"test: User: alice",
			},
		},
		{
			name:     "CONNECT with JWT",
			input:    "CONNECT {\"jwt\":\"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJuYW1lIjoiYm9iIiwiaWF0IjoxNTE2MjM5MDIyfQ.invalid\"}\r\n",
			expected: "CONNECT {\"jwt\":\"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJuYW1lIjoiYm9iIiwiaWF0IjoxNTE2MjM5MDIyfQ.invalid\"}\r\n",
			expectedLogs: []string{
				"test: CONNECT {\"jwt\":\"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJuYW1lIjoiYm9iIiwiaWF0IjoxNTE2MjM5MDIyfQ.invalid\"}\r\n",
				"test: Authenticated user (JWT): bob",
				"test: User: bob",
			},
		},
		{
			name:     "CONNECT without authentication",
			input:    "CONNECT {\"verbose\":true}\r\n",
			expected: "CONNECT {\"verbose\":true}\r\n",
			expectedLogs: []string{
				"test: CONNECT {\"verbose\":true}\r\n",
				"test: Warning: CONNECT message contains no valid authentication",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loggedMessages = nil // Reset for each test
			parser := &NATSProxyParser{LogFunc: logFunc}
			input := strings.NewReader(tt.input)
			output := &bytes.Buffer{}

			bytesTransferred, err := parser.ParseAndForward(input, output, "test")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			result := output.String()
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}

			if bytesTransferred != int64(len(tt.expected)) {
				t.Errorf("Expected %d bytes transferred, got %d", len(tt.expected), bytesTransferred)
			}

			// Check logged messages
			if len(loggedMessages) != len(tt.expectedLogs) {
				t.Fatalf("Expected %d log messages, got %d: %v", len(tt.expectedLogs), len(loggedMessages), loggedMessages)
			}

			for i, expectedLog := range tt.expectedLogs {
				if loggedMessages[i] != expectedLog {
					t.Errorf("Expected log message %d: %q, got %q", i, expectedLog, loggedMessages[i])
				}
			}
		})
	}
}

func TestNATSProxyParser_ParseAndForward_MalformedMessages(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "PUB with invalid size",
			input:    "PUB test.subject invalid\r\nhello\r\n",
			expected: "PUB test.subject invalid\r\nhello\r\n",
		},
		{
			name:     "PUB with negative size",
			input:    "PUB test.subject -5\r\nhello\r\n",
			expected: "PUB test.subject -5\r\nhello\r\n",
		},
		{
			name:     "PUB with missing size",
			input:    "PUB test.subject\r\nhello\r\n",
			expected: "PUB test.subject\r\nhello\r\n",
		},
		{
			name:     "HPUB with invalid size",
			input:    "HPUB test.subject invalid 10\r\nheader\r\n\r\nhello\r\n",
			expected: "HPUB test.subject invalid 10\r\nheader\r\n\r\nhello\r\n",
		},
		{
			name:     "HPUB with missing arguments",
			input:    "HPUB test.subject\r\nheader\r\n\r\nhello\r\n",
			expected: "HPUB test.subject\r\nheader\r\n\r\nhello\r\n",
		},
		{
			name:     "Incomplete PUB message (missing payload)",
			input:    "PUB test.subject 5\r\nhi",
			expected: "PUB test.subject 5\r\nh", // Parser forwards until EOF
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := &NATSProxyParser{}
			input := strings.NewReader(tt.input)
			output := &bytes.Buffer{}

			bytesTransferred, err := parser.ParseAndForward(input, output, "test")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			result := output.String()
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}

			if bytesTransferred != int64(len(tt.expected)) {
				t.Errorf("Expected %d bytes transferred, got %d", len(tt.expected), bytesTransferred)
			}
		})
	}
}

func TestNATSProxyParser_ParseAndForward_CaseInsensitive(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "lowercase pub",
			input:    "pub test.subject 5\r\nhello\r\n",
			expected: "pub test.subject 5\r\nhello\r\n",
		},
		{
			name:     "lowercase hpub",
			input:    "hpub test.subject 2 7\r\n\r\nhello\r\n",
			expected: "hpub test.subject 2 7\r\n\r\nhello\r\n",
		},
		{
			name:     "mixed case PuB",
			input:    "PuB test.subject 5\r\nhello\r\n",
			expected: "PuB test.subject 5\r\nhello\r\n",
		},
		{
			name:     "mixed case HpUb",
			input:    "HpUb test.subject 2 7\r\n\r\nhello\r\n",
			expected: "HpUb test.subject 2 7\r\n\r\nhello\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := &NATSProxyParser{}
			input := strings.NewReader(tt.input)
			output := &bytes.Buffer{}

			bytesTransferred, err := parser.ParseAndForward(input, output, "test")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			result := output.String()
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}

			if bytesTransferred != int64(len(tt.expected)) {
				t.Errorf("Expected %d bytes transferred, got %d", len(tt.expected), bytesTransferred)
			}
		})
	}
}
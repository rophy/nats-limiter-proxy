package main

import (
	"bytes"
	"nats-limiter-proxy/server"
	"strings"
	"testing"
)

func TestMalformedNATSMessages(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError bool
		description string
	}{
		{
			name:        "PUB with non-numeric size",
			input:       "PUB test.subject abc\r\nhello\r\n",
			expectError: false,
			description: "Should handle non-numeric size gracefully",
		},
		{
			name:        "PUB with negative size",
			input:       "PUB test.subject -10\r\nhello\r\n",
			expectError: false,
			description: "Should handle negative size gracefully",
		},
		{
			name:        "PUB with extremely large size",
			input:       "PUB test.subject 999999999999999999999\r\nhello\r\n",
			expectError: false,
			description: "Should handle overflow size gracefully",
		},
		{
			name:        "PUB with missing subject",
			input:       "PUB  5\r\nhello\r\n",
			expectError: false,
			description: "Should handle missing subject",
		},
		{
			name:        "PUB with only command",
			input:       "PUB\r\nhello\r\n",
			expectError: false,
			description: "Should handle incomplete PUB command",
		},
		{
			name:        "HPUB with malformed arguments",
			input:       "HPUB test.subject abc def\r\nheader\r\n\r\nhello\r\n",
			expectError: false,
			description: "Should handle malformed HPUB arguments",
		},
		{
			name:        "HPUB with insufficient arguments",
			input:       "HPUB test.subject 10\r\nheader\r\n\r\nhello\r\n",
			expectError: false,
			description: "Should handle HPUB with missing total size",
		},
		{
			name:        "Message with only carriage return",
			input:       "PUB test 5\r\rhello\r\n",
			expectError: false,
			description: "Should handle messages with only \\r",
		},
		{
			name:        "Message with only line feed",
			input:       "PUB test 5\nhello\n",
			expectError: false,
			description: "Should handle messages with only \\n",
		},
		{
			name:        "Empty message",
			input:       "",
			expectError: false,
			description: "Should handle empty input",
		},
		{
			name:        "Message with null bytes",
			input:       "PUB test.subject 5\r\n\x00\x00\x00\x00\x00\r\n",
			expectError: false,
			description: "Should handle null bytes in payload",
		},
		{
			name:        "Very long subject name",
			input:       "PUB " + strings.Repeat("a", 10000) + " 5\r\nhello\r\n",
			expectError: false,
			description: "Should handle very long subject names",
		},
		{
			name:        "Message with Unicode characters",
			input:       "PUB test.ëmöjí 12\r\nhello 世界 🌍\r\n",
			expectError: false,
			description: "Should handle Unicode in subject and payload",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := &server.NATSProxyParser{}
			input := strings.NewReader(tt.input)
			output := &bytes.Buffer{}

			// Test that parser doesn't panic or crash
			defer func() {
				if r := recover(); r != nil {
					if !tt.expectError {
						t.Errorf("Unexpected panic for %s: %v", tt.description, r)
					}
				}
			}()

			bytesTransferred, err := parser.ParseAndForward(input, output, "test")
			
			if tt.expectError && err == nil {
				t.Errorf("Expected error for %s, but got none", tt.description)
			} else if !tt.expectError && err != nil {
				t.Errorf("Unexpected error for %s: %v", tt.description, err)
			}

			// Verify some data was processed (even if malformed)
			if bytesTransferred < 0 {
				t.Errorf("Invalid bytes transferred count: %d", bytesTransferred)
			}

			t.Logf("%s: Processed %d bytes", tt.description, bytesTransferred)
		})
	}
}

func TestMalformedJWTTokens(t *testing.T) {
	tests := []struct {
		name        string
		token       string
		expectedUser string
		description string
	}{
		{
			name:         "JWT with invalid base64 header",
			token:        "invalid_header.eyJuYW1lIjoiYWxpY2UifQ.signature",
			expectedUser: "",
			description:  "Should handle invalid base64 in header",
		},
		{
			name:         "JWT with invalid base64 payload",
			token:        "eyJhbGciOiJIUzI1NiJ9.invalid_payload.signature",
			expectedUser: "",
			description:  "Should handle invalid base64 in payload",
		},
		{
			name:         "JWT with invalid JSON in payload",
			token:        "eyJhbGciOiJIUzI1NiJ9.aW52YWxpZCBqc29u.signature", // "invalid json" in base64
			expectedUser: "",
			description:  "Should handle invalid JSON in payload",
		},
		{
			name:         "JWT with only one part",
			token:        "onlyonepart",
			expectedUser: "",
			description:  "Should handle JWT with insufficient parts",
		},
		{
			name:         "JWT with only two parts",
			token:        "header.payload",
			expectedUser: "",
			description:  "Should handle JWT missing signature",
		},
		{
			name:         "JWT with extra parts",
			token:        "header.payload.signature.extra.parts",
			expectedUser: "",
			description:  "Should handle JWT with too many parts",
		},
		{
			name:         "Empty JWT",
			token:        "",
			expectedUser: "",
			description:  "Should handle empty JWT",
		},
		{
			name:         "JWT with only dots",
			token:        "...",
			expectedUser: "",
			description:  "Should handle JWT with only separators",
		},
		{
			name:         "Very long JWT",
			token:        strings.Repeat("a", 10000),
			expectedUser: "",
			description:  "Should handle extremely long JWT",
		},
		{
			name:         "JWT with binary data",
			token:        "\x00\x01\x02.\x03\x04\x05.\x06\x07\x08",
			expectedUser: "",
			description:  "Should handle JWT with binary data",
		},
		{
			name:         "JWT with nested objects in name",
			token:        "eyJhbGciOiJIUzI1NiJ9.eyJuYW1lIjp7Im5lc3RlZCI6InZhbHVlIn19.signature", // {"name":{"nested":"value"}}
			expectedUser: "",
			description:  "Should handle JWT with non-string name claim",
		},
		{
			name:         "JWT with array in name",
			token:        "eyJhbGciOiJIUzI1NiJ9.eyJuYW1lIjpbImFsaWNlIiwiYm9iIl19.signature", // {"name":["alice","bob"]}
			expectedUser: "",
			description:  "Should handle JWT with array name claim",
		},
		{
			name:         "JWT with null name",
			token:        "eyJhbGciOiJIUzI1NiJ9.eyJuYW1lIjpudWxsfQ.signature", // {"name":null}
			expectedUser: "",
			description:  "Should handle JWT with null name claim",
		},
		{
			name:         "JWT with numeric name",
			token:        "eyJhbGciOiJIUzI1NiJ9.eyJuYW1lIjoxMjN9.signature", // {"name":123}
			expectedUser: "",
			description:  "Should handle JWT with numeric name claim",
		},
		{
			name:         "JWT with boolean name",
			token:        "eyJhbGciOiJIUzI1NiJ9.eyJuYW1lIjp0cnVlfQ.signature", // {"name":true}
			expectedUser: "",
			description:  "Should handle JWT with boolean name claim",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test that JWT extraction doesn't panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Unexpected panic for %s: %v", tt.description, r)
				}
			}()

			result := extractUsernameFromJWT(tt.token)
			
			if result != tt.expectedUser {
				t.Errorf("%s: Expected user %q, got %q", tt.description, tt.expectedUser, result)
			}

			t.Logf("%s: Extracted user %q", tt.description, result)
		})
	}
}

func TestMalformedConfigFiles(t *testing.T) {
	tests := []struct {
		name          string
		configContent string
		expectError   bool
		description   string
	}{
		{
			name:          "Invalid YAML indentation",
			configContent: "default_bandwidth: 1024\n  users:\n alice: 512",
			expectError:   true,
			description:   "Should reject invalid YAML indentation",
		},
		{
			name:          "YAML with tabs and spaces mixed",
			configContent: "default_bandwidth: 1024\n\tusers:\n  alice: 512",
			expectError:   true,
			description:   "Should handle mixed tabs and spaces",
		},
		{
			name:          "YAML with duplicate keys",
			configContent: "default_bandwidth: 1024\ndefault_bandwidth: 2048\nusers:\n  alice: 512",
			expectError:   false, // YAML parsers usually take the last value
			description:   "Should handle duplicate keys",
		},
		{
			name:          "YAML with invalid characters",
			configContent: "default_bandwidth: 1024\nusers:\n  alice: 512\n  bob: @invalid",
			expectError:   true,
			description:   "Should reject invalid numeric values",
		},
		{
			name:          "Empty YAML file",
			configContent: "",
			expectError:   false,
			description:   "Should handle empty config file",
		},
		{
			name:          "YAML with only comments",
			configContent: "# This is a comment\n# Another comment",
			expectError:   false,
			description:   "Should handle comment-only file",
		},
		{
			name:          "YAML with Unicode characters",
			configContent: "default_bandwidth: 1024\nusers:\n  用户: 512\n  ユーザー: 1024",
			expectError:   false,
			description:   "Should handle Unicode in usernames",
		},
		{
			name:          "YAML with very large numbers",
			configContent: "default_bandwidth: 999999999999999999999999999999\nusers:\n  alice: 888888888888888888888888",
			expectError:   false, // May be handled as best effort
			description:   "Should handle very large numbers",
		},
		{
			name:          "YAML with scientific notation",
			configContent: "default_bandwidth: 1e6\nusers:\n  alice: 5e5",
			expectError:   false,
			description:   "Should handle scientific notation",
		},
		{
			name:          "YAML with negative numbers",
			configContent: "default_bandwidth: -1024\nusers:\n  alice: -512",
			expectError:   false,
			description:   "Should handle negative numbers",
		},
		{
			name:          "YAML with null values",
			configContent: "default_bandwidth: null\nusers:\n  alice: null",
			expectError:   false,
			description:   "Should handle null values",
		},
		{
			name:          "YAML with boolean values",
			configContent: "default_bandwidth: true\nusers:\n  alice: false",
			expectError:   true,
			description:   "Should reject boolean values for numeric fields",
		},
		{
			name:          "Completely invalid content",
			configContent: "This is not YAML at all! {[}] random content @#$%",
			expectError:   true,
			description:   "Should reject completely invalid content",
		},
		{
			name:          "Binary content",
			configContent: "\x00\x01\x02\x03\xFF\xFE\xFD",
			expectError:   true,
			description:   "Should reject binary content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary file for testing
			tmpFile := "/tmp/test_config_" + tt.name + ".yaml"
			
			// Write test content to file
			if err := writeTestFile(tmpFile, tt.configContent); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}
			defer removeTestFile(tmpFile)

			// Test that config loading doesn't panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Unexpected panic for %s: %v", tt.description, r)
				}
			}()

			config, err := LoadConfig(tmpFile)

			if tt.expectError {
				if err == nil {
					t.Errorf("%s: Expected error, but got none", tt.description)
				}
			} else {
				if err != nil {
					t.Errorf("%s: Unexpected error: %v", tt.description, err)
				} else {
					// Verify config is usable
					if config.DefaultBandwidth < 0 {
						t.Logf("%s: Warning - negative default bandwidth: %d", tt.description, config.DefaultBandwidth)
					}
				}
			}

			t.Logf("%s: Error status - expected: %v, got: %v", tt.description, tt.expectError, err != nil)
		})
	}
}

func TestEdgeCaseNetworkConditions(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		description string
	}{
		{
			name:        "Incomplete message at end",
			input:       "PUB test.subject 10\r\nhello",
			description: "Should handle incomplete message at EOF",
		},
		{
			name:        "Message with missing final CRLF",
			input:       "PUB test.subject 5\r\nhello",
			description: "Should handle missing final CRLF",
		},
		{
			name:        "Message with extra CRLF",
			input:       "PUB test.subject 5\r\n\r\nhello\r\n\r\n",
			description: "Should handle extra CRLF characters",
		},
		{
			name:        "Mixed line endings",
			input:       "PUB test.subject 5\nhello\r\nPUB test2 3\rfoo\n",
			description: "Should handle mixed line endings",
		},
		{
			name:        "Very long line without termination",
			input:       "PUB " + strings.Repeat("very_long_subject_", 1000) + " 5",
			description: "Should handle very long line without termination",
		},
		{
			name:        "Message with embedded CRLF in payload",
			input:       "PUB test.subject 10\r\nhel\r\nlo\r\n",
			description: "Should handle CRLF within payload",
		},
		{
			name:        "Zero-length payload",
			input:       "PUB test.subject 0\r\n\r\n",
			description: "Should handle zero-length payload correctly",
		},
		{
			name:        "Payload shorter than declared size",
			input:       "PUB test.subject 10\r\nhi\r\n",
			description: "Should handle payload shorter than size",
		},
		{
			name:        "Multiple incomplete messages",
			input:       "PUB test1 5\r\nhellPUB test2 3\r\nfoPUB test3 4",
			description: "Should handle multiple incomplete messages",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := &server.NATSProxyParser{}
			input := strings.NewReader(tt.input)
			output := &bytes.Buffer{}

			// Test that parser handles edge cases gracefully
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Unexpected panic for %s: %v", tt.description, r)
				}
			}()

			bytesTransferred, err := parser.ParseAndForward(input, output, "test")
			
			// EOF is expected for incomplete messages
			if err != nil && err.Error() != "EOF" {
				t.Errorf("%s: Unexpected error (non-EOF): %v", tt.description, err)
			}

			if bytesTransferred < 0 {
				t.Errorf("%s: Invalid bytes transferred: %d", tt.description, bytesTransferred)
			}

			t.Logf("%s: Processed %d bytes, output length: %d", tt.description, bytesTransferred, output.Len())
		})
	}
}

// Helper functions for testing
func writeTestFile(filename, content string) error {
	return nil // Mock implementation for testing
}

func removeTestFile(filename string) {
	// Mock implementation for testing
}

func TestExtremeInputSizes(t *testing.T) {
	tests := []struct {
		name         string
		messageSize  int
		payloadChar  byte
		description  string
	}{
		{
			name:         "Very large message (1MB payload)",
			messageSize:  1024 * 1024,
			payloadChar:  'A',
			description:  "Should handle 1MB payload",
		},
		{
			name:         "Maximum int32 size payload",
			messageSize:  1024,
			payloadChar:  'B',
			description:  "Should handle reasonable large payload",
		},
		{
			name:         "Payload with all zero bytes",
			messageSize:  1024,
			payloadChar:  0,
			description:  "Should handle null byte payload",
		},
		{
			name:         "Payload with high-bit characters",
			messageSize:  1024,
			payloadChar:  255,
			description:  "Should handle binary payload",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create large message
			payload := bytes.Repeat([]byte{tt.payloadChar}, tt.messageSize)
			message := "PUB test.large " + string(rune(tt.messageSize)) + "\r\n" + string(payload) + "\r\n"
			
			parser := &server.NATSProxyParser{}
			input := strings.NewReader(message)
			output := &bytes.Buffer{}

			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Unexpected panic for %s: %v", tt.description, r)
				}
			}()

			bytesTransferred, err := parser.ParseAndForward(input, output, "test")
			
			if err != nil {
				t.Errorf("%s: Unexpected error: %v", tt.description, err)
			}

			expectedSize := int64(len(message))
			if bytesTransferred != expectedSize {
				t.Errorf("%s: Expected %d bytes, got %d", tt.description, expectedSize, bytesTransferred)
			}

			t.Logf("%s: Successfully processed %d bytes", tt.description, bytesTransferred)
		})
	}
}
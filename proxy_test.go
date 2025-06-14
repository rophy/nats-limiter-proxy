package main

import (
	"io/ioutil"
	"os"
	"testing"
)

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
		{
			name:     "Malformed JWT (missing parts)",
			token:    "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJuYW1lIjoiYWxpY2UifQ",
			expected: "", // Missing signature makes it invalid
		},
		{
			name:     "JWT with empty name and sub claims",
			token:    "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJuYW1lIjoiIiwic3ViIjoiIn0.invalid_signature",
			expected: "",
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

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name           string
		configContent  string
		expectedError  bool
		expectedDefault int64
		expectedUsers  map[string]int64
	}{
		{
			name: "Valid config with users",
			configContent: `default_bandwidth: 5242880
users:
  alice: 1048576
  bob: 2097152`,
			expectedError:   false,
			expectedDefault: 5242880,
			expectedUsers: map[string]int64{
				"alice": 1048576,
				"bob":   2097152,
			},
		},
		{
			name: "Config with no default (should use 10MB default)",
			configContent: `users:
  charlie: 3145728`,
			expectedError:   false,
			expectedDefault: 10 * 1024 * 1024, // 10MB
			expectedUsers: map[string]int64{
				"charlie": 3145728,
			},
		},
		{
			name: "Empty config (should use defaults)",
			configContent: `{}`, // Valid empty YAML
			expectedError:   false,
			expectedDefault: 10 * 1024 * 1024, // 10MB
			expectedUsers:  nil,
		},
		{
			name: "Config with zero default (should use 10MB default)",
			configContent: `default_bandwidth: 0
users:
  diana: 1048576`,
			expectedError:   false,
			expectedDefault: 10 * 1024 * 1024, // 10MB
			expectedUsers: map[string]int64{
				"diana": 1048576,
			},
		},
		{
			name:          "Invalid YAML syntax",
			configContent: `default_bandwidth: invalid_yaml: [`,
			expectedError: true,
		},
		{
			name: "Config with negative bandwidth",
			configContent: `default_bandwidth: -1024
users:
  eve: -2048`,
			expectedError:   false,
			expectedDefault: -1024, // Negative values are preserved as-is
			expectedUsers: map[string]int64{
				"eve": -2048, // Negative values are preserved for users
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary config file
			tmpFile, err := ioutil.TempFile("", "config_test_*.yaml")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpFile.Name())

			// Write test config content
			if _, err := tmpFile.WriteString(tt.configContent); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			tmpFile.Close()

			// Load config
			config, err := LoadConfig(tmpFile.Name())

			if tt.expectedError {
				if err == nil {
					t.Errorf("Expected error, but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Check default bandwidth
			if config.DefaultBandwidth != tt.expectedDefault {
				t.Errorf("Expected default bandwidth %d, got %d", tt.expectedDefault, config.DefaultBandwidth)
			}

			// Check users
			if len(config.Users) != len(tt.expectedUsers) {
				t.Errorf("Expected %d users, got %d", len(tt.expectedUsers), len(config.Users))
			}

			for user, expectedBW := range tt.expectedUsers {
				if actualBW, exists := config.Users[user]; !exists {
					t.Errorf("Expected user %s not found in config", user)
				} else if actualBW != expectedBW {
					t.Errorf("Expected bandwidth %d for user %s, got %d", expectedBW, user, actualBW)
				}
			}
		})
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("nonexistent_file.yaml")
	if err == nil {
		t.Errorf("Expected error for nonexistent file, but got none")
	}
}

func TestGetBandwidthForUser(t *testing.T) {
	// Set up test config
	testConfig := &Config{
		DefaultBandwidth: 10 * 1024 * 1024, // 10MB
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,  // 5MB
			"bob":   2 * 1024 * 1024,  // 2MB
			"charlie": 0,              // 0 bytes
			"diana": -1024,            // Negative value
		},
	}

	// Temporarily replace global config
	originalConfig := config
	config = testConfig
	defer func() { config = originalConfig }()

	tests := []struct {
		name     string
		user     string
		expected int64
	}{
		{
			name:     "Existing user alice",
			user:     "alice",
			expected: 5 * 1024 * 1024,
		},
		{
			name:     "Existing user bob",
			user:     "bob",
			expected: 2 * 1024 * 1024,
		},
		{
			name:     "User with zero bandwidth",
			user:     "charlie",
			expected: 0,
		},
		{
			name:     "User with negative bandwidth",
			user:     "diana",
			expected: -1024,
		},
		{
			name:     "Nonexistent user",
			user:     "eve",
			expected: 10 * 1024 * 1024, // Default
		},
		{
			name:     "Empty username",
			user:     "",
			expected: 10 * 1024 * 1024, // Default
		},
		{
			name:     "Case sensitive check",
			user:     "ALICE",
			expected: 10 * 1024 * 1024, // Default (case sensitive)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getBandwidthForUser(tt.user)
			if result != tt.expected {
				t.Errorf("Expected %d for user %q, got %d", tt.expected, tt.user, result)
			}
		})
	}
}

func TestGetBandwidthForUser_NilUsersMap(t *testing.T) {
	// Test with nil users map
	testConfig := &Config{
		DefaultBandwidth: 8 * 1024 * 1024, // 8MB
		Users:            nil,
	}

	originalConfig := config
	config = testConfig
	defer func() { config = originalConfig }()

	result := getBandwidthForUser("alice")
	expected := int64(8 * 1024 * 1024)
	if result != expected {
		t.Errorf("Expected %d for user with nil users map, got %d", expected, result)
	}
}

func TestGetBandwidthForUser_EmptyUsersMap(t *testing.T) {
	// Test with empty users map
	testConfig := &Config{
		DefaultBandwidth: 12 * 1024 * 1024, // 12MB
		Users:            make(map[string]int64),
	}

	originalConfig := config
	config = testConfig
	defer func() { config = originalConfig }()

	result := getBandwidthForUser("alice")
	expected := int64(12 * 1024 * 1024)
	if result != expected {
		t.Errorf("Expected %d for user with empty users map, got %d", expected, result)
	}
}
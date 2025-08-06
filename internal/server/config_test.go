package server

import (
	"testing"
)

func TestConfig_ValidateLimitsConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    Config
		expectErr bool
		expected  *LimitsConfig
	}{
		{
			name: "Valid config with defaults and users",
			config: Config{
				Limits: &LimitsConfig{
					Defaults: &UserLimits{BPSLocal: 1000, BPSGlobal: 2000},
					Users: []*UserLimit{
						{User: "alice", BPSLocal: 5000, BPSGlobal: 10000},
					},
				},
			},
			expectErr: false,
			expected: &LimitsConfig{
				Defaults: &UserLimits{BPSLocal: 1000, BPSGlobal: 2000},
				Users: []*UserLimit{
					{User: "alice", BPSLocal: 5000, BPSGlobal: 10000},
				},
			},
		},
		{
			name:      "Missing limits config",
			config:    Config{},
			expectErr: true,
		},
		{
			name: "Missing defaults - should create them",
			config: Config{
				Limits: &LimitsConfig{
					Users: []*UserLimit{
						{User: "alice", BPSLocal: 5000, BPSGlobal: 10000},
					},
				},
			},
			expectErr: false,
			expected: &LimitsConfig{
				Defaults: &UserLimits{BPSLocal: 102400, BPSGlobal: 102400},
				Users: []*UserLimit{
					{User: "alice", BPSLocal: 5000, BPSGlobal: 10000},
				},
			},
		},
		{
			name: "Zero values should get defaults",
			config: Config{
				Limits: &LimitsConfig{
					Defaults: &UserLimits{BPSLocal: 0, BPSGlobal: 0},
					Users: []*UserLimit{
						{User: "alice", BPSLocal: 0, BPSGlobal: 0},
					},
				},
			},
			expectErr: false,
			expected: &LimitsConfig{
				Defaults: &UserLimits{BPSLocal: 102400, BPSGlobal: 102400},
				Users: []*UserLimit{
					{User: "alice", BPSLocal: 102400, BPSGlobal: 102400},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.config
			err := cfg.validateLimitsConfig()
			
			if tt.expectErr {
				if err == nil {
					t.Fatal("Expected error but got none")
				}
				return
			}
			
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Check that Limits config exists
			if cfg.Limits == nil {
				t.Fatal("Expected Limits config to exist")
			}

			// Check defaults
			if cfg.Limits.Defaults == nil {
				t.Fatal("Expected Defaults to be set")
			}
			if cfg.Limits.Defaults.BPSLocal != tt.expected.Defaults.BPSLocal {
				t.Errorf("Expected BPSLocal=%d, got %d", tt.expected.Defaults.BPSLocal, cfg.Limits.Defaults.BPSLocal)
			}
			if cfg.Limits.Defaults.BPSGlobal != tt.expected.Defaults.BPSGlobal {
				t.Errorf("Expected BPSGlobal=%d, got %d", tt.expected.Defaults.BPSGlobal, cfg.Limits.Defaults.BPSGlobal)
			}

			// Check users count
			if len(cfg.Limits.Users) != len(tt.expected.Users) {
				t.Errorf("Expected %d users, got %d", len(tt.expected.Users), len(cfg.Limits.Users))
				return
			}

			// Check each user
			for i, expectedUser := range tt.expected.Users {
				actualUser := cfg.Limits.Users[i]
				if actualUser.User != expectedUser.User {
					t.Errorf("User %d: expected %s, got %s", i, expectedUser.User, actualUser.User)
				}
				if actualUser.BPSLocal != expectedUser.BPSLocal {
					t.Errorf("User %s: expected BPSLocal=%d, got %d", expectedUser.User, expectedUser.BPSLocal, actualUser.BPSLocal)
				}
				if actualUser.BPSGlobal != expectedUser.BPSGlobal {
					t.Errorf("User %s: expected BPSGlobal=%d, got %d", expectedUser.User, expectedUser.BPSGlobal, actualUser.BPSGlobal)
				}
			}
		})
	}
}

func TestLimitsConfig_GetUserLimits(t *testing.T) {
	limitsConfig := &LimitsConfig{
		Defaults: &UserLimits{BPSLocal: 1000, BPSGlobal: 2000},
		Users: []*UserLimit{
			{User: "alice", BPSLocal: 5000, BPSGlobal: 10000},
			{User: "UC6NLCN7AS34YOJVCYD4PJ3QB7QGLYG5B5IMBT25VW5K4TNUJODM7BOX", BPSLocal: 3000, BPSGlobal: 6000}, // NKey user
			{User: "s3cr3t", BPSLocal: 500, BPSGlobal: 1000}, // Token user
		},
	}

	tests := []struct {
		name     string
		username string
		expected *UserLimits
	}{
		{
			name:     "Existing user - alice",
			username: "alice",
			expected: &UserLimits{BPSLocal: 5000, BPSGlobal: 10000},
		},
		{
			name:     "NKey user",
			username: "UC6NLCN7AS34YOJVCYD4PJ3QB7QGLYG5B5IMBT25VW5K4TNUJODM7BOX",
			expected: &UserLimits{BPSLocal: 3000, BPSGlobal: 6000},
		},
		{
			name:     "Token user",
			username: "s3cr3t",
			expected: &UserLimits{BPSLocal: 500, BPSGlobal: 1000},
		},
		{
			name:     "Non-existent user - returns defaults",
			username: "unknown",
			expected: &UserLimits{BPSLocal: 1000, BPSGlobal: 2000},
		},
		{
			name:     "Empty username - returns defaults",
			username: "",
			expected: &UserLimits{BPSLocal: 1000, BPSGlobal: 2000},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := limitsConfig.GetUserLimits(tt.username)
			if result == nil {
				t.Fatal("Expected non-nil result")
			}
			if result.BPSLocal != tt.expected.BPSLocal {
				t.Errorf("Expected BPSLocal=%d, got %d", tt.expected.BPSLocal, result.BPSLocal)
			}
			if result.BPSGlobal != tt.expected.BPSGlobal {
				t.Errorf("Expected BPSGlobal=%d, got %d", tt.expected.BPSGlobal, result.BPSGlobal)
			}
		})
	}
}
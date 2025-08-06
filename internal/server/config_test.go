package server

import (
	"testing"
)

func TestConfig_NormalizeLimits(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		expected *LimitsConfig
	}{
		{
			name: "New format already exists - no migration needed",
			config: Config{
				Limits: &LimitsConfig{
					Defaults: &UserLimits{BPSLocal: 1000, BPSGlobal: 2000},
					Users: []*UserLimit{
						{User: "alice", BPSLocal: 5000, BPSGlobal: 10000},
					},
				},
			},
			expected: &LimitsConfig{
				Defaults: &UserLimits{BPSLocal: 1000, BPSGlobal: 2000},
				Users: []*UserLimit{
					{User: "alice", BPSLocal: 5000, BPSGlobal: 10000},
				},
			},
		},
		{
			name: "Migrate from bandwidth config",
			config: Config{
				Bandwidth: &BandwidthConfig{
					DefaultLocal:  1024,
					DefaultGlobal: 2048,
					Users: map[string]*UserBandwidth{
						"alice": {Local: 5120, Global: 10240},
						"bob":   {Local: 2048, Global: 0}, // Global defaults to Local
					},
				},
			},
			expected: &LimitsConfig{
				Defaults: &UserLimits{BPSLocal: 1024, BPSGlobal: 2048},
				Users: []*UserLimit{
					{User: "alice", BPSLocal: 5120, BPSGlobal: 10240},
					{User: "bob", BPSLocal: 2048, BPSGlobal: 2048}, // Global defaulted to Local
				},
			},
		},
		{
			name: "Migrate from legacy format",
			config: Config{
				DefaultBandwidth: 1024,
				Users: map[string]int64{
					"alice": 5120,
					"bob":   2048,
				},
			},
			expected: &LimitsConfig{
				Defaults: &UserLimits{BPSLocal: 1024, BPSGlobal: 1024},
				Users: []*UserLimit{
					{User: "alice", BPSLocal: 5120, BPSGlobal: 5120},
					{User: "bob", BPSLocal: 2048, BPSGlobal: 2048},
				},
			},
		},
		{
			name: "No config - create defaults",
			config: Config{},
			expected: &LimitsConfig{
				Defaults: &UserLimits{BPSLocal: 102400, BPSGlobal: 102400},
				Users:    []*UserLimit{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make a copy to avoid modifying the test case
			cfg := tt.config
			cfg.NormalizeLimits()

			// Check that Limits config exists
			if cfg.Limits == nil {
				t.Fatal("Expected Limits config to be created")
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

			// Check each user (order doesn't matter for map migration)
			userMap := make(map[string]*UserLimit)
			for _, user := range cfg.Limits.Users {
				userMap[user.User] = user
			}

			for _, expectedUser := range tt.expected.Users {
				actualUser, exists := userMap[expectedUser.User]
				if !exists {
					t.Errorf("Expected user %s not found", expectedUser.User)
					continue
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
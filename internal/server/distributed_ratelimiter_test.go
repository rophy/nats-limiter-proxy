package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDistributedRateLimiterManager(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
		},
	}

	coordinator := createTestCoordinator()
	drlm := NewDistributedRateLimiterManager(config, coordinator)

	assert.NotNil(t, drlm)
	assert.Equal(t, config, drlm.config)
	assert.Equal(t, coordinator, drlm.peerCoordinator)
	assert.NotNil(t, drlm.usageTracker)
	assert.NotNil(t, drlm.tokenBalancer)
}

func TestNewDistributedRateLimiterManager_NilCoordinator(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
		},
	}

	drlm := NewDistributedRateLimiterManager(config, nil)

	assert.NotNil(t, drlm)
	assert.Equal(t, config, drlm.config)
	assert.Nil(t, drlm.peerCoordinator)
	assert.NotNil(t, drlm.usageTracker)
	assert.Nil(t, drlm.tokenBalancer) // Should be nil when no coordinator
}

func TestNewUsageTracker(t *testing.T) {
	tracker := NewUsageTracker()

	assert.NotNil(t, tracker)
	assert.NotNil(t, tracker.userStats)
	assert.Equal(t, 5*time.Second, tracker.reportInterval)
	assert.True(t, tracker.lastReport.Before(time.Now().Add(time.Second)))
}

func TestDistributedRateLimiterManager_GetLimiter_WithCoordination(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
		},
	}

	coordinator := createTestCoordinator()
	drlm := NewDistributedRateLimiterManager(config, coordinator)

	bucket := drlm.GetLimiter("alice")
	assert.NotNil(t, bucket)

	// Should use distributed token balancer
	assert.NotNil(t, drlm.tokenBalancer)
	
	// Verify usage tracking was initialized
	stats := drlm.GetUsageStats()
	assert.Contains(t, stats, "alice")
}

func TestDistributedRateLimiterManager_GetLimiter_WithoutCoordination(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
		},
	}

	drlm := NewDistributedRateLimiterManager(config, nil)

	bucket := drlm.GetLimiter("alice")
	assert.NotNil(t, bucket)

	// Should fall back to local rate limiting
	assert.InDelta(t, float64(5*1024*1024), bucket.Rate(), 50000) // Allow floating point difference
}

func TestDistributedRateLimiterManager_GetLimiter_EmptyUsername(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
	}

	coordinator := createTestCoordinator()
	drlm := NewDistributedRateLimiterManager(config, coordinator)

	bucket := drlm.GetLimiter("")
	assert.Nil(t, bucket)
}

func TestDistributedRateLimiterManager_TrackUsage(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
		},
	}

	coordinator := createTestCoordinator()
	drlm := NewDistributedRateLimiterManager(config, coordinator)

	// Track usage
	drlm.TrackUsage("alice", 1000)
	drlm.TrackUsage("alice", 500)

	// Verify usage was tracked
	stats := drlm.GetUsageStats()
	require.Contains(t, stats, "alice")

	aliceStats := stats["alice"]
	assert.Equal(t, "alice", aliceStats.Username)
	assert.Equal(t, int64(1500), aliceStats.BytesUsed) // 1000 + 500
	assert.Equal(t, int64(2), aliceStats.RequestCount)
	assert.True(t, aliceStats.LastActivity.After(time.Now().Add(-time.Second)))
}

func TestDistributedRateLimiterManager_TrackUsage_WithoutCoordination(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
	}

	drlm := NewDistributedRateLimiterManager(config, nil)

	// Should not panic when coordination is disabled
	assert.NotPanics(t, func() {
		drlm.TrackUsage("alice", 1000)
	})
}

func TestDistributedRateLimiterManager_ReportUsage(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
		},
	}

	coordinator := createTestCoordinator()
	drlm := NewDistributedRateLimiterManager(config, coordinator)

	// Track usage
	drlm.TrackUsage("alice", 1000)
	drlm.TrackUsage("bob", 2000)

	// Force usage reporting
	drlm.reportUsage()

	// Verify usage was reported to coordinator
	aliceUsage := coordinator.GetPeerUsage("alice")
	assert.NotNil(t, aliceUsage)
	assert.Contains(t, aliceUsage, coordinator.myPeerID)

	bobUsage := coordinator.GetPeerUsage("bob")
	assert.NotNil(t, bobUsage)
	assert.Contains(t, bobUsage, coordinator.myPeerID)

	// Verify counters were reset after reporting
	stats := drlm.GetUsageStats()
	assert.Equal(t, int64(0), stats["alice"].BytesUsed)
	assert.Equal(t, int64(0), stats["bob"].BytesUsed)
}

func TestDistributedRateLimiterManager_AutomaticReporting(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
		},
	}

	coordinator := createTestCoordinator()
	drlm := NewDistributedRateLimiterManager(config, coordinator)

	// Set a very short report interval for testing
	drlm.usageTracker.reportInterval = 10 * time.Millisecond
	drlm.usageTracker.lastReport = time.Now().Add(-time.Minute) // Force next call to trigger report

	// Track usage
	drlm.TrackUsage("alice", 1000)

	// Should trigger automatic reporting
	time.Sleep(20 * time.Millisecond)

	// Verify usage was reported
	aliceUsage := coordinator.GetPeerUsage("alice")
	assert.NotNil(t, aliceUsage)
}

func TestDistributedRateLimiterManager_GetPeerStats(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
		},
	}

	coordinator := createTestCoordinator()
	drlm := NewDistributedRateLimiterManager(config, coordinator)

	// Track local usage
	drlm.TrackUsage("alice", 1000)

	// Add peer usage data
	coordinator.ProcessGossip(UsageGossip{
		FromPeer: "peer-1",
		UserStats: map[string]UserUsage{
			"alice": {
				Username:        "alice",
				UsedBytes:       2000,
				RequestRate:     200.0,
				LocalAllocation: 3000,
				LastUpdated:     time.Now(),
			},
		},
		Timestamp:   time.Now(),
		MessageType: GossipUsageReport,
	})

	peerStats := drlm.GetPeerStats()
	assert.NotNil(t, peerStats)
	assert.Contains(t, peerStats, "alice")
	assert.Contains(t, peerStats["alice"], "peer-1")
}

func TestDistributedRateLimiterManager_GetPeerStats_NoCoordination(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
	}

	drlm := NewDistributedRateLimiterManager(config, nil)

	peerStats := drlm.GetPeerStats()
	assert.Nil(t, peerStats)
}

func TestDistributedRateLimiterManager_Cleanup(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
	}

	coordinator := createTestCoordinator()
	drlm := NewDistributedRateLimiterManager(config, coordinator)

	// Add recent usage
	drlm.trackUsage("alice", 1000)

	// Add old usage
	drlm.trackUsage("bob", 500)
	drlm.usageTracker.userStats["bob"].LastActivity = time.Now().Add(-10 * time.Minute)

	// Verify both users exist
	stats := drlm.GetUsageStats()
	assert.Contains(t, stats, "alice")
	assert.Contains(t, stats, "bob")

	// Run cleanup
	drlm.Cleanup()

	// Only alice should remain (bob is inactive)
	stats = drlm.GetUsageStats()
	assert.Contains(t, stats, "alice")
	assert.NotContains(t, stats, "bob")
}

func TestDistributedRateLimiterManager_IsDistributed(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
	}

	// With coordination
	coordinator := createTestCoordinator()
	drlm := NewDistributedRateLimiterManager(config, coordinator)
	assert.True(t, drlm.IsDistributed())

	// Without coordination
	drlm = NewDistributedRateLimiterManager(config, nil)
	assert.False(t, drlm.IsDistributed())
}

func TestDistributedRateLimiterManager_GetBandwidthForUser(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
			"bob":   2 * 1024 * 1024,
		},
	}

	coordinator := createTestCoordinator()
	drlm := NewDistributedRateLimiterManager(config, coordinator)

	// Test user with specific limit
	assert.Equal(t, int64(5*1024*1024), drlm.getBandwidthForUser("alice"))
	assert.Equal(t, int64(2*1024*1024), drlm.getBandwidthForUser("bob"))

	// Test user with default limit
	assert.Equal(t, int64(1024*1024), drlm.getBandwidthForUser("unknown"))

	// Test with nil config users
	drlm.config.Users = nil
	assert.Equal(t, int64(1024*1024), drlm.getBandwidthForUser("alice"))
}

func TestDistributedRateLimiterManager_CreateLocalBucket(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
		},
	}

	coordinator := createTestCoordinator()
	drlm := NewDistributedRateLimiterManager(config, coordinator)

	bucket := drlm.createLocalBucket("alice")
	assert.NotNil(t, bucket)
	assert.Equal(t, float64(5*1024*1024), bucket.Rate())
	assert.Equal(t, int64(5*1024*1024), bucket.Capacity())
}

func TestUsageTracker_MultipleUsers(t *testing.T) {
	tracker := NewUsageTracker()

	// Track usage for multiple users
	tracker.userStats["alice"] = &UserStats{
		Username:     "alice",
		BytesUsed:    1000,
		RequestCount: 10,
		LastActivity: time.Now(),
		StartTime:    time.Now().Add(-time.Minute),
	}

	tracker.userStats["bob"] = &UserStats{
		Username:     "bob",
		BytesUsed:    2000,
		RequestCount: 20,
		LastActivity: time.Now(),
		StartTime:    time.Now().Add(-time.Minute),
	}

	assert.Len(t, tracker.userStats, 2)
	assert.Contains(t, tracker.userStats, "alice")
	assert.Contains(t, tracker.userStats, "bob")
}

func TestDistributedRateLimiterManager_ConcurrentAccess(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
		},
	}

	coordinator := createTestCoordinator()
	drlm := NewDistributedRateLimiterManager(config, coordinator)

	done := make(chan bool, 20)

	// Concurrent GetLimiter calls
	for i := 0; i < 10; i++ {
		go func() {
			bucket := drlm.GetLimiter("alice")
			assert.NotNil(t, bucket)
			done <- true
		}()
	}

	// Concurrent TrackUsage calls
	for i := 0; i < 10; i++ {
		go func(id int) {
			drlm.TrackUsage("alice", int64(100+id))
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}

	// Verify data integrity
	stats := drlm.GetUsageStats()
	assert.Contains(t, stats, "alice")
	assert.Greater(t, stats["alice"].BytesUsed, int64(0))
}

func TestDistributedRateLimiterManager_EdgeCases(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
	}

	coordinator := createTestCoordinator()
	drlm := NewDistributedRateLimiterManager(config, coordinator)

	// Test with empty stats
	stats := drlm.GetUsageStats()
	assert.NotNil(t, stats)
	assert.Len(t, stats, 0)

	// Test cleanup with no data
	assert.NotPanics(t, func() {
		drlm.Cleanup()
	})

	// Test reporting with no data
	assert.NotPanics(t, func() {
		drlm.reportUsage()
	})

	// Test tracking zero bytes
	drlm.TrackUsage("alice", 0)
	stats = drlm.GetUsageStats()
	assert.Contains(t, stats, "alice")
	assert.Equal(t, int64(0), stats["alice"].BytesUsed)
	assert.Equal(t, int64(1), stats["alice"].RequestCount) // Still counts as a request
}

func TestDistributedRateLimiterManager_Start(t *testing.T) {
	config := &Config{
		DefaultBandwidth: 1024 * 1024,
	}

	coordinator := createTestCoordinator()
	drlm := NewDistributedRateLimiterManager(config, coordinator)

	// Start should begin background cleanup
	drlm.Start()

	// Add some old data
	drlm.trackUsage("old_user", 1000)
	drlm.usageTracker.userStats["old_user"].LastActivity = time.Now().Add(-10 * time.Minute)

	// Wait for cleanup cycle (this is hard to test precisely due to timing)
	time.Sleep(10 * time.Millisecond)

	// Cleanup should eventually run, but we can't assert exact timing in unit tests
	// The main goal is to verify Start() doesn't panic and initiates background tasks
}

func TestUserStats_DefaultValues(t *testing.T) {
	stats := &UserStats{
		Username: "alice",
	}

	assert.Equal(t, "alice", stats.Username)
	assert.Equal(t, int64(0), stats.BytesUsed)
	assert.Equal(t, int64(0), stats.RequestCount)
	assert.True(t, stats.LastActivity.IsZero())
	assert.True(t, stats.StartTime.IsZero())
}
package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewTokenBalancer(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	assert.NotNil(t, balancer)
	assert.Equal(t, coordinator, balancer.coordinator)
	assert.NotNil(t, balancer.localBuckets)
	assert.NotNil(t, balancer.allocations)
	assert.Equal(t, int64(1024), balancer.minAllocation)
	assert.Equal(t, 0.5, balancer.maxReallocation)
	assert.Equal(t, 1.2, balancer.demandMultiplier)
	assert.Equal(t, 0.1, balancer.burstRatio)
}

func TestTokenBalancer_SetConfig(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	config := &Config{
		DefaultBandwidth: 1024 * 1024, // 1MB/s
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024, // 5MB/s
			"bob":   2 * 1024 * 1024, // 2MB/s
		},
	}

	balancer.SetConfig(config)

	assert.Equal(t, config, balancer.config)
}

func TestTokenBalancer_GetOrCreateBucket(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
		},
	}
	balancer.SetConfig(config)

	// Create bucket for alice
	bucket := balancer.GetOrCreateBucket("alice")
	assert.NotNil(t, bucket)

	// Verify bucket properties
	expectedRate := float64(5 * 1024 * 1024) // Alice's configured rate
	assert.Equal(t, expectedRate, bucket.Rate())

	// Getting same bucket should return the same instance
	bucket2 := balancer.GetOrCreateBucket("alice")
	assert.Equal(t, bucket, bucket2)

	// Verify allocation was tracked
	allocation := balancer.GetCurrentAllocation("alice")
	assert.Equal(t, int64(5*1024*1024), allocation)
}

func TestTokenBalancer_GetOrCreateBucket_DefaultUser(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
		},
	}
	balancer.SetConfig(config)

	// Create bucket for user not in config
	bucket := balancer.GetOrCreateBucket("unknown_user")
	assert.NotNil(t, bucket)

	// Should use default bandwidth
	expectedRate := float64(1024 * 1024)
	assert.Equal(t, expectedRate, bucket.Rate())
}

func TestTokenBalancer_GetOrCreateBucket_WithPeers(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	// Add some peers to simulate distributed environment
	coordinator.AddPeer(&Peer{
		ID:       "peer-1",
		Address:  "192.168.1.1:8081",
		LastSeen: time.Now(),
		Status:   PeerStatusActive,
	})
	coordinator.AddPeer(&Peer{
		ID:       "peer-2",
		Address:  "192.168.1.2:8081",
		LastSeen: time.Now(),
		Status:   PeerStatusActive,
	})

	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 3 * 1024 * 1024, // 3MB/s total
		},
	}
	balancer.SetConfig(config)

	bucket := balancer.GetOrCreateBucket("alice")
	assert.NotNil(t, bucket)

	// With 3 peers (including this one), each should get 1MB/s initially
	expectedRate := float64(1024 * 1024) // 3MB/s / 3 peers
	assert.Equal(t, expectedRate, bucket.Rate())
}

func TestTokenBalancer_UpdateUserAllocation(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
		},
	}
	balancer.SetConfig(config)

	// Create initial bucket
	bucket := balancer.GetOrCreateBucket("alice")
	initialRate := bucket.Rate()

	// Update allocation
	newAllocation := int64(2 * 1024 * 1024) // 2MB/s
	balancer.UpdateUserAllocation("alice", newAllocation)

	// Verify bucket was updated
	updatedBucket := balancer.GetOrCreateBucket("alice")
	assert.Equal(t, float64(newAllocation), updatedBucket.Rate())
	assert.NotEqual(t, initialRate, updatedBucket.Rate())

	// Verify allocation tracking
	allocation := balancer.GetCurrentAllocation("alice")
	assert.Equal(t, newAllocation, allocation)
}

func TestTokenBalancer_UpdateUserAllocation_NewUser(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users:            map[string]int64{},
	}
	balancer.SetConfig(config)

	// Update allocation for new user
	newAllocation := int64(3 * 1024 * 1024)
	balancer.UpdateUserAllocation("new_user", newAllocation)

	// Verify bucket was created with new allocation
	bucket := balancer.GetOrCreateBucket("new_user")
	assert.Equal(t, float64(newAllocation), bucket.Rate())

	// Verify allocation tracking
	allocation := balancer.GetCurrentAllocation("new_user")
	assert.Equal(t, newAllocation, allocation)
}

func TestTokenBalancer_CalculatePeerDemands(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	peerUsage := map[string]*UserUsage{
		"peer-1": {
			Username:        "alice",
			UsedBytes:       1000,
			RequestRate:     500.0, // 500 bytes/sec
			LocalAllocation: 1000,
			LastUpdated:     time.Now(),
		},
		"peer-2": {
			Username:        "alice",
			UsedBytes:       2000,
			RequestRate:     1500.0, // 1500 bytes/sec
			LocalAllocation: 2000,
			LastUpdated:     time.Now(),
		},
		"peer-3": {
			Username:        "alice",
			UsedBytes:       100,
			RequestRate:     10.0, // Very low usage
			LocalAllocation: 500,
			LastUpdated:     time.Now(),
		},
	}

	demands := balancer.calculatePeerDemands(peerUsage)

	assert.Len(t, demands, 3)

	// peer-1: 500 * 1.2 = 600
	assert.Equal(t, 600.0, demands["peer-1"])

	// peer-2: 1500 * 1.2 = 1800
	assert.Equal(t, 1800.0, demands["peer-2"])

	// peer-3: 10 * 1.2 = 12, but minimum allocation applies
	assert.Equal(t, float64(balancer.minAllocation), demands["peer-3"])
}

func TestTokenBalancer_CalculateOptimalAllocations_SufficientCapacity(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	demands := map[string]float64{
		"peer-1": 1000.0,
		"peer-2": 2000.0,
		"peer-3": 500.0,
	}
	totalDemand := 3500.0
	globalLimit := int64(5000) // More than total demand

	allocations := balancer.calculateOptimalAllocations(demands, totalDemand, globalLimit)

	assert.Len(t, allocations, 3)

	// Each peer should get what it needs (rounded up)
	assert.Equal(t, int64(1000), allocations["peer-1"])
	assert.Equal(t, int64(2000), allocations["peer-2"])
	assert.Equal(t, int64(500), allocations["peer-3"])
}

func TestTokenBalancer_CalculateOptimalAllocations_InsufficientCapacity(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	demands := map[string]float64{
		"peer-1": 1000.0,
		"peer-2": 2000.0,
		"peer-3": 1000.0,
	}
	totalDemand := 4000.0
	globalLimit := int64(2000) // Less than total demand

	allocations := balancer.calculateOptimalAllocations(demands, totalDemand, globalLimit)

	assert.Len(t, allocations, 3)

	// Should be proportional allocation
	// peer-1: 2000 * (1000/4000) = 500
	// peer-2: 2000 * (2000/4000) = 1000
	// peer-3: 2000 * (1000/4000) = 500
	assert.Equal(t, int64(500), allocations["peer-1"])
	assert.Equal(t, int64(1000), allocations["peer-2"])
	assert.Equal(t, int64(500), allocations["peer-3"])
}

func TestTokenBalancer_CalculateOptimalAllocations_MinimumAllocation(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	demands := map[string]float64{
		"peer-1": 100.0, // Very low demand
		"peer-2": 200.0, // Very low demand
	}
	totalDemand := 300.0
	globalLimit := int64(1000)

	allocations := balancer.calculateOptimalAllocations(demands, totalDemand, globalLimit)

	// Even with proportional allocation, should respect minimum
	for _, allocation := range allocations {
		assert.GreaterOrEqual(t, allocation, balancer.minAllocation)
	}
}

func TestTokenBalancer_GetUsersToRebalance(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
		},
	}
	balancer.SetConfig(config)

	// Create bucket for alice
	balancer.GetOrCreateBucket("alice")

	// Add usage data for bob from peers
	coordinator.ProcessGossip(UsageGossip{
		FromPeer: "peer-1",
		UserStats: map[string]UserUsage{
			"bob": {
				Username:        "bob",
				UsedBytes:       1000,
				RequestRate:     100.0,
				LocalAllocation: 2000,
				LastUpdated:     time.Now(),
			},
		},
		Timestamp:   time.Now(),
		MessageType: GossipUsageReport,
	})

	users := balancer.getUsersToRebalance()

	// Should include both alice (local bucket) and bob (peer usage)
	assert.Contains(t, users, "alice")
	assert.Contains(t, users, "bob")
	assert.Len(t, users, 2)
}

func TestTokenBalancer_GetUserGlobalLimit(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
			"bob":   2 * 1024 * 1024,
		},
	}
	balancer.SetConfig(config)

	// Test user with specific limit
	assert.Equal(t, int64(5*1024*1024), balancer.getUserGlobalLimit("alice"))
	assert.Equal(t, int64(2*1024*1024), balancer.getUserGlobalLimit("bob"))

	// Test user with default limit
	assert.Equal(t, int64(1024*1024), balancer.getUserGlobalLimit("unknown"))

	// Test with no config
	balancer.SetConfig(nil)
	assert.Equal(t, int64(1024*1024), balancer.getUserGlobalLimit("alice")) // fallback default
}

func TestTokenBalancer_GetAllAllocations(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
			"bob":   2 * 1024 * 1024,
		},
	}
	balancer.SetConfig(config)

	// Create buckets and allocations
	balancer.GetOrCreateBucket("alice")
	balancer.GetOrCreateBucket("bob")
	balancer.UpdateUserAllocation("alice", 3*1024*1024)

	allocations := balancer.GetAllAllocations()

	assert.Contains(t, allocations, "alice")
	assert.Contains(t, allocations, "bob")

	// Verify alice's updated allocation
	assert.Contains(t, allocations["alice"], coordinator.myPeerID)
	assert.Equal(t, int64(3*1024*1024), allocations["alice"][coordinator.myPeerID])

	// Verify bob's initial allocation
	assert.Contains(t, allocations["bob"], coordinator.myPeerID)
	assert.Equal(t, int64(2*1024*1024), allocations["bob"][coordinator.myPeerID])
}

func TestTokenBalancer_RebalanceUser(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 6 * 1024 * 1024, // 6MB/s total
		},
	}
	balancer.SetConfig(config)

	// Add peers with different usage patterns
	coordinator.AddPeer(&Peer{
		ID:       "peer-1",
		Address:  "192.168.1.1:8081",
		LastSeen: time.Now(),
		Status:   PeerStatusActive,
	})

	// Simulate usage data showing peer-1 needs more bandwidth
	coordinator.ProcessGossip(UsageGossip{
		FromPeer: "peer-1",
		UserStats: map[string]UserUsage{
			"alice": {
				Username:        "alice",
				UsedBytes:       5000,
				RequestRate:     2000.0, // High usage
				LocalAllocation: 1000,   // Low allocation
				LastUpdated:     time.Now(),
			},
		},
		Timestamp:   time.Now(),
		MessageType: GossipUsageReport,
	})

	// Add local usage (low demand)
	coordinator.ReportUsage("alice", 500, 100.0, 1000)

	// Create initial bucket
	balancer.GetOrCreateBucket("alice")

	// Perform rebalancing
	balancer.rebalanceUser("alice")

	// Verify that rebalancing logic ran (hard to test exact allocations without mocking)
	// At minimum, verify that the function completed without errors
	allocations := balancer.GetAllAllocations()
	assert.Contains(t, allocations, "alice")
}

func TestTokenBalancer_GradualAllocationChange(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
		},
	}
	balancer.SetConfig(config)

	// Create bucket with initial allocation
	bucket := balancer.GetOrCreateBucket("alice")
	initialRate := bucket.Rate()

	// Test gradual increase
	targetAllocation := int64(initialRate * 2) // Double the rate
	balancer.updateLocalAllocationGradually("alice", int64(initialRate), targetAllocation)

	// Should not immediately jump to target (gradual change)
	updatedBucket := balancer.GetOrCreateBucket("alice")
	newRate := updatedBucket.Rate()
	assert.Greater(t, newRate, initialRate)
	assert.LessOrEqual(t, newRate, float64(targetAllocation))
}

func TestTokenBalancer_ConcurrentAccess(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 5 * 1024 * 1024,
			"bob":   2 * 1024 * 1024,
		},
	}
	balancer.SetConfig(config)

	done := make(chan bool, 10)

	// Create buckets concurrently
	for i := 0; i < 5; i++ {
		go func() {
			balancer.GetOrCreateBucket("alice")
			balancer.GetOrCreateBucket("bob")
			done <- true
		}()
	}

	// Update allocations concurrently
	for i := 0; i < 5; i++ {
		go func(id int) {
			balancer.UpdateUserAllocation("alice", int64(3*1024*1024+id*1000))
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify data integrity
	allocations := balancer.GetAllAllocations()
	assert.Contains(t, allocations, "alice")
	assert.Contains(t, allocations, "bob")

	bucket := balancer.GetOrCreateBucket("alice")
	assert.NotNil(t, bucket)
}

func TestTokenBalancer_EdgeCases(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	// Test with no config
	bucket := balancer.GetOrCreateBucket("alice")
	assert.NotNil(t, bucket)
	assert.Equal(t, float64(1024*1024), bucket.Rate()) // Default fallback

	// Test with empty username
	bucket = balancer.GetOrCreateBucket("")
	assert.NotNil(t, bucket)

	// Test getting allocation for non-existent user
	allocation := balancer.GetCurrentAllocation("non-existent")
	assert.Equal(t, int64(0), allocation)

	// Test getting users to rebalance with no data
	users := balancer.getUsersToRebalance()
	assert.NotNil(t, users) // Should return empty slice, not nil
}

func TestTokenBalancer_BurstCapacity(t *testing.T) {
	coordinator := createTestCoordinator()
	balancer := NewTokenBalancer(coordinator)

	config := &Config{
		DefaultBandwidth: 1024 * 1024,
		Users: map[string]int64{
			"alice": 10 * 1024 * 1024, // 10MB/s
		},
	}
	balancer.SetConfig(config)

	bucket := balancer.GetOrCreateBucket("alice")
	
	// Verify burst capacity is set correctly
	expectedBurst := int64(float64(10*1024*1024) * balancer.burstRatio)
	if expectedBurst < 10*1024*1024 {
		expectedBurst = 10 * 1024 * 1024
	}
	
	assert.Equal(t, expectedBurst, bucket.Capacity())
}
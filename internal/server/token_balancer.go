package server

import (
	"math"
	"sync"
	"time"

	"github.com/juju/ratelimit"
	"github.com/rs/zerolog/log"
)

// Coordinator interface for both Redis and Gossip coordinators
type Coordinator interface {
	GetPeerUsage(username string) map[string]*UserUsage
	GetActivePeers() []*Peer
}

// TokenBalancer manages distributed token allocation across peers
type TokenBalancer struct {
	coordinator   Coordinator
	config        *Config  // Rate limiting config
	myPeerID      string
	localBuckets  map[string]*ratelimit.Bucket
	allocations   map[string]map[string]int64  // username -> peerID -> allocation
	bucketsMutex  sync.RWMutex
	allocMutex    sync.RWMutex
	shutdownChan  chan struct{}
	
	// Configuration parameters
	minAllocation     int64   // Minimum allocation per peer (bytes/sec)
	maxReallocation   float64 // Maximum % change per rebalance cycle
	demandMultiplier  float64 // Multiplier for demand calculation
	burstRatio        float64 // Burst capacity as ratio of rate
}

// NewTokenBalancer creates a new token balancer
func NewTokenBalancer(coordinator Coordinator) *TokenBalancer {
	return &TokenBalancer{
		coordinator:      coordinator,
		localBuckets:     make(map[string]*ratelimit.Bucket),
		allocations:      make(map[string]map[string]int64),
		shutdownChan:     make(chan struct{}),
		minAllocation:    1024,    // 1KB/s minimum
		maxReallocation:  0.5,     // 50% max change per cycle
		demandMultiplier: 1.2,     // 20% buffer for demand
		burstRatio:       0.1,     // 10% burst capacity
	}
}

// SetPeerID sets the peer ID for this token balancer
func (tb *TokenBalancer) SetPeerID(peerID string) {
	tb.myPeerID = peerID
}

// Start begins the token balancer
func (tb *TokenBalancer) Start() error {
	log.Info().Msg("Starting token balancer")
	return nil
}

// Stop shuts down the token balancer
func (tb *TokenBalancer) Stop() {
	close(tb.shutdownChan)
}

// SetConfig sets the rate limiting configuration
func (tb *TokenBalancer) SetConfig(config *Config) {
	tb.config = config
}

// GetOrCreateBucket gets or creates a local rate limiting bucket for a user
func (tb *TokenBalancer) GetOrCreateBucket(username string) *ratelimit.Bucket {
	tb.bucketsMutex.Lock()
	defer tb.bucketsMutex.Unlock()
	
	if bucket, exists := tb.localBuckets[username]; exists {
		return bucket
	}
	
	// Get user's global limit
	globalLimit := tb.getUserGlobalLimit(username)
	
	// For distributed rate limiting, use demand-based allocation
	// If this is the only proxy with this user, give full allocation
	// If multiple proxies have this user, distribute based on actual usage
	
	var initialAllocation int64
	
	// Check if other peers are actually serving this user
	activePeersForUser := tb.getActivePeersForUser(username)
	if activePeersForUser <= 1 {
		// This is the only proxy serving this user - give full allocation
		initialAllocation = globalLimit
	} else {
		// Multiple proxies serving this user - start with fair share
		// Rebalancing will adjust based on actual demand
		initialAllocation = globalLimit / int64(activePeersForUser)
	}
	
	// Ensure minimum allocation
	if initialAllocation < tb.minAllocation {
		initialAllocation = tb.minAllocation
	}
	
	// Create bucket with burst capacity
	burstCapacity := int64(float64(initialAllocation) * tb.burstRatio)
	if burstCapacity < initialAllocation {
		burstCapacity = initialAllocation
	}
	
	bucket := ratelimit.NewBucketWithRate(float64(initialAllocation), burstCapacity)
	tb.localBuckets[username] = bucket
	
	// Track initial allocation
	tb.allocMutex.Lock()
	if tb.allocations[username] == nil {
		tb.allocations[username] = make(map[string]int64)
	}
	tb.allocations[username][tb.myPeerID] = initialAllocation
	tb.allocMutex.Unlock()
	
	log.Info().
		Str("username", username).
		Int64("initial_allocation", initialAllocation).
		Int64("burst_capacity", burstCapacity).
		Msg("Created rate limiting bucket")
	
	return bucket
}

// UpdateUserAllocation updates the local allocation for a user
func (tb *TokenBalancer) UpdateUserAllocation(username string, newAllocation int64) {
	tb.bucketsMutex.Lock()
	defer tb.bucketsMutex.Unlock()
	
	bucket, exists := tb.localBuckets[username]
	if !exists {
		// Create new bucket with the allocation
		burstCapacity := int64(float64(newAllocation) * tb.burstRatio)
		if burstCapacity < newAllocation {
			burstCapacity = newAllocation
		}
		bucket = ratelimit.NewBucketWithRate(float64(newAllocation), burstCapacity)
		tb.localBuckets[username] = bucket
	} else {
		// Replace bucket with new rate (juju/ratelimit doesn't have SetRate)
		burstCapacity := int64(float64(newAllocation) * tb.burstRatio)
		if burstCapacity < newAllocation {
			burstCapacity = newAllocation
		}
		bucket = ratelimit.NewBucketWithRate(float64(newAllocation), burstCapacity)
		tb.localBuckets[username] = bucket
	}
	
	// Track the allocation
	tb.allocMutex.Lock()
	if tb.allocations[username] == nil {
		tb.allocations[username] = make(map[string]int64)
	}
	tb.allocations[username][tb.myPeerID] = newAllocation
	tb.allocMutex.Unlock()
	
	log.Info().
		Str("username", username).
		Int64("new_allocation", newAllocation).
		Msg("Updated user allocation")
}

// RebalanceAll rebalances token allocations for all users
func (tb *TokenBalancer) RebalanceAll() {
	if tb.config == nil {
		return
	}
	
	log.Debug().Msg("Starting token rebalancing cycle")
	
	// Get all users that need rebalancing
	usersToRebalance := tb.getUsersToRebalance()
	
	for _, username := range usersToRebalance {
		tb.rebalanceUser(username)
	}
	
	log.Debug().Int("users_rebalanced", len(usersToRebalance)).Msg("Token rebalancing cycle completed")
}

// rebalanceUser rebalances token allocation for a specific user
func (tb *TokenBalancer) rebalanceUser(username string) {
	// Get current usage across all peers
	peerUsage := tb.coordinator.GetPeerUsage(username)
	if len(peerUsage) == 0 {
		return // No usage data available
	}
	
	globalLimit := tb.getUserGlobalLimit(username)
	
	// Calculate demand for each peer
	demands := tb.calculatePeerDemands(peerUsage)
	totalDemand := tb.sumDemands(demands)
	
	// Calculate new allocations
	newAllocations := tb.calculateOptimalAllocations(demands, totalDemand, globalLimit)
	
	// Apply gradual reallocation to avoid traffic spikes
	tb.applyGradualReallocation(username, newAllocations)
	
	log.Debug().
		Str("username", username).
		Float64("total_demand", totalDemand).
		Int64("global_limit", globalLimit).
		Int("peer_count", len(newAllocations)).
		Msg("Rebalanced user tokens")
}

// calculatePeerDemands calculates demand for each peer based on usage
func (tb *TokenBalancer) calculatePeerDemands(peerUsage map[string]*UserUsage) map[string]float64 {
	demands := make(map[string]float64)
	
	for peerID, usage := range peerUsage {
		// Base demand on recent request rate with buffer
		demand := usage.RequestRate * tb.demandMultiplier
		
		// Ensure minimum allocation
		if demand < float64(tb.minAllocation) {
			demand = float64(tb.minAllocation)
		}
		
		demands[peerID] = demand
	}
	
	return demands
}

// sumDemands calculates total demand across all peers
func (tb *TokenBalancer) sumDemands(demands map[string]float64) float64 {
	total := 0.0
	for _, demand := range demands {
		total += demand
	}
	return total
}

// calculateOptimalAllocations calculates optimal allocation for each peer
func (tb *TokenBalancer) calculateOptimalAllocations(demands map[string]float64, totalDemand float64, globalLimit int64) map[string]int64 {
	allocations := make(map[string]int64)
	
	if totalDemand <= float64(globalLimit) {
		// Sufficient capacity - give each peer what it needs
		for peerID, demand := range demands {
			allocations[peerID] = int64(math.Ceil(demand))
		}
	} else {
		// Over capacity - proportional allocation
		for peerID, demand := range demands {
			proportion := demand / totalDemand
			allocation := int64(float64(globalLimit) * proportion)
			
			// Ensure minimum allocation
			if allocation < tb.minAllocation {
				allocation = tb.minAllocation
			}
			
			allocations[peerID] = allocation
		}
	}
	
	return allocations
}

// applyGradualReallocation applies new allocations gradually
func (tb *TokenBalancer) applyGradualReallocation(username string, newAllocations map[string]int64) {
	tb.allocMutex.Lock()
	currentAllocations := tb.allocations[username]
	if currentAllocations == nil {
		currentAllocations = make(map[string]int64)
	}
	tb.allocMutex.Unlock()
	
	for peerID, newAllocation := range newAllocations {
		currentAllocation := currentAllocations[peerID]
		
		if peerID == tb.myPeerID {
			// Update local allocation
			tb.updateLocalAllocationGradually(username, currentAllocation, newAllocation)
		} else {
			// Send allocation update to peer
			tb.sendAllocationUpdate(peerID, username, newAllocation)
		}
		
		// Update tracking
		tb.allocMutex.Lock()
		if tb.allocations[username] == nil {
			tb.allocations[username] = make(map[string]int64)
		}
		tb.allocations[username][peerID] = newAllocation
		tb.allocMutex.Unlock()
	}
}

// updateLocalAllocationGradually updates local allocation gradually to avoid spikes
func (tb *TokenBalancer) updateLocalAllocationGradually(username string, currentAllocation, targetAllocation int64) {
	if currentAllocation == 0 {
		// First allocation
		tb.UpdateUserAllocation(username, targetAllocation)
		return
	}
	
	// Calculate maximum change per cycle
	maxChange := int64(float64(currentAllocation) * tb.maxReallocation)
	if maxChange == 0 {
		maxChange = 1
	}
	
	var newAllocation int64
	if targetAllocation > currentAllocation {
		// Increase gradually
		newAllocation = currentAllocation + maxChange
		if newAllocation > targetAllocation {
			newAllocation = targetAllocation
		}
	} else {
		// Decrease gradually
		newAllocation = currentAllocation - maxChange
		if newAllocation < targetAllocation {
			newAllocation = targetAllocation
		}
	}
	
	tb.UpdateUserAllocation(username, newAllocation)
}

// sendAllocationUpdate handles allocation updates for different coordinator types
func (tb *TokenBalancer) sendAllocationUpdate(peerID, username string, allocation int64) {
	// For Redis coordination, we don't send peer-to-peer allocation updates
	// Instead, allocation changes are handled through Redis rebalancing
	log.Debug().
		Str("peer_id", peerID).
		Str("username", username).
		Int64("allocation", allocation).
		Msg("Allocation update tracked locally - will sync via Redis rebalancing")
}

// getUsersToRebalance returns list of users that need rebalancing
func (tb *TokenBalancer) getUsersToRebalance() []string {
	var users []string
	
	// Get users from local buckets
	tb.bucketsMutex.RLock()
	for username := range tb.localBuckets {
		users = append(users, username)
	}
	tb.bucketsMutex.RUnlock()
	
	// Get users from coordinator usage data
	// Note: For Redis coordinator, this is handled differently
	
	return users
}

// getUserGlobalLimit gets the global rate limit for a user
func (tb *TokenBalancer) getUserGlobalLimit(username string) int64 {
	if tb.config == nil {
		return 1024 * 1024 // 1MB/s default
	}
	
	if userLimit, exists := tb.config.Users[username]; exists {
		return userLimit
	}
	
	return tb.config.DefaultBandwidth
}

// GetCurrentAllocation returns current allocation for a user on this peer
func (tb *TokenBalancer) GetCurrentAllocation(username string) int64 {
	tb.allocMutex.RLock()
	defer tb.allocMutex.RUnlock()
	
	if userAllocations, exists := tb.allocations[username]; exists {
		if allocation, exists := userAllocations[tb.myPeerID]; exists {
			return allocation
		}
	}
	
	return 0
}

// GetAllAllocations returns all current allocations for debugging
func (tb *TokenBalancer) GetAllAllocations() map[string]map[string]int64 {
	tb.allocMutex.RLock()
	defer tb.allocMutex.RUnlock()
	
	// Return deep copy to avoid concurrent access issues
	result := make(map[string]map[string]int64)
	for username, peerAllocations := range tb.allocations {
		result[username] = make(map[string]int64)
		for peerID, allocation := range peerAllocations {
			result[username][peerID] = allocation
		}
	}
	
	return result
}

// getActivePeersForUser returns number of peers actively serving a user
func (tb *TokenBalancer) getActivePeersForUser(username string) int {
	// Check if we have usage data for this user from other peers
	peerUsage := tb.coordinator.GetPeerUsage(username)
	
	activePeers := 1 // Count ourselves
	
	// Count peers that have recent usage for this user
	now := time.Now()
	for _, usage := range peerUsage {
		if usage.Username == username && now.Sub(usage.LastUpdated) < 2*time.Minute {
			activePeers++
		}
	}
	
	return activePeers
}
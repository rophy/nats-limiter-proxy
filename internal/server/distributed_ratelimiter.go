package server

import (
	"sync"
	"time"

	"github.com/juju/ratelimit"
	"github.com/rs/zerolog/log"
)

// DistributedRateLimiterManager manages rate limiters with peer coordination
type DistributedRateLimiterManager struct {
	config           *Config
	tokenBalancer    *TokenBalancer
	peerCoordinator  *PeerCoordinator
	usageTracker     *UsageTracker
	mu               sync.RWMutex
}

// UsageTracker tracks usage statistics for reporting to peers
type UsageTracker struct {
	userStats        map[string]*UserStats
	mu               sync.RWMutex
	reportInterval   time.Duration
	lastReport       time.Time
}

// UserStats tracks usage statistics for a single user
type UserStats struct {
	Username         string
	BytesUsed        int64
	RequestCount     int64
	LastActivity     time.Time
	StartTime        time.Time
}

// NewDistributedRateLimiterManager creates a new distributed rate limiter manager
func NewDistributedRateLimiterManager(config *Config, peerCoordinator *PeerCoordinator) *DistributedRateLimiterManager {
	drlm := &DistributedRateLimiterManager{
		config:          config,
		peerCoordinator: peerCoordinator,
		usageTracker:    NewUsageTracker(),
	}
	
	if peerCoordinator != nil {
		// Initialize token balancer with coordination
		tokenBalancer := NewTokenBalancer(peerCoordinator)
		tokenBalancer.SetConfig(config)
		drlm.tokenBalancer = tokenBalancer
	}
	
	return drlm
}

// NewUsageTracker creates a new usage tracker
func NewUsageTracker() *UsageTracker {
	return &UsageTracker{
		userStats:      make(map[string]*UserStats),
		reportInterval: 5 * time.Second,
		lastReport:     time.Now(),
	}
}

// GetLimiter returns the rate limiter for a user (implements RateLimiterManagerInterface)
func (drlm *DistributedRateLimiterManager) GetLimiter(username string) *ratelimit.Bucket {
	if username == "" {
		return nil
	}
	
	if drlm.tokenBalancer != nil {
		// Use distributed rate limiting
		bucket := drlm.tokenBalancer.GetOrCreateBucket(username)
		drlm.trackUsage(username, 0) // Initialize tracking
		return bucket
	}
	
	// Fall back to local rate limiting
	return drlm.createLocalBucket(username)
}

// createLocalBucket creates a local rate limiter bucket
func (drlm *DistributedRateLimiterManager) createLocalBucket(username string) *ratelimit.Bucket {
	bandwidth := drlm.getBandwidthForUser(username)
	return ratelimit.NewBucketWithRate(float64(bandwidth), bandwidth)
}

// getBandwidthForUser returns the bandwidth limit for a user
func (drlm *DistributedRateLimiterManager) getBandwidthForUser(username string) int64 {
	if drlm.config.Users != nil {
		if bw, ok := drlm.config.Users[username]; ok {
			return bw
		}
	}
	return drlm.config.DefaultBandwidth
}

// TrackUsage tracks usage for a user and reports to peers periodically
func (drlm *DistributedRateLimiterManager) TrackUsage(username string, bytesUsed int64) {
	if drlm.peerCoordinator == nil {
		return // No coordination enabled
	}
	
	drlm.trackUsage(username, bytesUsed)
	
	// Check if it's time to report usage
	drlm.usageTracker.mu.RLock()
	shouldReport := time.Since(drlm.usageTracker.lastReport) >= drlm.usageTracker.reportInterval
	drlm.usageTracker.mu.RUnlock()
	
	if shouldReport {
		go drlm.reportUsage()
	}
}

// trackUsage updates local usage statistics
func (drlm *DistributedRateLimiterManager) trackUsage(username string, bytesUsed int64) {
	drlm.usageTracker.mu.Lock()
	defer drlm.usageTracker.mu.Unlock()
	
	stats, exists := drlm.usageTracker.userStats[username]
	if !exists {
		stats = &UserStats{
			Username:     username,
			StartTime:    time.Now(),
			LastActivity: time.Now(),
		}
		drlm.usageTracker.userStats[username] = stats
	}
	
	stats.BytesUsed += bytesUsed
	stats.RequestCount++
	stats.LastActivity = time.Now()
}

// reportUsage reports current usage to peer coordinator
func (drlm *DistributedRateLimiterManager) reportUsage() {
	drlm.usageTracker.mu.Lock()
	
	// Create snapshot of current usage
	userStatsSnapshot := make(map[string]*UserStats)
	for username, stats := range drlm.usageTracker.userStats {
		userStatsSnapshot[username] = &UserStats{
			Username:     stats.Username,
			BytesUsed:    stats.BytesUsed,
			RequestCount: stats.RequestCount,
			LastActivity: stats.LastActivity,
			StartTime:    stats.StartTime,
		}
		
		// Reset counters for next period
		stats.BytesUsed = 0
		stats.RequestCount = 0
		stats.StartTime = time.Now()
	}
	
	drlm.usageTracker.lastReport = time.Now()
	drlm.usageTracker.mu.Unlock()
	
	// Report to peer coordinator
	for username, stats := range userStatsSnapshot {
		// Calculate request rate (requests per second)
		duration := stats.LastActivity.Sub(stats.StartTime)
		if duration <= 0 {
			duration = time.Second // Avoid division by zero
		}
		requestRate := float64(stats.BytesUsed) / duration.Seconds()
		
		// Get current allocation
		var currentAllocation int64
		if drlm.tokenBalancer != nil {
			currentAllocation = drlm.tokenBalancer.GetCurrentAllocation(username)
		}
		
		// Report to coordinator
		drlm.peerCoordinator.ReportUsage(username, stats.BytesUsed, requestRate, currentAllocation)
		
		log.Debug().
			Str("username", username).
			Int64("bytes_used", stats.BytesUsed).
			Float64("request_rate", requestRate).
			Int64("allocation", currentAllocation).
			Msg("Reported usage to peer coordinator")
	}
}

// GetUsageStats returns current usage statistics
func (drlm *DistributedRateLimiterManager) GetUsageStats() map[string]*UserStats {
	drlm.usageTracker.mu.RLock()
	defer drlm.usageTracker.mu.RUnlock()
	
	// Return copy to avoid concurrent access
	stats := make(map[string]*UserStats)
	for username, userStats := range drlm.usageTracker.userStats {
		stats[username] = &UserStats{
			Username:     userStats.Username,
			BytesUsed:    userStats.BytesUsed,
			RequestCount: userStats.RequestCount,
			LastActivity: userStats.LastActivity,
			StartTime:    userStats.StartTime,
		}
	}
	return stats
}

// GetPeerStats returns peer usage statistics (if coordination is enabled)
func (drlm *DistributedRateLimiterManager) GetPeerStats() map[string]map[string]*UserUsage {
	if drlm.peerCoordinator == nil {
		return nil
	}
	
	result := make(map[string]map[string]*UserUsage)
	
	// Get all active users
	localStats := drlm.GetUsageStats()
	for username := range localStats {
		peerUsage := drlm.peerCoordinator.GetPeerUsage(username)
		if peerUsage != nil {
			result[username] = peerUsage
		}
	}
	
	return result
}

// Cleanup removes old usage data
func (drlm *DistributedRateLimiterManager) Cleanup() {
	now := time.Now()
	inactiveThreshold := 5 * time.Minute
	
	drlm.usageTracker.mu.Lock()
	defer drlm.usageTracker.mu.Unlock()
	
	for username, stats := range drlm.usageTracker.userStats {
		if now.Sub(stats.LastActivity) > inactiveThreshold {
			delete(drlm.usageTracker.userStats, username)
			log.Debug().Str("username", username).Msg("Cleaned up inactive user stats")
		}
	}
}

// Start begins background tasks
func (drlm *DistributedRateLimiterManager) Start() {
	// Start cleanup routine
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		
		for range ticker.C {
			drlm.Cleanup()
		}
	}()
}

// IsDistributed returns true if distributed coordination is enabled
func (drlm *DistributedRateLimiterManager) IsDistributed() bool {
	return drlm.peerCoordinator != nil && drlm.tokenBalancer != nil
}
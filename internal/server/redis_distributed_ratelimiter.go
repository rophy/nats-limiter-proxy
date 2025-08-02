package server

import (
	"sync"
	"time"

	"github.com/juju/ratelimit"
	"github.com/rs/zerolog/log"
)

// RedisDistributedRateLimiterManager manages rate limiters with Redis coordination
type RedisDistributedRateLimiterManager struct {
	config           *Config
	redisCoordinator *RedisCoordinator
	usageTracker     *UsageTracker
	mu               sync.RWMutex
}

// NewDistributedRateLimiterManagerWithRedis creates a new distributed rate limiter manager with Redis
func NewDistributedRateLimiterManagerWithRedis(config *Config, redisCoordinator *RedisCoordinator) *RedisDistributedRateLimiterManager {
	return &RedisDistributedRateLimiterManager{
		config:           config,
		redisCoordinator: redisCoordinator,
		usageTracker:     NewUsageTracker(),
	}
}

// GetLimiter returns the rate limiter for a user (implements RateLimiterManagerInterface)
func (rdrlm *RedisDistributedRateLimiterManager) GetLimiter(username string) *ratelimit.Bucket {
	if username == "" {
		return nil
	}
	
	if rdrlm.redisCoordinator != nil {
		// Use distributed rate limiting via Redis
		bucket := rdrlm.redisCoordinator.GetOrCreateBucket(username)
		rdrlm.trackUsage(username, 0) // Initialize tracking
		return bucket
	}
	
	// Fall back to local rate limiting
	return rdrlm.createLocalBucket(username)
}

// createLocalBucket creates a local rate limiter bucket
func (rdrlm *RedisDistributedRateLimiterManager) createLocalBucket(username string) *ratelimit.Bucket {
	bandwidth := rdrlm.getBandwidthForUser(username)
	return ratelimit.NewBucketWithRate(float64(bandwidth), bandwidth)
}

// getBandwidthForUser returns the bandwidth limit for a user
func (rdrlm *RedisDistributedRateLimiterManager) getBandwidthForUser(username string) int64 {
	if rdrlm.config.Users != nil {
		if bw, ok := rdrlm.config.Users[username]; ok {
			return bw
		}
	}
	return rdrlm.config.DefaultBandwidth
}

// TrackUsage tracks usage for a user and reports to Redis periodically
func (rdrlm *RedisDistributedRateLimiterManager) TrackUsage(username string, bytesUsed int64) {
	if rdrlm.redisCoordinator == nil {
		return // No coordination enabled
	}
	
	rdrlm.trackUsage(username, bytesUsed)
	
	// Check if it's time to report usage
	rdrlm.usageTracker.mu.RLock()
	shouldReport := time.Since(rdrlm.usageTracker.lastReport) >= rdrlm.usageTracker.reportInterval
	rdrlm.usageTracker.mu.RUnlock()
	
	if shouldReport {
		go rdrlm.reportUsage()
	}
}

// trackUsage updates local usage statistics
func (rdrlm *RedisDistributedRateLimiterManager) trackUsage(username string, bytesUsed int64) {
	rdrlm.usageTracker.mu.Lock()
	defer rdrlm.usageTracker.mu.Unlock()
	
	stats, exists := rdrlm.usageTracker.userStats[username]
	if !exists {
		stats = &UserStats{
			Username:     username,
			StartTime:    time.Now(),
			LastActivity: time.Now(),
		}
		rdrlm.usageTracker.userStats[username] = stats
	}
	
	stats.BytesUsed += bytesUsed
	stats.RequestCount++
	stats.LastActivity = time.Now()
}

// reportUsage reports current usage to Redis coordinator
func (rdrlm *RedisDistributedRateLimiterManager) reportUsage() {
	rdrlm.usageTracker.mu.Lock()
	
	// Create snapshot of current usage
	userStatsSnapshot := make(map[string]*UserStats)
	for username, stats := range rdrlm.usageTracker.userStats {
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
	
	rdrlm.usageTracker.lastReport = time.Now()
	rdrlm.usageTracker.mu.Unlock()
	
	// Report to Redis coordinator
	for username, stats := range userStatsSnapshot {
		// Calculate request rate (requests per second)
		duration := stats.LastActivity.Sub(stats.StartTime)
		if duration <= 0 {
			duration = time.Second // Avoid division by zero
		}
		requestRate := float64(stats.BytesUsed) / duration.Seconds()
		
		// Get current allocation from token balancer
		var currentAllocation int64
		if rdrlm.redisCoordinator.tokenBalancer != nil {
			currentAllocation = rdrlm.redisCoordinator.tokenBalancer.GetCurrentAllocation(username)
		}
		
		// Report to Redis coordinator
		rdrlm.redisCoordinator.ReportUsage(username, stats.BytesUsed, requestRate, currentAllocation)
		
		log.Debug().
			Str("username", username).
			Int64("bytes_used", stats.BytesUsed).
			Float64("request_rate", requestRate).
			Int64("allocation", currentAllocation).
			Msg("Reported usage to Redis coordinator")
	}
}

// GetUsageStats returns current usage statistics
func (rdrlm *RedisDistributedRateLimiterManager) GetUsageStats() map[string]*UserStats {
	rdrlm.usageTracker.mu.RLock()
	defer rdrlm.usageTracker.mu.RUnlock()
	
	// Return copy to avoid concurrent access
	stats := make(map[string]*UserStats)
	for username, userStats := range rdrlm.usageTracker.userStats {
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

// GetPeerStats returns peer usage statistics from Redis
func (rdrlm *RedisDistributedRateLimiterManager) GetPeerStats() map[string]map[string]*UserUsage {
	if rdrlm.redisCoordinator == nil {
		return nil
	}
	
	result := make(map[string]map[string]*UserUsage)
	
	// Get all active users
	localStats := rdrlm.GetUsageStats()
	for username := range localStats {
		peerUsage := rdrlm.redisCoordinator.GetPeerUsage(username)
		if peerUsage != nil {
			result[username] = peerUsage
		}
	}
	
	return result
}

// Cleanup removes old usage data
func (rdrlm *RedisDistributedRateLimiterManager) Cleanup() {
	now := time.Now()
	inactiveThreshold := 5 * time.Minute
	
	rdrlm.usageTracker.mu.Lock()
	defer rdrlm.usageTracker.mu.Unlock()
	
	for username, stats := range rdrlm.usageTracker.userStats {
		if now.Sub(stats.LastActivity) > inactiveThreshold {
			delete(rdrlm.usageTracker.userStats, username)
			log.Debug().Str("username", username).Msg("Cleaned up inactive user stats")
		}
	}
}

// Start begins background tasks
func (rdrlm *RedisDistributedRateLimiterManager) Start() {
	// Start the Redis coordinator first
	if rdrlm.redisCoordinator != nil {
		// The coordinator will start itself during proxy startup
		// We just need to start our cleanup routine
	}
	
	// Start cleanup routine
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		
		for range ticker.C {
			rdrlm.Cleanup()
		}
	}()
}

// IsDistributed returns true if Redis coordination is enabled
func (rdrlm *RedisDistributedRateLimiterManager) IsDistributed() bool {
	return rdrlm.redisCoordinator != nil && rdrlm.redisCoordinator.IsHealthy()
}

// IsHealthy returns true if the rate limiter manager is healthy
func (rdrlm *RedisDistributedRateLimiterManager) IsHealthy() bool {
	if rdrlm.redisCoordinator == nil {
		return true // Local mode is always healthy
	}
	return rdrlm.redisCoordinator.IsHealthy()
}
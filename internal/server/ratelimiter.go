package server

import (
	"sync"

	"github.com/juju/ratelimit"
)

// RateLimiterManager manages rate limiters per user to ensure consistent
// rate limiting across multiple connections from the same user.
type RateLimiterManager struct {
	mu       sync.RWMutex
	limiters map[string]*ratelimit.Bucket
	config   *Config
}

// NewRateLimiterManager creates a new rate limiter manager.
func NewRateLimiterManager(config *Config) *RateLimiterManager {
	return &RateLimiterManager{
		limiters: make(map[string]*ratelimit.Bucket),
		config:   config,
	}
}

// GetLimiter returns the rate limiter for a user, creating one if it doesn't exist.
// This ensures all connections from the same user share the same rate limiter.
func (rlm *RateLimiterManager) GetLimiter(username string) *ratelimit.Bucket {
	if username == "" {
		return nil
	}

	// Try read lock first for common case
	rlm.mu.RLock()
	limiter, exists := rlm.limiters[username]
	rlm.mu.RUnlock()

	if exists {
		return limiter
	}

	// Need to create limiter, use write lock
	rlm.mu.Lock()
	defer rlm.mu.Unlock()

	// Double-check in case another goroutine created it while we were waiting
	if limiter, exists := rlm.limiters[username]; exists {
		return limiter
	}

	// Create new rate limiter for this user
	bandwidth := rlm.getBandwidthForUser(username)
	limiter = ratelimit.NewBucketWithRate(float64(bandwidth), bandwidth)
	rlm.limiters[username] = limiter

	return limiter
}

// getBandwidthForUser returns the bandwidth limit for a user.
func (rlm *RateLimiterManager) getBandwidthForUser(username string) int64 {
	if rlm.config.Users != nil {
		if bw, ok := rlm.config.Users[username]; ok {
			return bw
		}
	}
	return rlm.config.DefaultBandwidth
}

// RemoveLimiter removes a rate limiter for a user (useful for cleanup).
func (rlm *RateLimiterManager) RemoveLimiter(username string) {
	rlm.mu.Lock()
	defer rlm.mu.Unlock()
	delete(rlm.limiters, username)
}

// GetStats returns statistics about active rate limiters.
func (rlm *RateLimiterManager) GetStats() map[string]int64 {
	rlm.mu.RLock()
	defer rlm.mu.RUnlock()

	stats := make(map[string]int64)
	for username, limiter := range rlm.limiters {
		stats[username] = limiter.Available()
	}
	return stats
}
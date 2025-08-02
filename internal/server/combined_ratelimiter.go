package server

import (
	"github.com/juju/ratelimit"
)

// CombinedRateLimiter combines local rate limiting with optional global usage tracking
type CombinedRateLimiter struct {
	localRateLimiter  *LocalRateLimiter
	globalRateLimiter *GlobalRateLimiter // Optional - only for global mode
}

// NewCombinedRateLimiter creates a combined rate limiter
func NewCombinedRateLimiter(local *LocalRateLimiter, global *GlobalRateLimiter) *CombinedRateLimiter {
	return &CombinedRateLimiter{
		localRateLimiter:  local,
		globalRateLimiter: global,
	}
}

// GetLimiter returns the local rate limiter for a user (implements RateLimiterManagerInterface)
func (crl *CombinedRateLimiter) GetLimiter(username string) *ratelimit.Bucket {
	return crl.localRateLimiter.GetLimiter(username)
}

// TrackUsage tracks usage for global aggregation if enabled (implements UsageReporter interface)
func (crl *CombinedRateLimiter) TrackUsage(username string, bytesUsed int64) {
	if crl.globalRateLimiter != nil {
		crl.globalRateLimiter.TrackUsage(username, bytesUsed)
	}
	// Note: Local rate limiter doesn't need usage tracking - it enforces statically
}

// Start starts both rate limiters
func (crl *CombinedRateLimiter) Start() error {
	if err := crl.localRateLimiter.Start(); err != nil {
		return err
	}
	
	if crl.globalRateLimiter != nil {
		if err := crl.globalRateLimiter.Start(); err != nil {
			return err
		}
	}
	
	return nil
}

// Stop stops both rate limiters
func (crl *CombinedRateLimiter) Stop() {
	crl.localRateLimiter.Stop()
	
	if crl.globalRateLimiter != nil {
		crl.globalRateLimiter.Stop()
	}
}
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

// GetLocalLimiter returns the local rate limiter for a user (implements RateLimiterManagerInterface)
func (crl *CombinedRateLimiter) GetLocalLimiter(username string) *ratelimit.Bucket {
	return crl.localRateLimiter.GetLimiter(username)
}

// GetGlobalLimiter returns the global rate limiter for a user (implements RateLimiterManagerInterface)
func (crl *CombinedRateLimiter) GetGlobalLimiter(username string) *ratelimit.Bucket {
	if crl.globalRateLimiter != nil {
		return crl.globalRateLimiter.GetGlobalBucket(username)
	}
	return nil // No global limiter in local mode
}

// TrackUsage tracks usage for global aggregation if enabled (implements UsageReporter interface)
func (crl *CombinedRateLimiter) TrackUsage(username string, bytesUsed int64) {
	if crl.globalRateLimiter != nil {
		crl.globalRateLimiter.TrackUsage(username, bytesUsed)
	}
	// Note: Local rate limiter doesn't need usage tracking - it enforces statically
}

// UserConnected notifies that a user has connected
func (crl *CombinedRateLimiter) UserConnected(username string) {
	if crl.globalRateLimiter != nil {
		crl.globalRateLimiter.UserConnected(username)
	}
}

// UserDisconnected notifies that a user has disconnected
func (crl *CombinedRateLimiter) UserDisconnected(username string) {
	if crl.globalRateLimiter != nil {
		crl.globalRateLimiter.UserDisconnected(username)
	}
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
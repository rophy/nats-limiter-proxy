package server

import (
	"sync"

	"github.com/juju/ratelimit"
	"github.com/rs/zerolog/log"
)

// LocalRateLimiter provides per-instance rate limiting without external dependencies
type LocalRateLimiter struct {
	config       *Config
	buckets      map[string]*ratelimit.Bucket
	bucketsMutex sync.RWMutex
}

// NewLocalRateLimiter creates a new local rate limiter
func NewLocalRateLimiter(config *Config) *LocalRateLimiter {
	return &LocalRateLimiter{
		config:  config,
		buckets: make(map[string]*ratelimit.Bucket),
	}
}

// GetLimiter returns the rate limiter for a user, creating one if needed
func (lrl *LocalRateLimiter) GetLimiter(username string) *ratelimit.Bucket {
	lrl.bucketsMutex.RLock()
	if bucket, exists := lrl.buckets[username]; exists {
		lrl.bucketsMutex.RUnlock()
		return bucket
	}
	lrl.bucketsMutex.RUnlock()

	// Create new bucket under write lock
	lrl.bucketsMutex.Lock()
	defer lrl.bucketsMutex.Unlock()

	// Double-check after acquiring write lock
	if bucket, exists := lrl.buckets[username]; exists {
		return bucket
	}

	// Get user's local rate limit from config
	var rateLimit int64
	if userLimits := lrl.config.Limits.GetUserLimits(username); userLimits != nil {
		rateLimit = userLimits.BPSLocal
	} else {
		rateLimit = lrl.config.Limits.Defaults.BPSLocal
	}

	// Create bucket with 1:1 burst ratio (rate == capacity)
	bucket := ratelimit.NewBucketWithRate(float64(rateLimit), rateLimit)
	lrl.buckets[username] = bucket

	log.Info().
		Str("username", username).
		Int64("rate_limit_bps", rateLimit).
		Msg("Created local rate limiter")

	return bucket
}

// Start starts the local rate limiter (no-op for local limiter)
func (lrl *LocalRateLimiter) Start() error {
	log.Info().Msg("Local rate limiter started")
	return nil
}

// Stop stops the local rate limiter
func (lrl *LocalRateLimiter) Stop() {
	log.Info().Msg("Local rate limiter stopped")
}
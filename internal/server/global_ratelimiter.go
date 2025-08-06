package server

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/juju/ratelimit"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// RedisCoordinationConfig holds Redis coordination settings
type RedisCoordinationConfig struct {
	Enabled           bool          `yaml:"enabled"`
	Sentinels         []string      `yaml:"redis_sentinels"`
	MasterName        string        `yaml:"redis_master_name"`
	Password          string        `yaml:"redis_password"`
	Database          int           `yaml:"redis_database"`
	SyncInterval      time.Duration `yaml:"sync_interval"`
	RebalanceInterval time.Duration `yaml:"rebalance_interval"`
	CleanupInterval   time.Duration `yaml:"cleanup_interval"`
	StartupTimeout    time.Duration `yaml:"startup_timeout"`
}

// GeneratePeerID generates a unique peer identifier
func GeneratePeerID() string {
	bytes := make([]byte, 6)
	rand.Read(bytes)
	return fmt.Sprintf("%x", bytes)
}

// GlobalRateLimiter manages dynamic global rate limiting with Redis coordination
type GlobalRateLimiter struct {
	config       *Config
	redisClient  *redis.Client
	myPeerID     string
	isShuttingDown atomic.Bool
	
	// Usage tracking
	usageBuffers  map[string]int64         // username -> bytes used since last sync
	usageStats    map[string]*GlobalUserStats // username -> connection stats
	bufferMutex   sync.Mutex
	
	// Global rate limiting buckets
	globalBuckets map[string]*ratelimit.Bucket // username -> dynamic global bucket
	bucketsMutex  sync.RWMutex
}

// GlobalUserStats tracks connection statistics for a user in global rate limiter
type GlobalUserStats struct {
	TotalBytes      int64
	TimeConnected   time.Time
	ConnectionCount int    // Number of active connections for this user
	mutex           sync.Mutex
}

// NewGlobalRateLimiter creates a new global rate limiter
func NewGlobalRateLimiter(config *Config, peerID string) (*GlobalRateLimiter, error) {
	if !config.Redis.Enabled {
		return nil, fmt.Errorf("global rate limiter requires Redis to be enabled")
	}

	grl := &GlobalRateLimiter{
		config:        config,
		myPeerID:      peerID,
		usageBuffers:  make(map[string]int64),
		usageStats:    make(map[string]*GlobalUserStats),
		globalBuckets: make(map[string]*ratelimit.Bucket),
	}

	// Connect to Redis
	if err := grl.connectToRedis(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return grl, nil
}

// connectToRedis establishes Redis connection
func (grl *GlobalRateLimiter) connectToRedis() error {
	if len(grl.config.Redis.Sentinels) > 0 {
		// Use Redis Sentinel
		grl.redisClient = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:    grl.config.Redis.MasterName,
			SentinelAddrs: grl.config.Redis.Sentinels,
			Password:      grl.config.Redis.Password,
			DB:            grl.config.Redis.Database,
		})
	} else {
		return fmt.Errorf("Redis Sentinel configuration required for global rate limiter")
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), grl.config.Redis.StartupTimeout)
	defer cancel()

	if err := grl.redisClient.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("Redis ping failed: %w", err)
	}

	log.Info().Msg("Connected to Redis for global rate limiting")
	return nil
}

// GetGlobalBucket returns or creates a global rate limiting bucket for a user
func (grl *GlobalRateLimiter) GetGlobalBucket(username string) *ratelimit.Bucket {
	grl.bucketsMutex.RLock()
	if bucket, exists := grl.globalBuckets[username]; exists {
		grl.bucketsMutex.RUnlock()
		return bucket
	}
	grl.bucketsMutex.RUnlock()

	// Create new global bucket under write lock
	grl.bucketsMutex.Lock()
	defer grl.bucketsMutex.Unlock()

	// Double-check after acquiring write lock
	if bucket, exists := grl.globalBuckets[username]; exists {
		return bucket
	}

	// Get user's configured global rate limit
	var rateLimit int64
	if userLimits := grl.config.Limits.GetUserLimits(username); userLimits != nil {
		rateLimit = userLimits.BPSGlobal
	} else {
		rateLimit = grl.config.Limits.Defaults.BPSGlobal
	}

	// Create global bucket with same initial rate as local (will be rebalanced)
	bucket := ratelimit.NewBucketWithRate(float64(rateLimit), rateLimit)
	grl.globalBuckets[username] = bucket

	log.Info().
		Str("username", username).
		Int64("initial_global_rate_bps", rateLimit).
		Msg("Created global rate limiter bucket")

	return bucket
}

// initializeUserStats initializes usage tracking for a user
func (grl *GlobalRateLimiter) initializeUserStats(username string) {
	grl.bufferMutex.Lock()
	defer grl.bufferMutex.Unlock()
	
	if _, exists := grl.usageStats[username]; !exists {
		grl.usageStats[username] = &GlobalUserStats{
			TotalBytes:      0,
			TimeConnected:   time.Now(),
			ConnectionCount: 0,
		}
	}
}

// UserConnected increments connection count for a user
func (grl *GlobalRateLimiter) UserConnected(username string) {
	grl.bufferMutex.Lock()
	defer grl.bufferMutex.Unlock()
	
	// Initialize user stats if they don't exist
	if _, exists := grl.usageStats[username]; !exists {
		grl.usageStats[username] = &GlobalUserStats{
			TotalBytes:      0,
			TimeConnected:   time.Now(),
			ConnectionCount: 0,
		}
	}
	
	stats := grl.usageStats[username]
	stats.mutex.Lock()
	stats.ConnectionCount++
	connectionCount := stats.ConnectionCount
	stats.mutex.Unlock()
	
	log.Info().
		Str("username", username).
		Int("connection_count", connectionCount).
		Msg("User connection added")
}

// UserDisconnected decrements connection count for a user and cleans up if needed
func (grl *GlobalRateLimiter) UserDisconnected(username string) {
	grl.bufferMutex.Lock()
	defer grl.bufferMutex.Unlock()
	
	if stats, exists := grl.usageStats[username]; exists {
		stats.mutex.Lock()
		stats.ConnectionCount--
		connectionCount := stats.ConnectionCount
		stats.mutex.Unlock()
		
		log.Info().
			Str("username", username).
			Int("connection_count", connectionCount).
			Msg("User connection removed")
		
		// If no more connections, clean up user stats and global bucket
		if connectionCount <= 0 {
			delete(grl.usageStats, username)
			delete(grl.usageBuffers, username)
			
			// Also clean up global bucket to stop rebalancing
			grl.bucketsMutex.Lock()
			delete(grl.globalBuckets, username)
			grl.bucketsMutex.Unlock()
			
			log.Info().
				Str("username", username).
				Msg("Cleaned up user stats and global bucket - no active connections")
		}
	}
}

// TrackUsage records usage for a user (implements UsageReporter interface)
func (grl *GlobalRateLimiter) TrackUsage(username string, bytesUsed int64) {
	grl.bufferMutex.Lock()
	defer grl.bufferMutex.Unlock()
	
	// Update buffer for Redis sync
	grl.usageBuffers[username] += bytesUsed
	
	// Update total usage stats
	if stats, exists := grl.usageStats[username]; exists {
		stats.mutex.Lock()
		stats.TotalBytes += bytesUsed
		stats.mutex.Unlock()
	}
}

// Start begins the global rate limiter
func (grl *GlobalRateLimiter) Start() error {
	log.Info().Msg("Starting global rate limiter")
	
	// Start 1-second Redis publishing loop
	go grl.publishLoop()
	
	// Start 5-second rebalancing loop
	go grl.rebalanceLoop()
	
	return nil
}

// Stop shuts down the global rate limiter
func (grl *GlobalRateLimiter) Stop() {
	log.Info().Msg("Stopping global rate limiter")
	grl.isShuttingDown.Store(true)
	
	if grl.redisClient != nil {
		grl.redisClient.Close()
	}
}

// publishLoop publishes usage data to Redis every 1 second
func (grl *GlobalRateLimiter) publishLoop() {
	for !grl.isShuttingDown.Load() {
		grl.publishUsageToRedis()
		time.Sleep(1 * time.Second)
	}
}

// rebalanceLoop rebalances global quotas every 5 seconds
func (grl *GlobalRateLimiter) rebalanceLoop() {
	for !grl.isShuttingDown.Load() {
		grl.rebalanceGlobalQuotas()
		time.Sleep(5 * time.Second)
	}
}

// publishUsageToRedis publishes current usage data to Redis every 1 second
func (grl *GlobalRateLimiter) publishUsageToRedis() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	grl.bufferMutex.Lock()
	defer grl.bufferMutex.Unlock()

	for username, stats := range grl.usageStats {
		stats.mutex.Lock()
		totalBytes := stats.TotalBytes
		timeConnected := stats.TimeConnected
		connectionCount := stats.ConnectionCount
		stats.mutex.Unlock()

		// Only publish if user has active connections
		if connectionCount <= 0 {
			log.Debug().
				Str("username", username).
				Msg("Skipping publish - no active connections")
			continue
		}

		// Calculate current MB/s for this user on this proxy
		duration := time.Since(timeConnected).Seconds()
		if duration < 1.0 {
			duration = 1.0 // Avoid division by zero
		}
		currentMBps := float64(totalBytes) / (1024 * 1024) / duration

		// Publish to Redis with 3-second TTL (if proxy stops, it gets removed)
		pipe := grl.redisClient.Pipeline()
		
		// Track this proxy as connected for this user
		proxyKey := fmt.Sprintf("user:%s:proxies", username)
		pipe.SAdd(ctx, proxyKey, grl.myPeerID)
		pipe.Expire(ctx, proxyKey, 3*time.Second)
		
		// Track current MB/s for this proxy
		usageKey := fmt.Sprintf("user:%s:usage:%s", username, grl.myPeerID)
		pipe.Set(ctx, usageKey, fmt.Sprintf("%.3f", currentMBps), 3*time.Second)
		
		if _, err := pipe.Exec(ctx); err != nil {
			log.Error().Err(err).
				Str("username", username).
				Msg("Failed to publish usage to Redis")
		} else {
			log.Debug().
				Str("username", username).
				Float64("current_mbps", currentMBps).
				Int("connection_count", connectionCount).
				Str("proxy_id", grl.myPeerID).
				Msg("Published usage to Redis")
		}
	}
}

// rebalanceGlobalQuotas rebalances global rate limits every 5 seconds
func (grl *GlobalRateLimiter) rebalanceGlobalQuotas() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	grl.bucketsMutex.RLock()
	usernames := make([]string, 0, len(grl.globalBuckets))
	for username := range grl.globalBuckets {
		usernames = append(usernames, username)
	}
	grl.bucketsMutex.RUnlock()

	for _, username := range usernames {
		grl.rebalanceUserQuota(ctx, username)
	}
}

// rebalanceUserQuota rebalances quota for a specific user
func (grl *GlobalRateLimiter) rebalanceUserQuota(ctx context.Context, username string) {
	// Get user's total configured global quota
	var totalQuota int64
	if userLimits := grl.config.Limits.GetUserLimits(username); userLimits != nil {
		totalQuota = userLimits.BPSGlobal
	} else {
		totalQuota = grl.config.Limits.Defaults.BPSGlobal
	}

	// Get number of connected proxies for this user
	proxyKey := fmt.Sprintf("user:%s:proxies", username)
	proxies, err := grl.redisClient.SMembers(ctx, proxyKey).Result()
	if err != nil {
		log.Error().Err(err).
			Str("username", username).
			Msg("Failed to get connected proxies from Redis")
		return
	}

	proxyCount := len(proxies)
	if proxyCount == 0 {
		// No proxies connected, keep current quota
		log.Debug().
			Str("username", username).
			Msg("No proxies found in Redis, keeping current quota")
		return
	}

	// Calculate even distribution
	quotaPerProxy := totalQuota / int64(proxyCount)
	if quotaPerProxy < 1024 { // Minimum 1KB/s
		quotaPerProxy = 1024
	}

	// Update this proxy's global rate limiter bucket
	grl.bucketsMutex.Lock()
	if _, exists := grl.globalBuckets[username]; exists {
		// Replace bucket with new rate (juju/ratelimit doesn't have SetRate)
		newBucket := ratelimit.NewBucketWithRate(float64(quotaPerProxy), quotaPerProxy)
		grl.globalBuckets[username] = newBucket
		grl.bucketsMutex.Unlock()

		log.Info().
			Str("username", username).
			Int("proxy_count", proxyCount).
			Int64("total_quota_bps", totalQuota).
			Int64("new_quota_per_proxy_bps", quotaPerProxy).
			Float64("new_rate_mbps", float64(quotaPerProxy)/(1024*1024)).
			Msg("Rebalanced global quota")
	} else {
		grl.bucketsMutex.Unlock()
	}
}
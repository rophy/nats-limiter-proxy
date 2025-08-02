package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// GlobalRateLimiter aggregates usage counters across all proxy instances
// It does NOT enforce rate limits - that's handled by LocalRateLimiter
type GlobalRateLimiter struct {
	config       *Config
	redisClient  *redis.Client
	myPeerID     string
	shutdown     chan struct{}
	usageBuffers map[string]int64 // username -> bytes used since last sync
	bufferMutex  sync.Mutex
}

// NewGlobalRateLimiter creates a new global rate limiter
func NewGlobalRateLimiter(config *Config, peerID string) (*GlobalRateLimiter, error) {
	if !config.Redis.Enabled {
		return nil, fmt.Errorf("global rate limiter requires Redis to be enabled")
	}

	grl := &GlobalRateLimiter{
		config:       config,
		myPeerID:     peerID,
		shutdown:     make(chan struct{}),
		usageBuffers: make(map[string]int64),
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

// TrackUsage records usage for a user (implements UsageReporter interface)
func (grl *GlobalRateLimiter) TrackUsage(username string, bytesUsed int64) {
	grl.bufferMutex.Lock()
	grl.usageBuffers[username] += bytesUsed
	grl.bufferMutex.Unlock()
}

// Start begins the global rate limiter
func (grl *GlobalRateLimiter) Start() error {
	log.Info().Msg("Starting global rate limiter")
	
	// Start periodic sync to Redis
	go grl.syncLoop()
	
	return nil
}

// Stop shuts down the global rate limiter
func (grl *GlobalRateLimiter) Stop() {
	log.Info().Msg("Stopping global rate limiter")
	close(grl.shutdown)
	
	if grl.redisClient != nil {
		grl.redisClient.Close()
	}
}

// syncLoop periodically syncs usage data to Redis
func (grl *GlobalRateLimiter) syncLoop() {
	ticker := time.NewTicker(grl.config.Redis.SyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			grl.syncUsageToRedis()
		case <-grl.shutdown:
			return
		}
	}
}

// syncUsageToRedis syncs buffered usage data to Redis
func (grl *GlobalRateLimiter) syncUsageToRedis() {
	grl.bufferMutex.Lock()
	// Copy and reset buffers
	currentUsage := make(map[string]int64)
	for username, bytes := range grl.usageBuffers {
		currentUsage[username] = bytes
		grl.usageBuffers[username] = 0
	}
	grl.bufferMutex.Unlock()

	if len(currentUsage) == 0 {
		return // Nothing to sync
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Store usage data with current timestamp
	timestamp := time.Now().Unix()
	
	for username, bytesUsed := range currentUsage {
		if bytesUsed == 0 {
			continue
		}

		// Store current usage with TTL
		key := fmt.Sprintf("global_usage:%s:%s:%d", username, grl.myPeerID, timestamp)
		
		pipe := grl.redisClient.Pipeline()
		pipe.Set(ctx, key, bytesUsed, 5*time.Minute) // TTL for cleanup
		
		// Also maintain an aggregate counter (sliding window sum)
		aggregateKey := fmt.Sprintf("global_aggregate:%s", username)
		pipe.ZAdd(ctx, aggregateKey, redis.Z{
			Score:  float64(timestamp),
			Member: fmt.Sprintf("%s:%d", grl.myPeerID, bytesUsed),
		})
		pipe.ZRemRangeByScore(ctx, aggregateKey, "0", fmt.Sprintf("%d", timestamp-300)) // Keep 5 minutes
		pipe.Expire(ctx, aggregateKey, 10*time.Minute)
		
		if _, err := pipe.Exec(ctx); err != nil {
			log.Error().Err(err).
				Str("username", username).
				Msg("Failed to sync usage to Redis")
		} else {
			log.Debug().
				Str("username", username).
				Int64("bytes_used", bytesUsed).
				Msg("Synced usage to global counter")
		}
	}
}

// GetGlobalUsage returns aggregated usage across all proxies for a user
func (grl *GlobalRateLimiter) GetGlobalUsage(username string) (map[string]int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	aggregateKey := fmt.Sprintf("global_aggregate:%s", username)
	
	// Get all entries from the last 5 minutes
	now := time.Now().Unix()
	entries, err := grl.redisClient.ZRangeByScore(ctx, aggregateKey, &redis.ZRangeBy{
		Min: fmt.Sprintf("%d", now-300),
		Max: fmt.Sprintf("%d", now),
	}).Result()
	
	if err != nil {
		return nil, fmt.Errorf("failed to get global usage: %w", err)
	}

	usage := make(map[string]int64)
	for _, entry := range entries {
		// Parse "peerID:bytes" format
		var peerID string
		var bytes int64
		if n, err := fmt.Sscanf(entry, "%[^:]:%d", &peerID, &bytes); n == 2 && err == nil {
			usage[peerID] += bytes
		}
	}

	return usage, nil
}

// GetTotalGlobalUsage returns total usage across all proxies for a user
func (grl *GlobalRateLimiter) GetTotalGlobalUsage(username string) (int64, error) {
	usage, err := grl.GetGlobalUsage(username)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, bytes := range usage {
		total += bytes
	}
	
	return total, nil
}
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
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

// RedisCoordinator manages coordination via Redis instead of gossip protocol
type RedisCoordinator struct {
	config         *RedisCoordinationConfig
	rateConfig     *Config  // Rate limiting config
	myPeerID       string
	redis          *redis.Client
	tokenBalancer  *TokenBalancer
	shutdownChan   chan struct{}
	mu             sync.RWMutex
	
	// Local state cached from Redis
	lastSyncTime   time.Time
	redisHealthy   bool
}

// NewRedisCoordinator creates a new Redis-based coordinator
func NewRedisCoordinator(config *RedisCoordinationConfig, peerID string) *RedisCoordinator {
	rc := &RedisCoordinator{
		config:       config,
		myPeerID:     peerID,
		shutdownChan: make(chan struct{}),
		redisHealthy: false,
	}
	
	rc.tokenBalancer = NewTokenBalancer(rc)
	rc.tokenBalancer.SetPeerID(peerID)
	
	return rc
}

// Start initializes Redis connection and begins coordination
func (rc *RedisCoordinator) Start() error {
	if !rc.config.Enabled {
		log.Info().Msg("Redis coordination disabled")
		return nil
	}
	
	// Connect to Redis with startup timeout
	ctx, cancel := context.WithTimeout(context.Background(), rc.config.StartupTimeout)
	defer cancel()
	
	if err := rc.connectToRedis(ctx); err != nil {
		log.Fatal().Err(err).Msg("Cannot connect to Redis on startup - crashing as designed")
		return fmt.Errorf("redis connection failed: %w", err)
	}
	
	// Start token balancer
	if err := rc.tokenBalancer.Start(); err != nil {
		return err
	}
	
	// Start background tasks
	go rc.syncLoop()
	go rc.rebalanceLoop()
	go rc.cleanupLoop()
	go rc.healthCheckLoop()
	
	log.Info().
		Str("peer_id", rc.myPeerID).
		Str("master_name", rc.config.MasterName).
		Msg("Started Redis coordination")
	
	return nil
}

// Stop shuts down Redis coordination
func (rc *RedisCoordinator) Stop() {
	log.Info().Msg("Stopping Redis coordination")
	close(rc.shutdownChan)
	
	if rc.tokenBalancer != nil {
		rc.tokenBalancer.Stop()
	}
	
	if rc.redis != nil {
		rc.redis.Close()
	}
}

// SetConfig sets the rate limiting configuration
func (rc *RedisCoordinator) SetConfig(config *Config) {
	rc.rateConfig = config
	if rc.tokenBalancer != nil {
		rc.tokenBalancer.SetConfig(config)
	}
}

// GetOrCreateBucket delegates to token balancer
func (rc *RedisCoordinator) GetOrCreateBucket(username string) *ratelimit.Bucket {
	return rc.tokenBalancer.GetOrCreateBucket(username)
}

// ReportUsage reports local usage statistics to Redis
func (rc *RedisCoordinator) ReportUsage(username string, usedBytes int64, requestRate float64, localAllocation int64) {
	if !rc.config.Enabled || !rc.isRedisHealthy() {
		return
	}
	
	usage := UserUsage{
		Username:        username,
		UsedBytes:       usedBytes,
		RequestRate:     requestRate,
		LocalAllocation: localAllocation,
		LastUpdated:     time.Now(),
	}
	
	go rc.syncUsageToRedis(username, usage)
}

// GetPeerUsage returns usage statistics for a user from Redis
func (rc *RedisCoordinator) GetPeerUsage(username string) map[string]*UserUsage {
	if !rc.config.Enabled || !rc.isRedisHealthy() {
		return nil
	}
	
	ctx := context.Background()
	key := fmt.Sprintf("usage:%s", username)
	
	result, err := rc.redis.HGetAll(ctx, key).Result()
	if err != nil {
		log.Debug().Err(err).Str("username", username).Msg("Failed to get peer usage from Redis")
		return nil
	}
	
	peerUsage := make(map[string]*UserUsage)
	for peerID, usageJSON := range result {
		var usage UserUsage
		if err := json.Unmarshal([]byte(usageJSON), &usage); err != nil {
			log.Debug().Err(err).Str("peer_id", peerID).Msg("Failed to unmarshal usage data")
			continue
		}
		
		// Skip stale data (older than 2 minutes)
		if time.Since(usage.LastUpdated) > 2*time.Minute {
			continue
		}
		
		peerUsage[peerID] = &usage
	}
	
	return peerUsage
}

// connectToRedis establishes connection to Redis via Sentinel
func (rc *RedisCoordinator) connectToRedis(ctx context.Context) error {
	if len(rc.config.Sentinels) == 0 {
		return fmt.Errorf("no Redis sentinels configured")
	}
	
	// Create Sentinel client
	sentinel := redis.NewSentinelClient(&redis.Options{
		Addr:     rc.config.Sentinels[0], // Connect to first sentinel
		Password: rc.config.Password,
	})
	
	// Get master address from sentinel
	masterAddr, err := sentinel.GetMasterAddrByName(ctx, rc.config.MasterName).Result()
	if err != nil {
		sentinel.Close()
		return fmt.Errorf("failed to get master address from sentinel: %w", err)
	}
	
	sentinel.Close()
	
	// Connect to Redis master
	rc.redis = redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", masterAddr[0], masterAddr[1]),
		Password: rc.config.Password,
		DB:       rc.config.Database,
	})
	
	// Test connection
	if err := rc.redis.Ping(ctx).Err(); err != nil {
		rc.redis.Close()
		return fmt.Errorf("failed to ping Redis master: %w", err)
	}
	
	rc.mu.Lock()
	rc.redisHealthy = true
	rc.mu.Unlock()
	
	log.Info().
		Str("master_addr", fmt.Sprintf("%s:%s", masterAddr[0], masterAddr[1])).
		Msg("Connected to Redis master")
	
	return nil
}

// syncUsageToRedis syncs local usage to Redis
func (rc *RedisCoordinator) syncUsageToRedis(username string, usage UserUsage) {
	ctx := context.Background()
	key := fmt.Sprintf("usage:%s", username)
	
	usageJSON, err := json.Marshal(usage)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal usage data")
		return
	}
	
	// Store usage with TTL
	if err := rc.redis.HSet(ctx, key, rc.myPeerID, usageJSON).Err(); err != nil {
		log.Debug().Err(err).Str("username", username).Msg("Failed to sync usage to Redis")
		rc.setRedisUnhealthy()
		return
	}
	
	// Set expiration on the hash key
	rc.redis.Expire(ctx, key, 5*time.Minute)
}

// syncLoop periodically syncs usage to Redis
func (rc *RedisCoordinator) syncLoop() {
	ticker := time.NewTicker(rc.config.SyncInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			rc.syncAllUsage()
		case <-rc.shutdownChan:
			return
		}
	}
}

// rebalanceLoop periodically rebalances token allocations
func (rc *RedisCoordinator) rebalanceLoop() {
	ticker := time.NewTicker(rc.config.RebalanceInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			rc.performRebalance()
		case <-rc.shutdownChan:
			return
		}
	}
}

// cleanupLoop periodically cleans up old data
func (rc *RedisCoordinator) cleanupLoop() {
	ticker := time.NewTicker(rc.config.CleanupInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			rc.cleanup()
		case <-rc.shutdownChan:
			return
		}
	}
}

// healthCheckLoop periodically checks Redis health
func (rc *RedisCoordinator) healthCheckLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			rc.checkRedisHealth()
		case <-rc.shutdownChan:
			return
		}
	}
}

// performRebalance performs distributed rebalancing with locking
func (rc *RedisCoordinator) performRebalance() {
	if !rc.isRedisHealthy() {
		log.Debug().Msg("Redis unhealthy, skipping rebalance")
		return
	}
	
	// Get all users that need rebalancing
	users := rc.getUsersToRebalance()
	
	for _, username := range users {
		rc.rebalanceUserWithLock(username)
	}
}

// rebalanceUserWithLock performs rebalancing for a single user with distributed locking
func (rc *RedisCoordinator) rebalanceUserWithLock(username string) {
	ctx := context.Background()
	lockKey := fmt.Sprintf("rebalance_lock:%s", username)
	lockTTL := 30 * time.Second
	
	// Try to acquire distributed lock
	acquired, err := rc.redis.SetNX(ctx, lockKey, rc.myPeerID, lockTTL).Result()
	if err != nil {
		log.Debug().Err(err).Str("username", username).Msg("Failed to acquire rebalance lock")
		return
	}
	
	if !acquired {
		log.Debug().Str("username", username).Msg("Another peer is rebalancing this user")
		return
	}
	
	// We have the lock - perform rebalancing
	defer rc.redis.Del(ctx, lockKey)
	
	log.Debug().Str("username", username).Msg("Acquired rebalance lock, performing rebalancing")
	rc.tokenBalancer.rebalanceUser(username)
}

// syncAllUsage syncs current local usage to Redis
func (rc *RedisCoordinator) syncAllUsage() {
	if !rc.isRedisHealthy() {
		return
	}
	
	// This would sync current usage from local buckets
	// For now, just update sync time
	rc.mu.Lock()
	rc.lastSyncTime = time.Now()
	rc.mu.Unlock()
}

// cleanup removes old data from Redis
func (rc *RedisCoordinator) cleanup() {
	if !rc.isRedisHealthy() {
		return
	}
	
	ctx := context.Background()
	
	// Clean up old usage data
	keys, err := rc.redis.Keys(ctx, "usage:*").Result()
	if err != nil {
		log.Debug().Err(err).Msg("Failed to get usage keys for cleanup")
		return
	}
	
	for _, key := range keys {
		// Remove stale peer data from hash
		peerData, err := rc.redis.HGetAll(ctx, key).Result()
		if err != nil {
			continue
		}
		
		stalePeers := []string{}
		for peerID, usageJSON := range peerData {
			var usage UserUsage
			if err := json.Unmarshal([]byte(usageJSON), &usage); err != nil {
				stalePeers = append(stalePeers, peerID)
				continue
			}
			
			if time.Since(usage.LastUpdated) > 5*time.Minute {
				stalePeers = append(stalePeers, peerID)
			}
		}
		
		if len(stalePeers) > 0 {
			rc.redis.HDel(ctx, key, stalePeers...)
		}
	}
}

// checkRedisHealth checks if Redis is still healthy
func (rc *RedisCoordinator) checkRedisHealth() {
	if rc.redis == nil {
		return
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	
	err := rc.redis.Ping(ctx).Err()
	
	rc.mu.Lock()
	wasHealthy := rc.redisHealthy
	rc.redisHealthy = (err == nil)
	rc.mu.Unlock()
	
	if !rc.redisHealthy && wasHealthy {
		log.Warn().Err(err).Msg("Redis became unhealthy - running in degraded mode")
	} else if rc.redisHealthy && !wasHealthy {
		log.Info().Msg("Redis connection restored")
	}
}

// isRedisHealthy returns current Redis health status
func (rc *RedisCoordinator) isRedisHealthy() bool {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.redisHealthy
}

// setRedisUnhealthy marks Redis as unhealthy
func (rc *RedisCoordinator) setRedisUnhealthy() {
	rc.mu.Lock()
	rc.redisHealthy = false
	rc.mu.Unlock()
}

// getUsersToRebalance returns users that need rebalancing
func (rc *RedisCoordinator) getUsersToRebalance() []string {
	if !rc.isRedisHealthy() {
		return nil
	}
	
	ctx := context.Background()
	keys, err := rc.redis.Keys(ctx, "usage:*").Result()
	if err != nil {
		log.Debug().Err(err).Msg("Failed to get users for rebalancing")
		return nil
	}
	
	users := make([]string, 0, len(keys))
	for _, key := range keys {
		// Extract username from "usage:username" key
		if len(key) > 6 { // len("usage:") = 6
			username := key[6:]
			users = append(users, username)
		}
	}
	
	return users
}

// GetActivePeers returns list of active peers from Redis
func (rc *RedisCoordinator) GetActivePeers() []*Peer {
	if !rc.isRedisHealthy() {
		return []*Peer{}
	}
	
	ctx := context.Background()
	keys, err := rc.redis.Keys(ctx, "usage:*").Result()
	if err != nil {
		return []*Peer{}
	}
	
	peerSet := make(map[string]bool)
	
	// Collect all active peer IDs from usage data
	for _, key := range keys {
		peerData, err := rc.redis.HGetAll(ctx, key).Result()
		if err != nil {
			continue
		}
		
		for peerID, usageJSON := range peerData {
			var usage UserUsage
			if err := json.Unmarshal([]byte(usageJSON), &usage); err != nil {
				continue
			}
			
			// Only include recent peers
			if time.Since(usage.LastUpdated) < 2*time.Minute {
				peerSet[peerID] = true
			}
		}
	}
	
	// Convert to peer list
	peers := make([]*Peer, 0, len(peerSet))
	for peerID := range peerSet {
		peers = append(peers, &Peer{
			ID:       peerID,
			Address:  peerID, // For Redis, we don't track addresses
			LastSeen: time.Now(),
			Status:   PeerStatusActive,
		})
	}
	
	return peers
}

// IsHealthy returns overall coordinator health
func (rc *RedisCoordinator) IsHealthy() bool {
	if !rc.config.Enabled {
		return true // Always healthy when disabled
	}
	return rc.isRedisHealthy()
}

// GetLastSyncTime returns when we last synced with Redis
func (rc *RedisCoordinator) GetLastSyncTime() time.Time {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.lastSyncTime
}
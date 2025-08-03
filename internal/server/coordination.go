package server

import (
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// CoordinationConfig holds peer coordination settings
type CoordinationConfig struct {
	Enabled          bool          `yaml:"enabled"`
	Port             int           `yaml:"port"`
	GossipInterval   time.Duration `yaml:"gossip_interval"`
	RebalanceInterval time.Duration `yaml:"rebalance_interval"`
	DiscoveryMethod  string        `yaml:"discovery_method"`
	CleanupInterval  time.Duration `yaml:"cleanup_interval"`
}

// Peer represents another proxy instance in the cluster
type Peer struct {
	ID       string    `json:"id"`
	Address  string    `json:"address"`
	LastSeen time.Time `json:"last_seen"`
	Status   PeerStatus `json:"status"`
}

type PeerStatus int

const (
	PeerStatusActive PeerStatus = iota
	PeerStatusInactive
	PeerStatusFailed
)

// UserUsage represents usage statistics for a user on a specific peer
type UserUsage struct {
	Username        string    `json:"username"`
	UsedBytes       int64     `json:"used_bytes"`
	RequestRate     float64   `json:"request_rate"`
	LocalAllocation int64     `json:"local_allocation"`
	LastUpdated     time.Time `json:"last_updated"`
}

// UsageGossip represents gossip message containing usage information
type UsageGossip struct {
	FromPeer    string               `json:"from_peer"`
	UserStats   map[string]UserUsage `json:"user_stats"`
	Timestamp   time.Time            `json:"timestamp"`
	MessageType GossipMessageType    `json:"message_type"`
}

type GossipMessageType int

const (
	GossipUsageReport GossipMessageType = iota
	GossipAllocationUpdate
	GossipPeerJoin
	GossipPeerLeave
)

// AllocationUpdate represents a token allocation update message
type AllocationUpdate struct {
	FromPeer    string    `json:"from_peer"`
	ToPeer      string    `json:"to_peer"`
	Username    string    `json:"username"`
	Allocation  int64     `json:"allocation"`
	Timestamp   time.Time `json:"timestamp"`
}

// PeerCoordinator manages peer-to-peer coordination for distributed rate limiting
type PeerCoordinator struct {
	config        *CoordinationConfig
	myPeerID      string
	myAddress     string
	peers         map[string]*Peer
	userUsage     map[string]map[string]*UserUsage // username -> peerID -> usage
	peerMutex     sync.RWMutex
	usageMutex    sync.RWMutex
	
	// Communication channels
	gossipChan     chan UsageGossip
	allocationChan chan AllocationUpdate
	shutdownChan   chan struct{}
	
	// Peer discovery and communication
	peerManager    *PeerManager
	gossipService  *GossipService
	tokenBalancer  *TokenBalancer
}

// NewPeerCoordinator creates a new peer coordinator
func NewPeerCoordinator(config *CoordinationConfig, peerID, address string) *PeerCoordinator {
	pc := &PeerCoordinator{
		config:         config,
		myPeerID:       peerID,
		myAddress:      address,
		peers:          make(map[string]*Peer),
		userUsage:      make(map[string]map[string]*UserUsage),
		gossipChan:     make(chan UsageGossip, 100),
		allocationChan: make(chan AllocationUpdate, 100),
		shutdownChan:   make(chan struct{}),
	}
	
	pc.peerManager = NewPeerManager(pc)
	pc.gossipService = NewGossipService(pc)
	pc.tokenBalancer = NewTokenBalancer(pc)
	
	return pc
}

// Start begins peer coordination services
func (pc *PeerCoordinator) Start() error {
	if !pc.config.Enabled {
		log.Info().Msg("Peer coordination disabled")
		return nil
	}
	
	log.Info().
		Str("peer_id", pc.myPeerID).
		Str("address", pc.myAddress).
		Msg("Starting peer coordination")
	
	// Start peer discovery
	if err := pc.peerManager.Start(); err != nil {
		return err
	}
	
	// Start gossip service
	if err := pc.gossipService.Start(); err != nil {
		return err
	}
	
	// Start token balancer
	if err := pc.tokenBalancer.Start(); err != nil {
		return err
	}
	
	// Start background tasks
	go pc.gossipLoop()
	go pc.rebalanceLoop()
	go pc.cleanupLoop()
	
	return nil
}

// Stop shuts down peer coordination
func (pc *PeerCoordinator) Stop() {
	log.Info().Msg("Stopping peer coordination")
	close(pc.shutdownChan)
	
	if pc.peerManager != nil {
		pc.peerManager.Stop()
	}
	if pc.gossipService != nil {
		pc.gossipService.Stop()
	}
	if pc.tokenBalancer != nil {
		pc.tokenBalancer.Stop()
	}
}

// ReportUsage reports local usage statistics for a user
func (pc *PeerCoordinator) ReportUsage(username string, usedBytes int64, requestRate float64, localAllocation int64) {
	if !pc.config.Enabled {
		return
	}
	
	pc.usageMutex.Lock()
	defer pc.usageMutex.Unlock()
	
	if pc.userUsage[username] == nil {
		pc.userUsage[username] = make(map[string]*UserUsage)
	}
	
	pc.userUsage[username][pc.myPeerID] = &UserUsage{
		Username:        username,
		UsedBytes:       usedBytes,
		RequestRate:     requestRate,
		LocalAllocation: localAllocation,
		LastUpdated:     time.Now(),
	}
}

// GetPeerUsage returns aggregated usage statistics for a user across all peers
func (pc *PeerCoordinator) GetPeerUsage(username string) map[string]*UserUsage {
	pc.usageMutex.RLock()
	defer pc.usageMutex.RUnlock()
	
	if userStats, exists := pc.userUsage[username]; exists {
		// Return a copy to avoid concurrent access issues
		result := make(map[string]*UserUsage)
		for peerID, usage := range userStats {
			result[peerID] = &UserUsage{
				Username:        usage.Username,
				UsedBytes:       usage.UsedBytes,
				RequestRate:     usage.RequestRate,
				LocalAllocation: usage.LocalAllocation,
				LastUpdated:     usage.LastUpdated,
			}
		}
		return result
	}
	return nil
}

// GetActivePeers returns list of active peers
func (pc *PeerCoordinator) GetActivePeers() []*Peer {
	pc.peerMutex.RLock()
	defer pc.peerMutex.RUnlock()
	
	var activePeers []*Peer
	for _, peer := range pc.peers {
		if peer.Status == PeerStatusActive {
			activePeers = append(activePeers, &Peer{
				ID:       peer.ID,
				Address:  peer.Address,
				LastSeen: peer.LastSeen,
				Status:   peer.Status,
			})
		}
	}
	return activePeers
}

// ProcessGossip processes incoming gossip messages
func (pc *PeerCoordinator) ProcessGossip(gossip UsageGossip) {
	if !pc.config.Enabled {
		return
	}
	
	log.Debug().
		Str("from_peer", gossip.FromPeer).
		Int("user_count", len(gossip.UserStats)).
		Msg("Processing gossip message")
	
	pc.usageMutex.Lock()
	defer pc.usageMutex.Unlock()
	
	// Update peer last seen
	pc.peerMutex.Lock()
	if peer, exists := pc.peers[gossip.FromPeer]; exists {
		peer.LastSeen = time.Now()
		peer.Status = PeerStatusActive
	}
	pc.peerMutex.Unlock()
	
	// Update user usage statistics
	for username, usage := range gossip.UserStats {
		if pc.userUsage[username] == nil {
			pc.userUsage[username] = make(map[string]*UserUsage)
		}
		pc.userUsage[username][gossip.FromPeer] = &usage
	}
}

// gossipLoop periodically sends usage gossip to peers
func (pc *PeerCoordinator) gossipLoop() {
	ticker := time.NewTicker(pc.config.GossipInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			pc.sendGossip()
		case <-pc.shutdownChan:
			return
		}
	}
}

// rebalanceLoop periodically rebalances token allocations
func (pc *PeerCoordinator) rebalanceLoop() {
	ticker := time.NewTicker(pc.config.RebalanceInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			pc.tokenBalancer.RebalanceAll()
		case <-pc.shutdownChan:
			return
		}
	}
}

// cleanupLoop periodically cleans up old data
func (pc *PeerCoordinator) cleanupLoop() {
	ticker := time.NewTicker(pc.config.CleanupInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			pc.cleanup()
		case <-pc.shutdownChan:
			return
		}
	}
}

// sendGossip sends current usage statistics to random peers
func (pc *PeerCoordinator) sendGossip() {
	pc.usageMutex.RLock()
	localStats := make(map[string]UserUsage)
	
	// Collect local usage statistics
	for username, peerStats := range pc.userUsage {
		if localUsage, exists := peerStats[pc.myPeerID]; exists {
			localStats[username] = *localUsage
		}
	}
	pc.usageMutex.RUnlock()
	
	if len(localStats) == 0 {
		return
	}
	
	gossip := UsageGossip{
		FromPeer:    pc.myPeerID,
		UserStats:   localStats,
		Timestamp:   time.Now(),
		MessageType: GossipUsageReport,
	}
	
	// Send to random peers
	peers := pc.getRandomPeers(3)
	for _, peer := range peers {
		go pc.gossipService.SendGossip(peer.Address, gossip)
	}
}

// cleanup removes old peer and usage data
func (pc *PeerCoordinator) cleanup() {
	now := time.Now()
	peerTimeout := 60 * time.Second
	usageTimeout := 5 * time.Minute
	
	// Clean up inactive peers
	pc.peerMutex.Lock()
	for peerID, peer := range pc.peers {
		if now.Sub(peer.LastSeen) > peerTimeout {
			log.Info().Str("peer_id", peerID).Msg("Removing inactive peer")
			delete(pc.peers, peerID)
		}
	}
	pc.peerMutex.Unlock()
	
	// Clean up old usage data
	pc.usageMutex.Lock()
	for username, peerStats := range pc.userUsage {
		for peerID, usage := range peerStats {
			if now.Sub(usage.LastUpdated) > usageTimeout {
				delete(peerStats, peerID)
			}
		}
		if len(peerStats) == 0 {
			delete(pc.userUsage, username)
		}
	}
	pc.usageMutex.Unlock()
}

// getRandomPeers returns random subset of active peers
func (pc *PeerCoordinator) getRandomPeers(count int) []*Peer {
	activePeers := pc.GetActivePeers()
	if len(activePeers) <= count {
		return activePeers
	}
	
	// Simple random selection (could be improved with better algorithm)
	selected := make([]*Peer, 0, count)
	for i := 0; i < count && i < len(activePeers); i++ {
		selected = append(selected, activePeers[i])
	}
	return selected
}

// AddPeer adds a new peer to the cluster
func (pc *PeerCoordinator) AddPeer(peer *Peer) {
	pc.peerMutex.Lock()
	defer pc.peerMutex.Unlock()
	
	pc.peers[peer.ID] = peer
	log.Info().
		Str("peer_id", peer.ID).
		Str("address", peer.Address).
		Msg("Added peer to cluster")
}

// RemovePeer removes a peer from the cluster
func (pc *PeerCoordinator) RemovePeer(peerID string) {
	pc.peerMutex.Lock()
	defer pc.peerMutex.Unlock()
	
	delete(pc.peers, peerID)
	
	// Also remove usage data for this peer
	pc.usageMutex.Lock()
	for username, peerStats := range pc.userUsage {
		delete(peerStats, peerID)
		if len(peerStats) == 0 {
			delete(pc.userUsage, username)
		}
	}
	pc.usageMutex.Unlock()
	
	log.Info().Str("peer_id", peerID).Msg("Removed peer from cluster")
}
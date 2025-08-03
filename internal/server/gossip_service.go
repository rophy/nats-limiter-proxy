package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// GossipService handles peer-to-peer communication via HTTP
type GossipService struct {
	coordinator  *PeerCoordinator
	httpServer   *http.Server
	client       *http.Client
	shutdownChan chan struct{}
	wg           sync.WaitGroup
}

// NewGossipService creates a new gossip service
func NewGossipService(coordinator *PeerCoordinator) *GossipService {
	return &GossipService{
		coordinator:  coordinator,
		client:       &http.Client{Timeout: 5 * time.Second},
		shutdownChan: make(chan struct{}),
	}
}

// Start begins the gossip service HTTP server
func (gs *GossipService) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/gossip", gs.handleGossip)
	mux.HandleFunc("/allocation", gs.handleAllocation)
	mux.HandleFunc("/health", gs.handleHealth)
	mux.HandleFunc("/peers", gs.handlePeers)
	
	gs.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", gs.coordinator.config.Port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	
	// Start HTTP server in background
	gs.wg.Add(1)
	go func() {
		defer gs.wg.Done()
		
		log.Info().
			Int("port", gs.coordinator.config.Port).
			Msg("Starting gossip HTTP server")
		
		if err := gs.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("Gossip HTTP server failed")
		}
	}()
	
	return nil
}

// Stop shuts down the gossip service
func (gs *GossipService) Stop() {
	close(gs.shutdownChan)
	
	if gs.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gs.httpServer.Shutdown(ctx)
	}
	
	gs.wg.Wait()
}

// SendGossip sends a gossip message to a peer
func (gs *GossipService) SendGossip(peerAddress string, gossip UsageGossip) error {
	url := fmt.Sprintf("http://%s/gossip", peerAddress)
	
	jsonData, err := json.Marshal(gossip)
	if err != nil {
		return fmt.Errorf("failed to marshal gossip: %w", err)
	}
	
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := gs.client.Do(req)
	if err != nil {
		log.Debug().
			Str("peer_address", peerAddress).
			Err(err).
			Msg("Failed to send gossip")
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gossip request failed with status: %d", resp.StatusCode)
	}
	
	log.Debug().
		Str("peer_address", peerAddress).
		Int("user_count", len(gossip.UserStats)).
		Msg("Gossip sent successfully")
	
	return nil
}

// SendAllocation sends a token allocation update to a peer
func (gs *GossipService) SendAllocation(peerAddress string, allocation AllocationUpdate) error {
	url := fmt.Sprintf("http://%s/allocation", peerAddress)
	
	jsonData, err := json.Marshal(allocation)
	if err != nil {
		return fmt.Errorf("failed to marshal allocation: %w", err)
	}
	
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := gs.client.Do(req)
	if err != nil {
		log.Debug().
			Str("peer_address", peerAddress).
			Err(err).
			Msg("Failed to send allocation")
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("allocation request failed with status: %d", resp.StatusCode)
	}
	
	log.Debug().
		Str("peer_address", peerAddress).
		Str("username", allocation.Username).
		Int64("allocation", allocation.Allocation).
		Msg("Allocation sent successfully")
	
	return nil
}

// handleGossip handles incoming gossip messages
func (gs *GossipService) handleGossip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to read gossip request body")
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	
	var gossip UsageGossip
	if err := json.Unmarshal(body, &gossip); err != nil {
		log.Error().Err(err).Msg("Failed to unmarshal gossip")
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	
	// Validate gossip message
	if gossip.FromPeer == "" || gossip.FromPeer == gs.coordinator.myPeerID {
		log.Debug().Str("from_peer", gossip.FromPeer).Msg("Ignoring invalid gossip")
		w.WriteHeader(http.StatusOK)
		return
	}
	
	// Process the gossip
	gs.coordinator.ProcessGossip(gossip)
	
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// handleAllocation handles incoming allocation updates
func (gs *GossipService) handleAllocation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to read allocation request body")
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	
	var allocation AllocationUpdate
	if err := json.Unmarshal(body, &allocation); err != nil {
		log.Error().Err(err).Msg("Failed to unmarshal allocation")
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	
	// Validate allocation message
	if allocation.ToPeer != gs.coordinator.myPeerID {
		log.Debug().
			Str("to_peer", allocation.ToPeer).
			Str("my_peer", gs.coordinator.myPeerID).
			Msg("Allocation not for this peer")
		w.WriteHeader(http.StatusOK)
		return
	}
	
	// Process the allocation update
	gs.processAllocationUpdate(allocation)
	
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// handleHealth provides health check endpoint
func (gs *GossipService) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	health := map[string]interface{}{
		"status":    "healthy",
		"peer_id":   gs.coordinator.myPeerID,
		"timestamp": time.Now(),
		"peers":     len(gs.coordinator.GetActivePeers()),
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

// handlePeers provides peer information endpoint
func (gs *GossipService) handlePeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	peers := gs.coordinator.GetActivePeers()
	
	response := map[string]interface{}{
		"my_peer_id": gs.coordinator.myPeerID,
		"peers":      peers,
		"count":      len(peers),
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// processAllocationUpdate processes an incoming allocation update
func (gs *GossipService) processAllocationUpdate(allocation AllocationUpdate) {
	log.Info().
		Str("from_peer", allocation.FromPeer).
		Str("username", allocation.Username).
		Int64("allocation", allocation.Allocation).
		Msg("Received allocation update")
	
	// This would typically update the local rate limiter
	// The actual implementation depends on how the rate limiter manager is structured
	// For now, we'll just log it
	
	// TODO: Integrate with rate limiter manager to update local allocation
	// gs.coordinator.rateLimiterManager.UpdateUserAllocation(allocation.Username, allocation.Allocation)
}

// BroadcastToAllPeers sends a message to all active peers
func (gs *GossipService) BroadcastToAllPeers(gossip UsageGossip) {
	peers := gs.coordinator.GetActivePeers()
	
	var wg sync.WaitGroup
	for _, peer := range peers {
		wg.Add(1)
		go func(p *Peer) {
			defer wg.Done()
			if err := gs.SendGossip(p.Address, gossip); err != nil {
				log.Debug().
					Str("peer_id", p.ID).
					Str("address", p.Address).
					Err(err).
					Msg("Failed to broadcast gossip to peer")
			}
		}(peer)
	}
	wg.Wait()
}

// GetPeerHealth checks if a specific peer is reachable
func (gs *GossipService) GetPeerHealth(peerAddress string) error {
	url := fmt.Sprintf("http://%s/health", peerAddress)
	
	resp, err := gs.client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check failed with status: %d", resp.StatusCode)
	}
	
	return nil
}

// waitForPort waits for a TCP port to be available
func waitForPort(address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	
	return fmt.Errorf("port %s not available after %v", address, timeout)
}
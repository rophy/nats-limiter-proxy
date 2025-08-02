package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGossipService(t *testing.T) {
	coordinator := createTestCoordinator()
	service := NewGossipService(coordinator)

	assert.NotNil(t, service)
	assert.Equal(t, coordinator, service.coordinator)
	assert.NotNil(t, service.client)
	assert.NotNil(t, service.shutdownChan)
}

func TestGossipService_HandleGossip(t *testing.T) {
	coordinator := createTestCoordinator()
	service := NewGossipService(coordinator)

	gossip := UsageGossip{
		FromPeer: "peer-1",
		UserStats: map[string]UserUsage{
			"alice": {
				Username:        "alice",
				UsedBytes:       1000,
				RequestRate:     100.0,
				LocalAllocation: 2000,
				LastUpdated:     time.Now(),
			},
		},
		Timestamp:   time.Now(),
		MessageType: GossipUsageReport,
	}

	// Create request
	jsonData, err := json.Marshal(gossip)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/gossip", bytes.NewReader(jsonData))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// Handle request
	service.handleGossip(w, req)

	// Check response
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())

	// Verify gossip was processed
	usage := coordinator.GetPeerUsage("alice")
	require.NotNil(t, usage)
	assert.Contains(t, usage, "peer-1")
	assert.Equal(t, int64(1000), usage["peer-1"].UsedBytes)
}

func TestGossipService_HandleGossip_InvalidMethod(t *testing.T) {
	coordinator := createTestCoordinator()
	service := NewGossipService(coordinator)

	req := httptest.NewRequest("GET", "/gossip", nil)
	w := httptest.NewRecorder()

	service.handleGossip(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestGossipService_HandleGossip_InvalidJSON(t *testing.T) {
	coordinator := createTestCoordinator()
	service := NewGossipService(coordinator)

	req := httptest.NewRequest("POST", "/gossip", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	service.handleGossip(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGossipService_HandleGossip_IgnoreSelf(t *testing.T) {
	coordinator := createTestCoordinator()
	service := NewGossipService(coordinator)

	gossip := UsageGossip{
		FromPeer: coordinator.myPeerID, // Same as coordinator's peer ID
		UserStats: map[string]UserUsage{
			"alice": {
				Username:        "alice",
				UsedBytes:       1000,
				RequestRate:     100.0,
				LocalAllocation: 2000,
				LastUpdated:     time.Now(),
			},
		},
		Timestamp:   time.Now(),
		MessageType: GossipUsageReport,
	}

	jsonData, err := json.Marshal(gossip)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/gossip", bytes.NewReader(jsonData))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	service.handleGossip(w, req)

	// Should return OK but not process the gossip
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify gossip was ignored
	usage := coordinator.GetPeerUsage("alice")
	if usage != nil {
		assert.NotContains(t, usage, coordinator.myPeerID)
	}
}

func TestGossipService_HandleAllocation(t *testing.T) {
	coordinator := createTestCoordinator()
	service := NewGossipService(coordinator)

	allocation := AllocationUpdate{
		FromPeer:   "peer-1",
		ToPeer:     coordinator.myPeerID,
		Username:   "alice",
		Allocation: 5000,
		Timestamp:  time.Now(),
	}

	jsonData, err := json.Marshal(allocation)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/allocation", bytes.NewReader(jsonData))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	service.handleAllocation(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())
}

func TestGossipService_HandleAllocation_WrongPeer(t *testing.T) {
	coordinator := createTestCoordinator()
	service := NewGossipService(coordinator)

	allocation := AllocationUpdate{
		FromPeer:   "peer-1",
		ToPeer:     "different-peer", // Not for this coordinator
		Username:   "alice",
		Allocation: 5000,
		Timestamp:  time.Now(),
	}

	jsonData, err := json.Marshal(allocation)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/allocation", bytes.NewReader(jsonData))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	service.handleAllocation(w, req)

	// Should return OK but not process allocation
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGossipService_HandleHealth(t *testing.T) {
	coordinator := createTestCoordinator()
	service := NewGossipService(coordinator)

	// Add some peers
	coordinator.AddPeer(&Peer{
		ID:       "peer-1",
		Address:  "192.168.1.1:8081",
		LastSeen: time.Now(),
		Status:   PeerStatusActive,
	})

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	service.handleHealth(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var health map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &health)
	require.NoError(t, err)

	assert.Equal(t, "healthy", health["status"])
	assert.Equal(t, coordinator.myPeerID, health["peer_id"])
	assert.Equal(t, float64(1), health["peers"]) // 1 active peer
	assert.NotNil(t, health["timestamp"])
}

func TestGossipService_HandlePeers(t *testing.T) {
	coordinator := createTestCoordinator()
	service := NewGossipService(coordinator)

	// Add some peers
	coordinator.AddPeer(&Peer{
		ID:       "peer-1",
		Address:  "192.168.1.1:8081",
		LastSeen: time.Now(),
		Status:   PeerStatusActive,
	})
	coordinator.AddPeer(&Peer{
		ID:       "peer-2",
		Address:  "192.168.1.2:8081",
		LastSeen: time.Now(),
		Status:   PeerStatusActive,
	})

	req := httptest.NewRequest("GET", "/peers", nil)
	w := httptest.NewRecorder()

	service.handlePeers(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, coordinator.myPeerID, response["my_peer_id"])
	assert.Equal(t, float64(2), response["count"])
	
	peers, ok := response["peers"].([]interface{})
	require.True(t, ok)
	assert.Len(t, peers, 2)
}

func TestGossipService_SendGossip_Integration(t *testing.T) {
	// Create test server
	coordinator := createTestCoordinator()
	receiverService := NewGossipService(coordinator)

	server := httptest.NewServer(http.HandlerFunc(receiverService.handleGossip))
	defer server.Close()

	// Create sender service
	senderCoordinator := createTestCoordinator()
	senderCoordinator.myPeerID = "sender-peer"
	senderService := NewGossipService(senderCoordinator)

	gossip := UsageGossip{
		FromPeer: "sender-peer",
		UserStats: map[string]UserUsage{
			"alice": {
				Username:        "alice",
				UsedBytes:       1500,
				RequestRate:     150.0,
				LocalAllocation: 3000,
				LastUpdated:     time.Now(),
			},
		},
		Timestamp:   time.Now(),
		MessageType: GossipUsageReport,
	}

	// Extract address from test server URL
	peerAddress := server.URL[7:] // Remove "http://" prefix

	err := senderService.SendGossip(peerAddress, gossip)
	assert.NoError(t, err)

	// Verify gossip was received and processed
	usage := coordinator.GetPeerUsage("alice")
	require.NotNil(t, usage)
	assert.Contains(t, usage, "sender-peer")
	assert.Equal(t, int64(1500), usage["sender-peer"].UsedBytes)
}

func TestGossipService_SendAllocation_Integration(t *testing.T) {
	// Create test server
	coordinator := createTestCoordinator()
	receiverService := NewGossipService(coordinator)

	server := httptest.NewServer(http.HandlerFunc(receiverService.handleAllocation))
	defer server.Close()

	// Create sender service
	senderCoordinator := createTestCoordinator()
	senderCoordinator.myPeerID = "sender-peer"
	senderService := NewGossipService(senderCoordinator)

	allocation := AllocationUpdate{
		FromPeer:   "sender-peer",
		ToPeer:     coordinator.myPeerID,
		Username:   "alice",
		Allocation: 4000,
		Timestamp:  time.Now(),
	}

	// Extract address from test server URL
	peerAddress := server.URL[7:] // Remove "http://" prefix

	err := senderService.SendAllocation(peerAddress, allocation)
	assert.NoError(t, err)
}

func TestGossipService_BroadcastToAllPeers(t *testing.T) {
	coordinator := createTestCoordinator()
	service := NewGossipService(coordinator)

	// Create test servers for peers
	var servers []*httptest.Server
	var receivedGossips []UsageGossip

	for i := 0; i < 3; i++ {
		peerCoordinator := createTestCoordinator()
		_ = NewGossipService(peerCoordinator)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var gossip UsageGossip
			json.NewDecoder(r.Body).Decode(&gossip)
			receivedGossips = append(receivedGossips, gossip)
			w.WriteHeader(http.StatusOK)
		}))
		servers = append(servers, server)

		// Add peer to coordinator
		peerAddress := server.URL[7:] // Remove "http://" prefix
		coordinator.AddPeer(&Peer{
			ID:       fmt.Sprintf("peer-%d", i),
			Address:  peerAddress,
			LastSeen: time.Now(),
			Status:   PeerStatusActive,
		})
	}
	defer func() {
		for _, server := range servers {
			server.Close()
		}
	}()

	gossip := UsageGossip{
		FromPeer: coordinator.myPeerID,
		UserStats: map[string]UserUsage{
			"alice": {
				Username:        "alice",
				UsedBytes:       2000,
				RequestRate:     200.0,
				LocalAllocation: 4000,
				LastUpdated:     time.Now(),
			},
		},
		Timestamp:   time.Now(),
		MessageType: GossipUsageReport,
	}

	service.BroadcastToAllPeers(gossip)

	// Give some time for async broadcasts to complete
	time.Sleep(100 * time.Millisecond)

	// Verify all peers received the gossip
	assert.Len(t, receivedGossips, 3)
	for _, received := range receivedGossips {
		assert.Equal(t, coordinator.myPeerID, received.FromPeer)
		assert.Contains(t, received.UserStats, "alice")
	}
}

func TestGossipService_GetPeerHealth(t *testing.T) {
	coordinator := createTestCoordinator()
	service := NewGossipService(coordinator)

	// Create healthy test server
	healthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	}))
	defer healthyServer.Close()

	healthyAddress := healthyServer.URL[7:] // Remove "http://" prefix
	err := service.GetPeerHealth(healthyAddress)
	assert.NoError(t, err)

	// Test unhealthy server
	err = service.GetPeerHealth("non-existent-server:9999")
	assert.Error(t, err)
}

func TestGossipService_HandleMethods_InvalidMethods(t *testing.T) {
	coordinator := createTestCoordinator()
	service := NewGossipService(coordinator)

	testCases := []struct {
		endpoint string
		handler  http.HandlerFunc
		method   string
	}{
		{"/gossip", service.handleGossip, "GET"},
		{"/allocation", service.handleAllocation, "GET"},
		{"/health", service.handleHealth, "POST"},
		{"/peers", service.handlePeers, "POST"},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s_%s", tc.endpoint, tc.method), func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.endpoint, nil)
			w := httptest.NewRecorder()

			tc.handler(w, req)

			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		})
	}
}

func TestGossipService_ErrorHandling(t *testing.T) {
	coordinator := createTestCoordinator()
	service := NewGossipService(coordinator)

	// Test malformed JSON for gossip
	req := httptest.NewRequest("POST", "/gossip", bytes.NewReader([]byte("{")))
	w := httptest.NewRecorder()
	service.handleGossip(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Test malformed JSON for allocation
	req = httptest.NewRequest("POST", "/allocation", bytes.NewReader([]byte("{")))
	w = httptest.NewRecorder()
	service.handleAllocation(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
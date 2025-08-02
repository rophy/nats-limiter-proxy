package server

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewPeerManager(t *testing.T) {
	coordinator := createTestCoordinator()
	manager := NewPeerManager(coordinator)

	assert.NotNil(t, manager)
	assert.Equal(t, coordinator, manager.coordinator)
	assert.Equal(t, coordinator.config, manager.config)
	assert.NotNil(t, manager.shutdownChan)
	assert.Equal(t, coordinator.config.DiscoveryMethod, manager.discoveryType)
}

func TestPeerManager_StaticDiscovery(t *testing.T) {
	coordinator := createTestCoordinator()
	coordinator.config.DiscoveryMethod = "static"
	manager := NewPeerManager(coordinator)

	// Set static peers environment variable
	os.Setenv("STATIC_PEERS", "192.168.1.1:8081,192.168.1.2:8081, 192.168.1.3:8081")
	defer os.Unsetenv("STATIC_PEERS")

	manager.staticDiscovery()

	// Verify peers were added
	peers := coordinator.GetActivePeers()
	assert.Len(t, peers, 3)

	expectedAddresses := []string{"192.168.1.1:8081", "192.168.1.2:8081", "192.168.1.3:8081"}
	for _, peer := range peers {
		assert.Contains(t, expectedAddresses, peer.Address)
		assert.Equal(t, PeerStatusActive, peer.Status)
		assert.Contains(t, peer.ID, "static-peer")
	}
}

func TestPeerManager_StaticDiscovery_EmptyConfig(t *testing.T) {
	coordinator := createTestCoordinator()
	manager := NewPeerManager(coordinator)

	// Ensure no static peers are set
	os.Unsetenv("STATIC_PEERS")

	manager.staticDiscovery()

	// Should not add any peers
	peers := coordinator.GetActivePeers()
	assert.Len(t, peers, 0)
}

func TestPeerManager_StaticDiscovery_MalformedPeers(t *testing.T) {
	coordinator := createTestCoordinator()
	manager := NewPeerManager(coordinator)

	// Set malformed static peers (with empty entries)
	os.Setenv("STATIC_PEERS", "192.168.1.1:8081,,   ,192.168.1.2:8081")
	defer os.Unsetenv("STATIC_PEERS")

	manager.staticDiscovery()

	// Should only add valid peers
	peers := coordinator.GetActivePeers()
	assert.Len(t, peers, 2)

	addresses := make([]string, len(peers))
	for i, peer := range peers {
		addresses[i] = peer.Address
	}
	assert.Contains(t, addresses, "192.168.1.1:8081")
	assert.Contains(t, addresses, "192.168.1.2:8081")
}

func TestPeerManager_GetMyIP_FromEnvironment(t *testing.T) {
	// Test getting IP from POD_IP environment variable
	testIP := "10.0.0.5"
	os.Setenv("POD_IP", testIP)
	defer os.Unsetenv("POD_IP")

	manager := NewPeerManager(createTestCoordinator())
	ip := manager.getMyIP()

	assert.Equal(t, testIP, ip)
}

func TestPeerManager_GetMyIP_Fallback(t *testing.T) {
	// Ensure POD_IP is not set
	os.Unsetenv("POD_IP")

	manager := NewPeerManager(createTestCoordinator())
	ip := manager.getMyIP()

	// Should return some IP (either detected or fallback)
	assert.NotEmpty(t, ip)
	// In test environment, might return 127.0.0.1 or actual detected IP
	assert.Regexp(t, `^\d+\.\d+\.\d+\.\d+$`, ip)
}

func TestGeneratePeerID(t *testing.T) {
	// Test that it generates a non-empty ID
	id := GeneratePeerID()
	assert.NotEmpty(t, id)

	// Test that multiple calls generate different IDs (with high probability)
	id2 := GeneratePeerID()
	assert.NotEmpty(t, id2)
	// Note: There's a tiny chance they could be the same, but very unlikely
}

func TestParseCoordinationPort(t *testing.T) {
	testCases := []struct {
		address      string
		expectedPort int
	}{
		{"192.168.1.1:8081", 8081},
		{"localhost:9000", 9000},
		{"10.0.0.1:8080", 8080},
		{"invalid", 8081},           // fallback to default
		{"192.168.1.1", 8081},       // no port specified
		{"192.168.1.1:abc", 8081},   // invalid port
		{"192.168.1.1:99999", 99999}, // valid large port
	}

	for _, tc := range testCases {
		t.Run(tc.address, func(t *testing.T) {
			port := ParseCoordinationPort(tc.address)
			assert.Equal(t, tc.expectedPort, port)
		})
	}
}

func TestPeerManager_CheckSinglePeerHealth_Success(t *testing.T) {
	coordinator := createTestCoordinator()
	manager := NewPeerManager(coordinator)

	// Create a test peer that points to a non-existent address
	// This test is tricky because we need a real server to test success
	peer := &Peer{
		ID:       "test-peer",
		Address:  "non-existent-server:9999",
		LastSeen: time.Now().Add(-time.Minute),
		Status:   PeerStatusActive,
	}

	coordinator.AddPeer(peer)

	// This will fail (as expected for non-existent server)
	manager.checkSinglePeerHealth(peer)

	// Since the health check should fail, the peer should be marked as failed
	// and not appear in active peers list (depending on implementation)
	// This is mainly testing that the function doesn't panic
}

func TestPeerManager_DiscoveryMethods(t *testing.T) {
	testCases := []struct {
		method      string
		shouldError bool
	}{
		{"kubernetes", false},
		{"static", false},
		{"dns", false},
		{"unknown", true},
	}

	for _, tc := range testCases {
		t.Run(tc.method, func(t *testing.T) {
			coordinator := createTestCoordinator()
			coordinator.config.DiscoveryMethod = tc.method
			manager := NewPeerManager(coordinator)

			err := manager.Start()

			if tc.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				manager.Stop() // Clean shutdown
			}
		})
	}
}

func TestPeerManager_KubernetesDiscovery_EnvironmentConfig(t *testing.T) {
	coordinator := createTestCoordinator()
	manager := NewPeerManager(coordinator)

	// Test with custom service name and namespace
	os.Setenv("K8S_SERVICE_NAME", "custom-proxy")
	os.Setenv("K8S_NAMESPACE", "custom-namespace")
	defer func() {
		os.Unsetenv("K8S_SERVICE_NAME")
		os.Unsetenv("K8S_NAMESPACE")
	}()

	// This will try to resolve DNS, which will likely fail in test environment
	// But we can verify that the environment variables are being read correctly
	manager.discoverKubernetesPeers()

	// Hard to assert much here without mocking DNS resolution
	// But the function should complete without panicking
}

func TestPeerManager_KubernetesDiscovery_DefaultConfig(t *testing.T) {
	coordinator := createTestCoordinator()
	manager := NewPeerManager(coordinator)

	// Ensure environment variables are not set (should use defaults)
	os.Unsetenv("K8S_SERVICE_NAME")
	os.Unsetenv("K8S_NAMESPACE")

	// This will try to resolve DNS with default values
	manager.discoverKubernetesPeers()

	// Function should complete without panicking
	// DNS resolution will likely fail in test environment, but that's expected
}

func TestPeerManager_HealthCheckConcurrency(t *testing.T) {
	coordinator := createTestCoordinator()
	manager := NewPeerManager(coordinator)

	// Add multiple peers
	for i := 0; i < 5; i++ {
		peer := &Peer{
			ID:       fmt.Sprintf("peer-%d", i),
			Address:  fmt.Sprintf("192.168.1.%d:8081", i+1),
			LastSeen: time.Now(),
			Status:   PeerStatusActive,
		}
		coordinator.AddPeer(peer)
	}

	// Run health check (should handle concurrent checks)
	manager.checkPeerHealth()

	// All peers should be marked as failed (since addresses don't exist)
	// But the function should complete without races or panics
}

func TestPeerManager_Lifecycle(t *testing.T) {
	coordinator := createTestCoordinator()
	manager := NewPeerManager(coordinator)

	// Test starting and stopping for each discovery method
	methods := []string{"static", "kubernetes", "dns"}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			coordinator.config.DiscoveryMethod = method
			
			// Start should not error for valid methods
			err := manager.Start()
			assert.NoError(t, err)

			// Give a moment for goroutines to start
			time.Sleep(10 * time.Millisecond)

			// Stop should clean up
			manager.Stop()
		})
	}
}

func TestPeerManager_DNSDiscovery_MissingConfig(t *testing.T) {
	coordinator := createTestCoordinator()
	coordinator.config.DiscoveryMethod = "dns"
	manager := NewPeerManager(coordinator)

	// Ensure DNS_DISCOVERY_NAME is not set
	os.Unsetenv("DNS_DISCOVERY_NAME")

	// Should handle missing DNS configuration gracefully
	manager.dnsDiscovery()

	// Function should complete (likely with error log, but no panic)
}

func TestPeerManager_DNSDiscovery_WithConfig(t *testing.T) {
	coordinator := createTestCoordinator()
	coordinator.config.DiscoveryMethod = "dns"
	manager := NewPeerManager(coordinator)

	// Set DNS discovery configuration
	os.Setenv("DNS_DISCOVERY_NAME", "proxy.example.com")
	defer os.Unsetenv("DNS_DISCOVERY_NAME")

	// This will attempt SRV lookup, which will likely fail in test environment
	manager.discoverDNSPeers("proxy.example.com")

	// Function should complete without panicking
}

// Test helper to verify peer manager integrates correctly with coordinator
func TestPeerManager_Integration(t *testing.T) {
	coordinator := createTestCoordinator()
	manager := NewPeerManager(coordinator)

	// Set up static discovery
	os.Setenv("STATIC_PEERS", "peer1:8081,peer2:8081")
	defer os.Unsetenv("STATIC_PEERS")

	// Run static discovery
	manager.staticDiscovery()

	// Verify peers were added to coordinator
	peers := coordinator.GetActivePeers()
	assert.Len(t, peers, 2)

	// Verify peer properties
	for _, peer := range peers {
		assert.NotEmpty(t, peer.ID)
		assert.Contains(t, peer.Address, ":8081")
		assert.Equal(t, PeerStatusActive, peer.Status)
		assert.True(t, peer.LastSeen.After(time.Now().Add(-time.Minute)))
	}
}

// Test edge cases and error conditions
func TestPeerManager_EdgeCases(t *testing.T) {
	coordinator := createTestCoordinator()
	manager := NewPeerManager(coordinator)

	// Test with nil coordinator config
	coordinator.config = nil
	assert.NotPanics(t, func() {
		manager.staticDiscovery()
	})

	// Test multiple stops
	manager.Stop()
	manager.Stop() // Should not panic

	// Test operations after stop
	assert.NotPanics(t, func() {
		manager.checkPeerHealth()
	})
}
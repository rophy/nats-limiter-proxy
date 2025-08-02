package server

import (
	"fmt"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// PeerManager handles peer discovery and lifecycle management
type PeerManager struct {
	coordinator   *PeerCoordinator
	config        *CoordinationConfig
	shutdownChan  chan struct{}
	discoveryType string
}

// NewPeerManager creates a new peer manager
func NewPeerManager(coordinator *PeerCoordinator) *PeerManager {
	return &PeerManager{
		coordinator:   coordinator,
		config:        coordinator.config,
		shutdownChan:  make(chan struct{}),
		discoveryType: coordinator.config.DiscoveryMethod,
	}
}

// Start begins peer discovery
func (pm *PeerManager) Start() error {
	log.Info().
		Str("discovery_method", pm.discoveryType).
		Msg("Starting peer discovery")
	
	switch pm.discoveryType {
	case "kubernetes":
		go pm.kubernetesDiscovery()
	case "static":
		go pm.staticDiscovery()
	case "dns":
		go pm.dnsDiscovery()
	default:
		return fmt.Errorf("unsupported discovery method: %s", pm.discoveryType)
	}
	
	// Start periodic peer health checks
	go pm.healthCheckLoop()
	
	return nil
}

// Stop shuts down peer discovery
func (pm *PeerManager) Stop() {
	close(pm.shutdownChan)
}

// kubernetesDiscovery discovers peers using Kubernetes API
func (pm *PeerManager) kubernetesDiscovery() {
	// For now, implement a simple approach using environment variables
	// In production, this would use the Kubernetes API client
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			pm.discoverKubernetesPeers()
		case <-pm.shutdownChan:
			return
		}
	}
}

// discoverKubernetesPeers discovers peers in Kubernetes or Docker Compose environment
func (pm *PeerManager) discoverKubernetesPeers() {
	// Get service name from environment (works for both K8s and Docker Compose)
	serviceName := os.Getenv("SERVICE_NAME")
	if serviceName == "" {
		// Legacy support for K8S_SERVICE_NAME
		serviceName = os.Getenv("K8S_SERVICE_NAME")
	}
	if serviceName == "" {
		serviceName = "nats-limiter-proxy"
	}
	
	// Try simple service name first (Docker Compose, K8s short name)
	ips, err := net.LookupIP(serviceName)
	if err != nil {
		// If that fails, try with default namespace (Kubernetes fallback)
		namespace := os.Getenv("K8S_NAMESPACE")
		if namespace == "" {
			namespace = "default"
		}
		kubernetesName := fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace)
		ips, err = net.LookupIP(kubernetesName)
		if err != nil {
			log.Error().Err(err).
				Str("service_name", serviceName).
				Str("kubernetes_name", kubernetesName).
				Msg("Failed to resolve service DNS with both simple and FQDN")
			return
		}
		log.Debug().Str("dns_name", kubernetesName).Msg("Resolved service using Kubernetes FQDN")
	} else {
		log.Debug().Str("dns_name", serviceName).Msg("Resolved service using simple name")
	}
	
	myIP := pm.getMyIP()
	
	for _, ip := range ips {
		ipStr := ip.String()
		if ipStr == myIP {
			continue // Skip ourselves
		}
		
		peerID := fmt.Sprintf("peer-%s", strings.ReplaceAll(ipStr, ".", "-"))
		peerAddress := fmt.Sprintf("%s:%d", ipStr, pm.config.Port)
		
		peer := &Peer{
			ID:       peerID,
			Address:  peerAddress,
			LastSeen: time.Now(),
			Status:   PeerStatusActive,
		}
		
		pm.coordinator.AddPeer(peer)
	}
}

// staticDiscovery discovers peers from static configuration
func (pm *PeerManager) staticDiscovery() {
	// Read peer addresses from environment variable
	peersEnv := os.Getenv("STATIC_PEERS")
	if peersEnv == "" {
		log.Info().Msg("No static peers configured")
		return
	}
	
	peerAddresses := strings.Split(peersEnv, ",")
	for i, address := range peerAddresses {
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		
		peerID := fmt.Sprintf("static-peer-%d", i)
		peer := &Peer{
			ID:       peerID,
			Address:  address,
			LastSeen: time.Now(),
			Status:   PeerStatusActive,
		}
		
		pm.coordinator.AddPeer(peer)
	}
}

// dnsDiscovery discovers peers using DNS SRV records
func (pm *PeerManager) dnsDiscovery() {
	dnsName := os.Getenv("DNS_DISCOVERY_NAME")
	if dnsName == "" {
		log.Error().Msg("DNS_DISCOVERY_NAME not set for DNS discovery")
		return
	}
	
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			pm.discoverDNSPeers(dnsName)
		case <-pm.shutdownChan:
			return
		}
	}
}

// discoverDNSPeers discovers peers using DNS
func (pm *PeerManager) discoverDNSPeers(dnsName string) {
	_, srvRecords, err := net.LookupSRV("", "", dnsName)
	if err != nil {
		log.Error().Err(err).Str("dns_name", dnsName).Msg("Failed to lookup SRV records")
		return
	}
	
	for _, srv := range srvRecords {
		peerAddress := fmt.Sprintf("%s:%d", srv.Target, srv.Port)
		peerID := fmt.Sprintf("dns-%s", strings.ReplaceAll(srv.Target, ".", "-"))
		
		peer := &Peer{
			ID:       peerID,
			Address:  peerAddress,
			LastSeen: time.Now(),
			Status:   PeerStatusActive,
		}
		
		pm.coordinator.AddPeer(peer)
	}
}

// healthCheckLoop periodically checks peer health
func (pm *PeerManager) healthCheckLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			pm.checkPeerHealth()
		case <-pm.shutdownChan:
			return
		}
	}
}

// checkPeerHealth checks health of all known peers
func (pm *PeerManager) checkPeerHealth() {
	peers := pm.coordinator.GetActivePeers()
	
	var wg sync.WaitGroup
	for _, peer := range peers {
		wg.Add(1)
		go func(p *Peer) {
			defer wg.Done()
			pm.checkSinglePeerHealth(p)
		}(peer)
	}
	wg.Wait()
}

// checkSinglePeerHealth checks health of a single peer
func (pm *PeerManager) checkSinglePeerHealth(peer *Peer) {
	// Simple TCP connection test
	conn, err := net.DialTimeout("tcp", peer.Address, 5*time.Second)
	if err != nil {
		log.Debug().
			Str("peer_id", peer.ID).
			Str("address", peer.Address).
			Err(err).
			Msg("Peer health check failed")
		
		pm.coordinator.peerMutex.Lock()
		if p, exists := pm.coordinator.peers[peer.ID]; exists {
			p.Status = PeerStatusFailed
		}
		pm.coordinator.peerMutex.Unlock()
		return
	}
	conn.Close()
	
	// Peer is healthy
	pm.coordinator.peerMutex.Lock()
	if p, exists := pm.coordinator.peers[peer.ID]; exists {
		p.Status = PeerStatusActive
		p.LastSeen = time.Now()
	}
	pm.coordinator.peerMutex.Unlock()
}

// getMyIP returns the current instance's IP address
func (pm *PeerManager) getMyIP() string {
	// Try to get IP from environment first (Kubernetes sets this)
	if podIP := os.Getenv("POD_IP"); podIP != "" {
		return podIP
	}
	
	// Fall back to detecting local IP
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Error().Err(err).Msg("Failed to detect local IP")
		return "127.0.0.1"
	}
	defer conn.Close()
	
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

// GeneratePeerID generates a unique peer ID for this instance
func GeneratePeerID() string {
	// Try to use hostname first
	if hostname, err := os.Hostname(); err == nil {
		return hostname
	}
	
	// Fall back to random ID
	return fmt.Sprintf("peer-%d", rand.Int63())
}

// ParseCoordinationPort extracts port from address string
func ParseCoordinationPort(address string) int {
	parts := strings.Split(address, ":")
	if len(parts) < 2 {
		return 8081 // default
	}
	
	port, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 8081 // default
	}
	
	return port
}
package testutil

import (
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// DockerComposeEnv provides access to services running in docker-compose
type DockerComposeEnv struct {
	// NATS server endpoint (direct connection)
	NATSDirectURL string
	
	// Proxy endpoints (load balanced through HAProxy or direct to replicas)
	ProxyURL        string  // Load-balanced endpoint
	ProxyReplica1   string  // Direct to replica 1
	ProxyReplica2   string  // Direct to replica 2  
	ProxyReplica3   string  // Direct to replica 3
	
	// Redis endpoint
	RedisURL string
}

// NewDockerComposeEnv creates a new docker-compose environment accessor
func NewDockerComposeEnv() *DockerComposeEnv {
	return &DockerComposeEnv{
		// Direct NATS server (bypass proxy)
		NATSDirectURL: "nats://nats:4222",
		
		// Proxy endpoints (from inside docker-compose network)
		ProxyURL:      "nats://proxy:4223",     // Load-balanced via docker-compose
		ProxyReplica1: "nats://proxy:4223",     // In docker-compose, this goes to one of the replicas
		ProxyReplica2: "nats://proxy:4224",     // If using port mapping
		ProxyReplica3: "nats://proxy:4225",     // If using port mapping
		
		// Redis endpoint
		RedisURL: "redis://redis-master:6379",
	}
}

// ConnectToNATSDirect creates a connection directly to NATS server (bypassing proxy)
func (env *DockerComposeEnv) ConnectToNATSDirect(t *testing.T) *nats.Conn {
	nc, err := nats.Connect(env.NATSDirectURL, nats.Timeout(10*time.Second))
	if err != nil {
		t.Fatalf("Failed to connect to NATS server directly: %v", err)
	}
	
	t.Cleanup(func() {
		nc.Close()
	})
	
	return nc
}

// ConnectToProxy creates a connection through the proxy
func (env *DockerComposeEnv) ConnectToProxy(t *testing.T) *nats.Conn {
	nc, err := nats.Connect(env.ProxyURL, nats.Timeout(10*time.Second))
	if err != nil {
		t.Fatalf("Failed to connect to proxy: %v", err)
	}
	
	t.Cleanup(func() {
		nc.Close()
	})
	
	return nc
}

// ConnectToProxyWithAuth creates an authenticated connection through the proxy
func (env *DockerComposeEnv) ConnectToProxyWithAuth(t *testing.T, credsFile string) *nats.Conn {
	nc, err := nats.Connect(env.ProxyURL, 
		nats.UserCredentials(credsFile),
		nats.Timeout(10*time.Second),
	)
	if err != nil {
		t.Fatalf("Failed to connect to proxy with auth: %v", err)
	}
	
	t.Cleanup(func() {
		nc.Close()
	})
	
	return nc
}

// ConnectToSpecificProxy connects to a specific proxy replica for distributed testing
func (env *DockerComposeEnv) ConnectToSpecificProxy(t *testing.T, replicaNum int) *nats.Conn {
	var url string
	switch replicaNum {
	case 1:
		url = env.ProxyReplica1
	case 2:
		url = env.ProxyReplica2
	case 3:
		url = env.ProxyReplica3
	default:
		t.Fatalf("Invalid replica number: %d (must be 1, 2, or 3)", replicaNum)
	}
	
	nc, err := nats.Connect(url, nats.Timeout(10*time.Second))
	if err != nil {
		t.Fatalf("Failed to connect to proxy replica %d: %v", replicaNum, err)
	}
	
	t.Cleanup(func() {
		nc.Close()
	})
	
	return nc
}

// WaitForProxyReady waits for proxy to be ready
func (env *DockerComposeEnv) WaitForProxyReady(t *testing.T) {
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-timeout:
			t.Fatal("Timeout waiting for proxy to be ready")
		case <-ticker.C:
			nc, err := nats.Connect(env.ProxyURL, nats.Timeout(2*time.Second))
			if err == nil {
				nc.Close()
				t.Log("Proxy is ready")
				return
			}
			t.Logf("Waiting for proxy... (%v)", err)
		}
	}
}

// WaitForNATSReady waits for NATS server to be ready
func (env *DockerComposeEnv) WaitForNATSReady(t *testing.T) {
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-timeout:
			t.Fatal("Timeout waiting for NATS to be ready")
		case <-ticker.C:
			nc, err := nats.Connect(env.NATSDirectURL, nats.Timeout(2*time.Second))
			if err == nil {
				nc.Close()
				t.Log("NATS server is ready")
				return
			}
			t.Logf("Waiting for NATS server... (%v)", err)
		}
	}
}

// GetCredentialsPath returns the path to user credentials inside nats-box
func GetCredentialsPath(username string) string {
	return fmt.Sprintf("/nsc/creds/%s.creds", username)
}

// GetAliceCredentials returns Alice's credentials path
func GetAliceCredentials() string {
	return GetCredentialsPath("alice")
}

// GetBobCredentials returns Bob's credentials path  
func GetBobCredentials() string {
	return GetCredentialsPath("bob")
}
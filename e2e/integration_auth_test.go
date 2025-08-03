package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"nats-limiter-proxy/e2e/testutil"
)

// TestE2E_AuthRequirement tests that the proxy enforces authentication
func TestE2E_AuthRequirement(t *testing.T) {
	env := testutil.NewDockerComposeEnv()

	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)

	t.Run("NoCredentials", func(t *testing.T) {
		// Attempt connection without credentials - should fail
		_, err := nats.Connect(env.ProxyURL, nats.Timeout(5*time.Second))
		if err == nil {
			t.Fatal("Expected connection to fail without credentials")
		}
		
		// Check error indicates authorization required
		if !strings.Contains(err.Error(), "authorization") && 
		   !strings.Contains(err.Error(), "Authentication Required") &&
		   !strings.Contains(err.Error(), "authentication") {
			t.Logf("Warning: Error message doesn't clearly indicate auth requirement: %v", err)
		}
		
		t.Logf("✓ Proxy correctly rejects unauthenticated connections: %v", err)
	})

	t.Run("InvalidCredentials", func(t *testing.T) {
		// Try with invalid user/pass
		_, err := nats.Connect(env.ProxyURL, 
			nats.UserInfo("invalid", "credentials"),
			nats.Timeout(5*time.Second),
		)
		if err == nil {
			t.Fatal("Expected connection to fail with invalid credentials")
		}
		
		t.Logf("✓ Proxy correctly rejects invalid credentials: %v", err)
	})

	t.Run("ValidCredentials", func(t *testing.T) {
		// Valid credentials should work
		nc := env.ConnectToProxy(t)
		
		// Verify connection is functional
		subject := "test.auth.valid"
		received := make(chan string, 1)
		
		sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
			received <- string(msg.Data)
		})
		if err != nil {
			t.Fatalf("Failed to subscribe with valid credentials: %v", err)
		}
		defer sub.Unsubscribe()
		
		if err := nc.Flush(); err != nil {
			t.Fatalf("Failed to flush: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
		
		testMsg := "auth test message"
		if err := nc.Publish(subject, []byte(testMsg)); err != nil {
			t.Fatalf("Failed to publish with valid credentials: %v", err)
		}
		
		select {
		case msg := <-received:
			if msg != testMsg {
				t.Errorf("Message mismatch: expected %q, got %q", testMsg, msg)
			}
			t.Logf("✓ Valid credentials work correctly: %q", msg)
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for message with valid credentials")
		}
	})
}

// TestE2E_MultiUserAuth tests multiple user authentication scenarios
func TestE2E_MultiUserAuth(t *testing.T) {
	env := testutil.NewDockerComposeEnv()

	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)

	testCases := []struct {
		name      string
		credsFunc func() string
		userID    string
	}{
		{
			name:      "Alice",
			credsFunc: testutil.GetAliceCredentials,
			userID:    "alice",
		},
		{
			name:      "Bob", 
			credsFunc: testutil.GetBobCredentials,
			userID:    "bob",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Connect with user credentials
			nc := env.ConnectToProxyWithAuth(t, tc.credsFunc())
			
			// Test pub/sub functionality
			subject := fmt.Sprintf("test.user.%s", tc.userID)
			testMsg := fmt.Sprintf("Message from %s", tc.name)
			received := make(chan string, 1)
			
			sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
				received <- string(msg.Data)
			})
			if err != nil {
				t.Fatalf("Failed to subscribe as %s: %v", tc.name, err)
			}
			defer sub.Unsubscribe()
			
			if err := nc.Flush(); err != nil {
				t.Fatalf("Failed to flush for %s: %v", tc.name, err)
			}
			time.Sleep(100 * time.Millisecond)
			
			if err := nc.Publish(subject, []byte(testMsg)); err != nil {
				t.Fatalf("Failed to publish as %s: %v", tc.name, err)
			}
			
			select {
			case msg := <-received:
				if msg != testMsg {
					t.Errorf("%s message mismatch: expected %q, got %q", tc.name, testMsg, msg)
				}
				t.Logf("✓ %s authentication and messaging works: %q", tc.name, msg)
			case <-time.After(5 * time.Second):
				t.Fatalf("Timeout waiting for %s message", tc.name)
			}
		})
	}
}

// TestE2E_AuthenticationPersistence tests auth across reconnections
func TestE2E_AuthenticationPersistence(t *testing.T) {
	env := testutil.NewDockerComposeEnv()

	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)

	// Connect with credentials
	nc := env.ConnectToProxy(t)
	
	// Verify initial connection works
	subject := "test.auth.persistence"
	testMsg := "persistence test"
	received := make(chan string, 1)
	
	sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
		received <- string(msg.Data)
	})
	if err != nil {
		t.Fatalf("Failed to subscribe initially: %v", err)
	}
	defer sub.Unsubscribe()
	
	if err := nc.Flush(); err != nil {
		t.Fatalf("Failed to flush initially: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	
	// Publish message
	if err := nc.Publish(subject, []byte(testMsg)); err != nil {
		t.Fatalf("Failed to publish initially: %v", err)
	}
	
	// Verify message received
	select {
	case msg := <-received:
		if msg != testMsg {
			t.Errorf("Initial message mismatch: expected %q, got %q", testMsg, msg)
		}
		t.Log("✓ Initial authenticated connection works")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for initial message")
	}
	
	// Close and reconnect
	nc.Close()
	
	// Wait a moment
	time.Sleep(1 * time.Second)
	
	// Reconnect with same credentials
	nc2 := env.ConnectToProxy(t)
	defer nc2.Close()
	
	// Test that reconnection works
	received2 := make(chan string, 1)
	sub2, err := nc2.Subscribe(subject+"_reconnect", func(msg *nats.Msg) {
		received2 <- string(msg.Data)
	})
	if err != nil {
		t.Fatalf("Failed to subscribe after reconnect: %v", err)
	}
	defer sub2.Unsubscribe()
	
	if err := nc2.Flush(); err != nil {
		t.Fatalf("Failed to flush after reconnect: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	
	testMsg2 := "reconnect test"
	if err := nc2.Publish(subject+"_reconnect", []byte(testMsg2)); err != nil {
		t.Fatalf("Failed to publish after reconnect: %v", err)
	}
	
	select {
	case msg := <-received2:
		if msg != testMsg2 {
			t.Errorf("Reconnect message mismatch: expected %q, got %q", testMsg2, msg)
		}
		t.Logf("✓ Authentication persists across reconnections: %q", msg)
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for reconnect message")
	}
}
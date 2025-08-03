package e2e

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"nats-limiter-proxy/e2e/testutil"
)

// TestE2E_MaxPayloadEnforcement tests that NATS server's max payload limits work through proxy
func TestE2E_MaxPayloadEnforcement(t *testing.T) {
	env := testutil.NewDockerComposeEnv()

	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)

	t.Run("ProxyVsDirectMaxPayload", func(t *testing.T) {
		// Test both direct connection and proxy connection to ensure same behavior
		testCases := []struct {
			name   string
			ncFunc func(*testing.T) *nats.Conn
		}{
			{
				name:   "DirectNATS",
				ncFunc: env.ConnectToNATSDirect,
			},
			{
				name:   "ThroughProxy", 
				ncFunc: env.ConnectToProxy,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				nc := tc.ncFunc(t)
				defer nc.Close()

				// Get server info to check max payload
				servers := nc.Servers()
				if len(servers) == 0 {
					t.Fatal("No server info available")
				}

				// Try to publish a message that's likely within limits (1KB)
				subject := "test.maxpayload.small"
				smallPayload := make([]byte, 1024) // 1KB
				for i := range smallPayload {
					smallPayload[i] = byte('A' + (i % 26))
				}

				err := nc.Publish(subject, smallPayload)
				if err != nil {
					t.Errorf("%s: Failed to publish small payload (%d bytes): %v", tc.name, len(smallPayload), err)
				} else {
					t.Logf("✓ %s: Successfully published small payload (%d bytes)", tc.name, len(smallPayload))
				}

				// Try progressively larger messages to find the limit
				sizes := []int{
					64 * 1024,   // 64KB
					128 * 1024,  // 128KB
					256 * 1024,  // 256KB
					512 * 1024,  // 512KB
					1024 * 1024, // 1MB (default NATS limit)
					2 * 1024 * 1024, // 2MB (should fail)
				}

				for _, size := range sizes {
					payload := make([]byte, size)
					for i := range payload {
						payload[i] = byte(i % 256)
					}

					err := nc.Publish(subject, payload)
					if err != nil {
						// Check if it's a max payload error
						if strings.Contains(err.Error(), "maximum payload") || 
						   strings.Contains(err.Error(), "max payload") ||
						   strings.Contains(err.Error(), "Maximum Payload") {
							t.Logf("✓ %s: Max payload enforced at %d bytes: %v", tc.name, size, err)
							break
						} else {
							t.Errorf("%s: Unexpected error at %d bytes: %v", tc.name, size, err)
						}
					} else {
						t.Logf("✓ %s: Successfully published %d bytes", tc.name, size)
					}
				}
			})
		}
	})
}

// TestE2E_MaxPayloadWithSubscription tests max payload with active subscription
func TestE2E_MaxPayloadWithSubscription(t *testing.T) {
	env := testutil.NewDockerComposeEnv()

	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)

	// Test with both direct and proxy connections
	testCases := []struct {
		name        string
		pubConn     func(*testing.T) *nats.Conn
		subConn     func(*testing.T) *nats.Conn
		description string
	}{
		{
			name:        "DirectToDirectNATS",
			pubConn:     env.ConnectToNATSDirect,
			subConn:     env.ConnectToNATSDirect,
			description: "Direct NATS pub/sub",
		},
		{
			name:        "ProxyToProxy",
			pubConn:     env.ConnectToProxy,
			subConn:     env.ConnectToProxy,
			description: "Proxy pub/sub",
		},
		{
			name:        "ProxyToDirectNATS",
			pubConn:     env.ConnectToProxy,
			subConn:     env.ConnectToNATSDirect,
			description: "Proxy publisher to direct subscriber",
		},
		{
			name:        "DirectToProxyNATS",
			pubConn:     env.ConnectToNATSDirect,
			subConn:     env.ConnectToProxy,
			description: "Direct publisher to proxy subscriber",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			publisher := tc.pubConn(t)
			subscriber := tc.subConn(t)

			subject := "test.maxpayload.withsub"
			received := make(chan []byte, 1)
			
			// Set up subscription
			sub, err := subscriber.Subscribe(subject, func(msg *nats.Msg) {
				received <- msg.Data
			})
			if err != nil {
				t.Fatalf("Failed to subscribe in %s: %v", tc.name, err)
			}
			defer sub.Unsubscribe()

			if err := subscriber.Flush(); err != nil {
				t.Fatalf("Failed to flush subscriber in %s: %v", tc.name, err)
			}
			time.Sleep(100 * time.Millisecond)

			// Test with a large but acceptable payload (256KB)
			payloadSize := 256 * 1024
			largePayload := make([]byte, payloadSize)
			for i := range largePayload {
				largePayload[i] = byte(i % 256)
			}

			t.Logf("Testing %s with %d byte payload", tc.description, payloadSize)

			err = publisher.Publish(subject, largePayload)
			if err != nil {
				// Check if this is expected max payload error
				if strings.Contains(err.Error(), "maximum payload") || 
				   strings.Contains(err.Error(), "max payload") ||
				   strings.Contains(err.Error(), "Maximum Payload") {
					t.Logf("✓ %s: Max payload correctly enforced at %d bytes: %v", tc.description, payloadSize, err)
					return
				} else {
					t.Fatalf("%s: Unexpected error publishing large payload: %v", tc.description, err)
				}
			}

			// If publish succeeded, verify message was received correctly
			select {
			case receivedData := <-received:
				if len(receivedData) != payloadSize {
					t.Errorf("%s: Size mismatch - sent %d bytes, received %d bytes", tc.description, payloadSize, len(receivedData))
				}

				if !bytes.Equal(receivedData, largePayload) {
					t.Errorf("%s: Content mismatch - large payload corrupted", tc.description)
				} else {
					t.Logf("✓ %s: Large payload (%d bytes) transmitted successfully", tc.description, len(receivedData))
				}
			case <-time.After(10 * time.Second):
				t.Errorf("%s: Timeout waiting for large payload", tc.description)
			}
		})
	}
}

// TestE2E_MaxPayloadErrorHandling tests error handling for oversized payloads
func TestE2E_MaxPayloadErrorHandling(t *testing.T) {
	env := testutil.NewDockerComposeEnv()

	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)

	testCases := []struct {
		name   string
		ncFunc func(*testing.T) *nats.Conn
	}{
		{
			name:   "DirectNATS",
			ncFunc: env.ConnectToNATSDirect,
		},
		{
			name:   "ThroughProxy",
			ncFunc: env.ConnectToProxy,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			nc := tc.ncFunc(t)
			defer nc.Close()

			subject := "test.maxpayload.error"

			// Try to publish an extremely large message (2MB+)
			oversizedPayload := make([]byte, 2*1024*1024+1000) // 2MB + 1000 bytes
			for i := range oversizedPayload {
				oversizedPayload[i] = byte(i % 256)
			}

			t.Logf("Testing %s with oversized payload (%d bytes)", tc.name, len(oversizedPayload))

			err := nc.Publish(subject, oversizedPayload)
			if err != nil {
				// This should be a max payload error
				errStr := strings.ToLower(err.Error())
				if strings.Contains(errStr, "maximum payload") || 
				   strings.Contains(errStr, "max payload") ||
				   strings.Contains(errStr, "payload") {
					t.Logf("✓ %s: Correctly rejected oversized payload: %v", tc.name, err)
				} else {
					t.Errorf("%s: Unexpected error message for oversized payload: %v", tc.name, err)
				}
			} else {
				t.Errorf("%s: Oversized payload should have been rejected but wasn't", tc.name)
			}

			// Verify connection is still working after error
			smallPayload := []byte("test message after error")
			err = nc.Publish(subject, smallPayload)
			if err != nil {
				t.Errorf("%s: Connection should still work after max payload error: %v", tc.name, err)
			} else {
				t.Logf("✓ %s: Connection remains functional after max payload error", tc.name)
			}
		})
	}
}

// TestE2E_MaxPayloadConsistency tests that proxy doesn't modify payload size behavior
func TestE2E_MaxPayloadConsistency(t *testing.T) {
	env := testutil.NewDockerComposeEnv()

	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)

	// Test same payloads through both connections
	testSizes := []int{
		1024,      // 1KB - should work
		10 * 1024, // 10KB - should work  
		100 * 1024, // 100KB - should work
		500 * 1024, // 500KB - might work depending on NATS config
		1024 * 1024, // 1MB - default NATS limit, might fail
	}

	for _, size := range testSizes {
		t.Run(fmt.Sprintf("Size_%dKB", size/1024), func(t *testing.T) {
			// Test direct connection result
			directNC := env.ConnectToNATSDirect(t)
			defer directNC.Close()

			// Test proxy connection result  
			proxyNC := env.ConnectToProxy(t)
			defer proxyNC.Close()

			subject := fmt.Sprintf("test.consistency.%d", size)
			payload := make([]byte, size)
			for i := range payload {
				payload[i] = byte(i % 256)
			}

			// Try direct connection
			directErr := directNC.Publish(subject, payload)
			
			// Try proxy connection
			proxyErr := proxyNC.Publish(subject, payload)

			// Both should have same result (both succeed or both fail)
			if directErr != nil && proxyErr != nil {
				// Both failed - check error types are similar
				directErrStr := strings.ToLower(directErr.Error())
				proxyErrStr := strings.ToLower(proxyErr.Error())
				
				directIsMaxPayload := strings.Contains(directErrStr, "payload") || strings.Contains(directErrStr, "maximum")
				proxyIsMaxPayload := strings.Contains(proxyErrStr, "payload") || strings.Contains(proxyErrStr, "maximum")
				
				if directIsMaxPayload && proxyIsMaxPayload {
					t.Logf("✓ Both direct and proxy correctly rejected %d bytes", size)
				} else {
					t.Errorf("Error type mismatch - Direct: %v, Proxy: %v", directErr, proxyErr)
				}
			} else if directErr == nil && proxyErr == nil {
				t.Logf("✓ Both direct and proxy accepted %d bytes", size)
			} else {
				// One succeeded, one failed - this is a problem
				t.Errorf("Inconsistent behavior - Direct: %v, Proxy: %v", directErr, proxyErr)
			}
		})
	}
}
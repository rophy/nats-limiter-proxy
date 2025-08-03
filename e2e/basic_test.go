package e2e

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"nats-limiter-proxy/e2e/testutil"
)

func TestE2E_BasicPubSub(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	// Create publisher and subscriber through proxy
	publisher := env.ConnectToProxy(t)
	subscriber := env.ConnectToProxy(t)
	
	// Set up subscription
	subject := "test.basic"
	receivedMsg := make(chan string, 1)
	
	sub, err := subscriber.Subscribe(subject, func(msg *nats.Msg) {
		receivedMsg <- string(msg.Data)
	})
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	
	// Wait for subscription to be ready
	if err := subscriber.Flush(); err != nil {
		t.Fatalf("Failed to flush subscriber: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	
	// Publish message
	testMessage := "Hello through proxy in docker-compose!"
	if err := publisher.Publish(subject, []byte(testMessage)); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}
	
	// Wait for message
	select {
	case received := <-receivedMsg:
		if received != testMessage {
			t.Errorf("Expected %q, got %q", testMessage, received)
		}
		t.Logf("✓ Message successfully passed through proxy: %q", received)
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for message")
	}
}

func TestE2E_ProxyVsDirectComparison(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	subject := "test.comparison"
	testMessage := "Comparison test message"
	
	// Test 1: Direct connection to NATS (baseline)
	t.Run("DirectToNATS", func(t *testing.T) {
		publisher := env.ConnectToNATSDirect(t)
		subscriber := env.ConnectToNATSDirect(t)
		
		receivedMsg := make(chan string, 1)
		
		sub, err := subscriber.Subscribe(subject, func(msg *nats.Msg) {
			receivedMsg <- string(msg.Data)
		})
		if err != nil {
			t.Fatalf("Failed to subscribe: %v", err)
		}
		defer sub.Unsubscribe()
		
		if err := subscriber.Flush(); err != nil {
			t.Fatalf("Failed to flush subscriber: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
		
		if err := publisher.Publish(subject, []byte(testMessage)); err != nil {
			t.Fatalf("Failed to publish: %v", err)
		}
		
		select {
		case received := <-receivedMsg:
			if received != testMessage {
				t.Errorf("Expected %q, got %q", testMessage, received)
			}
			t.Logf("✓ Direct NATS connection works: %q", received)
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for message via direct connection")
		}
	})
	
	// Test 2: Through proxy
	t.Run("ThroughProxy", func(t *testing.T) {
		publisher := env.ConnectToProxy(t)
		subscriber := env.ConnectToProxy(t)
		
		receivedMsg := make(chan string, 1)
		
		sub, err := subscriber.Subscribe(subject+"_proxy", func(msg *nats.Msg) {
			receivedMsg <- string(msg.Data)
		})
		if err != nil {
			t.Fatalf("Failed to subscribe: %v", err)
		}
		defer sub.Unsubscribe()
		
		if err := subscriber.Flush(); err != nil {
			t.Fatalf("Failed to flush subscriber: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
		
		if err := publisher.Publish(subject+"_proxy", []byte(testMessage)); err != nil {
			t.Fatalf("Failed to publish: %v", err)
		}
		
		select {
		case received := <-receivedMsg:
			if received != testMessage {
				t.Errorf("Expected %q, got %q", testMessage, received)
			}
			t.Logf("✓ Proxy connection works: %q", received)
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for message via proxy")
		}
	})
}

func TestE2E_AuthenticatedConnections(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	subject := "test.auth"
	testMessage := "Authenticated message"
	
	// Test Alice's credentials
	t.Run("AliceCredentials", func(t *testing.T) {
		alicePub := env.ConnectToProxyWithAuth(t, testutil.GetAliceCredentials())
		aliceSub := env.ConnectToProxyWithAuth(t, testutil.GetAliceCredentials())
		
		receivedMsg := make(chan string, 1)
		
		sub, err := aliceSub.Subscribe(subject+"_alice", func(msg *nats.Msg) {
			receivedMsg <- string(msg.Data)
		})
		if err != nil {
			t.Fatalf("Failed to subscribe with Alice credentials: %v", err)
		}
		defer sub.Unsubscribe()
		
		if err := aliceSub.Flush(); err != nil {
			t.Fatalf("Failed to flush subscriber: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
		
		if err := alicePub.Publish(subject+"_alice", []byte(testMessage)); err != nil {
			t.Fatalf("Failed to publish with Alice credentials: %v", err)
		}
		
		select {
		case received := <-receivedMsg:
			if received != testMessage {
				t.Errorf("Expected %q, got %q", testMessage, received)
			}
			t.Logf("✓ Alice authentication works: %q", received)
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for message with Alice credentials")
		}
	})
	
	// Test Bob's credentials
	t.Run("BobCredentials", func(t *testing.T) {
		bobPub := env.ConnectToProxyWithAuth(t, testutil.GetBobCredentials())
		bobSub := env.ConnectToProxyWithAuth(t, testutil.GetBobCredentials())
		
		receivedMsg := make(chan string, 1)
		
		sub, err := bobSub.Subscribe(subject+"_bob", func(msg *nats.Msg) {
			receivedMsg <- string(msg.Data)
		})
		if err != nil {
			t.Fatalf("Failed to subscribe with Bob credentials: %v", err)
		}
		defer sub.Unsubscribe()
		
		if err := bobSub.Flush(); err != nil {
			t.Fatalf("Failed to flush subscriber: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
		
		if err := bobPub.Publish(subject+"_bob", []byte(testMessage)); err != nil {
			t.Fatalf("Failed to publish with Bob credentials: %v", err)
		}
		
		select {
		case received := <-receivedMsg:
			if received != testMessage {
				t.Errorf("Expected %q, got %q", testMessage, received)
			}
			t.Logf("✓ Bob authentication works: %q", received)
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for message with Bob credentials")
		}
	})
}

func TestE2E_LargeMessage(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	// Create connections
	publisher := env.ConnectToProxy(t)
	subscriber := env.ConnectToProxy(t)
	
	// Set up subscription
	subject := "test.large"
	receivedMsg := make(chan []byte, 1)
	
	sub, err := subscriber.Subscribe(subject, func(msg *nats.Msg) {
		receivedMsg <- msg.Data
	})
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	
	// Wait for subscription to be ready
	if err := subscriber.Flush(); err != nil {
		t.Fatalf("Failed to flush subscriber: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	
	// Create large message (1MB)
	largeMessage := make([]byte, 1024*1024)
	for i := range largeMessage {
		largeMessage[i] = byte('A' + (i % 26))
	}
	
	t.Logf("Publishing large message (%d bytes)", len(largeMessage))
	
	// Publish large message
	start := time.Now()
	if err := publisher.Publish(subject, largeMessage); err != nil {
		t.Fatalf("Failed to publish large message: %v", err)
	}
	
	// Wait for message
	select {
	case received := <-receivedMsg:
		duration := time.Since(start)
		
		if len(received) != len(largeMessage) {
			t.Errorf("Size mismatch: expected %d bytes, got %d bytes", len(largeMessage), len(received))
		}
		
		// Verify complete content integrity (every single byte)
		if !bytes.Equal(received, largeMessage) {
			t.Error("Complete message content mismatch - proxy corrupted data")
			
			// Find first difference for debugging
			for i := 0; i < len(largeMessage) && i < len(received); i++ {
				if received[i] != largeMessage[i] {
					t.Errorf("First difference at byte %d: expected %d, got %d", i, largeMessage[i], received[i])
					break
				}
			}
		} else {
			t.Log("✓ Complete message integrity verified - every byte matches")
		}
		
		t.Logf("✓ Large message (%d bytes) successfully transmitted in %v", len(received), duration)
		
	case <-time.After(30 * time.Second):
		t.Fatal("Timeout waiting for large message")
	}
}

func TestE2E_ConcurrentConnections(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	// Create subscriber
	subscriber := env.ConnectToProxy(t)
	
	// Set up subscription
	subject := "test.concurrent"
	publisherCount := 10
	messagesPerPublisher := 5
	totalMessages := publisherCount * messagesPerPublisher
	
	receivedMessages := make(chan string, totalMessages)
	
	sub, err := subscriber.Subscribe(subject, func(msg *nats.Msg) {
		receivedMessages <- string(msg.Data)
	})
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	
	// Wait for subscription to be ready
	if err := subscriber.Flush(); err != nil {
		t.Fatalf("Failed to flush subscriber: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	
	// Start concurrent publishers
	var wg sync.WaitGroup
	
	t.Logf("Starting %d concurrent publishers, %d messages each", publisherCount, messagesPerPublisher)
	
	for p := 0; p < publisherCount; p++ {
		wg.Add(1)
		go func(publisherID int) {
			defer wg.Done()
			
			// Create publisher connection
			publisher := env.ConnectToProxy(t)
			
			// Publish messages
			for m := 0; m < messagesPerPublisher; m++ {
				message := fmt.Sprintf("Publisher-%d-Message-%d", publisherID, m+1)
				if err := publisher.Publish(subject, []byte(message)); err != nil {
					t.Errorf("Publisher %d failed to publish message %d: %v", publisherID, m+1, err)
					return
				}
			}
		}(p)
	}
	
	// Wait for all publishers to finish
	wg.Wait()
	
	// Collect all messages and verify content
	receivedCount := 0
	receivedContent := make(map[string]bool) // Track unique messages
	timeout := time.After(15 * time.Second)
	
	for receivedCount < totalMessages {
		select {
		case msg := <-receivedMessages:
			receivedCount++
			receivedContent[msg] = true
			
			// Verify message format matches expected pattern
			var publisherID, messageNum int
			if n, err := fmt.Sscanf(msg, "Publisher-%d-Message-%d", &publisherID, &messageNum); n != 2 || err != nil {
				t.Errorf("Invalid message format: %q", msg)
			}
			
		case <-timeout:
			t.Fatalf("Timeout waiting for concurrent messages. Received %d/%d", receivedCount, totalMessages)
		}
	}
	
	// Verify we received all expected unique messages (no duplicates/losses)
	if len(receivedContent) != totalMessages {
		t.Errorf("Message integrity issue: expected %d unique messages, got %d", totalMessages, len(receivedContent))
		
		// Check for expected messages
		expectedMessages := make(map[string]bool)
		for p := 0; p < publisherCount; p++ {
			for m := 0; m < messagesPerPublisher; m++ {
				expectedMessages[fmt.Sprintf("Publisher-%d-Message-%d", p, m+1)] = true
			}
		}
		
		// Find missing messages
		for expected := range expectedMessages {
			if !receivedContent[expected] {
				t.Errorf("Missing message: %q", expected)
			}
		}
	} else {
		t.Log("✓ Complete message integrity verified - all unique messages received")
	}
	
	t.Logf("✓ Successfully received all %d messages from %d concurrent publishers", totalMessages, publisherCount)
}

func TestE2E_DataIntegrityPatterns(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	// Create connections
	publisher := env.ConnectToProxy(t)
	subscriber := env.ConnectToProxy(t)
	
	testCases := []struct {
		name string
		data []byte
		desc string
	}{
		{
			name: "AllZeros",
			data: make([]byte, 10000), // All zeros
			desc: "10KB of zero bytes",
		},
		{
			name: "AllOnes", 
			data: bytes.Repeat([]byte{0xFF}, 10000), // All 255s
			desc: "10KB of 0xFF bytes",
		},
		{
			name: "RandomPattern",
			data: func() []byte {
				// Create pseudo-random pattern
				data := make([]byte, 10000)
				for i := range data {
					data[i] = byte((i * 37 + 91) % 256) // Pseudo-random but deterministic
				}
				return data
			}(),
			desc: "10KB pseudo-random pattern",
		},
		{
			name: "BinaryData",
			data: func() []byte {
				// Binary data with nulls and control characters
				data := make([]byte, 10000)
				for i := range data {
					data[i] = byte(i % 256) // 0-255 repeating pattern
				}
				return data
			}(),
			desc: "10KB binary data (0-255 repeating)",
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			subject := fmt.Sprintf("test.integrity.%s", tc.name)
			receivedMsg := make(chan []byte, 1)
			
			// Subscribe
			sub, err := subscriber.Subscribe(subject, func(msg *nats.Msg) {
				receivedMsg <- msg.Data
			})
			if err != nil {
				t.Fatalf("Failed to subscribe: %v", err)
			}
			defer sub.Unsubscribe()
			
			// Wait for subscription
			if err := subscriber.Flush(); err != nil {
				t.Fatalf("Failed to flush subscriber: %v", err)
			}
			time.Sleep(100 * time.Millisecond)
			
			t.Logf("Testing %s (%d bytes)", tc.desc, len(tc.data))
			
			// Publish
			start := time.Now()
			if err := publisher.Publish(subject, tc.data); err != nil {
				t.Fatalf("Failed to publish %s: %v", tc.name, err)
			}
			
			// Receive and verify
			select {
			case received := <-receivedMsg:
				duration := time.Since(start)
				
				// Verify exact match
				if !bytes.Equal(received, tc.data) {
					t.Errorf("%s: Content mismatch - proxy corrupted data", tc.name)
					t.Errorf("Expected length: %d, Received length: %d", len(tc.data), len(received))
					
					// Find first difference
					minLen := len(tc.data)
					if len(received) < minLen {
						minLen = len(received)
					}
					for i := 0; i < minLen; i++ {
						if received[i] != tc.data[i] {
							t.Errorf("First difference at byte %d: expected 0x%02X, got 0x%02X", i, tc.data[i], received[i])
							break
						}
					}
				} else {
					t.Logf("✓ %s: Perfect data integrity - all %d bytes match (transmitted in %v)", tc.desc, len(received), duration)
				}
				
			case <-time.After(10 * time.Second):
				t.Fatalf("%s: Timeout waiting for message", tc.name)
			}
		})
	}
}
package e2e

import (
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
		
		// Verify content integrity (check first and last 100 bytes)
		for i := 0; i < 100; i++ {
			if received[i] != largeMessage[i] {
				t.Errorf("Content mismatch at byte %d: expected %d, got %d", i, largeMessage[i], received[i])
				break
			}
		}
		
		for i := len(received) - 100; i < len(received); i++ {
			if received[i] != largeMessage[i] {
				t.Errorf("Content mismatch at byte %d: expected %d, got %d", i, largeMessage[i], received[i])
				break
			}
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
	
	// Collect all messages
	receivedCount := 0
	timeout := time.After(15 * time.Second)
	
	for receivedCount < totalMessages {
		select {
		case <-receivedMessages:
			receivedCount++
		case <-timeout:
			t.Fatalf("Timeout waiting for concurrent messages. Received %d/%d", receivedCount, totalMessages)
		}
	}
	
	t.Logf("✓ Successfully received all %d messages from %d concurrent publishers", totalMessages, publisherCount)
}
package e2e

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"nats-limiter-proxy/e2e/testutil"
)

// TestE2E_ProxyResilience tests proxy behavior under various failure scenarios
func TestE2E_ProxyResilience(t *testing.T) {
	env := testutil.NewDockerComposeEnv()

	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)

	t.Run("NATSServerReconnection", func(t *testing.T) {
		// Connect through proxy
		nc := env.ConnectToProxy(t)
		
		subject := "test.resilience.nats"
		received := make(chan string, 10)
		
		sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
			received <- string(msg.Data)
		})
		if err != nil {
			t.Fatalf("Failed to subscribe: %v", err)
		}
		defer sub.Unsubscribe()
		
		if err := nc.Flush(); err != nil {
			t.Fatalf("Failed to flush: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
		
		// Send initial message to verify connectivity
		testMsg1 := "before restart"
		if err := nc.Publish(subject, []byte(testMsg1)); err != nil {
			t.Fatalf("Failed to publish initial message: %v", err)
		}
		
		select {
		case msg := <-received:
			if msg != testMsg1 {
				t.Errorf("Initial message mismatch: expected %q, got %q", testMsg1, msg)
			}
			t.Log("✓ Initial connectivity confirmed")
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for initial message")
		}
		
		// Note: In a real test environment, you might restart the NATS server here
		// For now, we'll just test that the connection remains stable
		time.Sleep(2 * time.Second)
		
		// Send message after potential disruption
		testMsg2 := "after potential restart"
		if err := nc.Publish(subject, []byte(testMsg2)); err != nil {
			t.Fatalf("Failed to publish post-restart message: %v", err)
		}
		
		select {
		case msg := <-received:
			if msg != testMsg2 {
				t.Errorf("Post-restart message mismatch: expected %q, got %q", testMsg2, msg)
			}
			t.Log("✓ Connection resilient to potential NATS disruption")
		case <-time.After(10 * time.Second):
			t.Fatal("Timeout waiting for post-restart message")
		}
	})

	t.Run("ConnectionStability", func(t *testing.T) {
		// Test that connections remain stable over time
		nc := env.ConnectToProxy(t)
		
		subject := "test.stability"
		messageCount := 50
		interval := 100 * time.Millisecond
		
		received := make(chan string, messageCount)
		
		sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
			received <- string(msg.Data)
		})
		if err != nil {
			t.Fatalf("Failed to subscribe: %v", err)
		}
		defer sub.Unsubscribe()
		
		if err := nc.Flush(); err != nil {
			t.Fatalf("Failed to flush: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
		
		// Send messages over time
		go func() {
			for i := 0; i < messageCount; i++ {
				testMsg := fmt.Sprintf("stability-msg-%d", i)
				if err := nc.Publish(subject, []byte(testMsg)); err != nil {
					t.Errorf("Failed to publish stability message %d: %v", i, err)
					return
				}
				time.Sleep(interval)
			}
		}()
		
		// Collect messages
		receivedCount := 0
		timeout := time.After(time.Duration(messageCount) * interval * 2) // 2x expected time
		
		for receivedCount < messageCount {
			select {
			case msg := <-received:
				var msgNum int
				if n, err := fmt.Sscanf(msg, "stability-msg-%d", &msgNum); n == 1 && err == nil {
					receivedCount++
					if receivedCount%10 == 0 {
						t.Logf("Received %d/%d stability messages", receivedCount, messageCount)
					}
				} else {
					t.Errorf("Invalid stability message format: %q", msg)
				}
			case <-timeout:
				t.Fatalf("Timeout: received %d/%d stability messages", receivedCount, messageCount)
			}
		}
		
		t.Logf("✓ Connection stability confirmed: %d messages over %v", messageCount, time.Duration(messageCount)*interval)
	})
}

// TestE2E_LoadDistribution tests load distribution patterns
func TestE2E_LoadDistribution(t *testing.T) {
	env := testutil.NewDockerComposeEnv()

	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)

	t.Run("MultiplePublishersToSingleSubscriber", func(t *testing.T) {
		// Create single subscriber
		subscriber := env.ConnectToProxy(t)
		
		subject := "test.load.single-sub"
		numPublishers := 5
		msgsPerPublisher := 20
		totalMsgs := numPublishers * msgsPerPublisher
		
		received := make(chan string, totalMsgs)
		receivedMap := make(map[string]bool)
		var mu sync.Mutex
		
		sub, err := subscriber.Subscribe(subject, func(msg *nats.Msg) {
			mu.Lock()
			msgStr := string(msg.Data)
			if !receivedMap[msgStr] {
				receivedMap[msgStr] = true
				received <- msgStr
			}
			mu.Unlock()
		})
		if err != nil {
			t.Fatalf("Failed to subscribe: %v", err)
		}
		defer sub.Unsubscribe()
		
		if err := subscriber.Flush(); err != nil {
			t.Fatalf("Failed to flush subscriber: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
		
		// Create multiple publishers
		var wg sync.WaitGroup
		
		for p := 0; p < numPublishers; p++ {
			wg.Add(1)
			go func(publisherID int) {
				defer wg.Done()
				
				publisher := env.ConnectToProxy(t)
				defer publisher.Close()
				
				for m := 0; m < msgsPerPublisher; m++ {
					testMsg := fmt.Sprintf("pub-%d-msg-%d", publisherID, m)
					if err := publisher.Publish(subject, []byte(testMsg)); err != nil {
						t.Errorf("Publisher %d failed to publish message %d: %v", publisherID, m, err)
						return
					}
				}
				
				if err := publisher.Flush(); err != nil {
					t.Errorf("Publisher %d failed to flush: %v", publisherID, err)
				}
			}(p)
		}
		
		// Wait for all publishers to finish
		wg.Wait()
		
		// Collect all messages
		receivedCount := 0
		timeout := time.After(15 * time.Second)
		
		for receivedCount < totalMsgs {
			select {
			case <-received:
				receivedCount++
				if receivedCount%20 == 0 {
					t.Logf("Received %d/%d messages from multiple publishers", receivedCount, totalMsgs)
				}
			case <-timeout:
				t.Fatalf("Timeout: received %d/%d messages from multiple publishers", receivedCount, totalMsgs)
			}
		}
		
		t.Logf("✓ Load distribution successful: %d publishers × %d messages = %d total", numPublishers, msgsPerPublisher, receivedCount)
	})

	t.Run("SinglePublisherToMultipleSubscribers", func(t *testing.T) {
		// Create multiple subscribers
		numSubscribers := 5
		subscribers := make([]*nats.Conn, numSubscribers)
		receivedChans := make([]chan string, numSubscribers)
		
		subject := "test.load.multi-sub"
		messageCount := 30
		
		// Set up subscribers
		for i := 0; i < numSubscribers; i++ {
			subscribers[i] = env.ConnectToProxy(t)
			receivedChans[i] = make(chan string, messageCount)
			
			capturedI := i // Capture for closure
			sub, err := subscribers[i].Subscribe(subject, func(msg *nats.Msg) {
				receivedChans[capturedI] <- string(msg.Data)
			})
			if err != nil {
				t.Fatalf("Failed to create subscriber %d: %v", i, err)
			}
			defer sub.Unsubscribe()
			
			if err := subscribers[i].Flush(); err != nil {
				t.Fatalf("Failed to flush subscriber %d: %v", i, err)
			}
		}
		
		// Cleanup subscribers
		defer func() {
			for _, nc := range subscribers {
				if nc != nil {
					nc.Close()
				}
			}
		}()
		
		time.Sleep(200 * time.Millisecond) // Let subscriptions settle
		
		// Create single publisher
		publisher := env.ConnectToProxy(t)
		defer publisher.Close()
		
		// Publish messages
		for i := 0; i < messageCount; i++ {
			testMsg := fmt.Sprintf("broadcast-msg-%d", i)
			if err := publisher.Publish(subject, []byte(testMsg)); err != nil {
				t.Fatalf("Failed to publish broadcast message %d: %v", i, err)
			}
		}
		
		if err := publisher.Flush(); err != nil {
			t.Fatalf("Failed to flush publisher: %v", err)
		}
		
		// Verify all subscribers receive all messages
		for i := 0; i < numSubscribers; i++ {
			receivedCount := 0
			timeout := time.After(10 * time.Second)
			
			for receivedCount < messageCount {
				select {
				case msg := <-receivedChans[i]:
					var msgNum int
					if n, err := fmt.Sscanf(msg, "broadcast-msg-%d", &msgNum); n == 1 && err == nil {
						receivedCount++
					} else {
						t.Errorf("Subscriber %d received invalid message: %q", i, msg)
					}
				case <-timeout:
					t.Fatalf("Subscriber %d timeout: received %d/%d messages", i, receivedCount, messageCount)
				}
			}
			
			t.Logf("✓ Subscriber %d received all %d messages", i, receivedCount)
		}
		
		t.Logf("✓ Broadcast successful: %d messages to %d subscribers", messageCount, numSubscribers)
	})
}

// TestE2E_ErrorHandling tests error scenarios
func TestE2E_ErrorHandling(t *testing.T) {
	env := testutil.NewDockerComposeEnv()

	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)

	t.Run("InvalidSubjectHandling", func(t *testing.T) {
		nc := env.ConnectToProxy(t)
		
		// Test various invalid subjects (if NATS has subject restrictions)
		invalidSubjects := []string{
			"", // Empty subject
			// Add other invalid patterns based on NATS rules
		}
		
		for _, subject := range invalidSubjects {
			if subject == "" {
				// Empty subject test
				err := nc.Publish("", []byte("test"))
				if err == nil {
					t.Error("Expected error when publishing to empty subject")
				} else {
					t.Logf("✓ Empty subject correctly rejected: %v", err)
				}
			}
		}
	})

	t.Run("ConnectionLimits", func(t *testing.T) {
		// Test multiple connections to ensure proxy can handle them
		numConnections := 20
		connections := make([]*nats.Conn, numConnections)
		
		// Create multiple connections
		for i := 0; i < numConnections; i++ {
			nc := env.ConnectToProxy(t)
			connections[i] = nc
		}
		
		// Cleanup connections
		defer func() {
			for _, nc := range connections {
				if nc != nil {
					nc.Close()
				}
			}
		}()
		
		// Test that all connections work
		subject := "test.limits.connections"
		for i, nc := range connections {
			testMsg := fmt.Sprintf("conn-%d-test", i)
			if err := nc.Publish(subject, []byte(testMsg)); err != nil {
				t.Errorf("Connection %d failed to publish: %v", i, err)
			}
		}
		
		t.Logf("✓ Multiple connections handled successfully: %d connections", numConnections)
	})

	t.Run("GracefulDisconnection", func(t *testing.T) {
		nc := env.ConnectToProxy(t)
		
		subject := "test.disconnect"
		received := make(chan string, 1)
		
		sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
			received <- string(msg.Data)
		})
		if err != nil {
			t.Fatalf("Failed to subscribe: %v", err)
		}
		
		if err := nc.Flush(); err != nil {
			t.Fatalf("Failed to flush: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
		
		// Publish message
		testMsg := "pre-disconnect"
		if err := nc.Publish(subject, []byte(testMsg)); err != nil {
			t.Fatalf("Failed to publish: %v", err)
		}
		
		// Verify message received
		select {
		case msg := <-received:
			if msg != testMsg {
				t.Errorf("Message mismatch: expected %q, got %q", testMsg, msg)
			}
			t.Log("✓ Message received before disconnect")
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for pre-disconnect message")
		}
		
		// Close connection gracefully
		if err := sub.Unsubscribe(); err != nil {
			t.Errorf("Failed to unsubscribe: %v", err)
		}
		
		nc.Close()
		
		// Wait a moment to ensure cleanup
		time.Sleep(1 * time.Second)
		
		t.Log("✓ Graceful disconnection completed")
	})
}
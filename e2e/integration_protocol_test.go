package e2e

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"nats-limiter-proxy/e2e/testutil"
)

// TestE2E_ProtocolCompliance tests NATS protocol handling through proxy
func TestE2E_ProtocolCompliance(t *testing.T) {
	env := testutil.NewDockerComposeEnv()

	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)

	t.Run("PingPong", func(t *testing.T) {
		nc := env.ConnectToProxy(t)
		
		// Test ping/pong mechanism
		rtt, err := nc.RTT()
		if err != nil {
			t.Fatalf("Failed to get RTT through proxy: %v", err)
		}
		
		if rtt <= 0 {
			t.Error("Invalid RTT received")
		}
		
		t.Logf("✓ PING/PONG works through proxy: RTT=%v", rtt)
	})

	t.Run("ServerInfo", func(t *testing.T) {
		nc := env.ConnectToProxy(t)
		
		// Check that we can get server info
		if !nc.IsConnected() {
			t.Fatal("Connection not established")
		}
		
		servers := nc.Servers()
		if len(servers) == 0 {
			t.Error("No server info available")
		}
		
		t.Logf("✓ Server info available through proxy: %d servers", len(servers))
	})

	t.Run("FlushMechanism", func(t *testing.T) {
		nc := env.ConnectToProxy(t)
		
		subject := "test.protocol.flush"
		received := make(chan bool, 1)
		
		sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
			received <- true
		})
		if err != nil {
			t.Fatalf("Failed to subscribe: %v", err)
		}
		defer sub.Unsubscribe()
		
		// Flush subscription
		if err := nc.Flush(); err != nil {
			t.Fatalf("Failed to flush subscription: %v", err)
		}
		
		// Publish and flush
		if err := nc.Publish(subject, []byte("flush test")); err != nil {
			t.Fatalf("Failed to publish: %v", err)
		}
		
		if err := nc.Flush(); err != nil {
			t.Fatalf("Failed to flush publish: %v", err)
		}
		
		// Should receive message immediately after flush
		select {
		case <-received:
			t.Log("✓ Flush mechanism works through proxy")
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for flushed message")
		}
	})
}

// TestE2E_SubscriptionManagement tests subscription handling
func TestE2E_SubscriptionManagement(t *testing.T) {
	env := testutil.NewDockerComposeEnv()

	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)

	t.Run("MultipleSubscriptions", func(t *testing.T) {
		nc := env.ConnectToProxy(t)
		
		numSubs := 10
		subjects := make([]string, numSubs)
		subs := make([]*nats.Subscription, numSubs)
		received := make([]chan string, numSubs)
		
		// Create multiple subscriptions
		for i := 0; i < numSubs; i++ {
			subjects[i] = fmt.Sprintf("test.multi.%d", i)
			received[i] = make(chan string, 1)
			
			capturedI := i // Capture for closure
			sub, err := nc.Subscribe(subjects[i], func(msg *nats.Msg) {
				received[capturedI] <- string(msg.Data)
			})
			if err != nil {
				t.Fatalf("Failed to create subscription %d: %v", i, err)
			}
			subs[i] = sub
		}
		
		// Cleanup subscriptions
		defer func() {
			for _, sub := range subs {
				if sub != nil {
					sub.Unsubscribe()
				}
			}
		}()
		
		if err := nc.Flush(); err != nil {
			t.Fatalf("Failed to flush subscriptions: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
		
		// Publish to all subjects
		for i, subject := range subjects {
			testMsg := fmt.Sprintf("message-%d", i)
			if err := nc.Publish(subject, []byte(testMsg)); err != nil {
				t.Fatalf("Failed to publish to subject %d: %v", i, err)
			}
		}
		
		// Verify all messages received
		for i := 0; i < numSubs; i++ {
			select {
			case msg := <-received[i]:
				expectedMsg := fmt.Sprintf("message-%d", i)
				if msg != expectedMsg {
					t.Errorf("Subscription %d: expected %q, got %q", i, expectedMsg, msg)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("Timeout waiting for message on subscription %d", i)
			}
		}
		
		t.Logf("✓ Multiple subscriptions work correctly: %d subscriptions", numSubs)
	})

	t.Run("QueueSubscriptions", func(t *testing.T) {
		nc1 := env.ConnectToProxy(t)
		nc2 := env.ConnectToProxy(t)
		
		subject := "test.queue"
		queueGroup := "workers"
		
		received1 := make(chan string, 10)
		received2 := make(chan string, 10)
		
		// Create queue subscriptions
		sub1, err := nc1.QueueSubscribe(subject, queueGroup, func(msg *nats.Msg) {
			received1 <- string(msg.Data)
		})
		if err != nil {
			t.Fatalf("Failed to create queue subscription 1: %v", err)
		}
		defer sub1.Unsubscribe()
		
		sub2, err := nc2.QueueSubscribe(subject, queueGroup, func(msg *nats.Msg) {
			received2 <- string(msg.Data)
		})
		if err != nil {
			t.Fatalf("Failed to create queue subscription 2: %v", err)
		}
		defer sub2.Unsubscribe()
		
		if err := nc1.Flush(); err != nil {
			t.Fatalf("Failed to flush nc1: %v", err)
		}
		if err := nc2.Flush(); err != nil {
			t.Fatalf("Failed to flush nc2: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
		
		// Publish multiple messages
		numMsgs := 10
		for i := 0; i < numMsgs; i++ {
			testMsg := fmt.Sprintf("queue-msg-%d", i)
			if err := nc1.Publish(subject, []byte(testMsg)); err != nil {
				t.Fatalf("Failed to publish queue message %d: %v", i, err)
			}
		}
		
		// Collect messages with timeout
		allReceived := make(map[string]bool)
		timeout := time.After(10 * time.Second)
		
		for len(allReceived) < numMsgs {
			select {
			case msg := <-received1:
				allReceived[msg] = true
				t.Logf("Received on connection 1: %s", msg)
			case msg := <-received2:
				allReceived[msg] = true
				t.Logf("Received on connection 2: %s", msg)
			case <-timeout:
				t.Fatalf("Timeout: received %d/%d messages", len(allReceived), numMsgs)
			}
		}
		
		// Verify all expected messages received
		for i := 0; i < numMsgs; i++ {
			expectedMsg := fmt.Sprintf("queue-msg-%d", i)
			if !allReceived[expectedMsg] {
				t.Errorf("Missing queue message: %s", expectedMsg)
			}
		}
		
		t.Logf("✓ Queue subscriptions work correctly: %d messages distributed", len(allReceived))
	})
}

// TestE2E_MessageSizes tests various payload sizes
func TestE2E_MessageSizes(t *testing.T) {
	env := testutil.NewDockerComposeEnv()

	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)

	nc := env.ConnectToProxy(t)

	testSizes := []struct {
		name string
		size int
	}{
		{"Empty", 0},
		{"Small", 32},
		{"Medium", 1024},
		{"Large", 32 * 1024},
		{"VeryLarge", 256 * 1024},
		{"MaxSize", 1024 * 1024}, // 1MB
	}

	for _, test := range testSizes {
		t.Run(test.name, func(t *testing.T) {
			subject := fmt.Sprintf("test.size.%s", test.name)
			received := make(chan []byte, 1)
			
			sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
				received <- msg.Data
			})
			if err != nil {
				t.Fatalf("Failed to subscribe for size %s: %v", test.name, err)
			}
			defer sub.Unsubscribe()
			
			if err := nc.Flush(); err != nil {
				t.Fatalf("Failed to flush for size %s: %v", test.name, err)
			}
			time.Sleep(100 * time.Millisecond)
			
			// Create test payload
			var payload []byte
			if test.size > 0 {
				payload = make([]byte, test.size)
				// Fill with deterministic pattern
				for i := range payload {
					payload[i] = byte(i % 256)
				}
			}
			
			start := time.Now()
			if err := nc.Publish(subject, payload); err != nil {
				t.Fatalf("Failed to publish %s payload: %v", test.name, err)
			}
			
			select {
			case receivedData := <-received:
				duration := time.Since(start)
				
				if len(receivedData) != test.size {
					t.Errorf("%s: Size mismatch - expected %d, got %d", test.name, test.size, len(receivedData))
				}
				
				if test.size > 0 && !bytes.Equal(receivedData, payload) {
					t.Errorf("%s: Content mismatch", test.name)
					
					// Find first difference
					minLen := test.size
					if len(receivedData) < minLen {
						minLen = len(receivedData)
					}
					for i := 0; i < minLen; i++ {
						if receivedData[i] != payload[i] {
							t.Errorf("First difference at byte %d: expected %d, got %d", i, payload[i], receivedData[i])
							break
						}
					}
				}
				
				t.Logf("✓ %s (%d bytes) transmitted correctly in %v", test.name, len(receivedData), duration)
				
			case <-time.After(30 * time.Second):
				t.Fatalf("Timeout waiting for %s message", test.name)
			}
		})
	}
}

// TestE2E_HighVolumeMessaging tests high message throughput
func TestE2E_HighVolumeMessaging(t *testing.T) {
	env := testutil.NewDockerComposeEnv()

	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)

	publisher := env.ConnectToProxy(t)
	subscriber := env.ConnectToProxy(t)

	subject := "test.highvolume"
	messageCount := 1000
	messageSize := 1024 // 1KB messages
	
	received := make(chan int, messageCount)
	receivedMessages := sync.Map{}
	
	// Subscribe
	sub, err := subscriber.Subscribe(subject, func(msg *nats.Msg) {
		// Extract message number from payload
		var msgNum int
		if n, err := fmt.Sscanf(string(msg.Data[:8]), "%08d", &msgNum); n == 1 && err == nil {
			receivedMessages.Store(msgNum, true)
			received <- msgNum
		} else {
			t.Errorf("Failed to parse message number from: %s", string(msg.Data[:8]))
		}
	})
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	
	if err := subscriber.Flush(); err != nil {
		t.Fatalf("Failed to flush subscriber: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	
	// Publish messages rapidly
	t.Logf("Publishing %d messages of %d bytes each...", messageCount, messageSize)
	start := time.Now()
	
	for i := 0; i < messageCount; i++ {
		// Create message with number prefix and padding
		msgData := make([]byte, messageSize)
		msgHeader := fmt.Sprintf("%08d", i)
		copy(msgData, msgHeader)
		
		// Fill rest with random data
		for j := 8; j < messageSize; j++ {
			msgData[j] = byte(rand.Intn(256))
		}
		
		if err := publisher.Publish(subject, msgData); err != nil {
			t.Fatalf("Failed to publish message %d: %v", i, err)
		}
		
		// Occasional flush to avoid buffer overflow
		if i%100 == 0 {
			if err := publisher.Flush(); err != nil {
				t.Fatalf("Failed to flush at message %d: %v", i, err)
			}
		}
	}
	
	// Final flush
	if err := publisher.Flush(); err != nil {
		t.Fatalf("Failed to final flush: %v", err)
	}
	
	publishDuration := time.Since(start)
	
	// Collect all messages
	receivedCount := 0
	timeout := time.After(30 * time.Second)
	
	for receivedCount < messageCount {
		select {
		case msgNum := <-received:
			receivedCount++
			if receivedCount%100 == 0 {
				t.Logf("Received %d/%d messages (latest: %d)", receivedCount, messageCount, msgNum)
			}
		case <-timeout:
			t.Fatalf("Timeout: received %d/%d messages", receivedCount, messageCount)
		}
	}
	
	totalDuration := time.Since(start)
	
	// Verify all messages received
	for i := 0; i < messageCount; i++ {
		if _, exists := receivedMessages.Load(i); !exists {
			t.Errorf("Missing message: %d", i)
		}
	}
	
	// Calculate throughput
	mbps := float64(messageCount*messageSize) / (1024*1024) / totalDuration.Seconds()
	msgPerSec := float64(messageCount) / totalDuration.Seconds()
	
	t.Logf("✓ High volume messaging successful:")
	t.Logf("  - Messages: %d", messageCount)
	t.Logf("  - Message size: %d bytes", messageSize)
	t.Logf("  - Publish time: %v", publishDuration)
	t.Logf("  - Total time: %v", totalDuration)
	t.Logf("  - Throughput: %.2f MB/s", mbps)
	t.Logf("  - Rate: %.0f msg/s", msgPerSec)
}
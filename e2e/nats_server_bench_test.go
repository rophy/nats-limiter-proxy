package e2e

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"nats-limiter-proxy/e2e/testutil"
)

// BenchmarkE2E_ProxyThroughput benchmarks message throughput through proxy
func BenchmarkE2E_ProxyThroughput(b *testing.B) {
	if testing.Short() {
		b.Skip("Skipping benchmark in short mode")
	}

	env := testutil.NewDockerComposeEnv()
	
	// These don't use testing.T, so we'll use basic checks
	nc, err := nats.Connect(env.ProxyURL, 
		nats.UserCredentials(testutil.GetAliceCredentials()),
		nats.Timeout(10*time.Second),
	)
	if err != nil {
		b.Fatalf("Failed to connect to proxy: %v", err)
	}
	defer nc.Close()

	subject := "bench.throughput"
	payload := make([]byte, 1024) // 1KB payload
	
	// Fill payload with data
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := nc.Publish(subject, payload); err != nil {
				b.Errorf("Failed to publish: %v", err)
				return
			}
		}
	})
	
	if err := nc.Flush(); err != nil {
		b.Errorf("Failed to final flush: %v", err)
	}
}

// BenchmarkE2E_ProxyLatency benchmarks round-trip latency through proxy
func BenchmarkE2E_ProxyLatency(b *testing.B) {
	if testing.Short() {
		b.Skip("Skipping benchmark in short mode")
	}

	env := testutil.NewDockerComposeEnv()
	
	publisher, err := nats.Connect(env.ProxyURL, 
		nats.UserCredentials(testutil.GetAliceCredentials()),
		nats.Timeout(10*time.Second),
	)
	if err != nil {
		b.Fatalf("Failed to connect publisher to proxy: %v", err)
	}
	defer publisher.Close()

	subscriber, err := nats.Connect(env.ProxyURL, 
		nats.UserCredentials(testutil.GetAliceCredentials()),
		nats.Timeout(10*time.Second),
	)
	if err != nil {
		b.Fatalf("Failed to connect subscriber to proxy: %v", err)
	}
	defer subscriber.Close()

	subject := "bench.latency"
	payload := []byte("latency test message")
	
	received := make(chan time.Time, 1)
	
	sub, err := subscriber.Subscribe(subject, func(msg *nats.Msg) {
		received <- time.Now()
	})
	if err != nil {
		b.Fatalf("Failed to subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	
	if err := subscriber.Flush(); err != nil {
		b.Fatalf("Failed to flush subscriber: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		start := time.Now()
		
		if err := publisher.Publish(subject, payload); err != nil {
			b.Errorf("Failed to publish: %v", err)
			continue
		}
		
		select {
		case <-received:
			// Latency measured
		case <-time.After(5 * time.Second):
			b.Errorf("Timeout waiting for message %d", i)
			continue
		}
		
		// Record latency timing is handled by the benchmark framework
		_ = start // Prevent unused variable warning
	}
}

// TestE2E_PerformanceComparison compares proxy vs direct performance
func TestE2E_PerformanceComparison(t *testing.T) {
	env := testutil.NewDockerComposeEnv()

	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)

	messageCount := 1000
	messageSize := 1024
	
	payload := make([]byte, messageSize)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	t.Run("DirectNATSPerformance", func(t *testing.T) {
		publisher := env.ConnectToNATSDirect(t)
		subscriber := env.ConnectToNATSDirect(t)
		
		subject := "perf.direct"
		received := make(chan int, messageCount)
		
		sub, err := subscriber.Subscribe(subject, func(msg *nats.Msg) {
			received <- len(msg.Data)
		})
		if err != nil {
			t.Fatalf("Failed to subscribe direct: %v", err)
		}
		defer sub.Unsubscribe()
		
		if err := subscriber.Flush(); err != nil {
			t.Fatalf("Failed to flush direct subscriber: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
		
		// Measure direct performance
		start := time.Now()
		
		for i := 0; i < messageCount; i++ {
			if err := publisher.Publish(subject, payload); err != nil {
				t.Fatalf("Failed to publish direct message %d: %v", i, err)
			}
		}
		
		if err := publisher.Flush(); err != nil {
			t.Fatalf("Failed to flush direct publisher: %v", err)
		}
		
		// Collect messages
		receivedCount := 0
		timeout := time.After(30 * time.Second)
		
		for receivedCount < messageCount {
			select {
			case size := <-received:
				if size != messageSize {
					t.Errorf("Direct: Size mismatch at message %d: expected %d, got %d", receivedCount, messageSize, size)
				}
				receivedCount++
			case <-timeout:
				t.Fatalf("Direct: Timeout at %d/%d messages", receivedCount, messageCount)
			}
		}
		
		directDuration := time.Since(start)
		directThroughput := float64(messageCount*messageSize) / (1024*1024) / directDuration.Seconds()
		
		t.Logf("✓ Direct NATS performance:")
		t.Logf("  - Duration: %v", directDuration)
		t.Logf("  - Throughput: %.2f MB/s", directThroughput)
		t.Logf("  - Rate: %.0f msg/s", float64(messageCount)/directDuration.Seconds())
	})

	t.Run("ProxyPerformance", func(t *testing.T) {
		publisher := env.ConnectToProxy(t)
		subscriber := env.ConnectToProxy(t)
		
		subject := "perf.proxy"
		received := make(chan int, messageCount)
		
		sub, err := subscriber.Subscribe(subject, func(msg *nats.Msg) {
			received <- len(msg.Data)
		})
		if err != nil {
			t.Fatalf("Failed to subscribe proxy: %v", err)
		}
		defer sub.Unsubscribe()
		
		if err := subscriber.Flush(); err != nil {
			t.Fatalf("Failed to flush proxy subscriber: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
		
		// Measure proxy performance
		start := time.Now()
		
		for i := 0; i < messageCount; i++ {
			if err := publisher.Publish(subject, payload); err != nil {
				t.Fatalf("Failed to publish proxy message %d: %v", i, err)
			}
		}
		
		if err := publisher.Flush(); err != nil {
			t.Fatalf("Failed to flush proxy publisher: %v", err)
		}
		
		// Collect messages
		receivedCount := 0
		timeout := time.After(30 * time.Second)
		
		for receivedCount < messageCount {
			select {
			case size := <-received:
				if size != messageSize {
					t.Errorf("Proxy: Size mismatch at message %d: expected %d, got %d", receivedCount, messageSize, size)
				}
				receivedCount++
			case <-timeout:
				t.Fatalf("Proxy: Timeout at %d/%d messages", receivedCount, messageCount)
			}
		}
		
		proxyDuration := time.Since(start)
		proxyThroughput := float64(messageCount*messageSize) / (1024*1024) / proxyDuration.Seconds()
		
		t.Logf("✓ Proxy performance:")
		t.Logf("  - Duration: %v", proxyDuration)
		t.Logf("  - Throughput: %.2f MB/s", proxyThroughput)
		t.Logf("  - Rate: %.0f msg/s", float64(messageCount)/proxyDuration.Seconds())
	})
}

// TestE2E_ConcurrentLoadTesting tests proxy under concurrent load
func TestE2E_ConcurrentLoadTesting(t *testing.T) {
	env := testutil.NewDockerComposeEnv()

	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)

	numPublishers := 10
	numSubscribers := 5
	msgsPerPublisher := 100
	totalMessages := numPublishers * msgsPerPublisher

	// Create subscribers
	subscribers := make([]*nats.Conn, numSubscribers)
	receivedChans := make([]chan string, numSubscribers)
	
	subject := "test.concurrent.load"
	
	for i := 0; i < numSubscribers; i++ {
		subscribers[i] = env.ConnectToProxy(t)
		receivedChans[i] = make(chan string, totalMessages)
		
		capturedI := i
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

	// Start concurrent publishers
	var publisherWG sync.WaitGroup
	start := time.Now()
	
	for p := 0; p < numPublishers; p++ {
		publisherWG.Add(1)
		go func(publisherID int) {
			defer publisherWG.Done()
			
			publisher := env.ConnectToProxy(t)
			defer publisher.Close()
			
			for m := 0; m < msgsPerPublisher; m++ {
				testMsg := fmt.Sprintf("pub%d-msg%d", publisherID, m)
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
	
	// Wait for all publishers to complete
	publisherWG.Wait()
	publishDuration := time.Since(start)
	
	t.Logf("All publishers completed in %v", publishDuration)
	
	// Collect messages from all subscribers
	var collectorWG sync.WaitGroup
	allReceived := make([]map[string]bool, numSubscribers)
	
	for i := 0; i < numSubscribers; i++ {
		allReceived[i] = make(map[string]bool)
		collectorWG.Add(1)
		
		go func(subID int) {
			defer collectorWG.Done()
			
			receivedCount := 0
			timeout := time.After(30 * time.Second)
			
			for receivedCount < totalMessages {
				select {
				case msg := <-receivedChans[subID]:
					allReceived[subID][msg] = true
					receivedCount++
					
					if receivedCount%100 == 0 {
						t.Logf("Subscriber %d received %d/%d messages", subID, receivedCount, totalMessages)
					}
				case <-timeout:
					t.Errorf("Subscriber %d timeout: received %d/%d messages", subID, receivedCount, totalMessages)
					return
				}
			}
		}(i)
	}
	
	// Wait for all subscribers to collect messages
	collectorWG.Wait()
	totalDuration := time.Since(start)
	
	// Verify message integrity across all subscribers
	expectedMessages := make(map[string]bool)
	for p := 0; p < numPublishers; p++ {
		for m := 0; m < msgsPerPublisher; m++ {
			expectedMessages[fmt.Sprintf("pub%d-msg%d", p, m)] = true
		}
	}
	
	// Check each subscriber received all messages
	for i := 0; i < numSubscribers; i++ {
		if len(allReceived[i]) != totalMessages {
			t.Errorf("Subscriber %d: expected %d unique messages, got %d", i, totalMessages, len(allReceived[i]))
		}
		
		// Check for missing messages
		missingCount := 0
		for expectedMsg := range expectedMessages {
			if !allReceived[i][expectedMsg] {
				missingCount++
				if missingCount <= 5 { // Only log first few missing messages
					t.Errorf("Subscriber %d missing message: %s", i, expectedMsg)
				}
			}
		}
		
		if missingCount > 5 {
			t.Errorf("Subscriber %d missing %d additional messages", i, missingCount-5)
		}
	}
	
	// Calculate performance metrics
	totalBytesTransmitted := int64(numSubscribers) * int64(totalMessages) * int64(len(fmt.Sprintf("pub%d-msg%d", numPublishers-1, msgsPerPublisher-1)))
	throughputMBps := float64(totalBytesTransmitted) / (1024*1024) / totalDuration.Seconds()
	msgRate := float64(numSubscribers*totalMessages) / totalDuration.Seconds()
	
	t.Logf("✓ Concurrent load test completed:")
	t.Logf("  - Publishers: %d", numPublishers)
	t.Logf("  - Subscribers: %d", numSubscribers)
	t.Logf("  - Messages per publisher: %d", msgsPerPublisher)
	t.Logf("  - Total messages sent: %d", totalMessages)
	t.Logf("  - Total messages received: %d", numSubscribers*totalMessages)
	t.Logf("  - Publish duration: %v", publishDuration)
	t.Logf("  - Total duration: %v", totalDuration)
	t.Logf("  - Throughput: %.2f MB/s", throughputMBps)
	t.Logf("  - Message rate: %.0f msg/s", msgRate)
}
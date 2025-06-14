package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	ProxyURL     = "nats://localhost:4223"
	DirectURL    = "nats://localhost:4222"
	MessageSize  = 64 * 1024 // 64KB
	TestDuration = 10 * time.Second
)

type TestResult struct {
	User           string
	MessageCount   int
	TotalBytes     int64
	Duration       time.Duration
	ThroughputMBps float64
	Success        bool
}

// Create a large message payload
func createPayload(size int) []byte {
	return []byte(strings.Repeat("A", size))
}

// Measure throughput for a specific user
func measureThroughput(user, credsFile, serverURL string, duration time.Duration, messageSize int) TestResult {
	result := TestResult{
		User: user,
	}

	// Load credentials
	opt := nats.UserCredentials(credsFile)
	nc, err := nats.Connect(serverURL, opt)
	if err != nil {
		log.Printf("Failed to connect as %s: %v", user, err)
		return result
	}
	defer nc.Close()

	payload := createPayload(messageSize)
	subject := fmt.Sprintf("throughput.test.%s", user)

	startTime := time.Now()
	endTime := startTime.Add(duration)
	messageCount := 0

	for time.Now().Before(endTime) {
		if err := nc.Publish(subject, payload); err != nil {
			log.Printf("Publish error for %s: %v", user, err)
			break
		}
		messageCount++
	}

	actualDuration := time.Since(startTime)
	totalBytes := int64(messageCount * messageSize)
	throughputMBps := float64(totalBytes) / actualDuration.Seconds() / (1024 * 1024)

	result.MessageCount = messageCount
	result.TotalBytes = totalBytes
	result.Duration = actualDuration
	result.ThroughputMBps = throughputMBps
	result.Success = true

	return result
}

// Test concurrent users
func testConcurrentUsers() {
	fmt.Println("=== Concurrent User Test ===")
	
	var wg sync.WaitGroup
	results := make(chan TestResult, 2)

	// Start Alice test
	wg.Add(1)
	go func() {
		defer wg.Done()
		result := measureThroughput("alice", "local/alice.creds", ProxyURL, TestDuration, MessageSize)
		results <- result
	}()

	// Start Bob test
	wg.Add(1)
	go func() {
		defer wg.Done()
		result := measureThroughput("bob", "local/bob.creds", ProxyURL, TestDuration, MessageSize)
		results <- result
	}()

	// Wait for completion
	wg.Wait()
	close(results)

	// Process results
	for result := range results {
		if result.Success {
			fmt.Printf("%s: %d messages, %.2f MB/s (%.0f bytes/s)\n",
				result.User, result.MessageCount, result.ThroughputMBps, 
				result.ThroughputMBps*1024*1024)
			
			// Validate against expected limits
			var expectedLimit float64
			switch result.User {
			case "alice":
				expectedLimit = 5.0 // 5MB/s
			case "bob":
				expectedLimit = 2.0 // 2MB/s
			}
			
			if result.ThroughputMBps <= expectedLimit*1.2 { // 20% tolerance
				fmt.Printf("  ✓ %s throughput within expected limit (%.1f MB/s)\n", result.User, expectedLimit)
			} else {
				fmt.Printf("  ⚠ %s throughput exceeds limit! Expected ≤%.1f MB/s\n", result.User, expectedLimit)
			}
		} else {
			fmt.Printf("  ✗ %s test failed\n", result.User)
		}
	}
}

// Test individual users
func testIndividualUsers() {
	fmt.Println("=== Individual User Tests ===")
	
	users := []struct {
		name      string
		credsFile string
		limit     float64
	}{
		{"alice", "local/alice.creds", 5.0},
		{"bob", "local/bob.creds", 2.0},
	}

	for _, user := range users {
		fmt.Printf("\nTesting %s (expected limit: %.1f MB/s)...\n", user.name, user.limit)
		
		result := measureThroughput(user.name, user.credsFile, ProxyURL, TestDuration, MessageSize)
		
		if result.Success {
			fmt.Printf("  Messages: %d\n", result.MessageCount)
			fmt.Printf("  Duration: %v\n", result.Duration)
			fmt.Printf("  Throughput: %.2f MB/s (%.0f bytes/s)\n", 
				result.ThroughputMBps, result.ThroughputMBps*1024*1024)
			
			if result.ThroughputMBps <= user.limit*1.2 { // 20% tolerance
				fmt.Printf("  ✓ Throughput within expected limit\n")
			} else {
				fmt.Printf("  ⚠ Throughput exceeds limit!\n")
			}
		} else {
			fmt.Printf("  ✗ Test failed for %s\n", user.name)
		}
	}
}

// Compare direct vs proxy performance
func testDirectVsProxy() {
	fmt.Println("\n=== Direct vs Proxy Comparison ===")
	
	// Test Alice direct connection
	fmt.Println("Testing Alice direct connection (no rate limiting)...")
	directResult := measureThroughput("alice", "local/alice.creds", DirectURL, 5*time.Second, MessageSize)
	
	// Test Alice through proxy
	fmt.Println("Testing Alice through proxy (with rate limiting)...")
	proxyResult := measureThroughput("alice", "local/alice.creds", ProxyURL, 5*time.Second, MessageSize)
	
	if directResult.Success && proxyResult.Success {
		fmt.Printf("Direct:  %.2f MB/s\n", directResult.ThroughputMBps)
		fmt.Printf("Proxy:   %.2f MB/s\n", proxyResult.ThroughputMBps)
		
		reduction := (directResult.ThroughputMBps - proxyResult.ThroughputMBps) / directResult.ThroughputMBps * 100
		if reduction > 0 {
			fmt.Printf("Rate limiting effectiveness: %.1f%% reduction\n", reduction)
			fmt.Printf("✓ Proxy successfully limits throughput\n")
		} else {
			fmt.Printf("⚠ No significant throughput reduction detected\n")
		}
	} else {
		fmt.Printf("✗ Comparison test failed\n")
	}
}

// Test burst behavior
func testBurstBehavior() {
	fmt.Println("\n=== Burst Behavior Test ===")
	
	// Quick burst test - send many messages rapidly
	credsFile := "local/alice.creds"
	opt := nats.UserCredentials(credsFile)
	nc, err := nats.Connect(ProxyURL, opt)
	if err != nil {
		fmt.Printf("Failed to connect: %v\n", err)
		return
	}
	defer nc.Close()

	payload := createPayload(MessageSize)
	burstCount := 50
	
	fmt.Printf("Sending %d messages rapidly...\n", burstCount)
	startTime := time.Now()
	
	for i := 0; i < burstCount; i++ {
		if err := nc.Publish("burst.test", payload); err != nil {
			fmt.Printf("Burst publish failed at message %d: %v\n", i, err)
			break
		}
	}
	
	duration := time.Since(startTime)
	totalBytes := int64(burstCount * MessageSize)
	burstThroughput := float64(totalBytes) / duration.Seconds() / (1024 * 1024)
	
	fmt.Printf("Burst completed in %v\n", duration)
	fmt.Printf("Burst throughput: %.2f MB/s\n", burstThroughput)
	
	if burstThroughput <= 6.0 { // Allow some tolerance above Alice's limit
		fmt.Printf("✓ Burst throughput appropriately limited\n")
	} else {
		fmt.Printf("⚠ Burst throughput may exceed expected limits\n")
	}
}

func main() {
	fmt.Println("NATS Limiter Proxy Throughput Test")
	fmt.Println("==================================")
	fmt.Println("Configuration:")
	fmt.Println("  Alice limit: 5MB/s")
	fmt.Println("  Bob limit:   2MB/s")
	fmt.Println("  Message size:", MessageSize, "bytes")
	fmt.Println("  Test duration:", TestDuration)
	fmt.Println()

	// Check if we can connect to the proxy
	opt := nats.UserCredentials("local/alice.creds")
	nc, err := nats.Connect(ProxyURL, opt)
	if err != nil {
		fmt.Printf("Cannot connect to proxy at %s: %v\n", ProxyURL, err)
		fmt.Println("Make sure 'docker compose up -d' is running")
		os.Exit(1)
	}
	nc.Close()

	// Run tests
	testIndividualUsers()
	fmt.Println()
	testConcurrentUsers()
	testDirectVsProxy()
	testBurstBehavior()
	
	fmt.Println("\n=== Test Summary ===")
	fmt.Println("Review the results above to verify rate limiting is working correctly.")
	fmt.Println("Throughput should be limited to the configured values for each user.")
}
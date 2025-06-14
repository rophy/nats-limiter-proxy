package main

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// Precise test to understand actual throughput through the proxy
func main() {
	fmt.Println("=== Precise Rate Limiting Test ===")
	fmt.Println()

	// Test with Alice's credentials
	testUserThroughput("alice", "local/alice.creds", 5.0)
	fmt.Println()
	testUserThroughput("bob", "local/bob.creds", 2.0)
}

func testUserThroughput(user, credsFile string, expectedMBps float64) {
	fmt.Printf("Testing %s (expected: %.1f MB/s)\n", user, expectedMBps)
	fmt.Println("----------------------------------------")

	// Connect to proxy
	opt := nats.UserCredentials(credsFile)
	nc, err := nats.Connect("nats://localhost:4223", opt)
	if err != nil {
		fmt.Printf("Failed to connect: %v\n", err)
		return
	}
	defer nc.Close()

	// Test different message sizes and durations
	tests := []struct {
		name        string
		messageSize int
		duration    time.Duration
		messages    int
	}{
		{"Small msgs (1KB)", 1024, 10 * time.Second, 0},
		{"Medium msgs (64KB)", 64 * 1024, 10 * time.Second, 0},
		{"Large msgs (256KB)", 256 * 1024, 5 * time.Second, 0},
		{"Fixed count (100x64KB)", 64 * 1024, 0, 100},
	}

	for _, test := range tests {
		fmt.Printf("\n%s:\n", test.name)
		
		payload := make([]byte, test.messageSize)
		for i := range payload {
			payload[i] = 'A'
		}

		var totalBytes int64
		var messageCount int
		startTime := time.Now()

		if test.duration > 0 {
			// Time-based test
			endTime := startTime.Add(test.duration)
			for time.Now().Before(endTime) {
				if err := nc.Publish("test.precise", payload); err != nil {
					fmt.Printf("  Publish error: %v\n", err)
					break
				}
				totalBytes += int64(test.messageSize)
				messageCount++
			}
		} else {
			// Count-based test
			for i := 0; i < test.messages; i++ {
				msgStart := time.Now()
				if err := nc.Publish("test.precise", payload); err != nil {
					fmt.Printf("  Publish error: %v\n", err)
					break
				}
				msgDuration := time.Since(msgStart)
				
				totalBytes += int64(test.messageSize)
				messageCount++
				
				// Log first few messages to see rate limiting effect
				if i < 5 {
					fmt.Printf("  Msg %d: %v", i+1, msgDuration.Round(time.Millisecond))
					if msgDuration < time.Millisecond {
						fmt.Printf(" (fast)")
					} else if msgDuration > 10*time.Millisecond {
						fmt.Printf(" (limited)")
					}
					fmt.Println()
				}
			}
		}

		actualDuration := time.Since(startTime)
		actualMBps := float64(totalBytes) / actualDuration.Seconds() / (1024 * 1024)
		
		fmt.Printf("  Result: %d messages, %d bytes in %v\n", 
			messageCount, totalBytes, actualDuration.Round(time.Millisecond))
		fmt.Printf("  Throughput: %.2f MB/s", actualMBps)
		
		if actualMBps > expectedMBps*1.05 {
			overagePercent := (actualMBps/expectedMBps - 1) * 100
			fmt.Printf(" ⚠️  EXCEEDS by %.1f%%", overagePercent)
		} else if actualMBps > expectedMBps*0.95 {
			fmt.Printf(" ✓ Within range")
		} else {
			underagePercent := (1 - actualMBps/expectedMBps) * 100
			fmt.Printf(" ⚠️  UNDER by %.1f%%", underagePercent)
		}
		fmt.Println()
	}
}
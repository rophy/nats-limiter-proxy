package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/juju/ratelimit"
)

// Debug the exact rate limiting behavior we're seeing
func main() {
	fmt.Println("=== Rate Limiting Debug Analysis ===")
	fmt.Println()

	// Test the EXACT configuration we use for Alice
	aliceBandwidth := int64(5242880) // 5MB/s
	
	fmt.Printf("Alice's configured limit: %d bytes/s (%.2f MB/s)\n", 
		aliceBandwidth, float64(aliceBandwidth)/(1024*1024))
	fmt.Println()

	// Create bucket with SAME configuration as production
	limiter := ratelimit.NewBucketWithRate(float64(aliceBandwidth), aliceBandwidth)
	
	fmt.Println("=== Test 1: Burst Behavior ===")
	testBurstBehavior(limiter, aliceBandwidth)
	
	fmt.Println()
	fmt.Println("=== Test 2: Sustained Rate ===")
	testSustainedRate(limiter, aliceBandwidth)
	
	fmt.Println()
	fmt.Println("=== Test 3: Different Bucket Configurations ===")
	testDifferentConfigurations(aliceBandwidth)
}

func testBurstBehavior(limiter *ratelimit.Bucket, expectedRate int64) {
	// Test what happens when we try to send a large amount quickly
	largeData := strings.NewReader(strings.Repeat("A", int(expectedRate*2))) // 10MB data
	limitedReader := ratelimit.Reader(largeData, limiter)
	
	buffer := make([]byte, 64*1024) // 64KB chunks (like our test)
	totalBytes := 0
	startTime := time.Now()
	
	fmt.Println("Sending 10MB in 64KB chunks:")
	for i := 0; i < 5; i++ { // First 5 chunks
		chunkStart := time.Now()
		n, err := limitedReader.Read(buffer)
		chunkDuration := time.Since(chunkStart)
		
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			break
		}
		
		totalBytes += n
		elapsed := time.Since(startTime)
		currentRate := float64(totalBytes) / elapsed.Seconds()
		
		fmt.Printf("Chunk %d: %d bytes in %v (total: %d, rate: %.2f MB/s)\n",
			i+1, n, chunkDuration.Round(time.Millisecond), totalBytes, 
			currentRate/(1024*1024))
	}
	
	finalDuration := time.Since(startTime)
	finalRate := float64(totalBytes) / finalDuration.Seconds()
	
	fmt.Printf("Final: %d bytes in %v = %.2f MB/s", 
		totalBytes, finalDuration.Round(time.Millisecond), finalRate/(1024*1024))
	
	if finalRate > float64(expectedRate)*1.1 { // 10% tolerance
		fmt.Printf(" ⚠️  EXCEEDS LIMIT!")
	} else {
		fmt.Printf(" ✓ Within limit")
	}
	fmt.Println()
}

func testSustainedRate(limiter *ratelimit.Bucket, expectedRate int64) {
	// Test sustained throughput over longer period
	data := strings.NewReader(strings.Repeat("B", int(expectedRate*3))) // 15MB data
	limitedReader := ratelimit.Reader(data, limiter)
	
	buffer := make([]byte, 64*1024)
	totalBytes := 0
	startTime := time.Now()
	
	fmt.Println("Sustained transfer test (15MB):")
	
	// Read for 3 seconds
	for time.Since(startTime) < 3*time.Second {
		n, err := limitedReader.Read(buffer)
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			break
		}
		totalBytes += n
	}
	
	actualDuration := time.Since(startTime)
	actualRate := float64(totalBytes) / actualDuration.Seconds()
	
	fmt.Printf("Sustained: %d bytes in %v = %.2f MB/s (expected: %.2f MB/s)",
		totalBytes, actualDuration.Round(time.Millisecond), 
		actualRate/(1024*1024), float64(expectedRate)/(1024*1024))
	
	if actualRate > float64(expectedRate)*1.05 { // 5% tolerance
		fmt.Printf(" ⚠️  EXCEEDS LIMIT!")
	} else {
		fmt.Printf(" ✓ Within limit")
	}
	fmt.Println()
}

func testDifferentConfigurations(baseRate int64) {
	configs := []struct {
		name     string
		rate     float64
		capacity int64
	}{
		{"Current (rate=capacity)", float64(baseRate), baseRate},
		{"Small burst (capacity=rate/10)", float64(baseRate), baseRate / 10},
		{"No burst (capacity=1)", float64(baseRate), 1},
		{"Large burst (capacity=rate*2)", float64(baseRate), baseRate * 2},
	}
	
	for _, config := range configs {
		fmt.Printf("\n%s:\n", config.name)
		fmt.Printf("  Rate: %.0f B/s, Capacity: %d B\n", config.rate, config.capacity)
		
		limiter := ratelimit.NewBucketWithRate(config.rate, config.capacity)
		data := strings.NewReader(strings.Repeat("C", int(baseRate))) // 5MB
		limitedReader := ratelimit.Reader(data, limiter)
		
		// Try to read 5MB quickly
		buffer := make([]byte, 1024*1024) // 1MB chunks
		startTime := time.Now()
		totalBytes := 0
		
		for i := 0; i < 5; i++ {
			n, err := limitedReader.Read(buffer)
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			totalBytes += n
			
			// Check rate after first MB
			if i == 0 {
				elapsed := time.Since(startTime)
				rate := float64(n) / elapsed.Seconds()
				fmt.Printf("  First 1MB: %v, rate: %.2f MB/s", 
					elapsed.Round(time.Millisecond), rate/(1024*1024))
				
				if elapsed < time.Millisecond*10 {
					fmt.Printf(" (burst allowed)")
				} else {
					fmt.Printf(" (rate limited)")
				}
				fmt.Println()
			}
		}
	}
}
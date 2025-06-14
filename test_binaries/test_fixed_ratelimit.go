package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/juju/ratelimit"
)

// Test the FIXED rate limiting configuration
func main() {
	fmt.Println("=== Testing FIXED Rate Limiting Configuration ===")
	fmt.Println()

	// Alice's configuration
	aliceBandwidth := int64(5242880) // 5MB/s
	burstCapacity := aliceBandwidth / 10 // 524KB burst
	if burstCapacity < 1024 {
		burstCapacity = 1024
	}

	fmt.Printf("Alice's limits:\n")
	fmt.Printf("  Rate: %d bytes/s (%.2f MB/s)\n", aliceBandwidth, float64(aliceBandwidth)/(1024*1024))
	fmt.Printf("  Burst: %d bytes (%.2f KB)\n", burstCapacity, float64(burstCapacity)/1024)
	fmt.Println()

	// Test the FIXED configuration
	limiter := ratelimit.NewBucketWithRate(float64(aliceBandwidth), burstCapacity)

	fmt.Println("=== Test 1: Burst Behavior (Fixed) ===")
	testBurstBehavior(limiter, aliceBandwidth)

	fmt.Println()
	fmt.Println("=== Test 2: Sustained Rate (Fixed) ===")
	testSustainedRate(limiter, aliceBandwidth)

	fmt.Println()
	fmt.Println("=== Test 3: Comparison with Old vs New ===")
	compareConfigurations(aliceBandwidth)
}

func testBurstBehavior(limiter *ratelimit.Bucket, expectedRate int64) {
	// Test sending 5MB in 64KB chunks (like our real test)
	data := strings.NewReader(strings.Repeat("A", int(expectedRate))) // 5MB
	limitedReader := ratelimit.Reader(data, limiter)

	buffer := make([]byte, 64*1024) // 64KB chunks
	totalBytes := 0
	startTime := time.Now()

	fmt.Println("Sending 5MB in 64KB chunks:")
	for i := 0; i < 10; i++ {
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

		fmt.Printf("Chunk %d: %d bytes in %v (total: %d, rate: %.2f MB/s)",
			i+1, n, chunkDuration.Round(time.Millisecond), totalBytes,
			currentRate/(1024*1024))

		if i < 3 || chunkDuration > time.Millisecond*10 {
			if chunkDuration < time.Millisecond*10 {
				fmt.Printf(" [burst]")
			} else {
				fmt.Printf(" [limited]")
			}
		}
		fmt.Println()

		if i == 4 { // Check rate after first few chunks
			break
		}
	}

	finalDuration := time.Since(startTime)
	finalRate := float64(totalBytes) / finalDuration.Seconds()

	fmt.Printf("\nSummary: %d bytes in %v = %.2f MB/s",
		totalBytes, finalDuration.Round(time.Millisecond), finalRate/(1024*1024))

	expectedMBps := float64(expectedRate) / (1024 * 1024)
	if finalRate > float64(expectedRate)*1.05 { // 5% tolerance
		fmt.Printf(" ⚠️  EXCEEDS LIMIT by %.1f%%", 
			(finalRate/float64(expectedRate)-1)*100)
	} else {
		fmt.Printf(" ✓ Within limit (%.1f%% of %.2f MB/s)", 
			(finalRate/float64(expectedRate))*100, expectedMBps)
	}
	fmt.Println()
}

func testSustainedRate(limiter *ratelimit.Bucket, expectedRate int64) {
	// Test sustained throughput
	data := strings.NewReader(strings.Repeat("B", int(expectedRate*3))) // 15MB
	limitedReader := ratelimit.Reader(data, limiter)

	buffer := make([]byte, 64*1024)
	totalBytes := 0
	startTime := time.Now()

	fmt.Println("Sustained transfer (3 seconds):")

	// Read for 3 seconds
	for time.Since(startTime) < 3*time.Second {
		n, err := limitedReader.Read(buffer)
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		totalBytes += n
	}

	actualDuration := time.Since(startTime)
	actualRate := float64(totalBytes) / actualDuration.Seconds()
	expectedMBps := float64(expectedRate) / (1024 * 1024)

	fmt.Printf("Result: %d bytes in %v = %.2f MB/s (expected: %.2f MB/s)",
		totalBytes, actualDuration.Round(time.Millisecond),
		actualRate/(1024*1024), expectedMBps)

	if actualRate > float64(expectedRate)*1.02 { // 2% tolerance
		fmt.Printf(" ⚠️  EXCEEDS LIMIT by %.1f%%", 
			(actualRate/float64(expectedRate)-1)*100)
	} else {
		fmt.Printf(" ✓ Within limit (%.1f%% of target)", 
			(actualRate/float64(expectedRate))*100)
	}
	fmt.Println()
}

func compareConfigurations(baseRate int64) {
	configs := []struct {
		name     string
		capacity int64
		expected string
	}{
		{"OLD (capacity=rate)", baseRate, "BROKEN - allows massive bursts"},
		{"NEW (capacity=rate/10)", baseRate / 10, "FIXED - controlled bursts"},
		{"STRICT (capacity=1KB)", 1024, "STRICT - minimal bursts"},
	}

	for _, config := range configs {
		fmt.Printf("\n%s:\n", config.name)
		fmt.Printf("  Capacity: %d bytes (%.1f KB)\n", config.capacity, float64(config.capacity)/1024)

		limiter := ratelimit.NewBucketWithRate(float64(baseRate), config.capacity)
		data := strings.NewReader(strings.Repeat("C", int(baseRate))) // 5MB
		limitedReader := ratelimit.Reader(data, limiter)

		// Try to read first 1MB quickly
		buffer := make([]byte, 1024*1024) // 1MB
		startTime := time.Now()
		n, err := limitedReader.Read(buffer)
		elapsed := time.Since(startTime)

		if err != nil && err != io.EOF {
			fmt.Printf("  Error: %v\n", err)
			continue
		}

		rate := float64(n) / elapsed.Seconds()
		fmt.Printf("  First 1MB: %v, rate: %.2f MB/s", 
			elapsed.Round(time.Millisecond), rate/(1024*1024))

		if elapsed < time.Millisecond*50 {
			fmt.Printf(" (burst) - %s", config.expected)
		} else {
			fmt.Printf(" (limited) - Good!")
		}
		fmt.Println()
	}
}
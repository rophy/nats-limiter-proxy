package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/juju/ratelimit"
)

// Demonstrate how juju/ratelimit token bucket works
func main() {
	fmt.Println("=== juju/ratelimit Token Bucket Demonstration ===")
	fmt.Println()

	// Create a rate limiter: 10 bytes per second, capacity 10 bytes
	rate := 10.0                    // 10 bytes per second
	capacity := int64(10)           // 10 byte bucket capacity
	bucket := ratelimit.NewBucketWithRate(rate, capacity)

	fmt.Printf("Created bucket: %.1f bytes/sec rate, %d bytes capacity\n", rate, capacity)
	fmt.Println()

	// Demonstrate burst behavior
	fmt.Println("=== Burst Behavior Demo ===")
	
	// Create a large data source
	data := strings.NewReader("This is a long message that exceeds the bucket capacity and should be rate limited")
	
	// Wrap with rate limiter
	limitedReader := ratelimit.Reader(data, bucket)
	
	// Read data and measure timing
	buffer := make([]byte, 5) // Read 5 bytes at a time
	startTime := time.Now()
	totalBytes := 0
	
	fmt.Println("Reading data (5 bytes at a time):")
	for i := 0; i < 8; i++ {
		readStart := time.Now()
		n, err := limitedReader.Read(buffer)
		readDuration := time.Since(readStart)
		
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
		
		fmt.Printf("Read %d: %d bytes in %v (total: %d bytes, rate: %.1f B/s)\n", 
			i+1, n, readDuration.Round(time.Millisecond), totalBytes, currentRate)
	}
	
	totalDuration := time.Since(startTime)
	finalRate := float64(totalBytes) / totalDuration.Seconds()
	
	fmt.Printf("\nSummary:\n")
	fmt.Printf("Total bytes: %d\n", totalBytes)
	fmt.Printf("Total time: %v\n", totalDuration.Round(time.Millisecond))
	fmt.Printf("Average rate: %.1f bytes/sec (limit was %.1f)\n", finalRate, rate)
	
	fmt.Println()
	
	// Demonstrate different bucket configurations
	fmt.Println("=== Different Bucket Configurations ===")
	
	configs := []struct {
		name     string
		rate     float64
		capacity int64
	}{
		{"Small burst", 5.0, 5},      // No burst capacity
		{"Large burst", 5.0, 20},     // 4x burst capacity
		{"High rate", 20.0, 20},      // High rate
	}
	
	for _, config := range configs {
		fmt.Printf("\n%s (%.1f B/s, %d capacity):\n", config.name, config.rate, config.capacity)
		
		testBucket := ratelimit.NewBucketWithRate(config.rate, config.capacity)
		testData := strings.NewReader("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")
		testReader := ratelimit.Reader(testData, testBucket)
		
		start := time.Now()
		testBuffer := make([]byte, 10) // Try to read 10 bytes at once
		
		n, _ := testReader.Read(testBuffer)
		duration := time.Since(start)
		
		fmt.Printf("  Read %d bytes in %v", n, duration.Round(time.Millisecond))
		if n == 10 && duration < time.Millisecond*100 {
			fmt.Printf(" (burst allowed)")
		} else {
			fmt.Printf(" (rate limited)")
		}
		fmt.Println()
	}
	
	fmt.Println()
	
	// Show how this relates to NATS proxy
	fmt.Println("=== NATS Proxy Usage ===")
	fmt.Println("In the NATS proxy, for each user:")
	fmt.Println()
	
	users := map[string]int64{
		"alice":   5242880, // 5MB/s
		"bob":     2097152, // 2MB/s  
		"charlie": 3145728, // 3MB/s
		"diana":   1048576, // 1MB/s
	}
	
	for user, limit := range users {
		mbps := float64(limit) / (1024 * 1024)
		fmt.Printf("%s: ratelimit.NewBucketWithRate(%.0f, %d)\n", user, float64(limit), limit)
		fmt.Printf("  → %.1f MB/s rate, %.1f MB burst capacity\n", mbps, mbps)
	}
	
	fmt.Println()
	fmt.Println("Each client connection gets wrapped with:")
	fmt.Println("  limitedReader := ratelimit.Reader(clientConn, bucket)")
	fmt.Println("This ensures all data from that client is rate limited!")
}
package main

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/juju/ratelimit"
)

func TestRateLimiterCreation(t *testing.T) {
	tests := []struct {
		name             string
		bandwidthLimit   int64
		expectedRate     float64
		expectedCapacity int64
		minCapacity      int64
	}{
		{
			name:             "Alice's configuration (5MB/s)",
			bandwidthLimit:   5 * 1024 * 1024, // 5MB
			expectedRate:     5 * 1024 * 1024,
			expectedCapacity: (5 * 1024 * 1024) / 10, // 512KB
			minCapacity:      1024,
		},
		{
			name:             "Bob's configuration (2MB/s)",
			bandwidthLimit:   2 * 1024 * 1024, // 2MB
			expectedRate:     2 * 1024 * 1024,
			expectedCapacity: (2 * 1024 * 1024) / 10, // 204KB
			minCapacity:      1024,
		},
		{
			name:             "Small bandwidth (5KB/s - should use minimum)",
			bandwidthLimit:   5 * 1024, // 5KB
			expectedRate:     5 * 1024,
			expectedCapacity: 1024, // Uses minimum since 5KB/10 = 512B < 1KB
			minCapacity:      1024,
		},
		{
			name:             "Default bandwidth (10MB/s)",
			bandwidthLimit:   10 * 1024 * 1024, // 10MB
			expectedRate:     10 * 1024 * 1024,
			expectedCapacity: (10 * 1024 * 1024) / 10, // 1MB
			minCapacity:      1024,
		},
		{
			name:             "Very small bandwidth (100B/s - should use minimum)",
			bandwidthLimit:   100,
			expectedRate:     100,
			expectedCapacity: 1024, // Uses minimum since 100/10 = 10B < 1KB
			minCapacity:      1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the rate limiter creation logic from proxy.go
			burstCapacity := tt.bandwidthLimit / 10
			if burstCapacity < tt.minCapacity {
				burstCapacity = tt.minCapacity
			}

			limiter := ratelimit.NewBucketWithRate(float64(tt.bandwidthLimit), burstCapacity)

			// Verify the configuration by testing burst behavior
			if burstCapacity != tt.expectedCapacity {
				t.Errorf("Expected burst capacity %d, got %d", tt.expectedCapacity, burstCapacity)
			}

			// Test that the limiter was created successfully
			if limiter == nil {
				t.Fatalf("Rate limiter creation failed")
			}
		})
	}
}

func TestTokenBucketBurstBehavior(t *testing.T) {
	tests := []struct {
		name           string
		rate           int64
		capacity       int64
		testSize       int
		expectInstant  bool
		description    string
	}{
		{
			name:          "Fixed config - Alice (small burst)",
			rate:          5 * 1024 * 1024, // 5MB/s
			capacity:      512 * 1024,      // 512KB
			testSize:      100 * 1024,      // 100KB
			expectInstant: true,
			description:   "100KB should be instant with 512KB capacity",
		},
		{
			name:          "Fixed config - Alice (medium burst)",
			rate:          5 * 1024 * 1024, // 5MB/s
			capacity:      512 * 1024,      // 512KB
			testSize:      400 * 1024,      // 400KB
			expectInstant: true,
			description:   "400KB should be instant with 512KB capacity",
		},
		{
			name:          "Fixed config - Alice (over capacity)",
			rate:          5 * 1024 * 1024, // 5MB/s
			capacity:      512 * 1024,      // 512KB
			testSize:      1024 * 1024,     // 1MB
			expectInstant: false,
			description:   "1MB should be rate limited with 512KB capacity",
		},
		{
			name:          "Old broken config - Alice (massive burst)",
			rate:          5 * 1024 * 1024, // 5MB/s
			capacity:      5 * 1024 * 1024, // 5MB (broken config)
			testSize:      4 * 1024 * 1024, // 4MB
			expectInstant: true,
			description:   "4MB would be instant with broken 5MB capacity",
		},
		{
			name:          "Minimum capacity enforcement",
			rate:          100,  // 100B/s
			capacity:      1024, // 1KB minimum
			testSize:      500,  // 500B
			expectInstant: true,
			description:   "500B should be instant with 1KB minimum capacity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := ratelimit.NewBucketWithRate(float64(tt.rate), tt.capacity)
			
			// Create test data
			data := strings.NewReader(strings.Repeat("A", tt.testSize))
			limitedReader := ratelimit.Reader(data, limiter)

			// Try to read the data and measure time
			buffer := make([]byte, tt.testSize)
			startTime := time.Now()
			n, err := limitedReader.Read(buffer)
			elapsed := time.Since(startTime)

			if err != nil && err != io.EOF {
				t.Fatalf("Unexpected error: %v", err)
			}

			if n != tt.testSize {
				t.Errorf("Expected to read %d bytes, got %d", tt.testSize, n)
			}

			// Define "instant" as less than 10ms (burst behavior)
			isInstant := elapsed < 10*time.Millisecond

			if tt.expectInstant && !isInstant {
				t.Errorf("%s: Expected instant read but took %v", tt.description, elapsed)
			} else if !tt.expectInstant && isInstant {
				t.Errorf("%s: Expected rate limiting but was instant (%v)", tt.description, elapsed)
			}

			t.Logf("%s: Read %d bytes in %v (instant: %v)", tt.description, n, elapsed, isInstant)
		})
	}
}

func TestRateLimiterSustainedRate(t *testing.T) {
	tests := []struct {
		name           string
		rate           int64
		capacity       int64
		testDuration   time.Duration
		tolerancePercent float64
	}{
		{
			name:             "Alice sustained rate (fixed config)",
			rate:             5 * 1024 * 1024, // 5MB/s
			capacity:         512 * 1024,      // 512KB
			testDuration:     2 * time.Second,
			tolerancePercent: 6.0, // 6% tolerance for rate limiter accuracy
		},
		{
			name:             "Bob sustained rate (fixed config)",
			rate:             2 * 1024 * 1024, // 2MB/s
			capacity:         204 * 1024,      // ~204KB
			testDuration:     2 * time.Second,
			tolerancePercent: 6.0,
		},
		{
			name:             "Small rate with minimum capacity",
			rate:             10 * 1024, // 10KB/s
			capacity:         1024,      // 1KB minimum
			testDuration:     3 * time.Second,
			tolerancePercent: 10.0, // Higher tolerance for small rates
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := ratelimit.NewBucketWithRate(float64(tt.rate), tt.capacity)
			
			// Create large enough data for sustained test
			dataSize := int(tt.rate * int64(tt.testDuration.Seconds()) * 2) // 2x expected
			data := strings.NewReader(strings.Repeat("B", dataSize))
			limitedReader := ratelimit.Reader(data, limiter)

			// Read for the test duration
			buffer := make([]byte, 4096)
			totalBytes := 0
			startTime := time.Now()

			for time.Since(startTime) < tt.testDuration {
				n, err := limitedReader.Read(buffer)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}
				totalBytes += n
			}

			actualDuration := time.Since(startTime)
			actualRate := float64(totalBytes) / actualDuration.Seconds()
			expectedRate := float64(tt.rate)

			// Calculate percentage difference
			percentDiff := ((actualRate - expectedRate) / expectedRate) * 100

			t.Logf("Expected rate: %.2f B/s, Actual rate: %.2f B/s, Diff: %.1f%%", 
				expectedRate, actualRate, percentDiff)

			if percentDiff > tt.tolerancePercent {
				t.Errorf("Rate exceeded tolerance: expected within %.1f%%, got %.1f%% over", 
					tt.tolerancePercent, percentDiff)
			} else if percentDiff < -tt.tolerancePercent {
				t.Errorf("Rate too slow: expected within %.1f%%, got %.1f%% under", 
					tt.tolerancePercent, -percentDiff)
			}
		})
	}
}

func TestBurstCapacityCalculation(t *testing.T) {
	tests := []struct {
		name           string
		bandwidth      int64
		expectedBurst  int64
	}{
		{
			name:          "Normal bandwidth (5MB)",
			bandwidth:     5 * 1024 * 1024,
			expectedBurst: 524288, // 512KB
		},
		{
			name:          "Small bandwidth uses minimum",
			bandwidth:     5 * 1024, // 5KB
			expectedBurst: 1024,     // 1KB minimum
		},
		{
			name:          "Very small bandwidth uses minimum",
			bandwidth:     500, // 500B
			expectedBurst: 1024, // 1KB minimum
		},
		{
			name:          "Large bandwidth",
			bandwidth:     100 * 1024 * 1024, // 100MB
			expectedBurst: 10485760,          // 10MB
		},
		{
			name:          "Edge case: exactly 10KB",
			bandwidth:     10 * 1024,
			expectedBurst: 1024, // 1KB (10KB/10 = 1KB, equals minimum)
		},
		{
			name:          "Edge case: just over minimum threshold",
			bandwidth:     11 * 1024, // 11KB
			expectedBurst: 1126,      // 11KB/10 = 1.1KB > 1KB minimum
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use the same calculation logic as in proxy.go
			burstCapacity := tt.bandwidth / 10
			if burstCapacity < 1024 {
				burstCapacity = 1024
			}

			if burstCapacity != tt.expectedBurst {
				t.Errorf("Expected burst capacity %d, got %d", tt.expectedBurst, burstCapacity)
			}
		})
	}
}

func TestRateLimiterEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		rate        int64
		capacity    int64
		shouldPanic bool
	}{
		{
			name:        "Zero rate",
			rate:        0,
			capacity:    1024,
			shouldPanic: true, // juju/ratelimit panics on zero rate
		},
		{
			name:        "Zero capacity",
			rate:        1024,
			capacity:    0,
			shouldPanic: true, // juju/ratelimit panics on zero capacity
		},
		{
			name:        "Negative rate",
			rate:        -1024,
			capacity:    1024,
			shouldPanic: true, // juju/ratelimit panics on negative rate
		},
		{
			name:        "Very large rate",
			rate:        1024 * 1024 * 1024 * 1024, // 1TB/s
			capacity:    1024 * 1024 * 1024,       // 1GB capacity
			shouldPanic: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					if !tt.shouldPanic {
						t.Errorf("Unexpected panic: %v", r)
					}
				} else if tt.shouldPanic {
					t.Errorf("Expected panic but none occurred")
				}
			}()

			limiter := ratelimit.NewBucketWithRate(float64(tt.rate), tt.capacity)
			
			// Try basic operation
			if limiter != nil {
				data := strings.NewReader("test")
				limitedReader := ratelimit.Reader(data, limiter)
				buffer := make([]byte, 4)
				_, err := limitedReader.Read(buffer)
				
				// Error is acceptable for edge cases
				if err != nil && err != io.EOF {
					t.Logf("Read error for edge case (expected): %v", err)
				}
			}
		})
	}
}
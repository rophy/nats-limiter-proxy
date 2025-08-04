package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"nats-limiter-proxy/e2e/testutil"
)

// TestE2E_ServiceLatencyBasic tests that basic service request/response patterns work through proxy
// with preserved latency characteristics
func TestE2E_ServiceLatencyBasic(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	// Connect to proxy
	nc := env.ConnectToProxy(t)
	defer nc.Close()

	// Create a simple service responder
	serviceSub, err := nc.Subscribe("test.service", func(msg *nats.Msg) {
		// Simple echo service with small processing delay
		time.Sleep(10 * time.Millisecond)
		response := fmt.Sprintf("Echo: %s", string(msg.Data))
		msg.Respond([]byte(response))
	})
	if err != nil {
		t.Fatalf("Failed to create service subscriber: %v", err)
	}
	defer serviceSub.Unsubscribe()

	// Flush to ensure subscription is active
	nc.Flush()
	time.Sleep(100 * time.Millisecond)

	// Test service request with latency measurement
	start := time.Now()
	msg, err := nc.Request("test.service", []byte("hello world"), 5*time.Second)
	latency := time.Since(start)

	if err != nil {
		t.Fatalf("Service request failed: %v", err)
	}

	if !strings.Contains(string(msg.Data), "Echo: hello world") {
		t.Errorf("Unexpected service response: %s", string(msg.Data))
	}

	// Verify reasonable latency (should be > 10ms due to service delay, but < 1s)
	if latency < 10*time.Millisecond {
		t.Errorf("Latency too low, service delay not preserved: %v", latency)
	}
	if latency > 1*time.Second {
		t.Errorf("Latency too high, proxy may be adding excessive overhead: %v", latency)
	}

	t.Logf("✓ Service request/response completed with latency: %v", latency)
}

// TestE2E_ServiceLatencyComparison compares latency through proxy vs direct connection
func TestE2E_ServiceLatencyComparison(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	// Test through proxy
	proxyLatency, err := measureServiceLatencyWithEnv(env, true)
	if err != nil {
		t.Fatalf("Failed to measure proxy latency: %v", err)
	}

	// Test direct connection
	directLatency, err := measureServiceLatencyWithEnv(env, false)
	if err != nil {
		t.Fatalf("Failed to measure direct latency: %v", err)
	}

	// Proxy should not add more than 50ms overhead
	overhead := proxyLatency - directLatency
	if overhead > 50*time.Millisecond {
		t.Errorf("Proxy adds excessive latency overhead: proxy=%v, direct=%v, overhead=%v", 
			proxyLatency, directLatency, overhead)
	}

	t.Logf("✓ Latency comparison - Direct: %v, Proxy: %v, Overhead: %v", 
		directLatency, proxyLatency, overhead)
}

// TestE2E_ServiceErrorHandling tests that service errors are properly handled through proxy
func TestE2E_ServiceErrorHandling(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	nc := env.ConnectToProxy(t)
	defer nc.Close()

	// Test 1: No responders (should timeout)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := nc.RequestWithContext(ctx, "test.nonexistent", []byte("hello"))
	elapsed := time.Since(start)

	// NATS returns "no responders" error instead of timeout when there are no subscribers
	if err == nil {
		t.Error("Expected error for non-existent service, got nil")
	} else if err != nats.ErrTimeout && err != nats.ErrNoResponders {
		t.Errorf("Expected timeout or no responders error, got: %v", err)
	}

	// Should complete quickly since there are no responders, or timeout around 500ms
	if err == nats.ErrNoResponders {
		// No responders should be fast
		if elapsed > 100*time.Millisecond {
			t.Errorf("No responders error took too long: %v", elapsed)
		}
	} else if err == nats.ErrTimeout {
		// Timeout should be around 500ms
		if elapsed < 400*time.Millisecond || elapsed > 600*time.Millisecond {
			t.Errorf("Unexpected timeout duration: %v", elapsed)
		}
	}

	// Test 2: Service that returns error
	errorSub, err := nc.Subscribe("test.error", func(msg *nats.Msg) {
		msg.Respond([]byte("ERROR: Service unavailable"))
	})
	if err != nil {
		t.Fatalf("Failed to create error service: %v", err)
	}
	defer errorSub.Unsubscribe()

	nc.Flush()
	time.Sleep(100 * time.Millisecond)

	msg, err := nc.Request("test.error", []byte("test"), 2*time.Second)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	if !strings.Contains(string(msg.Data), "ERROR:") {
		t.Errorf("Error response not preserved: %s", string(msg.Data))
	}

	t.Logf("✓ Service error handling preserved through proxy")
}

// TestE2E_ServiceLatencyConcurrent tests concurrent service requests for latency consistency
func TestE2E_ServiceLatencyConcurrent(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	nc := env.ConnectToProxy(t)
	defer nc.Close()

	// Create service responder
	serviceSub, err := nc.Subscribe("test.concurrent", func(msg *nats.Msg) {
		// Variable processing delay to simulate real service
		delay := time.Duration(5+len(msg.Data)%20) * time.Millisecond
		time.Sleep(delay)
		msg.Respond([]byte(fmt.Sprintf("Processed: %s", string(msg.Data))))
	})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer serviceSub.Unsubscribe()

	nc.Flush()
	time.Sleep(100 * time.Millisecond)

	// Run concurrent requests
	const numRequests = 10
	latencies := make([]time.Duration, numRequests)
	errors := make([]error, numRequests)

	done := make(chan int, numRequests)
	
	for i := 0; i < numRequests; i++ {
		go func(requestID int) {
			defer func() { done <- requestID }()
			
			start := time.Now()
			msg, err := nc.Request("test.concurrent", 
				[]byte(fmt.Sprintf("request-%d", requestID)), 3*time.Second)
			latencies[requestID] = time.Since(start)
			errors[requestID] = err
			
			if err == nil && !strings.Contains(string(msg.Data), fmt.Sprintf("request-%d", requestID)) {
				errors[requestID] = fmt.Errorf("incorrect response: %s", string(msg.Data))
			}
		}(i)
	}

	// Wait for all requests to complete
	for i := 0; i < numRequests; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Concurrent requests timed out")
		}
	}

	// Analyze results
	var totalLatency time.Duration
	var successCount int
	var maxLatency, minLatency time.Duration

	for i, err := range errors {
		if err != nil {
			t.Errorf("Request %d failed: %v", i, err)
			continue
		}
		
		successCount++
		latency := latencies[i]
		totalLatency += latency
		
		if maxLatency == 0 || latency > maxLatency {
			maxLatency = latency
		}
		if minLatency == 0 || latency < minLatency {
			minLatency = latency
		}
	}

	if successCount == 0 {
		t.Fatal("No successful concurrent requests")
	}

	avgLatency := totalLatency / time.Duration(successCount)

	// Verify reasonable latency characteristics
	if avgLatency > 200*time.Millisecond {
		t.Errorf("Average latency too high: %v", avgLatency)
	}
	if maxLatency > 500*time.Millisecond {
		t.Errorf("Max latency too high: %v", maxLatency)
	}

	t.Logf("✓ Concurrent service requests - Success: %d/%d, Avg: %v, Min: %v, Max: %v", 
		successCount, numRequests, avgLatency, minLatency, maxLatency)
}

// TestE2E_ServiceLatencyWithHeaders tests that request headers are preserved through proxy
func TestE2E_ServiceLatencyWithHeaders(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	nc := env.ConnectToProxy(t)
	defer nc.Close()

	// Create service that checks headers
	serviceSub, err := nc.Subscribe("test.headers", func(msg *nats.Msg) {
		response := "Headers received: "
		if msg.Header != nil {
			for key, values := range msg.Header {
				response += fmt.Sprintf("%s=%s; ", key, strings.Join(values, ","))
			}
		} else {
			response += "none"
		}
		
		// Echo back with headers
		reply := nats.NewMsg(msg.Reply)
		reply.Data = []byte(response)
		reply.Header = nats.Header{}
		reply.Header.Set("Service-Response", "processed")
		reply.Header.Set("Processing-Time", "15ms")
		
		nc.PublishMsg(reply)
	})
	if err != nil {
		t.Fatalf("Failed to create header service: %v", err)
	}
	defer serviceSub.Unsubscribe()

	nc.Flush()
	time.Sleep(100 * time.Millisecond)

	// Create request with headers
	req := nats.NewMsg("test.headers")
	req.Data = []byte("test request")
	req.Header = nats.Header{}
	req.Header.Set("Client-ID", "test-client")
	req.Header.Set("Request-ID", "12345")
	req.Header.Set("Trace-ID", "abc-def-ghi")

	start := time.Now()
	msg, err := nc.RequestMsg(req, 3*time.Second)
	latency := time.Since(start)

	if err != nil {
		t.Fatalf("Header request failed: %v", err)
	}

	// Verify request headers were received
	response := string(msg.Data)
	if !strings.Contains(response, "Client-ID=test-client") {
		t.Errorf("Client-ID header not preserved: %s", response)
	}
	if !strings.Contains(response, "Request-ID=12345") {
		t.Errorf("Request-ID header not preserved: %s", response)
	}
	if !strings.Contains(response, "Trace-ID=abc-def-ghi") {
		t.Errorf("Trace-ID header not preserved: %s", response)
	}

	// Verify response headers
	if msg.Header == nil {
		t.Error("Response headers not preserved")
	} else {
		if msg.Header.Get("Service-Response") != "processed" {
			t.Error("Service-Response header not preserved")
		}
		if msg.Header.Get("Processing-Time") != "15ms" {
			t.Error("Processing-Time header not preserved")
		}
	}

	t.Logf("✓ Service headers preserved through proxy (latency: %v)", latency)
}

// Helper function to measure service latency using test environment
func measureServiceLatencyWithEnv(env *testutil.DockerComposeEnv, useProxy bool) (time.Duration, error) {
	var nc *nats.Conn
	var err error
	
	if useProxy {
		nc, err = nats.Connect(env.ProxyURL, 
			nats.UserCredentials(testutil.GetAliceCredentials()),
			nats.Timeout(10*time.Second),
		)
	} else {
		nc, err = nats.Connect(env.NATSDirectURL, 
			nats.UserCredentials(testutil.GetAliceCredentials()),
			nats.Timeout(10*time.Second),
		)
	}
	
	if err != nil {
		return 0, err
	}
	defer nc.Close()

	// Create temporary service
	sub, err := nc.Subscribe("test.latency.measure", func(msg *nats.Msg) {
		time.Sleep(5 * time.Millisecond) // Consistent small delay
		msg.Respond([]byte("measured"))
	})
	if err != nil {
		return 0, err
	}
	defer sub.Unsubscribe()

	nc.Flush()
	time.Sleep(50 * time.Millisecond)

	// Measure 5 requests and take average
	var totalLatency time.Duration
	const measurements = 5

	for i := 0; i < measurements; i++ {
		start := time.Now()
		_, err := nc.Request("test.latency.measure", []byte("test"), 2*time.Second)
		if err != nil {
			return 0, err
		}
		totalLatency += time.Since(start)
		
		// Small delay between measurements
		time.Sleep(10 * time.Millisecond)
	}

	return totalLatency / measurements, nil
}
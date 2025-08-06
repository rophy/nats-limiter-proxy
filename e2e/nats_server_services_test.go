package e2e

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"nats-limiter-proxy/e2e/testutil"
)

// TestE2E_ServicesBasicPubSub tests basic service request/response patterns through proxy
func TestE2E_ServicesBasicPubSub(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	// Connect to proxy
	nc := env.ConnectToProxy(t)
	defer nc.Close()

	// Create a basic service
	serviceName := "calculator"
	serviceSubject := fmt.Sprintf("services.%s", serviceName)
	
	serviceSub, err := nc.Subscribe(serviceSubject, func(msg *nats.Msg) {
		// Simple calculator service
		operation := string(msg.Data)
		var result string
		
		switch operation {
		case "add 2 3":
			result = "5"
		case "multiply 4 5":
			result = "20"
		case "subtract 10 3":
			result = "7"
		default:
			result = "error: unknown operation"
		}
		
		msg.Respond([]byte(result))
	})
	if err != nil {
		t.Fatalf("Failed to create service subscriber: %v", err)
	}
	defer serviceSub.Unsubscribe()

	// Flush to ensure subscription is active
	nc.Flush()
	time.Sleep(100 * time.Millisecond)

	// Test multiple service requests
	testCases := []struct {
		operation string
		expected  string
	}{
		{"add 2 3", "5"},
		{"multiply 4 5", "20"},
		{"subtract 10 3", "7"},
		{"divide 8 2", "error: unknown operation"},
	}

	for _, tc := range testCases {
		msg, err := nc.Request(serviceSubject, []byte(tc.operation), 3*time.Second)
		if err != nil {
			t.Fatalf("Service request failed for %s: %v", tc.operation, err)
		}

		result := string(msg.Data)
		if result != tc.expected {
			t.Errorf("Service response mismatch for %s: expected %s, got %s", 
				tc.operation, tc.expected, result)
		}
	}

	t.Logf("✓ Basic service request/response patterns work through proxy")
}

// TestE2E_ServicesMultipleEndpoints tests multiple service endpoints through proxy
func TestE2E_ServicesMultipleEndpoints(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	// Connect to proxy
	nc := env.ConnectToProxy(t)
	defer nc.Close()

	// Create multiple services
	services := map[string]func(string) string{
		"services.echo": func(input string) string {
			return fmt.Sprintf("echo: %s", input)
		},
		"services.uppercase": func(input string) string {
			return strings.ToUpper(input)
		},
		"services.reverse": func(input string) string {
			runes := []rune(input)
			for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
				runes[i], runes[j] = runes[j], runes[i]
			}
			return string(runes)
		},
		"services.length": func(input string) string {
			return fmt.Sprintf("%d", len(input))
		},
	}

	// Subscribe to each service
	var subscriptions []*nats.Subscription
	for subject, handler := range services {
		serviceFn := handler // Capture for closure
		sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
			result := serviceFn(string(msg.Data))
			msg.Respond([]byte(result))
		})
		if err != nil {
			t.Fatalf("Failed to create service %s: %v", subject, err)
		}
		subscriptions = append(subscriptions, sub)
	}
	
	// Cleanup subscriptions
	defer func() {
		for _, sub := range subscriptions {
			sub.Unsubscribe()
		}
	}()

	// Flush to ensure all subscriptions are active
	nc.Flush()
	time.Sleep(200 * time.Millisecond)

	// Test each service
	testInput := "hello world"
	expectedResults := map[string]string{
		"services.echo":      "echo: hello world",
		"services.uppercase": "HELLO WORLD",
		"services.reverse":   "dlrow olleh",
		"services.length":    "11",
	}

	for subject, expected := range expectedResults {
		msg, err := nc.Request(subject, []byte(testInput), 3*time.Second)
		if err != nil {
			t.Fatalf("Service request failed for %s: %v", subject, err)
		}

		result := string(msg.Data)
		if result != expected {
			t.Errorf("Service %s result mismatch: expected %s, got %s", 
				subject, expected, result)
		}
	}

	t.Logf("✓ Multiple service endpoints work correctly through proxy")
}

// TestE2E_ServicesStreamingResponse tests services that send multiple response chunks
func TestE2E_ServicesStreamingResponse(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	// Connect to proxy
	nc := env.ConnectToProxy(t)
	defer nc.Close()

	// Create streaming service that sends multiple responses
	streamSubject := "services.streaming"
	serviceSub, err := nc.Subscribe(streamSubject, func(msg *nats.Msg) {
		// Send multiple response chunks
		chunks := []string{
			"chunk-1: starting process",
			"chunk-2: processing data",
			"chunk-3: calculating results",
			"chunk-4: finalizing output",
			"chunk-5: process complete",
		}
		
		for i, chunk := range chunks {
			// Create response message
			response := fmt.Sprintf("%s (part %d/%d)", chunk, i+1, len(chunks))
			
			if i == len(chunks)-1 {
				// Last chunk - use Respond for final message
				msg.Respond([]byte(response))
			} else {
				// Intermediate chunks - publish to reply subject
				if msg.Reply != "" {
					nc.Publish(msg.Reply, []byte(response))
				}
			}
			
			// Small delay between chunks to simulate processing
			time.Sleep(10 * time.Millisecond)
		}
	})
	if err != nil {
		t.Fatalf("Failed to create streaming service: %v", err)
	}
	defer serviceSub.Unsubscribe()

	// Create subscriber to collect all response chunks
	replySubject := nats.NewInbox()
	var receivedChunks []string
	var mu sync.Mutex
	
	replySub, err := nc.Subscribe(replySubject, func(msg *nats.Msg) {
		mu.Lock()
		receivedChunks = append(receivedChunks, string(msg.Data))
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("Failed to create reply subscriber: %v", err)
	}
	defer replySub.Unsubscribe()

	// Flush to ensure subscriptions are active
	nc.Flush()
	time.Sleep(100 * time.Millisecond)

	// Send request with custom reply subject
	requestMsg := nats.NewMsg(streamSubject)
	requestMsg.Data = []byte("stream-request")
	requestMsg.Reply = replySubject
	
	err = nc.PublishMsg(requestMsg)
	if err != nil {
		t.Fatalf("Failed to send streaming request: %v", err)
	}

	// Wait for all chunks to arrive
	time.Sleep(500 * time.Millisecond)

	// Verify we received all chunks
	mu.Lock()
	numChunks := len(receivedChunks)
	mu.Unlock()

	if numChunks != 5 {
		t.Errorf("Expected 5 response chunks, got %d", numChunks)
	}

	// Verify chunk content
	mu.Lock()
	for i, chunk := range receivedChunks {
		expectedPattern := fmt.Sprintf("chunk-%d:", i+1)
		if !strings.Contains(chunk, expectedPattern) {
			t.Errorf("Chunk %d doesn't contain expected pattern %s: %s", 
				i, expectedPattern, chunk)
		}
	}
	mu.Unlock()

	t.Logf("✓ Streaming service responses work correctly through proxy (%d chunks)", numChunks)
}

// TestE2E_ServicesErrorHandling tests service error scenarios through proxy
func TestE2E_ServicesErrorHandling(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	// Connect to proxy
	nc := env.ConnectToProxy(t)
	defer nc.Close()

	// Test 1: Service that sometimes fails
	errorService := "services.unreliable"
	var requestCount int
	var mu sync.Mutex
	
	serviceSub, err := nc.Subscribe(errorService, func(msg *nats.Msg) {
		mu.Lock()
		requestCount++
		count := requestCount
		mu.Unlock()
		
		// Fail every 3rd request
		if count%3 == 0 {
			msg.Respond([]byte("ERROR: Service temporarily unavailable"))
		} else {
			msg.Respond([]byte(fmt.Sprintf("Success: Request %d processed", count)))
		}
	})
	if err != nil {
		t.Fatalf("Failed to create unreliable service: %v", err)
	}
	defer serviceSub.Unsubscribe()

	// Flush to ensure subscription is active
	nc.Flush()
	time.Sleep(100 * time.Millisecond)

	// Send multiple requests and track success/failure
	var successCount, errorCount int
	
	for i := 1; i <= 6; i++ {
		msg, err := nc.Request(errorService, []byte(fmt.Sprintf("request-%d", i)), 3*time.Second)
		if err != nil {
			t.Errorf("Request %d failed with connection error: %v", i, err)
			continue
		}
		
		response := string(msg.Data)
		if strings.Contains(response, "ERROR:") {
			errorCount++
		} else if strings.Contains(response, "Success:") {
			successCount++
		} else {
			t.Errorf("Unexpected response format: %s", response)
		}
	}

	// Verify we got expected mix of success/error responses
	if successCount != 4 {
		t.Errorf("Expected 4 successful responses, got %d", successCount)
	}
	if errorCount != 2 {
		t.Errorf("Expected 2 error responses, got %d", errorCount)
	}

	t.Logf("✓ Service error handling preserved through proxy (success: %d, errors: %d)", 
		successCount, errorCount)
}

// TestE2E_ServicesConcurrentClients tests multiple clients using services simultaneously
func TestE2E_ServicesConcurrentClients(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	// Connect to proxy for service
	serviceNC := env.ConnectToProxy(t)
	defer serviceNC.Close()

	// Create a counter service
	counterService := "services.counter"
	var counter int
	var counterMu sync.Mutex
	
	serviceSub, err := serviceNC.Subscribe(counterService, func(msg *nats.Msg) {
		counterMu.Lock()
		counter++
		currentCount := counter
		counterMu.Unlock()
		
		// Small processing delay to simulate real work
		time.Sleep(5 * time.Millisecond)
		
		msg.Respond([]byte(fmt.Sprintf("count: %d", currentCount)))
	})
	if err != nil {
		t.Fatalf("Failed to create counter service: %v", err)
	}
	defer serviceSub.Unsubscribe()

	// Flush to ensure subscription is active
	serviceNC.Flush()
	time.Sleep(100 * time.Millisecond)

	// Create multiple client connections
	const numClients = 5
	const requestsPerClient = 10
	
	var wg sync.WaitGroup
	results := make([][]string, numClients)
	errors := make([][]error, numClients)
	
	for clientID := 0; clientID < numClients; clientID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			// Each client gets its own connection
			clientNC := env.ConnectToProxy(t)
			defer clientNC.Close()
			
			clientResults := make([]string, requestsPerClient)
			clientErrors := make([]error, requestsPerClient)
			
			for reqID := 0; reqID < requestsPerClient; reqID++ {
				msg, err := clientNC.Request(counterService, 
					[]byte(fmt.Sprintf("client-%d-request-%d", id, reqID)), 
					3*time.Second)
				
				if err != nil {
					clientErrors[reqID] = err
				} else {
					clientResults[reqID] = string(msg.Data)
				}
				
				// Small delay between requests
				time.Sleep(2 * time.Millisecond)
			}
			
			results[id] = clientResults
			errors[id] = clientErrors
		}(clientID)
	}

	// Wait for all clients to complete
	wg.Wait()

	// Analyze results
	totalRequests := numClients * requestsPerClient
	successfulRequests := 0
	uniqueCounts := make(map[string]bool)
	
	for clientID := 0; clientID < numClients; clientID++ {
		for reqID := 0; reqID < requestsPerClient; reqID++ {
			if errors[clientID][reqID] != nil {
				t.Errorf("Client %d request %d failed: %v", 
					clientID, reqID, errors[clientID][reqID])
			} else {
				successfulRequests++
				response := results[clientID][reqID]
				uniqueCounts[response] = true
				
				// Verify response format
				if !strings.HasPrefix(response, "count: ") {
					t.Errorf("Invalid response format from client %d request %d: %s", 
						clientID, reqID, response)
				}
			}
		}
	}

	// Verify all requests succeeded
	if successfulRequests != totalRequests {
		t.Errorf("Expected %d successful requests, got %d", totalRequests, successfulRequests)
	}

	// Verify we got unique counter values (no race conditions)
	if len(uniqueCounts) != totalRequests {
		t.Errorf("Expected %d unique counter values, got %d (possible race condition)", 
			totalRequests, len(uniqueCounts))
	}

	// Verify final counter value
	counterMu.Lock()
	finalCount := counter
	counterMu.Unlock()
	
	if finalCount != totalRequests {
		t.Errorf("Expected final counter to be %d, got %d", totalRequests, finalCount)
	}

	t.Logf("✓ Concurrent service clients work correctly through proxy")
	t.Logf("  - Clients: %d, Requests per client: %d, Total: %d", 
		numClients, requestsPerClient, totalRequests)
	t.Logf("  - Successful requests: %d, Unique responses: %d", 
		successfulRequests, len(uniqueCounts))
}

// TestE2E_ServicesLifecycleManagement tests service lifecycle through proxy
func TestE2E_ServicesLifecycleManagement(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	// Connect to proxy
	nc := env.ConnectToProxy(t)
	defer nc.Close()

	// Test service startup and shutdown
	serviceSubject := "services.lifecycle"
	
	// Phase 1: No service available - should timeout/fail
	_, err := nc.Request(serviceSubject, []byte("test"), 500*time.Millisecond)
	if err == nil {
		t.Error("Expected error when no service is available")
	}

	// Phase 2: Start service
	serviceSub, err := nc.Subscribe(serviceSubject, func(msg *nats.Msg) {
		msg.Respond([]byte("service is running"))
	})
	if err != nil {
		t.Fatalf("Failed to create lifecycle service: %v", err)
	}

	// Flush and wait for service to be ready
	nc.Flush()
	time.Sleep(100 * time.Millisecond)

	// Service should now respond
	msg, err := nc.Request(serviceSubject, []byte("test"), 2*time.Second)
	if err != nil {
		t.Fatalf("Service request failed after startup: %v", err)
	}
	if string(msg.Data) != "service is running" {
		t.Errorf("Unexpected service response: %s", string(msg.Data))
	}

	// Phase 3: Stop service
	serviceSub.Unsubscribe()
	time.Sleep(100 * time.Millisecond)

	// Service should no longer respond
	_, err = nc.Request(serviceSubject, []byte("test"), 500*time.Millisecond)
	if err == nil {
		t.Error("Expected error after service shutdown")
	}

	// Phase 4: Restart service with different behavior
	serviceSub2, err := nc.Subscribe(serviceSubject, func(msg *nats.Msg) {
		msg.Respond([]byte("service restarted"))
	})
	if err != nil {
		t.Fatalf("Failed to restart lifecycle service: %v", err)
	}
	defer serviceSub2.Unsubscribe()

	// Flush and wait for service to be ready
	nc.Flush()
	time.Sleep(100 * time.Millisecond)

	// Service should respond with new behavior
	msg, err = nc.Request(serviceSubject, []byte("test"), 2*time.Second)
	if err != nil {
		t.Fatalf("Service request failed after restart: %v", err)
	}
	if string(msg.Data) != "service restarted" {
		t.Errorf("Unexpected service response after restart: %s", string(msg.Data))
	}

	t.Logf("✓ Service lifecycle management works correctly through proxy")
}
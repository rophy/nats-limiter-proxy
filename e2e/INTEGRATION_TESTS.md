# E2E Tests

This directory contains comprehensive end-to-end tests including tests adapted from the [NATS server test suite](https://github.com/nats-io/nats-server/tree/main/test). These tests validate that the proxy correctly handles the NATS protocol and maintains compatibility with NATS client expectations.

## Test Structure

### Core E2E Test Files

- **`e2e_basic_test.go`** - Original basic proxy functionality tests
- **`e2e_jetstream_test.go`** - Original JetStream-specific tests

### NATS Server-Adapted Test Files

- **`nats_server_auth_test.go`** - Authentication and authorization testing
- **`nats_server_proto_test.go`** - NATS protocol compliance testing  
- **`nats_server_cluster_test.go`** - Proxy resilience and load distribution
- **`nats_server_bench_test.go`** - Performance benchmarking and comparison

## Running Tests

### All Tests
```bash
make test-e2e          # Run all e2e tests
```

### Benchmark Tests
```bash
make test-bench        # Run performance benchmarks
```

### Individual Test Categories
```bash
# Inside nats-box container
/tmp/e2e.test -test.v -test.run "TestE2E_Auth"
/tmp/e2e.test -test.v -test.run "TestE2E_Protocol" 
/tmp/e2e.test -test.v -test.run "TestE2E_.*Resilience"
/tmp/e2e.test -test.v -test.run "TestE2E_.*Performance"
```

## Test Categories

### 1. Authentication Tests (`nats_server_auth_test.go`)

**Purpose**: Validate authentication mechanisms work correctly through the proxy.

**Key Tests**:
- `TestE2E_AuthRequirement` - Ensures proxy enforces authentication
- `TestE2E_MultiUserAuth` - Tests multiple user credentials (Alice, Bob)
- `TestE2E_AuthenticationPersistence` - Validates auth across reconnections

**Adapted From**: NATS server `auth_test.go`, `client_auth_test.go`

### 2. Protocol Compliance Tests (`nats_server_proto_test.go`)

**Purpose**: Ensure proxy correctly handles NATS protocol messages and semantics.

**Key Tests**:
- `TestE2E_ProtocolCompliance` - PING/PONG, server info, flush mechanisms
- `TestE2E_SubscriptionManagement` - Multiple subscriptions, queue groups
- `TestE2E_MessageSizes` - Various payload sizes (empty to 1MB)
- `TestE2E_HighVolumeMessaging` - High-throughput message handling

**Adapted From**: NATS server `proto_test.go`, `routes_test.go`

### 3. Resilience & Clustering Tests (`nats_server_cluster_test.go`)

**Purpose**: Test proxy behavior under failure conditions and load distribution.

**Key Tests**:
- `TestE2E_ProxyResilience` - Connection stability and NATS server reconnection
- `TestE2E_LoadDistribution` - Multiple publishers/subscribers patterns
- `TestE2E_ErrorHandling` - Invalid subjects, connection limits, graceful disconnection

**Adapted From**: NATS server `cluster_test.go`, connection handling patterns

### 4. Performance & Benchmarks (`nats_server_bench_test.go`)

**Purpose**: Measure and compare proxy performance against direct NATS connections.

**Key Tests**:
- `BenchmarkE2E_ProxyThroughput` - Message throughput benchmarking
- `BenchmarkE2E_ProxyLatency` - Round-trip latency measurement
- `TestE2E_PerformanceComparison` - Direct vs proxy performance comparison
- `TestE2E_ConcurrentLoadTesting` - Concurrent publishers/subscribers load testing

**Adapted From**: NATS server `bench_test.go`

## Test Patterns

### Authentication Testing Pattern
```go
// Test unauthenticated connection fails
_, err := nats.Connect(env.ProxyURL, nats.Timeout(5*time.Second))
if err == nil {
    t.Fatal("Expected connection to fail without credentials")
}

// Test authenticated connection works
nc := env.ConnectToProxyWithAuth(t, testutil.GetAliceCredentials())
// ... perform operations
```

### Protocol Compliance Pattern
```go
// Test PING/PONG mechanism
rtt, err := nc.RTT()
if err != nil {
    t.Fatalf("Failed to get RTT through proxy: %v", err)
}

// Test message integrity
if !bytes.Equal(received, original) {
    t.Error("Message content corrupted by proxy")
}
```

### Performance Testing Pattern
```go
// Measure throughput
start := time.Now()
for i := 0; i < messageCount; i++ {
    nc.Publish(subject, payload)
}
nc.Flush()
duration := time.Since(start)
throughput := float64(totalBytes) / duration.Seconds()
```

## Integration Test Features

### Comprehensive Message Validation
- **Byte-level integrity**: Every byte of transmitted messages is verified
- **Size validation**: Ensures message sizes are preserved exactly
- **Pattern testing**: Tests various data patterns (zeros, ones, random, binary)

### Concurrent Testing
- **Multiple publishers**: Tests proxy handling of concurrent publishers
- **Multiple subscribers**: Validates message broadcast to multiple consumers
- **Queue groups**: Tests load balancing across queue subscribers

### Performance Metrics
- **Throughput measurement**: MB/s and messages/s calculations
- **Latency testing**: Round-trip time measurement
- **Comparison baselines**: Direct NATS vs proxy performance comparison

### Error Scenario Coverage
- **Authentication failures**: Invalid credentials, missing auth
- **Connection limits**: Multiple simultaneous connections
- **Protocol edge cases**: Invalid subjects, malformed messages
- **Graceful degradation**: Connection cleanup and recovery

## Environment Requirements

These tests require:
- Docker Compose environment (`make docker-up`)
- NATS server with authentication configured
- Proxy running and accessible
- Test credentials available (Alice, Bob users)

## Maintenance Notes

### Adding New Tests
1. Follow existing naming pattern: `TestE2E_<Category><TestName>`
2. Use `testutil.NewDockerComposeEnv()` for environment setup
3. Include comprehensive error checking and cleanup
4. Add performance logging where appropriate

### Test Categories
- Use `t.Run()` for sub-tests within categories
- Include timeout mechanisms for all async operations
- Verify both success and failure scenarios
- Document expected behaviors in test comments

### Performance Considerations
- Benchmark tests should skip in short mode: `if testing.Short() { b.Skip() }`
- Use appropriate timeout values for different test types
- Include cleanup mechanisms to prevent resource leaks
- Log meaningful performance metrics for regression detection
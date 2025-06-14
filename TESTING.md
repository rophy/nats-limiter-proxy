# Throughput Testing for NATS Limiter Proxy

This directory contains several test tools to verify that the NATS proxy correctly applies per-user bandwidth limits.

## Configuration

The proxy enforces these bandwidth limits (configured in `config.yaml`):
- **Alice**: 5MB/s (5,242,880 bytes/s)
- **Bob**: 2MB/s (2,097,152 bytes/s)
- **Default**: 10MB/s (10,485,760 bytes/s)

## Test Tools

### 1. Simple Shell Test (`test_simple_throughput.sh`)

Quick bash-based test for basic verification:

```bash
./test_simple_throughput.sh
```

**Features:**
- Tests both alice and bob individually
- Uses 64KB messages for 5 seconds
- Simple pass/fail validation
- Minimal dependencies (requires `nats` CLI, `bc`, `python3`)

### 2. Comprehensive Shell Test (`test_throughput.sh`)

Full-featured bash test suite:

```bash
./test_throughput.sh
```

**Features:**
- Individual user throughput testing
- Burst behavior analysis
- Concurrent user testing
- Direct vs proxy comparison
- Detailed reporting with validation

### 3. Go-based Test (`throughput_test.go`)

Precise measurement using Go and NATS client library:

```bash
go run throughput_test.go
```

**Features:**
- High-precision timing measurements
- Concurrent goroutine testing
- Burst behavior analysis
- Direct connection comparison
- Comprehensive reporting

## Prerequisites

Before running tests, ensure:

1. **Services are running:**
   ```bash
   docker compose up -d
   ```

2. **NATS CLI is installed:**
   ```bash
   ./local/install_nats_tools.sh
   ```

3. **Dependencies for shell tests:**
   - `bc` calculator: `sudo apt install bc` (Ubuntu/Debian)
   - `python3`: Usually pre-installed

## Expected Results

If rate limiting is working correctly, you should see:

- **Alice**: Throughput limited to ~5MB/s
- **Bob**: Throughput limited to ~2MB/s
- **Concurrent tests**: Both users maintain their individual limits
- **Direct vs Proxy**: Proxy shows significant throughput reduction

## Interpreting Results

### ✅ Good Results
- Measured throughput is at or below the configured limit
- Consistent limiting across multiple test runs
- Proxy throughput significantly lower than direct connection

### ⚠️ Warning Signs
- Throughput significantly exceeds configured limits
- Inconsistent results between test runs
- No difference between direct and proxy connections

### 🔧 Troubleshooting

**Connection Issues:**
```bash
# Check if proxy is running
docker logs nats-limiter-proxy-proxy-1

# Test basic connectivity
nats --server=localhost:4223 --creds=local/alice.creds pub test.ping "hello"
```

**Rate Limiting Not Working:**
1. Verify configuration in `config.yaml`
2. Check proxy logs for authentication messages
3. Ensure JWT files are correctly named in `local/jwt/`

**Performance Issues:**
- Large message sizes (64KB+) show rate limiting more clearly
- Short test durations may not show steady-state behavior
- Network latency can affect measurements

## Test Design

The tests use different approaches to measure throughput:

1. **Message-based**: Count messages sent per second
2. **Time-based**: Measure bytes transferred over fixed duration
3. **Burst testing**: Rapid message sending to test buffer behavior
4. **Concurrent testing**: Multiple users simultaneously

Each approach validates different aspects of the rate limiting implementation.
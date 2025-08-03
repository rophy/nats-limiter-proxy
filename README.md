# NATS Limiter Proxy

A NATS server proxy that adds per-user bandwidth limiting functionality with distributed coordination support. The proxy sits between NATS clients and a NATS server, parsing the NATS protocol to extract user authentication information and applying rate limiting based on per-user configuration.

## Features

- **Dual Rate Limiting**: Separate local (per-instance) and global (total) bandwidth limits
- **Protocol-Aware**: Deep packet inspection of NATS CONNECT messages  
- **Authentication Support**: Username/password and JWT token extraction
- **Redis Coordination**: Distributed rate limiting with Redis Sentinel for multi-instance deployments
- **Docker Compose Ready**: Easy development with 3-replica setup
- **Backward Compatibility**: Legacy configuration format automatically migrated

## Quick Start

### Local Mode (Single Instance)
```bash
# Initialize NATS accounts and start services
make init
make docker-up

# Test rate limiting  
make test-perf

# View available commands
make help
```

### Global Mode (Distributed with Redis)
```bash
# Start 3 proxy replicas with Redis coordination
make docker-up  # Uses config-redis.yaml by default

# Test performance and rate limiting
make test-perf

# Monitor Redis coordination
docker compose logs proxy | grep -E "(rebalance|global)"
```

## Testing

The project includes comprehensive testing at multiple levels to ensure reliability and performance.

### Test Types

#### Unit Tests
Individual component testing for core functionality:

```bash
# Run all unit tests
go test ./...

# Run specific package tests with verbose output
go test -v ./internal/server

# Run tests with coverage report
go test -cover ./internal/server
```

#### End-to-End Tests
Full integration tests running in Docker Compose environment:

```bash
# Run all e2e tests (includes basic NATS, JetStream, and data integrity tests)
make test-e2e

# Run specific e2e test patterns
make build-e2e
docker compose exec nats-box /tmp/e2e.test -test.v -test.run "TestE2E_JetStream"
```

**E2E Test Coverage:**
- **Basic Pub/Sub**: Message routing through proxy with authentication
- **Large Message Integrity**: 1MB message transmission with byte-by-byte verification
- **Concurrent Connections**: Multi-client stress testing with message integrity verification
- **Data Integrity Patterns**: Various binary data patterns (zeros, ones, random, binary)
- **Authentication**: Multi-user credential validation (Alice, Bob)
- **JetStream**: Stream creation, pub/sub, large messages, direct vs proxy comparison
- **Proxy vs Direct**: Comparison testing to ensure proxy doesn't alter behavior

#### Performance Tests
Throughput and rate limiting validation:

```bash
# Performance/benchmark test
make test-perf

# Custom benchmark tests
nats --server=localhost:4223 --creds=local/app/alice.creds bench pub test --size=1024 --msgs=100000 --no-progress

# Large message performance
nats --server=localhost:4223 --creds=local/app/alice.creds bench pub test --size=1048576 --msgs=1000

# Test global rate limiting (multiple connections)
nats --server=localhost:4223 --creds=local/app/alice.creds bench pub test --size=1024 --msgs=100000 --clients=3

# Manual verification scripts
./manual_test.sh
```

### Test Environment Setup

#### Prerequisites
```bash
# Start the complete test environment
make docker-up

# Verify all services are running
docker compose ps
```

#### Test Data & Credentials
The test environment automatically creates:
- **NATS Users**: `alice`, `bob`, `admin` with JWT credentials
- **JetStream**: Enabled with disk persistence (`/data/jetstream`)
- **Rate Limits**: Alice (5MB/s), Bob (2MB/s), Default (100KB/s)
- **Proxy Replicas**: 3 instances (ports 4223-4225) for distributed testing

#### Running Specific Test Suites

```bash
# JetStream functionality tests
make build-e2e
docker compose exec nats-box /tmp/e2e.test -test.v -test.run "JetStream"

# Data integrity tests
docker compose exec nats-box /tmp/e2e.test -test.v -test.run "DataIntegrity"

# Authentication tests
docker compose exec nats-box /tmp/e2e.test -test.v -test.run "Authenticated"

# Performance/load tests
docker compose exec nats-box /tmp/e2e.test -test.v -test.run "Concurrent"
```

### Test Results Interpretation

#### Successful Test Output
```
✓ Message successfully passed through proxy
✓ Complete message integrity verified - every byte matches
✓ JetStream message received and acknowledged
✓ Large JetStream message (1048576 bytes) received and acknowledged
✓ Complete large JetStream message integrity verified - every byte matches
```

#### Data Integrity Verification
All tests include comprehensive data integrity checks:
- **Byte-by-byte comparison** for large messages (1MB+)
- **Message ordering** verification for concurrent tests
- **JetStream persistence** validation with acknowledgments
- **Binary data patterns** testing (zeros, ones, random, sequential)

#### Performance Metrics
Typical performance benchmarks:
- **Small messages (1KB)**: ~50,000-100,000 msgs/sec per proxy
- **Large messages (1MB)**: ~100-500 msgs/sec per proxy with full integrity verification
- **JetStream publish**: ~42ms for 1MB message
- **JetStream fetch**: ~9ms for 1MB message

### Troubleshooting Tests

#### Common Issues
```bash
# If tests fail with "Authorization Violation"
make clean && make init

# If Docker environment is not ready
make docker-down && make docker-up

# Check service health
docker compose logs nats
docker compose logs proxy
```

#### Test Environment Reset
```bash
# Complete environment reset
make clean
make init
make docker-up
make test-e2e
```

## Configuration

### Enhanced Bandwidth Configuration
The new configuration format supports separate local and global bandwidth limits:

```yaml
bandwidth:
  default_local: 102400   # 100KB/s per proxy instance
  default_global: 204800  # 200KB/s total across all proxies
  users:
    alice:
      local: 5242880      # 5MB/s per proxy instance
      global: 15728640    # 15MB/s total across all proxies  
    bob:
      local: 2097152      # 2MB/s per proxy instance
      # global defaults to local value (2MB/s)
```

### Local Mode (`config-local.yaml`)
```yaml
bandwidth:
  default_local: 102400
  users:
    alice:
      local: 5242880
    bob:
      local: 2097152

redis:
  enabled: false  # Local rate limiting only
```

### Global Mode (`config-redis.yaml`)
```yaml
bandwidth:
  default_local: 102400   # Per-instance limits
  default_global: 204800  # Total limits across all proxies
  users:
    alice:
      local: 5242880
      global: 15728640    # 3x local for distributed usage
    bob:
      local: 2097152

redis:
  enabled: true                           # Enable global coordination
  redis_sentinels: ["redis-sentinel:26379"]
  redis_master_name: "mymaster"
  redis_password: ""
  redis_database: 0
  sync_interval: 5s
  rebalance_interval: 30s
  cleanup_interval: 60s
  startup_timeout: 30s
```

### Legacy Format (Backward Compatible)
```yaml
default_bandwidth: 102400  # Automatically migrated
users:
  alice: 5242880
  bob: 2097152
```

## Architecture

### Local Mode (Single Instance)
```
NATS Client → Proxy (4223) → NATS Server (4222)
              ↓
        Local Rate Limiter
        (Static per-instance limits)
```

### Global Mode (Redis Coordination)
```
NATS Client A → Proxy 1 (4223) ──┐
NATS Client B → Proxy 2 (4224) ──┼── NATS Server (4222)
NATS Client C → Proxy 3 (4225) ──┘
                ↓     ↓     ↓
    Local + Global Rate Limiters
                ↓     ↓     ↓
         Redis Sentinel Cluster
    (Coordinates global quotas with 1s sync, 5s rebalancing)
```

## Rate Limiting Behavior

### Dual Rate Limiting System
Both local and global rate limiters apply simultaneously - the stricter limit wins:

- **Local Rate Limiter**: Static per-proxy-instance enforcement
- **Global Rate Limiter**: Dynamic total-across-all-proxies enforcement (requires Redis)

### Local Mode Examples
- **Alice → Proxy 1**: Gets 5MB/s (alice.local)
- **Alice → Proxy 2**: Gets 5MB/s (alice.local) 
- **Total Alice usage**: Up to 10MB/s across both proxies

### Global Mode Examples  
- **Alice → Proxy 1**: Gets min(5MB/s local, 15MB/s global ÷ proxy_count)
- **Alice → Proxy 2**: Gets min(5MB/s local, 15MB/s global ÷ proxy_count)
- **Total Alice usage**: Maximum 15MB/s globally, rebalanced every 5 seconds

### Rebalancing Logic
- **Even distribution**: Global quota split evenly across active proxies
- **Real-time updates**: Dynamic bucket adjustment based on proxy count
- **Connection tracking**: Quotas released when users disconnect

## Deployment Options

### Docker Compose (Development)
```yaml
# 3 proxy replicas with Redis Sentinel coordination
services:
  proxy:
    deploy:
      replicas: 3
    volumes:
      - ./config-redis.yaml:/app/config.yaml:ro
    depends_on:
      - nats
      - redis-master
      - redis-sentinel
```

### Kubernetes (Production)
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nats-limiter-proxy
spec:
  replicas: 3
  template:
    spec:
      containers:
      - name: proxy
        env:
        - name: UPSTREAM_HOST
          value: "nats-server"
        - name: UPSTREAM_PORT
          value: "4222"
        volumeMounts:
        - name: config
          mountPath: /app/config.yaml
          subPath: config-redis.yaml
```

### Single Instance (Local Mode)
```bash
# No Redis required for local-only rate limiting
UPSTREAM_HOST=nats-server UPSTREAM_PORT=4222 ./nats-limiter-proxy
```

## Monitoring

### Rate Limiting Logs
```bash
# Monitor local rate limiter creation
docker compose logs proxy | grep "Created local rate limiter"

# Monitor global coordination
docker compose logs proxy | grep -E "(global|rebalance|Redis)"

# Monitor user connections
docker compose logs proxy | grep -E "(authenticated|disconnected|connection.*added|connection.*removed)"
```

### Redis Coordination Status
```bash
# Check Redis connectivity
docker compose exec redis-master redis-cli ping

# Monitor Redis keys (user tracking)
docker compose exec redis-master redis-cli --scan --pattern "user:*"

# View proxy coordination data
docker compose exec redis-master redis-cli keys "user:alice:*"
```


## Port Configuration

| Service | Port | Purpose |
|---------|------|---------|
| NATS Server | 4222 | Upstream NATS server |
| Proxy 1 | 4223 | NATS client connections |
| Proxy 2 | 4224 | NATS client connections |  
| Proxy 3 | 4225 | NATS client connections |
| Redis Master | 6379 | Redis coordination backend |
| Redis Sentinel | 26379 | Redis Sentinel for failover |

## Development

### Prerequisites
- Go 1.24.2+
- Docker & Docker Compose
- NATS CLI tools (installed via `local/install_nats_tools.sh`)

### Build Commands
```bash
make help           # Show all available commands
make build          # Build Go binary
make docker-build   # Build Docker image
make test           # Run unit tests
make test-e2e       # Run e2e integration tests
make test-perf      # Run performance tests
make clean          # Clean build artifacts and Docker environment
```


## Dependencies

- `github.com/juju/ratelimit`: Token bucket rate limiting algorithm
- `github.com/redis/go-redis/v9`: Redis client for coordination
- `github.com/rs/zerolog`: Structured logging
- `gopkg.in/yaml.v3`: YAML configuration parsing
- `github.com/golang-jwt/jwt/v5`: JWT token parsing
- Go 1.24.2+

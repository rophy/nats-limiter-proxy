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
make test

# View available commands
make help
```

### Global Mode (Distributed with Redis)
```bash
# Start 3 proxy replicas with Redis coordination
make docker-up  # Uses config-redis.yaml by default

# Test distributed rate limiting
make test-distributed

# Monitor Redis coordination
docker compose logs proxy | grep -E "(rebalance|global)"
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

### Performance Testing
```bash
# Test local rate limiting (single proxy)
nats --server=localhost:4223 --user=alice --password=alicepass bench pub test --size=1024 --msgs=100000

# Test global rate limiting (multiple connections)
nats --server=localhost:4223 --user=alice --password=alicepass bench pub test --size=1024 --msgs=100000 --clients=3
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
make test           # Run integration tests
make clean          # Clean build artifacts
```

### Testing
```bash
# Local rate limiting (single proxy)
make test

# Global rate limiting (3 proxy replicas with Redis)
make test-distributed

# Manual verification
./manual_test.sh
```

## Dependencies

- `github.com/juju/ratelimit`: Token bucket rate limiting algorithm
- `github.com/redis/go-redis/v9`: Redis client for coordination
- `github.com/rs/zerolog`: Structured logging
- `gopkg.in/yaml.v3`: YAML configuration parsing
- `github.com/golang-jwt/jwt/v5`: JWT token parsing
- Go 1.24.2+

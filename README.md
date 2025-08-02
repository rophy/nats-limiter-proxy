# NATS Limiter Proxy

A NATS server proxy that adds per-user bandwidth limiting functionality with distributed coordination support. The proxy sits between NATS clients and a NATS server, parsing the NATS protocol to extract user authentication information and applying rate limiting based on per-user configuration.

## Features

- **Per-User Rate Limiting**: Configure bandwidth limits per user (bytes/second)
- **Protocol-Aware**: Deep packet inspection of NATS CONNECT messages
- **Authentication Support**: Username/password and JWT token extraction
- **Distributed Coordination**: Optional peer-to-peer coordination for multi-instance deployments
- **Docker Compose Ready**: Easy development with 3-replica setup
- **Zero External Dependencies**: Self-contained with optional distributed features

## Quick Start

### Single Instance (Development)
```bash
# Initialize NATS accounts and start services
make init
make docker-up

# Test rate limiting
make test

# View available commands
make help
```

### Distributed Mode (3 Replicas)
```bash
# Start 3 proxy replicas with peer coordination
make docker-up  # Uses config-distributed.yaml by default

# Test across multiple proxies
make test-distributed

# Monitor peer coordination
curl http://localhost:8081/peers  # Proxy 1
curl http://localhost:8082/peers  # Proxy 2  
curl http://localhost:8083/peers  # Proxy 3
```

## Configuration

### Basic Rate Limiting (`config.yaml`)
```yaml
default_bandwidth: 102400  # 100KB/s default
users:
  alice: 5242880   # 5MB/s
  bob: 2097152     # 2MB/s
```

### Distributed Coordination (`config-distributed.yaml`)
```yaml
default_bandwidth: 102400
users:
  alice: 5242880
  bob: 2097152

# Peer coordination for distributed rate limiting
coordination:
  enabled: true                     # Enable distributed coordination
  port: 8081                       # HTTP port for peer communication
  gossip_interval: 5s              # How often to send usage gossip
  rebalance_interval: 30s          # How often to rebalance tokens
  cleanup_interval: 60s            # How often to cleanup old data
  discovery_method: kubernetes     # kubernetes, static, or dns
```

## Architecture

### Single Instance Mode
```
NATS Client → Proxy (4223) → NATS Server (4222)
              ↓
          Rate Limiter
```

### Distributed Mode  
```
NATS Client A → Proxy 1 (4223) ──┐
NATS Client B → Proxy 2 (4224) ──┼── NATS Server (4222)
NATS Client C → Proxy 3 (4225) ──┘
                ↓     ↓     ↓
           Gossip Protocol (8081-8083)
           Coordinates rate limits across proxies
```

## Rate Limiting Behavior

### Single User, Single Proxy
- User gets their full configured bandwidth limit
- Example: Alice gets 5MB/s when connecting to any proxy

### Single User, Multiple Proxies  
- Rate limit enforced globally across all proxy instances
- Example: Alice gets total 5MB/s distributed across connections

### Multiple Users, Multiple Proxies
- Each user's limit enforced independently
- Dynamic token rebalancing based on actual usage patterns

## Deployment Options

### Docker Compose (Development)
```yaml
# Uses replica scaling and service discovery
services:
  proxy:
    deploy:
      replicas: 3
    environment:
      SERVICE_NAME: "proxy"
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
        - name: POD_IP
          valueFrom:
            fieldRef:
              fieldPath: status.podIP
        - name: SERVICE_NAME
          value: "nats-limiter-proxy"
```

### Static Deployment
```bash
# Environment variables for peer discovery
export SERVICE_NAME="nats-proxy"
export STATIC_PEERS="10.0.1.10:8081,10.0.1.11:8081,10.0.1.12:8081"
./nats-limiter-proxy
```

## Monitoring

### Health Checks
```bash
curl http://localhost:8081/health
curl http://localhost:8082/health
curl http://localhost:8083/health
```

### Peer Status
```bash
curl http://localhost:8081/peers | jq '.'
```

### Usage Statistics
```bash
# View distributed rate limiting in action
docker compose logs proxy | grep -E "(allocation|gossip|rebalance)"
```

## Port Configuration

| Service | Port | Purpose |
|---------|------|---------|
| NATS Server | 4222 | Upstream NATS server |
| Proxy 1 | 4223 | NATS client connections |
| Proxy 2 | 4224 | NATS client connections |  
| Proxy 3 | 4225 | NATS client connections |
| Gossip 1 | 8081 | Peer coordination |
| Gossip 2 | 8082 | Peer coordination |
| Gossip 3 | 8083 | Peer coordination |

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
# Basic functionality
make test

# Distributed coordination
make test-distributed

# Manual rate limiting verification
./manual_test.sh
```

## Dependencies

- `github.com/juju/ratelimit`: Token bucket rate limiting algorithm
- `github.com/rs/zerolog`: Structured logging
- `gopkg.in/yaml.v3`: YAML configuration parsing
- `github.com/golang-jwt/jwt/v5`: JWT token parsing
- Go 1.24.2+

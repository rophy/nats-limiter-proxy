# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A NATS server proxy that adds per-user bandwidth limiting functionality with distributed coordination support. The proxy sits between NATS clients and a NATS server, parsing the NATS protocol to extract user authentication information and applying rate limiting based on per-user configuration.

## Architecture

### Core Components
- **cmd/nats-limiter-proxy/main.go**: Main proxy server that handles TCP connections, extracts user authentication from NATS CONNECT messages, and applies rate limiting using token bucket algorithm
- **internal/server/parser.go**: NATS protocol parser that understands PUB, HPUB, and CONNECT messages, enabling the proxy to properly forward protocol data while maintaining message boundaries
- **internal/server/ratelimiter.go**: Local rate limiting using token bucket algorithm with per-user bucket management
- **config.yaml** / **config-distributed.yaml**: Configuration files defining bandwidth limits and coordination settings

### Distributed Components (Optional)
- **internal/server/coordination.go**: Peer coordination framework with gossip protocol for distributed rate limiting
- **internal/server/gossip_service.go**: HTTP-based gossip service for peer-to-peer communication (port 8081)
- **internal/server/peer_manager.go**: Peer discovery and lifecycle management (Kubernetes, Docker Compose, static)
- **internal/server/token_balancer.go**: Distributed token allocation with demand-based rebalancing
- **internal/server/distributed_ratelimiter.go**: Distributed rate limiter manager with usage tracking

### Operation Modes

**Single Instance Mode:**
1. Accepting client connections on port 4223
2. Parsing NATS CONNECT messages to extract username (basic auth or JWT)
3. Creating rate limiters based on user-specific bandwidth configuration
4. Forwarding bidirectional traffic between client and upstream NATS server with applied limits

**Distributed Mode (coordination.enabled: true):**
1. All single instance functionality, plus:
2. HTTP gossip service for peer communication (port 8081)
3. Peer discovery via DNS/Kubernetes service discovery
4. Usage statistics sharing every 5 seconds via gossip protocol
5. Dynamic token rebalancing every 30 seconds based on actual demand
6. Demand-based allocation: single user gets full bandwidth regardless of proxy count

## Development Commands

### Initial Setup
```bash
# Initialize NATS accounts, operators, and users (required before first run)
make init
```

### Building and Running
```bash
# Show all available commands
make help

# Build the Go binary (outputs to bin/ directory)
make build

# Run locally (requires UPSTREAM_HOST and UPSTREAM_PORT environment variables)
make run

# Build and run with Docker Compose (3 replicas with distributed coordination)
make docker-up

# Stop Docker Compose services
make docker-down

# Build Docker image
make docker-build

# Run integration tests (single proxy)
make test

# Test distributed rate limiting across 3 proxy replicas
make test-distributed

# Clean build artifacts and NATS configuration
make clean
```

### Development Workflow
```bash
# First time setup
make init
make docker-up

# Test single proxy connection (port 4223)
nats --server=localhost:4223 --creds=local/alice.creds pub test "hello world"

# Test distributed setup (ports 4223, 4224, 4225)
nats --server=localhost:4224 --creds=local/alice.creds pub test "via proxy 2"

# Monitor peer coordination
curl http://localhost:8081/peers | jq '.'

# Development cycle
make build
make test
make test-distributed
```

### Configuration Files
- **config.yaml**: Basic single-instance rate limiting configuration
- **config-distributed.yaml**: Distributed coordination with gossip protocol (default for Docker Compose)
- **config-dns-srv.yaml**: DNS SRV record discovery configuration

### Port Configuration
- **4223**: Main proxy port (Docker Compose maps to 4223-4225 for 3 replicas)
- **8081**: Gossip coordination port (Docker Compose maps to 8081-8083 for 3 replicas)  
- **4222**: Upstream NATS server
- **8222**: NATS server monitoring

### Rate Limiting Behavior
- **Single user → single proxy**: Gets full configured bandwidth (e.g., Alice gets 5MB/s)
- **Single user → multiple proxies**: Total bandwidth distributed across connections
- **Distributed coordination**: Gossip protocol shares usage and rebalances tokens based on demand

## Dependencies
- `github.com/juju/ratelimit`: Token bucket rate limiting algorithm
- `github.com/rs/zerolog`: Structured logging
- `gopkg.in/yaml.v3`: YAML configuration parsing
- `github.com/golang-jwt/jwt/v5`: JWT token parsing for authentication
- `github.com/nats-io/nats.go`: NATS client library (for testing)
- Go 1.24.2+ required

## Git Commit Guidelines

**IMPORTANT**: Never mention AI, Claude, or automated tools in commit messages. All commits should appear as if written by a human developer.

### Good Examples:
- `fix: correct distributed rate limiting allocation logic`
- `feat: add gossip protocol for peer coordination`
- `refactor: improve DNS discovery for multi-platform support`
- `docs: update README with distributed configuration examples`

### Avoid These:
- ❌ `fix: Claude corrected the rate limiting bug`
- ❌ `feat: AI-generated distributed coordination`
- ❌ `docs: Generated by Claude Code`
- ❌ `refactor: AI assistance with code cleanup`

### Commit Message Format:
```
type: short description

- Detailed explanation of changes
- Why the change was needed
- Any behavioral changes or breaking changes
- Test results if relevant

Closes #issue-number (if applicable)
```
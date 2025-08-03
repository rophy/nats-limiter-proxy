# End-to-End Tests

This directory contains end-to-end integration tests for the NATS Limiter Proxy.

## Overview

The e2e tests run inside the docker-compose environment and test real NATS communication through the proxy. Tests are designed to validate:

- Basic pub/sub functionality through proxy
- Message integrity and data I/O
- Authentication with user credentials  
- Large message handling
- Concurrent connections
- Rate limiting behavior
- Distributed proxy coordination

## Running Tests

### Prerequisites

1. Ensure docker-compose is installed and running
2. Run `make docker-up` to start all services (NATS, Redis, Proxy replicas)

### Execute Tests

```bash
# Build and run all e2e tests
make test-e2e

# Or manually:
make build-e2e                                    # Build test binary
docker compose cp bin/e2e.test nats-box:/tmp/     # Copy to nats-box
docker compose exec nats-box /tmp/e2e.test -test.v # Run tests
```

### Test Architecture

Tests run inside the `nats-box` container which has access to:
- **NATS Server**: `nats://nats:4222` (direct, bypassing proxy)
- **Proxy**: `nats://proxy:4223` (load-balanced across replicas)
- **Redis**: `redis://redis-master:6379` (for distributed coordination)
- **Credentials**: `/nsc/creds/alice.creds`, `/nsc/creds/bob.creds`

## Test Categories

### Basic Tests (`basic_test.go`)
- `TestE2E_BasicPubSub`: Simple message passing through proxy
- `TestE2E_ProxyVsDirectComparison`: Proxy vs direct NATS comparison
- `TestE2E_AuthenticatedConnections`: User authentication (Alice/Bob)
- `TestE2E_LargeMessage`: 1MB message integrity testing
- `TestE2E_ConcurrentConnections`: Multiple publishers/subscribers

### Rate Limiting Tests (future)
- Per-user bandwidth enforcement
- Local vs global rate limiting
- Distributed coordination validation

### Distributed Tests (future)  
- Multi-proxy coordination
- Redis Sentinel failover
- Load balancing behavior

## Adding New Tests

1. Create test files in the `e2e/` directory
2. Use `testutil.NewDockerComposeEnv()` for service access
3. Follow naming convention: `TestE2E_FeatureName`
4. Rebuild with `make build-e2e` after changes

## Debugging

To debug tests interactively:
```bash
# Enter nats-box container
docker compose exec nats-box bash

# Run specific test
/tmp/e2e.test -test.run TestE2E_BasicPubSub -test.v

# Check service connectivity
nats --server=nats:4222 pub test "direct to nats"
nats --server=proxy:4223 pub test "through proxy"
```

## Test Environment

The tests assume the following docker-compose setup:
- NATS server on port 4222
- Proxy replicas on ports 4223-4225 (load balanced)
- Redis master/sentinel for coordination
- Pre-configured Alice/Bob user credentials
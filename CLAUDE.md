# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A NATS server proxy that adds per-user bandwidth limiting functionality. The proxy sits between NATS clients and a NATS server, parsing the NATS protocol to extract user authentication information and applying rate limiting based on per-user configuration.

## Architecture

- **cmd/nats-limiter-proxy/main.go**: Main proxy server that handles TCP connections, extracts user authentication from NATS CONNECT messages, and applies rate limiting using token bucket algorithm
- **internal/server/parser.go**: NATS protocol parser that understands PUB, HPUB, and CONNECT messages, enabling the proxy to properly forward protocol data while maintaining message boundaries
- **config.yaml**: Configuration file defining default bandwidth limits and per-user overrides

The proxy operates by:
1. Accepting client connections on port 4223
2. Parsing NATS CONNECT messages to extract username
3. Creating rate limiters based on user-specific bandwidth configuration
4. Forwarding bidirectional traffic between client and upstream NATS server with applied limits

## Development Commands

### Initial Setup
```bash
# Initialize NATS accounts, operators, and users (required before first run)
make init
```

### Building and Running
```bash
# Build the Go binary (outputs to bin/ directory)
make build

# Run locally (requires UPSTREAM_HOST and UPSTREAM_PORT environment variables)
make run

# Build and run with Docker Compose (recommended - includes init)
make docker-up

# Stop Docker Compose services
make docker-down

# Build Docker image
make docker-build

# Run tests
make test

# Clean build artifacts and NATS configuration
make clean
```

### Development Workflow
```bash
# First time setup
make init
make docker-up

# Test connection through proxy (port 4223)
nats --server=localhost:4223 --user=alice --password=alicepass pub test "hello world"

# Development cycle
make build
make test
```

### Configuration
- Main proxy listens on port 4223
- Upstream NATS server expected on configurable host:port via environment variables
- Bandwidth limits configured in `config.yaml` (bytes per second)
- NATS server configuration in `local/nats-server.conf` with user authentication

## Dependencies
- `github.com/juju/ratelimit`: Token bucket rate limiting
- `gopkg.in/yaml.v3`: YAML configuration parsing
- Go 1.24.2+ required
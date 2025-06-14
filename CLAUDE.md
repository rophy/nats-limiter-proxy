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

### Building and Running
```bash
# Build the Go binary
go build -o nats-limiter-proxy ./cmd/nats-limiter-proxy

# Run locally (requires UPSTREAM_HOST and UPSTREAM_PORT environment variables)
UPSTREAM_HOST=localhost UPSTREAM_PORT=4222 ./nats-limiter-proxy

# Build and run with Docker Compose (recommended)
docker compose up -d

# Build Docker image
docker build -t nats-limiter-proxy .
```

### Development Setup
```bash
# Install NATS CLI tools for testing
./local/install_nats_tools.sh

# Start NATS server and proxy
docker compose up -d

# Test connection through proxy (port 4223)
nats --server=localhost:4223 --user=alice --password=alicepass pub test "hello world"
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
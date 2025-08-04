# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 🚨 CRITICAL RULES - READ FIRST 🚨

### Git Commit Messages - MANDATORY FORMAT
**NEVER include ANY of these in commit messages:**
- AI mentions (Claude, AI, automated, generated, etc.)
- Attribution lines (`Co-Authored-By: Claude`)  
- Generated footers (`🤖 Generated with...`)

**REQUIRED commit format:**
```
type: short description

- Bullet point explaining what changed
- Why the change was needed  
- Any breaking changes or important notes
```

**Examples:**
✅ `fix: resolve connection tracking race condition`
✅ `feat: add Redis Sentinel coordination support`
❌ `fix: Claude helped resolve the connection issue`
❌ `feat: AI-generated Redis coordination`

## Project Overview

A NATS server proxy that adds per-user bandwidth limiting functionality with distributed coordination support. See README.md for detailed architecture and usage information.

**Current Limitations**: TLS is not supported. The proxy currently handles plain-text NATS connections only.

## AI-Specific Development Guidelines

### Testing Approach
- **Always run tests**: Use `make test` (unit), `make test-e2e` (e2e including benchmarks)
- **Test environment setup**: `make docker-up` starts the complete environment
- **Clean environment**: Use `make clean` for complete reset (removes Docker volumes)
- **JetStream testing**: E2E tests handle dynamic stream creation to avoid permission issues
- **NATS server compatibility**: E2E tests include comprehensive NATS server-adapted tests

### Key File Locations for AI Context
- **Core proxy logic**: `internal/server/parser.go` - NATS protocol parsing and rate limiting
- **Authentication**: Extract usernames from CONNECT messages (basic auth or JWT)
- **Rate limiting**: `internal/server/ratelimiter.go` - Token bucket implementation
- **Distributed coordination**: `internal/server/coordination.go` - Multi-proxy coordination
- **E2E tests**: `e2e/` directory - Comprehensive e2e tests including JetStream
- **Original e2e tests**: `e2e/e2e_*.go` - Basic proxy functionality tests
- **NATS server-adapted tests**: `e2e/nats_server_*.go` - Protocol compliance tests
- **Unit tests**: `internal/server/*_test.go` - Component-level testing

### Common Development Tasks
```bash
# Quick development cycle
make test           # Run unit tests (fast, no Docker)
make test-e2e       # Run all e2e tests including benchmarks (requires Docker)

# Environment management  
make docker-up      # Start complete test environment
make reset          # Complete reset: clean + init + docker-up
make clean          # Clean environment (removes volumes)
make init           # Initialize NATS accounts/users
```

### Code Modification Guidelines
- **Parser changes**: When modifying `internal/server/parser.go`, always run unit tests first
- **Rate limiting**: Changes to `internal/server/ratelimiter.go` should include performance test validation
- **Authentication**: JWT and basic auth logic is in parser.go - test with both auth methods
- **JetStream**: E2E tests create streams dynamically to avoid init permission issues
- **Performance testing**: E2E tests include comprehensive benchmarks
- **NATS server compatibility**: Run `make test-e2e` to validate proxy doesn't break NATS server functionality

### Debugging Tips
- **When confused or stuck**: Use `make reset` for complete environment reset
- **Authorization failures**: `make reset` will fix NATS credential issues
- **Test failures**: Check `docker compose logs nats` and `docker compose logs proxy`
- **Rate limiting issues**: Unit tests in `internal/server/parser_test.go` have detailed rate limiting scenarios
- **JetStream problems**: E2E tests skip if JetStream unavailable (check NATS server config)
- **Environment corruption**: `make reset` does clean + init + docker-up in one command

## Test References and Origins

### E2E Test Structure
- **Original e2e tests** (`e2e/e2e_*.go`): Basic proxy functionality tests
- **NATS server-adapted tests** (`e2e/nats_server_*.go`): Protocol compliance tests

### NATS Server Test Mapping
The tests in `e2e/nats_server_*` are learned from https://github.com/nats-io/nats-server/blob/main/test/ with one-to-one file name mapping:

- `nats_server_auth_test.go` ← `auth_test.go`
- `nats_server_proto_test.go` ← `proto_test.go`
- `nats_server_cluster_test.go` ← `cluster_test.go`
- `nats_server_bench_test.go` ← `bench_test.go`
- `nats_server_maxpayload_test.go` ← `maxpayload_test.go`
- `nats_server_service_latency_test.go` ← `service_latency_test.go`

**Not implemented (future work)**:
- TLS-related tests (`cluster_tls_test.go`, `tls_test.go`) - TLS support not yet implemented

These tests verify that NATS server functionality works correctly **through** the proxy without interference.

### Service Latency Test Coverage
The `nats_server_service_latency_test.go` includes comprehensive service latency validation:

- **Basic Service Latency**: Request/response timing with processing delays
- **Proxy vs Direct Comparison**: Measures proxy overhead (typically < 1ms)
- **Error Handling**: Service timeouts, no responders, service errors
- **Concurrent Requests**: Multi-client latency consistency testing
- **Header Preservation**: Distributed tracing headers maintained through proxy

**Key Metrics Validated**:
- Service response latency preservation
- Minimal proxy overhead (< 50ms threshold)
- Proper error propagation (NATS timeout/no responders)
- Header pass-through for observability tools
- Concurrent request performance consistency

### TLS Implementation Considerations (Future Work)

When implementing TLS support, two architectural approaches need consideration:

1. **TLS Pass-Through**: Proxy forwards encrypted traffic
   - Pros: Maintains end-to-end encryption, simpler proxy logic
   - Cons: Cannot parse NATS protocol for rate limiting (encrypted payload)
   - Use case: When rate limiting can be based on connection-level metrics only

2. **TLS Termination**: Proxy decrypts, parses, re-encrypts  
   - Pros: Enables NATS protocol parsing for per-message rate limiting
   - Cons: More complex, proxy becomes part of security boundary
   - Use case: When detailed protocol-aware rate limiting is required

**Recommended Test Adaptation**: 
- `cluster_tls_test.go` → `nats_server_cluster_tls_test.go` (TLS cluster behavior)
- `tls_test.go` → `nats_server_tls_test.go` (TLS client-server behavior)

**Key Test Scenarios to Cover**:
- Certificate verification pass-through
- TLS handshake error propagation  
- Mutual TLS (mTLS) support
- TLS timeout handling
- Secure connection multiplexing

The choice between pass-through vs termination will determine which test patterns are most relevant.
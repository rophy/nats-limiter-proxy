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

## AI-Specific Development Guidelines

### Testing Approach
- **Always run tests**: Use `make test` (unit), `make test-e2e` (integration), `make test-perf` (performance)
- **Test environment setup**: `make docker-up` starts the complete environment
- **Clean environment**: Use `make clean` for complete reset (removes Docker volumes)
- **JetStream testing**: E2E tests handle dynamic stream creation to avoid permission issues

### Key File Locations for AI Context
- **Core proxy logic**: `internal/server/parser.go` - NATS protocol parsing and rate limiting
- **Authentication**: Extract usernames from CONNECT messages (basic auth or JWT)
- **Rate limiting**: `internal/server/ratelimiter.go` - Token bucket implementation
- **Distributed coordination**: `internal/server/coordination.go` - Multi-proxy coordination
- **E2E tests**: `e2e/` directory - Comprehensive integration tests including JetStream
- **Unit tests**: `internal/server/*_test.go` - Component-level testing

### Common Development Tasks
```bash
# Quick development cycle
make test           # Run unit tests (fast, no Docker)
make test-e2e       # Run integration tests (requires Docker)
make test-perf      # Run performance tests

# Environment management  
make docker-up      # Start complete test environment
make clean          # Complete reset (removes volumes)
make init           # Initialize NATS accounts/users
```

### Code Modification Guidelines
- **Parser changes**: When modifying `internal/server/parser.go`, always run unit tests first
- **Rate limiting**: Changes to `internal/server/ratelimiter.go` should include performance test validation
- **Authentication**: JWT and basic auth logic is in parser.go - test with both auth methods
- **JetStream**: E2E tests create streams dynamically to avoid init permission issues
- **Distributed features**: Test with `make test-perf` which uses 3 proxy replicas

### Debugging Tips
- **Authorization failures**: Use `make clean && make init` to reset NATS credentials
- **Test failures**: Check `docker compose logs nats` and `docker compose logs proxy`
- **Rate limiting issues**: Unit tests in `internal/server/parser_test.go` have detailed rate limiting scenarios
- **JetStream problems**: E2E tests skip if JetStream unavailable (check NATS server config)


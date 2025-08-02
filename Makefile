.PHONY: help init build run clean docker-build docker-up docker-down test test-distributed

# Show help
help:
	@echo "NATS Limiter Proxy - Available Commands:"
	@echo ""
	@echo "  help         - Show this help message"
	@echo "  init         - Initialize NATS accounts, operators, and users"
	@echo "  build        - Build the Go binary (outputs to bin/ directory)"
	@echo "  run          - Run locally (requires UPSTREAM_HOST and UPSTREAM_PORT)"
	@echo "  clean        - Clean build artifacts and NATS configuration"
	@echo "  docker-build - Build Docker image"
	@echo "  docker-up    - Start with Docker Compose (includes init)"
	@echo "  docker-down  - Stop Docker Compose services"
	@echo "  test         - Run integration tests (requires docker-up)"
	@echo "  test-distributed - Test distributed rate limiting with 3 proxy nodes"
	@echo ""
	@echo "Quick start:"
	@echo "  make docker-up    # Start all services"
	@echo "  make test         # Run tests"
	@echo "  make docker-down  # Stop services"

# Initialize 
init: local/nats/resolver.conf

# Build the binary
build:
	mkdir -p bin
	go build -o bin/nats-limiter-proxy ./cmd/nats-limiter-proxy

# Run locally (requires UPSTREAM_HOST and UPSTREAM_PORT)
run: build
	UPSTREAM_HOST=localhost UPSTREAM_PORT=4222 ./bin/nats-limiter-proxy

# Clean build artifacts
clean:
	local/scripts/cleanup.sh

# Build Docker image
docker-build:
	docker build -t nats-limiter-proxy .

# Start with Docker Compose
docker-up: init
	docker compose up -d

# Stop Docker Compose
docker-down:
	docker compose down

# Run tests
test: docker-up
	docker compose exec nats-box nats --context=alice bench pub test --size=1024 --msgs=100000 --no-progress

# Test distributed rate limiting with 3 proxy nodes
test-distributed: docker-up
	@echo "Waiting for all proxy nodes to start..."
	@sleep 10
	@echo "Running distributed rate limiting tests..."
	./test-distributed.sh

local/nats/resolver.conf:
	local/scripts/init.sh

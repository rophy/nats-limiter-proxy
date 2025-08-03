.PHONY: help init build run clean docker-build docker-up docker-down test test-distributed test-e2e build-e2e

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
	@echo "  test         - Run unit tests"
	@echo "  test-perf    - Run performance/benchmark tests (requires docker-up)"
	@echo "  test-e2e     - Run e2e tests inside docker-compose nats-box"
	@echo "  build-e2e    - Build e2e test binary"
	@echo ""
	@echo "Quick start:"
	@echo "  make docker-up    # Start all services"
	@echo "  make test-e2e     # Run e2e tests"
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

# Clean build artifacts and Docker environment
clean:
	docker compose down -v
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

# Run unit tests
test:
	go test -v ./internal/...

# Run performance/benchmark tests
test-perf: docker-up
	docker compose exec nats-box nats --context=alice bench pub test --size=1024 --msgs=100000 --no-progress


# Build e2e test binary (static for Alpine Linux containers)
build-e2e:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux go test -c ./e2e -o bin/e2e.test

# Run e2e tests inside docker-compose nats-box
test-e2e: docker-up build-e2e
	@echo "Copying e2e test binary to nats-box..."
	docker compose cp bin/e2e.test nats-box:/tmp/e2e.test
	@echo "Running e2e tests inside nats-box..."
	docker compose exec nats-box /tmp/e2e.test -test.v

local/nats/resolver.conf:
	local/scripts/init.sh

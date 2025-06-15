.PHONY: init build run clean docker-build docker-up docker-down test

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
	docker compose exec nats-box nats --context=alice bench pub test --size=1024 --msgs=10000

local/nats/resolver.conf:
	local/scripts/init.sh

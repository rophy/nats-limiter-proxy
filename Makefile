.PHONY: init build run clean docker-build docker-up docker-down test

# Initialize 
init: local/nsc/.config local/nats/resolver.conf

local/nsc/.config:
	docker compose pull nats-box
	docker compose run --rm nats-box nsc init --name=root

local/nats/resolver.conf: local/nsc/.config
	docker compose run --rm -it nats-box nsc generate config --nats-resolver > local/nats/resolver.conf

# Build the binary
build:
	mkdir -p bin
	go build -o bin/nats-limiter-proxy ./cmd/nats-limiter-proxy

# Run locally (requires UPSTREAM_HOST and UPSTREAM_PORT)
run: build
	UPSTREAM_HOST=localhost UPSTREAM_PORT=4222 ./bin/nats-limiter-proxy

# Clean build artifacts
clean:
	rm -rf bin/
	rm -rf local/nsc/*
	rm -rf local/nsc/.config
	rm -f  local/nats/resolver.conf

# Build Docker image
docker-build:
	docker build -t nats-limiter-proxy .

# Start with Docker Compose
docker-up:
	docker compose up -d

# Stop Docker Compose
docker-down:
	docker compose down

# Run tests
test:
	go test ./...


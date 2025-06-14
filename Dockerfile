# Start from the official Golang image for building
FROM golang:1.24.3-alpine3.22 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build the production proxy
RUN go build -o nats-limiter-proxy proxy_production.go

# Use a minimal image for running
FROM alpine:3.22.0

# Install wget for health checks
RUN apk add --no-cache wget

WORKDIR /app

# Copy the binary from the builder
COPY --from=builder /app/nats-limiter-proxy .

# Expose the proxy ports
EXPOSE 4223 8080 8081

# Run the production proxy
ENTRYPOINT ["./nats-limiter-proxy"]

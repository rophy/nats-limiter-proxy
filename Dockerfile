# Start from the official Golang image for building
FROM golang:1.24.3-alpine3.22 AS builder

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .

# Build the Go app
RUN go build -o nats-limiter-proxy proxy.go

# Use a minimal image for running
FROM alpine:3.22.0

WORKDIR /app

# Copy the binary from the builder
COPY --from=builder /app/nats-limiter-proxy .

# Expose the proxy port
EXPOSE 4223

# Run the proxy
ENTRYPOINT ["./nats-limiter-proxy"]

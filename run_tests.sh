#!/bin/bash

echo "Running all NATS limiter proxy tests..."
echo "========================================="

# Move conflicting file temporarily
if [ -f "proxy_production.go" ]; then
    mv proxy_production.go temp_proxy_production.go
    echo "Temporarily moved proxy_production.go to avoid conflicts"
fi

# Set required environment variables and run all tests
export UPSTREAM_HOST=localhost
export UPSTREAM_PORT=4222

echo ""
echo "Running all tests with coverage..."
go test -v -cover ./...

# Restore the file
if [ -f "temp_proxy_production.go" ]; then
    mv temp_proxy_production.go proxy_production.go
    echo ""
    echo "Restored proxy_production.go"
fi

echo ""
echo "All tests completed!"
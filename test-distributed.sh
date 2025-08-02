#!/bin/bash

# Test script for distributed rate limiting
echo "Testing Distributed Rate Limiting with Replicas"
echo "==============================================="

# Test connection to each proxy replica
echo "Testing proxy replicas:"
echo "- Replica 1: localhost:4223"
echo "- Replica 2: localhost:4224" 
echo "- Replica 3: localhost:4225"
echo ""

# Test Alice (5MB/s limit) through different proxy replicas
echo "Testing Alice (5MB/s limit) through different proxy replicas..."

echo "Via Replica 1 (port 4223):"
timeout 5s nats --server=localhost:4223 --creds=local/alice.creds pub test.replica1 "Hello from Alice via Replica1"

echo "Via Replica 2 (port 4224):"
timeout 5s nats --server=localhost:4224 --creds=local/alice.creds pub test.replica2 "Hello from Alice via Replica2"

echo "Via Replica 3 (port 4225):"
timeout 5s nats --server=localhost:4225 --creds=local/alice.creds pub test.replica3 "Hello from Alice via Replica3"

echo ""
echo "Check proxy logs for gossip activity:"
echo "docker compose logs | grep gossip"

echo ""
echo "Monitor peer coordination:"
echo "curl http://localhost:8081/peers"
echo "curl http://localhost:8082/peers"  
echo "curl http://localhost:8083/peers"

echo ""
echo "Health checks:"
echo "curl http://localhost:8081/health"
echo "curl http://localhost:8082/health"
echo "curl http://localhost:8083/health"

echo ""
echo "Note: With DNS discovery, replicas automatically discover each other"
echo "using Docker Compose's built-in service discovery (proxy service name)"
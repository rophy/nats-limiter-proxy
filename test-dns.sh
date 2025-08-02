#!/bin/bash

echo "Testing DNS Resolution in Docker Compose"
echo "========================================"

# Start the services
echo "Starting Docker Compose services..."
make docker-up

echo "Waiting for services to start..."
sleep 15

echo ""
echo "Testing DNS resolution from within containers:"

# Test DNS resolution from within a proxy container
echo "1. Testing 'proxy' DNS resolution:"
docker compose exec proxy1 nslookup proxy || echo "nslookup failed"

echo ""
echo "2. Testing container names:"
docker compose exec proxy1 ping -c 1 proxy2 || echo "proxy2 ping failed"
docker compose exec proxy1 ping -c 1 proxy3 || echo "proxy3 ping failed"

echo ""
echo "3. Checking proxy logs for DNS errors:"
docker compose logs proxy1 2>&1 | grep -E "(Failed to resolve|dns_name)" | tail -5

echo ""
echo "4. Checking if peers are discovered:"
sleep 10
curl -s http://localhost:8081/peers | jq '.' || echo "peers endpoint failed"

echo ""
echo "Cleanup:"
echo "Run 'make docker-down' to stop services"
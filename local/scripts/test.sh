#!/bin/sh

set -e

# Test with larger dataset to better observe sustained rate limiting
# 100,000 messages x 1KB = ~100MB total
# Expected: ~5MB burst + 95MB at 5MB/s = ~19 seconds total
# Target rate: ~5.26MB/s average (much closer to 5MB/s limit)

docker compose run -d --name=sub nats-box nats --context=bob bench sub test --msgs=100000
docker compose run --name=pub nats-box nats --context=alice bench pub test --msgs=100000 --size=1024
docker logs sub
docker stop pub sub
docker rm pub sub

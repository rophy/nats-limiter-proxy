#!/bin/sh

set -e

cleanup() {
  docker stop pub sub > /dev/null || true 
  docker rm pub sub > /dev/null || true
}

trap 'cleanup' EXIT

size=1024
msgs=20000

docker compose run -d --name=sub nats-box nats --context=bob bench sub test --no-progress --msgs=${msgs} --size=${size}
docker compose run --name=pub nats-box nats --context=alice bench pub test --no-progress --msgs=${msgs} --size=${size}
docker logs sub

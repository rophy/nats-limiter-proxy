#!/bin/sh

set -e

docker compose run --rm nats-box nsc init --name=root
docker compose run --rm nats-box nsc generate config --nats-resolver > local/nats/resolver.conf
docker compose up -d nats
docker compose run --rm nats-box /scripts/init-nats-box.sh

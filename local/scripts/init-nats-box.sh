#!/bin/sh

set -e

nsc edit operator --account-jwt-server-url nats://nats:4222
nsc add account --name app 2>/dev/null || echo "Account app already exists"
nsc edit account --name app --js-disk-storage 1G
nsc add user -a app admin 2>/dev/null || echo "User admin already exists"
nsc add user -a app alice 2>/dev/null || echo "User alice already exists"
nsc add user -a app bob 2>/dev/null || echo "User bob already exists"
nsc push -A || echo "Push failed - may already be pushed"
nats context add admin --server=nats://nats:4222 --creds=/nsc/nkeys/creds/root/app/admin.creds 2>/dev/null || echo "Context admin already exists"
nats context add alice --server=nats://proxy:4223 --creds=/nsc/nkeys/creds/root/app/alice.creds 2>/dev/null || echo "Context alice already exists"
nats context add bob --server=nats://proxy:4223 --creds=/nsc/nkeys/creds/root/app/bob.creds 2>/dev/null || echo "Context bob already exists"
nats context add default --server=nats://proxy:4223 2>/dev/null || echo "Context default already exists"
nats context select default

# Initialize JetStream streams and consumers
echo "Setting up JetStream streams and consumers..."

# JetStream setup will be done dynamically in e2e tests
# to avoid permission issues during init
echo "JetStream will be configured dynamically during e2e tests"

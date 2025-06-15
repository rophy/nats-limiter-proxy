#!/bin/sh

set -e

nsc edit operator --account-jwt-server-url nats://nats:4222
nsc add account --name app
nsc edit account --name app --js-disk-storage 1G
nsc add user -a app admin
nsc add user -a app alice
nsc add user -a app bob
nsc push -A
nats context add admin --server=nats://nats:4222 --creds=/nsc/nkeys/creds/root/app/admin.creds
nats context add alice --server=nats://proxy:4223 --creds=/nsc/nkeys/creds/root/app/alice.creds
nats context add bob --server=nats://proxy:4223 --creds=/nsc/nkeys/creds/root/app/bob.creds
nats context select admin

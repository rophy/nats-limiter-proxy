#!/bin/bash

# Test script to verify both authentication methods work

set -e

PROXY_SERVER="localhost:4223"
DIRECT_SERVER="localhost:4222"

log() {
    echo "[$(date '+%H:%M:%S')] $1"
}

success() {
    echo "✓ $1"
}

error() {
    echo "✗ $1"
}

test_auth_method() {
    local method=$1
    local user=$2
    local auth_args="$3"
    local server=$4
    
    log "Testing $method authentication for $user via $server"
    
    if nats --server="$server" $auth_args pub "test.auth.$method" "Hello from $user via $method" 2>/dev/null; then
        success "$user authenticated successfully via $method"
        return 0
    else
        error "$user failed to authenticate via $method"
        return 1
    fi
}

main() {
    echo "============================================"
    echo "Authentication Methods Test"
    echo "============================================"
    echo "Testing both JWT and username/password authentication"
    echo ""
    
    # Test username/password authentication
    echo "=== Username/Password Authentication ==="
    test_auth_method "password" "charlie" "--user=charlie --password=charliepass" "$PROXY_SERVER"
    test_auth_method "password" "diana" "--user=diana --password=dianapass" "$PROXY_SERVER"
    
    echo ""
    
    # Test JWT authentication (if JWT files exist)
    echo "=== JWT Authentication ==="
    if [ -f "local/alice.creds" ]; then
        test_auth_method "JWT" "alice" "--creds=local/alice.creds" "$PROXY_SERVER"
    else
        echo "⚠ Alice JWT credentials not found"
    fi
    
    if [ -f "local/bob.creds" ]; then
        test_auth_method "JWT" "bob" "--creds=local/bob.creds" "$PROXY_SERVER"
    else
        echo "⚠ Bob JWT credentials not found"
    fi
    
    echo ""
    
    # Test direct connection to verify NATS server config
    echo "=== Direct Connection Test ==="
    test_auth_method "password" "charlie" "--user=charlie --password=charliepass" "$DIRECT_SERVER"
    
    echo ""
    log "Authentication testing completed"
}

main "$@"
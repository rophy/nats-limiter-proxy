#!/bin/bash

# Simple Throughput Test for NATS Limiter Proxy
# Quick verification that rate limiting is working

set -e

# Configuration
PROXY_SERVER="localhost:4223"
MESSAGE_SIZE=65536  # 64KB
TEST_DURATION=5     # seconds

log() {
    echo "[$(date '+%H:%M:%S')] $1"
}

# Simple throughput measurement
measure_simple_throughput() {
    local user=$1
    local creds_file=$2
    
    log "Testing $user throughput for ${TEST_DURATION} seconds with ${MESSAGE_SIZE} byte messages..."
    
    # Generate payload
    local payload=$(python3 -c "print('X' * $MESSAGE_SIZE)")
    
    # Count messages sent in time window
    local start_time=$(date +%s)
    local end_time=$((start_time + TEST_DURATION))
    local count=0
    
    while [ $(date +%s) -lt $end_time ]; do
        if nats --server="$PROXY_SERVER" --creds="$creds_file" pub "test.$user" "$payload" 2>/dev/null; then
            ((count++))
        else
            break
        fi
    done
    
    local total_bytes=$((count * MESSAGE_SIZE))
    local throughput_mbps=$(echo "scale=2; $total_bytes / $TEST_DURATION / 1024 / 1024" | bc -l)
    
    echo "  $user: $count messages, $(printf "%'d" $total_bytes) bytes, $(printf "%.2f" $throughput_mbps) MB/s"
    
    # Simple validation
    case $user in
        "alice")
            if (( $(echo "$throughput_mbps <= 6.0" | bc -l) )); then
                echo "  ✓ Alice throughput appears limited (≤6MB/s, limit is 5MB/s)"
            else
                echo "  ⚠ Alice throughput may exceed limit: ${throughput_mbps}MB/s"
            fi
            ;;
        "bob")
            if (( $(echo "$throughput_mbps <= 3.0" | bc -l) )); then
                echo "  ✓ Bob throughput appears limited (≤3MB/s, limit is 2MB/s)"
            else
                echo "  ⚠ Bob throughput may exceed limit: ${throughput_mbps}MB/s"
            fi
            ;;
    esac
}

main() {
    echo "============================================"
    echo "Simple NATS Proxy Throughput Test"
    echo "============================================"
    echo "Expected limits:"
    echo "  Alice: 5MB/s"
    echo "  Bob:   2MB/s"
    echo ""
    
    # Check if services are available
    if ! nats --server="$PROXY_SERVER" --creds="local/alice.creds" pub test.ping "ping" 2>/dev/null; then
        echo "Error: Cannot connect to proxy at $PROXY_SERVER"
        echo "Make sure 'docker compose up -d' is running"
        exit 1
    fi
    
    echo "Testing individual user throughput:"
    measure_simple_throughput "alice" "local/alice.creds"
    echo ""
    measure_simple_throughput "bob" "local/bob.creds"
    echo ""
    
    log "Test completed. If throughput values are below the limits, rate limiting is working!"
}

main "$@"
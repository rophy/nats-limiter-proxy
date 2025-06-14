#!/bin/bash

# Throughput Testing Script for NATS Limiter Proxy
# Tests rate limiting functionality for different users

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
PROXY_SERVER="localhost:4223"
DIRECT_SERVER="localhost:4222"
TEST_DURATION=10  # seconds
LARGE_MESSAGE_SIZE=65536  # 64KB messages
BURST_MESSAGE_COUNT=100

# Expected rates (bytes per second)
ALICE_LIMIT=5242880   # 5MB/s
BOB_LIMIT=2097152     # 2MB/s
DEFAULT_LIMIT=10485760 # 10MB/s

log() {
    echo -e "${BLUE}[$(date '+%H:%M:%S')]${NC} $1"
}

success() {
    echo -e "${GREEN}✓${NC} $1"
}

warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

error() {
    echo -e "${RED}✗${NC} $1"
}

# Generate large message payload
generate_payload() {
    local size=$1
    python3 -c "print('A' * $size)"
}

# Measure throughput by publishing messages continuously
measure_throughput() {
    local user=$1
    local creds_file=$2
    local server=$3
    local duration=$4
    local message_size=$5
    
    log "Measuring throughput for $user (${message_size} byte messages, ${duration}s test)"
    
    # Create large payload
    local payload=$(generate_payload $message_size)
    local temp_file="/tmp/throughput_test_${user}_$$"
    
    # Start background publisher
    {
        local count=0
        local start_time=$(date +%s.%N)
        local end_time=$(($(date +%s) + duration))
        
        while [ $(date +%s) -lt $end_time ]; do
            if ! nats --server="$server" --creds="$creds_file" pub "throughput.test.$user" "$payload" 2>/dev/null; then
                break
            fi
            ((count++))
        done
        
        local actual_end_time=$(date +%s.%N)
        local actual_duration=$(echo "$actual_end_time - $start_time" | bc -l)
        local total_bytes=$((count * message_size))
        local throughput=$(echo "scale=2; $total_bytes / $actual_duration" | bc -l)
        
        echo "$count,$total_bytes,$actual_duration,$throughput" > "$temp_file"
    } &
    
    local pid=$!
    
    # Wait for test completion
    wait $pid
    
    # Read results
    if [ -f "$temp_file" ]; then
        local results=$(cat "$temp_file")
        rm -f "$temp_file"
        echo "$results"
    else
        echo "0,0,0,0"
    fi
}

# Test burst behavior
test_burst_behavior() {
    local user=$1
    local creds_file=$2
    local message_count=$3
    
    log "Testing burst behavior for $user ($message_count messages)"
    
    local payload=$(generate_payload $LARGE_MESSAGE_SIZE)
    local start_time=$(date +%s.%N)
    local success_count=0
    
    for i in $(seq 1 $message_count); do
        if nats --server="$PROXY_SERVER" --creds="$creds_file" pub "burst.test.$user" "$payload" 2>/dev/null; then
            ((success_count++))
        fi
    done
    
    local end_time=$(date +%s.%N)
    local duration=$(echo "$end_time - $start_time" | bc -l)
    local total_bytes=$((success_count * LARGE_MESSAGE_SIZE))
    local throughput=$(echo "scale=2; $total_bytes / $duration" | bc -l)
    
    echo "$success_count,$total_bytes,$duration,$throughput"
}

# Validate throughput against expected limits
validate_throughput() {
    local user=$1
    local measured_throughput=$2
    local expected_limit=$3
    local tolerance=0.2  # 20% tolerance
    
    local lower_bound=$(echo "scale=2; $expected_limit * (1 - $tolerance)" | bc -l)
    local upper_bound=$(echo "scale=2; $expected_limit * (1 + $tolerance)" | bc -l)
    
    if (( $(echo "$measured_throughput <= $upper_bound" | bc -l) )); then
        if (( $(echo "$measured_throughput >= $lower_bound" | bc -l) )); then
            success "$user throughput within expected range: $(printf "%.2f" $measured_throughput) bytes/s (limit: $expected_limit)"
            return 0
        else
            warning "$user throughput below expected range: $(printf "%.2f" $measured_throughput) bytes/s (expected: $lower_bound-$upper_bound)"
            return 1
        fi
    else
        error "$user throughput exceeds limit: $(printf "%.2f" $measured_throughput) bytes/s (limit: $expected_limit)"
        return 1
    fi
}

# Test concurrent users
test_concurrent_users() {
    log "Testing concurrent user throughput"
    
    local alice_results_file="/tmp/alice_concurrent_$$"
    local bob_results_file="/tmp/bob_concurrent_$$"
    
    # Start both users simultaneously
    (
        results=$(measure_throughput "alice" "local/alice.creds" "$PROXY_SERVER" "$TEST_DURATION" "$LARGE_MESSAGE_SIZE")
        echo "$results" > "$alice_results_file"
    ) &
    local alice_pid=$!
    
    (
        results=$(measure_throughput "bob" "local/bob.creds" "$PROXY_SERVER" "$TEST_DURATION" "$LARGE_MESSAGE_SIZE")
        echo "$results" > "$bob_results_file"
    ) &
    local bob_pid=$!
    
    # Wait for both to complete
    wait $alice_pid
    wait $bob_pid
    
    # Process results
    if [ -f "$alice_results_file" ] && [ -f "$bob_results_file" ]; then
        local alice_results=$(cat "$alice_results_file")
        local bob_results=$(cat "$bob_results_file")
        
        local alice_throughput=$(echo "$alice_results" | cut -d',' -f4)
        local bob_throughput=$(echo "$bob_results" | cut -d',' -f4)
        
        echo "Alice concurrent throughput: $alice_throughput bytes/s"
        echo "Bob concurrent throughput: $bob_throughput bytes/s"
        
        rm -f "$alice_results_file" "$bob_results_file"
        
        # Validate both users maintained their limits
        validate_throughput "alice" "$alice_throughput" "$ALICE_LIMIT"
        validate_throughput "bob" "$bob_throughput" "$BOB_LIMIT"
    else
        error "Failed to read concurrent test results"
        return 1
    fi
}

# Main test execution
main() {
    echo "========================================="
    echo "NATS Limiter Proxy Throughput Tests"
    echo "========================================="
    
    # Check dependencies
    if ! command -v nats &> /dev/null; then
        error "nats CLI not found. Please install NATS tools."
        exit 1
    fi
    
    if ! command -v bc &> /dev/null; then
        error "bc calculator not found. Please install bc."
        exit 1
    fi
    
    # Verify services are running
    log "Checking if services are running..."
    if ! nats --server="$PROXY_SERVER" --creds="local/alice.creds" pub test.connectivity "ping" 2>/dev/null; then
        error "Cannot connect to proxy server at $PROXY_SERVER"
        exit 1
    fi
    success "Proxy server is accessible"
    
    echo ""
    log "Configuration:"
    echo "  Alice limit: $(printf "%'d" $ALICE_LIMIT) bytes/s (5MB/s)"
    echo "  Bob limit:   $(printf "%'d" $BOB_LIMIT) bytes/s (2MB/s)"
    echo "  Default limit: $(printf "%'d" $DEFAULT_LIMIT) bytes/s (10MB/s)"
    echo ""
    
    # Test 1: Individual user throughput
    echo "========================================="
    echo "Test 1: Individual User Throughput"
    echo "========================================="
    
    log "Testing Alice throughput..."
    alice_results=$(measure_throughput "alice" "local/alice.creds" "$PROXY_SERVER" "$TEST_DURATION" "$LARGE_MESSAGE_SIZE")
    alice_throughput=$(echo "$alice_results" | cut -d',' -f4)
    echo "Alice results: $alice_results (messages,bytes,duration,throughput)"
    validate_throughput "alice" "$alice_throughput" "$ALICE_LIMIT"
    
    echo ""
    log "Testing Bob throughput..."
    bob_results=$(measure_throughput "bob" "local/bob.creds" "$PROXY_SERVER" "$TEST_DURATION" "$LARGE_MESSAGE_SIZE")
    bob_throughput=$(echo "$bob_results" | cut -d',' -f4)
    echo "Bob results: $bob_results (messages,bytes,duration,throughput)"
    validate_throughput "bob" "$bob_throughput" "$BOB_LIMIT"
    
    echo ""
    
    # Test 2: Burst behavior
    echo "========================================="
    echo "Test 2: Burst Behavior"
    echo "========================================="
    
    log "Testing Alice burst behavior..."
    alice_burst=$(test_burst_behavior "alice" "local/alice.creds" "$BURST_MESSAGE_COUNT")
    alice_burst_throughput=$(echo "$alice_burst" | cut -d',' -f4)
    echo "Alice burst results: $alice_burst (messages,bytes,duration,throughput)"
    validate_throughput "alice" "$alice_burst_throughput" "$ALICE_LIMIT"
    
    echo ""
    log "Testing Bob burst behavior..."
    bob_burst=$(test_burst_behavior "bob" "local/bob.creds" "$BURST_MESSAGE_COUNT")
    bob_burst_throughput=$(echo "$bob_burst" | cut -d',' -f4)
    echo "Bob burst results: $bob_burst (messages,bytes,duration,throughput)"
    validate_throughput "bob" "$bob_burst_throughput" "$BOB_LIMIT"
    
    echo ""
    
    # Test 3: Concurrent users
    echo "========================================="
    echo "Test 3: Concurrent User Throughput"
    echo "========================================="
    
    test_concurrent_users
    
    echo ""
    
    # Test 4: Direct vs Proxy comparison
    echo "========================================="
    echo "Test 4: Direct vs Proxy Comparison"
    echo "========================================="
    
    log "Testing Alice direct connection (no proxy)..."
    alice_direct=$(measure_throughput "alice" "local/alice.creds" "$DIRECT_SERVER" "5" "$LARGE_MESSAGE_SIZE")
    alice_direct_throughput=$(echo "$alice_direct" | cut -d',' -f4)
    echo "Alice direct results: $alice_direct (messages,bytes,duration,throughput)"
    
    log "Testing Alice through proxy..."
    alice_proxy=$(measure_throughput "alice" "local/alice.creds" "$PROXY_SERVER" "5" "$LARGE_MESSAGE_SIZE")
    alice_proxy_throughput=$(echo "$alice_proxy" | cut -d',' -f4)
    echo "Alice proxy results: $alice_proxy (messages,bytes,duration,throughput)"
    
    if (( $(echo "$alice_proxy_throughput < $alice_direct_throughput" | bc -l) )); then
        success "Proxy successfully limits Alice's throughput"
        log "Reduction: $(echo "scale=2; ($alice_direct_throughput - $alice_proxy_throughput) / $alice_direct_throughput * 100" | bc -l)%"
    else
        warning "Proxy may not be effectively limiting Alice's throughput"
    fi
    
    echo ""
    echo "========================================="
    echo "Test Summary"
    echo "========================================="
    success "Throughput testing completed"
    log "Review the results above to verify rate limiting is working correctly"
}

# Execute main function
main "$@"
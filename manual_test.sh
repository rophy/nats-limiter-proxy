#!/bin/bash

# Manual verification of rate limiting
echo "Manual Rate Limiting Test"
echo "========================"

# Create a 1MB message
echo "Creating 1MB test message..."
MESSAGE=$(python3 -c "print('X' * 1048576)")

echo "Sending 10 x 1MB messages as Alice (should be limited to 5MB/s)..."
echo "Expected time: ~2 seconds for 10MB at 5MB/s limit"

START_TIME=$(date +%s.%N)

for i in {1..10}; do
    echo -n "Message $i... "
    if nats --server=localhost:4223 --creds=local/alice.creds pub "test.manual" "$MESSAGE" 2>/dev/null; then
        echo "sent"
    else
        echo "failed"
        break
    fi
done

END_TIME=$(date +%s.%N)
DURATION=$(echo "$END_TIME - $START_TIME" | bc -l)

echo ""
echo "Results:"
echo "Duration: $(printf "%.2f" $DURATION) seconds"
echo "Data sent: 10MB"
echo "Effective rate: $(echo "scale=2; 10 / $DURATION" | bc -l) MB/s"

if (( $(echo "$DURATION >= 1.5" | bc -l) )); then
    echo "✓ Rate limiting appears to be working (took ≥1.5s)"
else
    echo "⚠ Rate limiting may not be working (completed too quickly)"
fi
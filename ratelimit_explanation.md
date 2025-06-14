# How juju/ratelimit Works

The `juju/ratelimit` package implements a **Token Bucket Algorithm** for rate limiting. Here's how it works in our NATS proxy:

## Token Bucket Algorithm Basics

The token bucket is a simple but powerful rate limiting algorithm:

1. **Bucket**: A container that holds tokens
2. **Tokens**: Represent permission to send data (1 token = 1 byte)
3. **Fill Rate**: Tokens are added to the bucket at a constant rate
4. **Capacity**: Maximum number of tokens the bucket can hold
5. **Consumption**: Each byte of data consumes one token

## How It Works in Our Proxy

### 1. Bucket Creation

```go
// In proxy.go line 162:
limiter := ratelimit.NewBucketWithRate(float64(getBandwidthForUser(user)), getBandwidthForUser(user))
```

This creates a bucket where:
- **Rate**: `getBandwidthForUser(user)` tokens per second (e.g., 5,242,880 for Alice = 5MB/s)
- **Capacity**: Same as rate (allows burst up to 1 second of data)

### 2. Reader Wrapping

```go
// In proxy.go line 163:
limitedReader := ratelimit.Reader(io.MultiReader(buffer, clientConn), limiter)
```

This wraps the client connection with rate limiting:
- Every byte read from the client consumes one token
- If no tokens available, reading blocks until tokens refill

## Example: Alice's 5MB/s Limit

Let's trace what happens when Alice connects:

```
1. Alice connects → proxy creates bucket with:
   - Rate: 5,242,880 tokens/second (5MB/s)
   - Capacity: 5,242,880 tokens
   - Initial tokens: 5,242,880 (bucket starts full)

2. Alice sends data:
   - 64KB message = 65,536 bytes = consumes 65,536 tokens
   - Remaining tokens: 5,242,880 - 65,536 = 5,177,344
   - Message goes through immediately

3. Alice sends more data rapidly:
   - If she tries to send faster than 5MB/s, tokens get depleted
   - When tokens = 0, further reads block
   - Tokens refill at 5,242,880 per second
   - Alice's effective rate is limited to 5MB/s
```

## Token Bucket Behavior

### Burst Handling
```
Initial state: [████████████████████] (full bucket = 5MB worth of tokens)
Large burst:   [████░░░░░░░░░░░░░░░░] (tokens depleted, but some data went through)
Refill:        [██████░░░░░░░░░░░░░░] (tokens slowly refill at 5MB/s rate)
```

### Steady State
```
Steady 5MB/s: [████████████████████] (tokens consumed = tokens refilled)
Under limit:  [████████████████████] (bucket stays full, ready for bursts)
Over limit:   [░░░░░░░░░░░░░░░░░░░░] (bucket empty, reads block until refill)
```

## Key Properties

### 1. **Fairness**
- Each user gets their own bucket
- Rate limiting is independent per user
- No user can affect another's bandwidth

### 2. **Smoothing**
- Allows short bursts up to bucket capacity
- Smooths out irregular traffic patterns
- Provides consistent long-term rate limiting

### 3. **Precision**
- Sub-second accuracy (though limited by clock resolution)
- Granular control (per-byte precision)
- Real-time rate adaptation

## Configuration in Our Proxy

```yaml
# config.yaml
users:
  alice: 5242880   # 5MB/s = 5,242,880 bytes/s
  bob: 2097152     # 2MB/s = 2,097,152 bytes/s
```

Each user gets:
```go
// Bucket configuration:
rate := getBandwidthForUser(user)     // bytes per second
capacity := rate                      // same as rate (1 second of burst)
bucket := NewBucketWithRate(float64(rate), capacity)
```

## Advantages of Token Bucket

✅ **Burst-friendly**: Allows brief periods above the rate limit
✅ **Smooth**: Provides consistent long-term rate limiting  
✅ **Efficient**: Low overhead, no complex queuing
✅ **Real-time**: Immediate response to rate changes
✅ **Simple**: Easy to understand and configure

## Alternative Algorithms

The token bucket is chosen over alternatives because:

- **Leaky Bucket**: More rigid, doesn't allow bursts
- **Fixed Window**: Can have boundary effects
- **Sliding Window**: More complex, higher memory usage

Token bucket provides the best balance of simplicity, efficiency, and flexibility for network rate limiting.
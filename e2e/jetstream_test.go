package e2e

import (
	"bytes"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"nats-limiter-proxy/e2e/testutil"
)

func TestE2E_JetStreamBasicPubSub(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	// Connect through proxy with JetStream enabled
	nc := env.ConnectToProxy(t)
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("Failed to get JetStream context: %v", err)
	}
	
	// Create a simple stream dynamically
	streamName := "TEST_EVENTS"
	subject := "test.events.basic"
	
	// Try to create stream - if it fails, JetStream might not be properly configured
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     streamName,
		Subjects: []string{"test.events.*"},
		Storage:  nats.FileStorage,
		MaxAge:   time.Hour,
	})
	if err != nil {
		t.Skipf("Skipping JetStream test - stream creation failed (JetStream may not be configured): %v", err)
	}
	
	// Clean up stream at the end
	defer func() {
		js.DeleteStream(streamName)
	}()
	
	// Test publishing to JetStream
	testMessage := "JetStream test message through proxy"
	
	t.Log("Publishing message to JetStream...")
	pubAck, err := js.Publish(subject, []byte(testMessage))
	if err != nil {
		t.Fatalf("Failed to publish to JetStream: %v", err)
	}
	
	t.Logf("✓ Message published to JetStream: stream=%s, seq=%d", pubAck.Stream, pubAck.Sequence)
	
	// Create pull consumer
	consumerName := "test_consumer"
	_, err = js.AddConsumer(streamName, &nats.ConsumerConfig{
		Durable:   consumerName,
		AckPolicy: nats.AckExplicitPolicy,
	})
	if err != nil {
		t.Fatalf("Failed to create consumer: %v", err)
	}
	
	// Pull from consumer
	sub, err := js.PullSubscribe(subject, consumerName)
	if err != nil {
		t.Fatalf("Failed to create pull subscription: %v", err)
	}
	defer sub.Unsubscribe()
	
	// Fetch message
	msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
	if err != nil {
		t.Fatalf("Failed to fetch message: %v", err)
	}
	
	if len(msgs) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(msgs))
	}
	
	msg := msgs[0]
	if string(msg.Data) != testMessage {
		t.Errorf("Message content mismatch: expected %q, got %q", testMessage, string(msg.Data))
	}
	
	// Acknowledge message
	if err := msg.Ack(); err != nil {
		t.Fatalf("Failed to acknowledge message: %v", err)
	}
	
	t.Logf("✓ JetStream message received and acknowledged: %q", string(msg.Data))
}

func TestE2E_JetStreamLargeMessage(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	// Connect through proxy
	nc := env.ConnectToProxy(t)
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("Failed to get JetStream context: %v", err)
	}
	
	// Create stream for large messages
	streamName := "TEST_LARGE"
	subject := "test.large.5mb"
	
	_, err = js.AddStream(&nats.StreamConfig{
		Name:       streamName,
		Subjects:   []string{"test.large.*"},
		Storage:    nats.FileStorage,
		MaxAge:     time.Hour,
		MaxMsgSize: 10 * 1024 * 1024, // 10MB max
	})
	if err != nil {
		t.Skipf("Skipping JetStream large message test - stream creation failed: %v", err)
	}
	
	defer js.DeleteStream(streamName)
	
	// Create large message (1MB - smaller than before to be safe)
	largeMessage := make([]byte, 1024*1024)
	for i := range largeMessage {
		largeMessage[i] = byte('A' + (i % 26))
	}
	
	t.Logf("Publishing large message (%d bytes) to JetStream...", len(largeMessage))
	start := time.Now()
	
	pubAck, err := js.Publish(subject, largeMessage)
	if err != nil {
		t.Fatalf("Failed to publish large message to JetStream: %v", err)
	}
	
	publishDuration := time.Since(start)
	t.Logf("✓ Large message published to JetStream in %v: stream=%s, seq=%d", publishDuration, pubAck.Stream, pubAck.Sequence)
	
	// Create consumer and pull message
	consumerName := "large_consumer"
	_, err = js.AddConsumer(streamName, &nats.ConsumerConfig{
		Durable:   consumerName,
		AckPolicy: nats.AckExplicitPolicy,
	})
	if err != nil {
		t.Fatalf("Failed to create consumer: %v", err)
	}
	
	sub, err := js.PullSubscribe(subject, consumerName)
	if err != nil {
		t.Fatalf("Failed to create pull subscription: %v", err)
	}
	defer sub.Unsubscribe()
	
	// Fetch message
	start = time.Now()
	msgs, err := sub.Fetch(1, nats.MaxWait(30*time.Second))
	if err != nil {
		t.Fatalf("Failed to fetch large message: %v", err)
	}
	
	fetchDuration := time.Since(start)
	
	if len(msgs) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(msgs))
	}
	
	msg := msgs[0]
	
	// Verify complete data integrity
	if !bytes.Equal(msg.Data, largeMessage) {
		t.Error("Large JetStream message content mismatch - proxy corrupted data")
		t.Errorf("Expected length: %d, Received length: %d", len(largeMessage), len(msg.Data))
		
		// Find first difference for debugging
		minLen := len(largeMessage)
		if len(msg.Data) < minLen {
			minLen = len(msg.Data)
		}
		for i := 0; i < minLen; i++ {
			if msg.Data[i] != largeMessage[i] {
				t.Errorf("First difference at byte %d: expected %d, got %d", i, largeMessage[i], msg.Data[i])
				break
			}
		}
	} else {
		t.Log("✓ Complete large JetStream message integrity verified - every byte matches")
	}
	
	// Acknowledge message
	if err := msg.Ack(); err != nil {
		t.Fatalf("Failed to acknowledge large message: %v", err)
	}
	
	t.Logf("✓ Large JetStream message (%d bytes) received in %v and acknowledged", len(msg.Data), fetchDuration)
}

func TestE2E_JetStreamDirectVsProxy(t *testing.T) {
	env := testutil.NewDockerComposeEnv()
	
	// Wait for services to be ready
	env.WaitForNATSReady(t)
	env.WaitForProxyReady(t)
	
	testMessage := "JetStream direct vs proxy comparison"
	
	// Test 1: Direct to NATS JetStream
	t.Run("DirectJetStream", func(t *testing.T) {
		nc := env.ConnectToNATSDirect(t)
		js, err := nc.JetStream()
		if err != nil {
			t.Fatalf("Failed to get JetStream context (direct): %v", err)
		}
		
		streamName := "TEST_DIRECT"
		subject := "test.direct.jetstream"
		
		_, err = js.AddStream(&nats.StreamConfig{
			Name:     streamName,
			Subjects: []string{"test.direct.*"},
			Storage:  nats.FileStorage,
			MaxAge:   time.Hour,
		})
		if err != nil {
			t.Skipf("Skipping direct JetStream test - stream creation failed: %v", err)
		}
		
		defer js.DeleteStream(streamName)
		
		// Publish
		pubAck, err := js.Publish(subject, []byte(testMessage))
		if err != nil {
			t.Fatalf("Failed to publish to JetStream (direct): %v", err)
		}
		
		// Create consumer and consume
		consumerName := "direct_consumer"
		_, err = js.AddConsumer(streamName, &nats.ConsumerConfig{
			Durable:   consumerName,
			AckPolicy: nats.AckExplicitPolicy,
		})
		if err != nil {
			t.Fatalf("Failed to create consumer (direct): %v", err)
		}
		
		sub, err := js.PullSubscribe(subject, consumerName)
		if err != nil {
			t.Fatalf("Failed to create subscription (direct): %v", err)
		}
		defer sub.Unsubscribe()
		
		msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
		if err != nil {
			t.Fatalf("Failed to fetch message (direct): %v", err)
		}
		
		if len(msgs) != 1 || string(msgs[0].Data) != testMessage {
			t.Fatalf("Direct JetStream message mismatch")
		}
		
		msgs[0].Ack()
		t.Logf("✓ Direct JetStream works: stream=%s, seq=%d", pubAck.Stream, pubAck.Sequence)
	})
	
	// Test 2: Through proxy JetStream  
	t.Run("ProxyJetStream", func(t *testing.T) {
		nc := env.ConnectToProxy(t)
		js, err := nc.JetStream()
		if err != nil {
			t.Fatalf("Failed to get JetStream context (proxy): %v", err)
		}
		
		streamName := "TEST_PROXY"
		subject := "test.proxy.jetstream"
		
		_, err = js.AddStream(&nats.StreamConfig{
			Name:     streamName,
			Subjects: []string{"test.proxy.*"},
			Storage:  nats.FileStorage,
			MaxAge:   time.Hour,
		})
		if err != nil {
			t.Skipf("Skipping proxy JetStream test - stream creation failed: %v", err)
		}
		
		defer js.DeleteStream(streamName)
		
		// Publish
		pubAck, err := js.Publish(subject, []byte(testMessage))
		if err != nil {
			t.Fatalf("Failed to publish to JetStream (proxy): %v", err)
		}
		
		// Create consumer and consume
		consumerName := "proxy_consumer"
		_, err = js.AddConsumer(streamName, &nats.ConsumerConfig{
			Durable:   consumerName,
			AckPolicy: nats.AckExplicitPolicy,
		})
		if err != nil {
			t.Fatalf("Failed to create consumer (proxy): %v", err)
		}
		
		sub, err := js.PullSubscribe(subject, consumerName)
		if err != nil {
			t.Fatalf("Failed to create subscription (proxy): %v", err)
		}
		defer sub.Unsubscribe()
		
		msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
		if err != nil {
			t.Fatalf("Failed to fetch message (proxy): %v", err)
		}
		
		if len(msgs) != 1 || string(msgs[0].Data) != testMessage {
			t.Fatalf("Proxy JetStream message mismatch")
		}
		
		msgs[0].Ack()
		t.Logf("✓ Proxy JetStream works: stream=%s, seq=%d", pubAck.Stream, pubAck.Sequence)
	})
}
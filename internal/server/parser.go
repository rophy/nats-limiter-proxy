// Copyright 2012-2025 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/juju/ratelimit"
	"github.com/rs/zerolog/log"
)

type parserState int

// Parser constants
const (
	OP_START parserState = iota
	OP_C
	OP_CO
	OP_CON
	OP_CONN
	OP_CONNE
	OP_CONNEC
	OP_CONNECT
	CONNECT_ARG
	OP_H
	OP_HP
	OP_HPU
	OP_HPUB
	OP_HPUB_SPC
	HPUB_ARG
	OP_HM
	OP_HMS
	OP_HMSG
	OP_HMSG_SPC
	HMSG_ARG
	OP_P
	OP_PU
	OP_PUB
	OP_PUB_SPC
	PUB_ARG
	OP_PI
	OP_PIN
	OP_PING
	OP_PO
	OP_PON
	OP_PONG
	MSG_PAYLOAD
	MSG_END_R
	MSG_END_N
	OP_S
	OP_SU
	OP_SUB
	OP_SUB_SPC
	SUB_ARG
	OP_A
	OP_ASUB
	OP_ASUB_SPC
	ASUB_ARG
	OP_AUSUB
	OP_AUSUB_SPC
	AUSUB_ARG
	OP_L
	OP_LS
	OP_R
	OP_RS
	OP_U
	OP_UN
	OP_UNS
	OP_UNSU
	OP_UNSUB
	OP_UNSUB_SPC
	UNSUB_ARG
	OP_M
	OP_MS
	OP_MSG
	OP_MSG_SPC
	MSG_ARG
	OP_I
	OP_IN
	OP_INF
	OP_INFO
	INFO_ARG
	OP_IGNORE
)

// RateLimiterManagerInterface defines the interface for dual rate limiter management
type RateLimiterManagerInterface interface {
	GetLocalLimiter(username string) *ratelimit.Bucket
	GetGlobalLimiter(username string) *ratelimit.Bucket
}

// UsageReporter defines the interface for tracking usage statistics
type UsageReporter interface {
	TrackUsage(username string, bytesUsed int64)
}

// RateLimitedWriter wraps an io.Writer and applies dual rate limiting to all writes
type RateLimitedWriter struct {
	writer             io.Writer
	localRateLimiter   *ratelimit.Bucket  // Static local rate limiter
	rateLimiterManager RateLimiterManagerInterface
	username           string
	metrics            MetricsCollector
}

// NewRateLimitedWriter creates a new rate-limited writer
func NewRateLimitedWriter(w io.Writer, rateLimiterManager RateLimiterManagerInterface, metrics MetricsCollector) *RateLimitedWriter {
	return &RateLimitedWriter{
		writer:             w,
		rateLimiterManager: rateLimiterManager,
		metrics:            metrics,
	}
}

// Write applies dual rate limiting (local AND global) and writes data to the underlying writer
func (rlw *RateLimitedWriter) Write(data []byte) (int, error) {
	dataLen := int64(len(data))
	
	// Apply local rate limiting (always available)
	if rlw.localRateLimiter != nil {
		rlw.localRateLimiter.Wait(dataLen)
	}
	
	// Apply global rate limiting (get current bucket each time for real-time updates)
	if rlw.rateLimiterManager != nil && rlw.username != "" {
		if globalLimiter := rlw.rateLimiterManager.GetGlobalLimiter(rlw.username); globalLimiter != nil {
			globalLimiter.Wait(dataLen)
		}
	}
	
	// Write the data
	n, err := rlw.writer.Write(data)
	
	// Track actual bytes written for coordination
	if err == nil && n > 0 && rlw.rateLimiterManager != nil && rlw.username != "" {
		// Check if the rate limiter manager supports usage tracking
		if tracker, ok := rlw.rateLimiterManager.(UsageReporter); ok {
			tracker.TrackUsage(rlw.username, int64(n))
		}
	}
	
	// Note: bytes_sent metrics are now recorded in copyWithMetrics for upstream->client flow
	
	return n, err
}

// UpdateLocalRateLimiter updates the local rate limiter
func (rlw *RateLimitedWriter) UpdateLocalRateLimiter(rateLimiter *ratelimit.Bucket) {
	rlw.localRateLimiter = rateLimiter
}

// UpdateUser updates the username for usage tracking
func (rlw *RateLimitedWriter) UpdateUser(username string) {
	rlw.username = username
}

// AuthState represents the authentication state of a connection
type AuthState int

const (
	AuthStateUnauthenticated AuthState = iota // Initial state - no authentication yet
	AuthStateConnectSent                      // CONNECT message sent, waiting for response
	AuthStateAuthenticated                    // Successfully authenticated
	AuthStateFailed                           // Authentication failed
)

// ClientMessageParser parses and forwards NATS protocol data efficiently for proxying.
type ClientMessageParser struct {
	clientReader *bufio.Reader
	serverWriter *RateLimitedWriter

	state              parserState
	as                 int
	drop               int
	rateLimiterManager RateLimiterManagerInterface
	metrics            MetricsCollector

	user      string
	authState AuthState

	// Fixed-size buffer for memory efficiency in high-throughput scenarios
	buffer    [4096]byte // Fixed buffer - no growth
	bufferPos int        // Current position in buffer

}

// NewClientMessageParser creates a new ClientMessageParser instance
func NewClientMessageParser(
	clientReader io.Reader,
	serverWriter io.Writer,
	rateLimiterManager RateLimiterManagerInterface,
	metrics MetricsCollector,
) *ClientMessageParser {
	// Create rate limited writer with anonymous rate limiting initially
	rateLimitedWriter := NewRateLimitedWriter(serverWriter, rateLimiterManager, metrics)
	
	// Set up anonymous rate limiting using default limits
	if rateLimiterManager != nil {
		anonymousLimiter := rateLimiterManager.GetLocalLimiter("<unauthenticated>")
		if anonymousLimiter != nil {
			rateLimitedWriter.UpdateLocalRateLimiter(anonymousLimiter)
		}
		rateLimitedWriter.UpdateUser("<unauthenticated>")
	}
	
	return &ClientMessageParser{
		clientReader:       bufio.NewReader(clientReader),
		serverWriter:       rateLimitedWriter,
		state:              OP_START,
		rateLimiterManager: rateLimiterManager,
		metrics:            metrics,
		authState:          AuthStateUnauthenticated,
		bufferPos:          0, // Start with empty buffer
	}
}

func (c *ClientMessageParser) ParseAndForward() error {
	reader := c.clientReader

	for {
		b, err := reader.ReadByte()
		if err != nil {
			if err == io.EOF {
				// Flush any remaining data in buffer
				if c.bufferPos > 0 {
					_, writeErr := c.serverWriter.Write(c.buffer[:c.bufferPos])
					if writeErr != nil {
						return writeErr
					}
					c.bufferPos = 0
				}
				return nil
			}
			return err
		}

		// Record bytes received FROM client (client->proxy requests)
		if c.metrics != nil {
			c.metrics.RecordBytesReceived(c.user, 1)
		}

		// Add byte to buffer
		if c.bufferPos >= 4096 {
			// Buffer full - flush it with rate limiting
			_, err = c.serverWriter.Write(c.buffer[:])
			if err != nil {
				return err
			}
			c.bufferPos = 0
		}

		c.buffer[c.bufferPos] = b
		c.bufferPos++

		switch c.state {
		case OP_START:
			switch b {
			case 'P', 'p':
				c.state = OP_P
			case 'H', 'h':
				c.state = OP_H
			case 'C', 'c':
				c.state = OP_C
			default:
				c.state = OP_IGNORE
			}
		case OP_H:
			switch b {
			case 'P', 'p':
				c.state = OP_HP
			default:
				c.state = OP_IGNORE
			}
		case OP_HP:
			switch b {
			case 'U', 'u':
				c.state = OP_HPU
			default:
				c.state = OP_IGNORE
			}
		case OP_HPU:
			switch b {
			case 'B', 'b':
				c.state = OP_HPUB
			default:
				c.state = OP_IGNORE
			}
		case OP_HPUB:
			switch b {
			case ' ', '\t':
				c.state = OP_IGNORE
				// Record message received (HPUB command)
				if c.metrics != nil {
					c.metrics.RecordMessageReceived(c.user)
				}
			default:
				c.state = OP_IGNORE
			}
		case OP_P:
			switch b {
			case 'U', 'u':
				c.state = OP_PU
			default:
				c.state = OP_IGNORE
			}
		case OP_PU:
			switch b {
			case 'B', 'b':
				c.state = OP_PUB
			default:
				c.state = OP_IGNORE
			}
		case OP_PUB:
			switch b {
			case ' ', '\t':
				c.state = OP_IGNORE
				// Record message received (PUB command)
				if c.metrics != nil {
					c.metrics.RecordMessageReceived(c.user)
				}
			default:
				c.state = OP_IGNORE
			}
		case OP_C:
			switch b {
			case 'O', 'o':
				c.state = OP_CO
			default:
				c.state = OP_IGNORE
			}
		case OP_CO:
			switch b {
			case 'N', 'n':
				c.state = OP_CON
			default:
				c.state = OP_IGNORE
			}
		case OP_CON:
			switch b {
			case 'N', 'n':
				c.state = OP_CONN
			default:
				c.state = OP_IGNORE
			}
		case OP_CONN:
			switch b {
			case 'E', 'e':
				c.state = OP_CONNE
			default:
				c.state = OP_IGNORE
			}
		case OP_CONNE:
			switch b {
			case 'C', 'c':
				c.state = OP_CONNEC
			default:
				c.state = OP_IGNORE
			}
		case OP_CONNEC:
			switch b {
			case 'T', 't':
				c.state = OP_CONNECT
			default:
				c.state = OP_IGNORE
			}
		case OP_CONNECT:
			switch b {
			case ' ', '\t':
				// do nothing.
			default:
				c.state = CONNECT_ARG
				c.as = c.bufferPos - 1
			}
		case CONNECT_ARG:
			switch b {
			case '\r':
				c.drop = 1
			case '\n':
				if c.drop > 0 {
					// Extract CONNECT argument from current buffer data
					// Note: For CONNECT, we assume the entire message fits in buffer
					// since CONNECT messages are typically small
					var arg []byte
					if c.as < c.bufferPos-2 {
						arg = c.buffer[c.as : c.bufferPos-2]
					}

					var obj map[string]interface{}
					if len(arg) > 0 && json.Unmarshal(arg, &obj) == nil {
						user := c.extractUserFromConnect(obj)
						if user != "" {
							// Store the user but don't set up rate limiting yet
							// Wait for authentication confirmation from server
							c.user = user
							c.authState = AuthStateConnectSent
							log.Info().Str("user", user).Msg("CONNECT message processed, waiting for authentication")
						}
					}
					c.drop, c.state = 0, OP_START
				}
			}
		case OP_IGNORE:
			// Continue processing but don't change state
		}

		if c.drop == 0 && b == '\r' {
			c.drop = 1
		}
		if c.drop == 1 && b == '\n' {
			c.drop, c.state = 0, OP_START
			// Message boundary reached - flush buffer to ensure message integrity
			_, err = c.serverWriter.Write(c.buffer[:c.bufferPos])
			if err != nil {
				return err
			}
			c.bufferPos = 0 // Reset buffer for next message
		}

	}
}

func (c *ClientMessageParser) processUser(user string) {
	if c.user != "" {
		log.Warn().Str("oldUser", c.user).Str("newUser", user).Msg("User already authenticated, cannot re-authenticate")
		if c.metrics != nil {
			c.metrics.RecordAuthFailure(user)
		}
		return
	}
	log.Info().Str("user", user).Msg("User authenticated")
	c.user = user
	
	// Record successful authentication
	if c.metrics != nil {
		c.metrics.RecordAuthentication(user)
	}
	
	if c.rateLimiterManager != nil {
		localLimiter := c.rateLimiterManager.GetLocalLimiter(user)
		// Note: We don't cache global limiter anymore - it's looked up dynamically
		c.serverWriter.UpdateLocalRateLimiter(localLimiter)
		c.serverWriter.UpdateUser(user)
		
		// Notify that user has connected (for global tracking)
		if combined, ok := c.rateLimiterManager.(*CombinedRateLimiter); ok {
			combined.UserConnected(user)
		}
	}

}

// switchToUserRateLimiter switches from anonymous to user-specific rate limiting after authentication success
func (c *ClientMessageParser) switchToUserRateLimiter(user string) {
	if c.rateLimiterManager != nil {
		// Get user-specific rate limiter
		localLimiter := c.rateLimiterManager.GetLocalLimiter(user)
		c.serverWriter.UpdateLocalRateLimiter(localLimiter)
		c.serverWriter.UpdateUser(user)
		
		// Notify that user has connected (for global tracking)
		if combined, ok := c.rateLimiterManager.(*CombinedRateLimiter); ok {
			combined.UserConnected(user)
		}
	}
}

// extractUserFromConnect extracts user identifier from CONNECT message for all auth methods
func (c *ClientMessageParser) extractUserFromConnect(obj map[string]interface{}) string {
	// Username/password authentication or TLS certificate mapped user
	if user, ok := obj["user"].(string); ok {
		return user
	}
	
	// JWT authentication - extract username from token claims
	if jwtToken, ok := obj["jwt"].(string); ok {
		return c.extractUsernameFromJWT(jwtToken)
	}
	
	// NKey authentication - use NKey public key as user identifier
	if nkey, ok := obj["nkey"].(string); ok {
		return nkey
	}
	
	// Token authentication - use token value as user identifier
	if token, ok := obj["auth_token"].(string); ok {
		return token
	}
	
	return ""
}

func (c *ClientMessageParser) extractUsernameFromJWT(jwtToken string) string {
	// Parse JWT without verification since we just need to extract claims
	token, _ := jwt.ParseWithClaims(jwtToken, jwt.MapClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Return nil to skip signature verification - we just need the claims
		return nil, nil
	})

	// Even with signature verification errors, we can still extract claims
	if token != nil {
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			if name, exists := claims["name"]; exists {
				if nameStr, ok := name.(string); ok {
					return nameStr
				}
			}
			if sub, exists := claims["sub"]; exists {
				if subStr, ok := sub.(string); ok {
					return subStr
				}
			}
		}
	}

	return ""
}

// GetUser returns the authenticated user name, or empty string if not authenticated
func (c *ClientMessageParser) GetUser() string {
	return c.user
}

// GetAuthenticatedUser returns the authenticated user name, or empty string if not authenticated
func (c *ClientMessageParser) GetAuthenticatedUser() string {
	return c.user
}

// Disconnect logs user disconnection for all connections
func (c *ClientMessageParser) Disconnect() {
	if c.user != "" {
		log.Info().Str("user", c.user).Msg("User disconnected")
		
		// Notify that user has disconnected (for global tracking)
		if c.rateLimiterManager != nil {
			if combined, ok := c.rateLimiterManager.(*CombinedRateLimiter); ok {
				combined.UserDisconnected(c.user)
			}
		}
	} else {
		log.Info().Msg("Client disconnected")
	}
}

// ServerResponseParser monitors server responses to detect authentication state
type ServerResponseParser struct {
	clientParser *ClientMessageParser
	buffer       [1024]byte
	bufferPos    int
}

// NewServerResponseParser creates a server response parser
func NewServerResponseParser(clientParser *ClientMessageParser) *ServerResponseParser {
	return &ServerResponseParser{
		clientParser: clientParser,
	}
}

// ParseResponse parses server responses and updates authentication state
func (s *ServerResponseParser) ParseResponse(data []byte) {
	// Only monitor responses if we're waiting for authentication
	if s.clientParser.authState != AuthStateConnectSent {
		return
	}
	
	// Add data to buffer
	for _, b := range data {
		if s.bufferPos < len(s.buffer) {
			s.buffer[s.bufferPos] = b
			s.bufferPos++
			
			// Check for complete line (CRLF)
			if s.bufferPos >= 2 && s.buffer[s.bufferPos-2] == '\r' && s.buffer[s.bufferPos-1] == '\n' {
				line := string(s.buffer[:s.bufferPos-2])
				s.processServerResponse(line)
				s.bufferPos = 0 // Reset buffer
			}
		} else {
			// Buffer full, reset
			s.bufferPos = 0
		}
	}
}

// processServerResponse analyzes server response and updates authentication state
func (s *ServerResponseParser) processServerResponse(line string) {
	// Check for authentication error
	if strings.HasPrefix(line, "-ERR 'Authorization Violation'") || 
	   strings.HasPrefix(line, "-ERR 'Authentication") {
		log.Info().
			Str("user", s.clientParser.user).
			Str("response", line).
			Msg("Authentication failed")
		
		s.clientParser.authState = AuthStateFailed
		return
	}
	
	// Check for any non-error response (indicates success)
	if !strings.HasPrefix(line, "-ERR ") && line != "" {
		// Authentication successful - switch to user-specific rate limiting
		log.Info().
			Str("user", s.clientParser.user).
			Str("response", line).
			Msg("Authentication successful, switching to user-specific rate limiting")
		
		s.clientParser.authState = AuthStateAuthenticated
		s.clientParser.switchToUserRateLimiter(s.clientParser.user)
	}
}

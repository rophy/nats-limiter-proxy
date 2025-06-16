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

	"github.com/golang-jwt/jwt/v5"
	"github.com/juju/ratelimit"
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

// RateLimiterManagerInterface defines the interface for rate limiter management
type RateLimiterManagerInterface interface {
	GetLimiter(username string) *ratelimit.Bucket
}

// RateLimitedWriter wraps an io.Writer and applies rate limiting to all writes
type RateLimitedWriter struct {
	writer      io.Writer
	rateLimiter *ratelimit.Bucket
}

// NewRateLimitedWriter creates a new rate-limited writer
func NewRateLimitedWriter(w io.Writer) *RateLimitedWriter {
	return &RateLimitedWriter{
		writer: w,
	}
}

// Write applies rate limiting and writes data to the underlying writer
func (rlw *RateLimitedWriter) Write(data []byte) (int, error) {
	if rlw.rateLimiter != nil {
		// Apply rate limiting for each byte
		rlw.rateLimiter.Wait(int64(len(data)))
	}
	return rlw.writer.Write(data)
}

// UpdateRateLimiter updates the rate limiter (e.g., when user changes)
func (rlw *RateLimitedWriter) UpdateRateLimiter(rateLimiter *ratelimit.Bucket) {
	rlw.rateLimiter = rateLimiter
}

// ClientMessageParser parses and forwards NATS protocol data efficiently for proxying.
type ClientMessageParser struct {
	clientReader *bufio.Reader
	serverWriter *RateLimitedWriter

	state               parserState
	as                  int
	drop                int
	rateLimiterManager  RateLimiterManagerInterface
	onUserAuthenticated func(user string)

	// Fixed-size buffer for memory efficiency in high-throughput scenarios
	buffer    [4096]byte // Fixed buffer - no growth
	bufferPos int        // Current position in buffer

}

// NewClientMessageParser creates a new ClientMessageParser instance
func NewClientMessageParser(
	clientReader io.Reader,
	serverWriter io.Writer,
	rateLimiterManager RateLimiterManagerInterface,
	onUserAuthenticated func(user string),
) *ClientMessageParser {
	return &ClientMessageParser{
		clientReader:        bufio.NewReader(clientReader),
		serverWriter:        NewRateLimitedWriter(serverWriter),
		state:               OP_START,
		rateLimiterManager:  rateLimiterManager,
		onUserAuthenticated: onUserAuthenticated,
		bufferPos:           0, // Start with empty buffer
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
						if user, ok := obj["user"].(string); ok {
							c.processUser(user)
						} else if jwtToken, ok := obj["jwt"].(string); ok {
							// Check for JWT authentication
							user := c.extractUsernameFromJWT(jwtToken)
							if user != "" {
								c.processUser(user)
							}
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
	if c.rateLimiterManager != nil {
		rateLimiter := c.rateLimiterManager.GetLimiter(user)
		c.serverWriter.UpdateRateLimiter(rateLimiter)
	}
	if c.onUserAuthenticated != nil {
		c.onUserAuthenticated(user)
	}
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

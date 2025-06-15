// Copyright 2012-2024 The NATS Authors
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
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Minimal parser state constants for proxy use
const (
	psOpStart = iota
	psOpPub
	psOpPubSpc
	psPubArg
	psOpHPub
	psOpHPubSpc
	psHPubArg
	psMsgPayload
)

// NATSProxyParser parses and forwards NATS protocol data efficiently for proxying.
type NATSProxyParser struct {
	state   int
	argBuf  []byte
	msgBuf  []byte
	as      int
	drop    int
	payload int
	LogFunc func(direction string, line string, user string)
}

func extractUsernameFromJWT(jwtToken string) string {
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

// Reset resets the parser state.
func (p *NATSProxyParser) Reset() {
	p.state = psOpStart
	p.argBuf = nil
	p.msgBuf = nil
	p.as = 0
	p.drop = 0
	p.payload = 0
}

// ParseAndForward reads from r, parses NATS protocol, and writes all bytes to w.
func (p *NATSProxyParser) ParseAndForward(r io.Reader, w io.Writer, direction string) error {
	reader := bufio.NewReader(r)
	buf := make([]byte, 0, 4096)
	var username string // cache the authenticated user

	for {
		b, err := reader.ReadByte()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		buf = append(buf, b)

		switch p.state {
		case psOpStart:
			if b == 'P' || b == 'p' {
				p.state = psOpPub
				p.as = len(buf) - 1
			} else if b == 'H' || b == 'h' {
				p.state = psOpHPub
				p.as = len(buf) - 1
			} else if b == '\n' {
				// End of line, flush
				if _, err := w.Write(buf); err != nil {
					return err
				}
				// Log the direction and line after parsing a line (for both PUB and non-PUB lines)
				if p.LogFunc != nil {
					line := string(buf)
					
					// Detect CONNECT and cache user
					if strings.HasPrefix(strings.TrimSpace(line), "CONNECT ") {
						var obj map[string]interface{}
						jsonStr := strings.TrimSpace(line)[8:]
						if err := json.Unmarshal([]byte(jsonStr), &obj); err == nil {
							// Check for traditional username/password authentication
							if user, ok := obj["user"].(string); ok {
								username = user
							} else if jwtToken, ok := obj["jwt"].(string); ok {
								// Check for JWT authentication
								user := extractUsernameFromJWT(jwtToken)
								if user != "" {
									username = user
								}
							}
						}
					}
					
					p.LogFunc(direction, line, username)
				}
				buf = buf[:0]
			}
		case psOpPub:
			if b == 'U' || b == 'u' {
				p.state = psOpPubSpc
			} else {
				p.state = psOpStart
			}
		case psOpPubSpc:
			if b == ' ' || b == '\t' {
				// Still in spaces
			} else {
				p.state = psPubArg
				p.as = len(buf) - 1
			}
		case psPubArg:
			if b == '\n' {
				// Parse size from PUB args
				line := buf[p.as : len(buf)-1]
				parts := bytes.Fields(line)
				if len(parts) < 2 {
					p.state = psOpStart
					break
				}
				sizeStr := string(parts[len(parts)-1])
				size, err := strconv.Atoi(sizeStr)
				if err != nil || size < 0 {
					p.state = psOpStart
					break
				}
				p.payload = size
				p.state = psMsgPayload
			}
		case psOpHPub:
			if b == 'P' || b == 'p' {
				p.state = psOpHPubSpc
			} else {
				p.state = psOpStart
			}
		case psOpHPubSpc:
			if b == ' ' || b == '\t' {
				// Still in spaces
			} else {
				p.state = psHPubArg
				p.as = len(buf) - 1
			}
		case psHPubArg:
			if b == '\n' {
				// Parse size from HPUB args
				line := buf[p.as : len(buf)-1]
				parts := bytes.Fields(line)
				if len(parts) < 3 {
					p.state = psOpStart
					break
				}
				sizeStr := string(parts[len(parts)-1])
				size, err := strconv.Atoi(sizeStr)
				if err != nil || size < 0 {
					p.state = psOpStart
					break
				}
				p.payload = size
				p.state = psMsgPayload
			}
		case psMsgPayload:
			if len(buf) >= p.as+p.payload+2 { // +2 for \r\n
				// Got full payload
				if _, err := w.Write(buf); err != nil {
					return err
				}
				// Log with user context
				if p.LogFunc != nil {
					p.LogFunc(direction, "", username)
				}
				buf = buf[:0]
				p.state = psOpStart
			}
		}
	}
}

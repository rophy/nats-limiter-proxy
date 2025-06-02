package main

import (
	"fmt"
	"net"
	"os"

	"nats-limiter-proxy/server"

	"github.com/juju/ratelimit"
)

const (
	localPort = 4223
)

var (
	upstreamHost string
	upstreamPort int
)

func init() {
	upstreamHost = os.Getenv("UPSTREAM_HOST")
	if upstreamHost == "" {
		fmt.Fprintln(os.Stderr, "Environment variable UPSTREAM_HOST is required")
		os.Exit(1)
	}
	portStr := os.Getenv("UPSTREAM_PORT")
	if portStr == "" {
		fmt.Fprintln(os.Stderr, "Environment variable UPSTREAM_PORT is required")
		os.Exit(1)
	}
	_, err := fmt.Sscanf(portStr, "%d", &upstreamPort)
	if err != nil || upstreamPort <= 0 || upstreamPort > 65535 {
		fmt.Fprintln(os.Stderr, "Invalid UPSTREAM_PORT value")
		os.Exit(1)
	}
}

func handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	upstreamConn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", upstreamHost, upstreamPort))
	if err != nil {
		fmt.Println("Failed to connect to upstream:", err)
		return
	}
	defer upstreamConn.Close()

	const bandwidth = 10024 * 1024 // bytes per second

	clientToUpstreamBucket := ratelimit.NewBucketWithRate(float64(bandwidth), int64(bandwidth))
	upstreamToClientBucket := ratelimit.NewBucketWithRate(float64(bandwidth), int64(bandwidth))

	limitedClientReader := ratelimit.Reader(clientConn, clientToUpstreamBucket)
	limitedUpstreamReader := ratelimit.Reader(upstreamConn, upstreamToClientBucket)
	// Client -> Upstream
	go func() {
		parser := server.NATSProxyParser{
			LogFunc: func(direction, line string) {
				fmt.Printf("C->S: %s", line)
			},
		}
		parser.ParseAndForward(limitedClientReader, upstreamConn, "C->S")
	}()

	// Upstream -> Client
	parser := server.NATSProxyParser{
		LogFunc: func(direction, line string) {
			fmt.Printf("S->C: %s", line)
		},
	}
	parser.ParseAndForward(limitedUpstreamReader, clientConn, "S->C")
}

func main() {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", localPort))
	if err != nil {
		fmt.Println("Failed to listen:", err)
		return
	}
	fmt.Printf("NATS proxy (TCP) listening on port %d\n", localPort)
	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Accept error:", err)
			continue
		}
		go handleConnection(conn)
	}
}

package main

import (
	"fmt"
	"log"
	"os"

	"nats-limiter-proxy/internal/server"
)

const (
	localPort = 4223
)

func main() {
	upstreamHost := os.Getenv("UPSTREAM_HOST")
	if upstreamHost == "" {
		log.Fatal("Environment variable UPSTREAM_HOST is required")
	}
	
	portStr := os.Getenv("UPSTREAM_PORT")
	if portStr == "" {
		log.Fatal("Environment variable UPSTREAM_PORT is required")
	}
	
	var upstreamPort int
	_, err := fmt.Sscanf(portStr, "%d", &upstreamPort)
	if err != nil || upstreamPort <= 0 || upstreamPort > 65535 {
		log.Fatal("Invalid UPSTREAM_PORT value")
	}

	proxy, err := server.NewProxy(upstreamHost, upstreamPort, "config.yaml")
	if err != nil {
		log.Fatal("Failed to create proxy:", err)
	}

	if err := proxy.Start(localPort); err != nil {
		log.Fatal("Proxy failed:", err)
	}
}

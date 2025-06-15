package main

import (
	"fmt"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"nats-limiter-proxy/internal/server"
)

const (
	localPort = 4223
)

func main() {
	// Configure zerolog
	logLevel, err := zerolog.ParseLevel(os.Getenv("LOG_LEVEL"))
	if err != nil {
		logLevel = zerolog.InfoLevel // default to info if parsing fails
	}
	zerolog.SetGlobalLevel(logLevel)

	upstreamHost := os.Getenv("UPSTREAM_HOST")
	if upstreamHost == "" {
		log.Fatal().Msg("Environment variable UPSTREAM_HOST is required")
	}

	portStr := os.Getenv("UPSTREAM_PORT")
	if portStr == "" {
		log.Fatal().Msg("Environment variable UPSTREAM_PORT is required")
	}

	var upstreamPort int
	_, parseErr := fmt.Sscanf(portStr, "%d", &upstreamPort)
	if parseErr != nil || upstreamPort <= 0 || upstreamPort > 65535 {
		log.Fatal().Str("port", portStr).Msg("Invalid UPSTREAM_PORT value")
	}

	proxy, err := server.NewProxy(upstreamHost, upstreamPort, "config.yaml")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create proxy")
	}

	if err := proxy.Start(localPort); err != nil {
		log.Fatal().Err(err).Msg("Proxy failed")
	}
}

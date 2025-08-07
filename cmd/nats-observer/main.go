package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"
)

var (
	listenAddr = flag.String("listen", "0.0.0.0:14222", "Address to listen on")
	upstreamAddr = flag.String("upstream", "localhost:4222", "NATS server address")
	verbose = flag.Bool("verbose", false, "Enable verbose logging")
)

func main() {
	flag.Parse()

	log.Printf("NATS Protocol Observer starting")
	log.Printf("Listening on: %s", *listenAddr)
	log.Printf("Upstream NATS: %s", *upstreamAddr)
	log.Printf("Verbose: %t", *verbose)

	listener, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	defer listener.Close()

	for {
		clientConn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}

		log.Printf("New client connection from: %s", clientConn.RemoteAddr())
		go handleConnection(clientConn)
	}
}

func handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	// Connect to upstream NATS server
	serverConn, err := net.Dial("tcp", *upstreamAddr)
	if err != nil {
		log.Printf("Failed to connect to upstream: %v", err)
		return
	}
	defer serverConn.Close()

	clientAddr := clientConn.RemoteAddr().String()
	log.Printf("[%s] Connected to upstream NATS server", clientAddr)

	// Create channels for error handling
	errChan := make(chan error, 2)

	// Client -> Server (with logging)
	go func() {
		err := copyWithLogging(serverConn, clientConn, fmt.Sprintf("[%s] CLIENT->SERVER", clientAddr))
		errChan <- err
	}()

	// Server -> Client (with logging)
	go func() {
		err := copyWithLogging(clientConn, serverConn, fmt.Sprintf("[%s] SERVER->CLIENT", clientAddr))
		errChan <- err
	}()

	// Wait for either direction to error or close
	err = <-errChan
	if err != nil {
		log.Printf("[%s] Connection closed: %v", clientAddr, err)
	} else {
		log.Printf("[%s] Connection closed gracefully", clientAddr)
	}
}

func copyWithLogging(dst io.Writer, src io.Reader, prefix string) error {
	buffer := make([]byte, 4096)
	
	for {
		n, err := src.Read(buffer)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		if n > 0 {
			// Log the raw data
			data := buffer[:n]
			logProtocolData(prefix, data)

			// Forward the data
			_, writeErr := dst.Write(data)
			if writeErr != nil {
				return writeErr
			}
		}
	}
}

func logProtocolData(prefix string, data []byte) {
	timestamp := time.Now().Format("15:04:05.000")
	
	// Convert data to string for protocol analysis
	dataStr := string(data)
	
	// Clean up the data for logging (replace CRLF with visible characters)
	cleanData := strings.ReplaceAll(dataStr, "\r\n", "\\r\\n")
	cleanData = strings.ReplaceAll(cleanData, "\r", "\\r")
	cleanData = strings.ReplaceAll(cleanData, "\n", "\\n")
	
	// Truncate very long messages
	if len(cleanData) > 200 {
		cleanData = cleanData[:200] + "..."
	}

	// Basic protocol detection
	protocolType := detectProtocolType(dataStr)

	log.Printf("%s %s [%d bytes] %s: %s", 
		timestamp, prefix, len(data), protocolType, cleanData)

	// Verbose mode: also show hex dump for binary data
	if *verbose && containsBinaryData(data) {
		log.Printf("%s %s [HEX]: %x", timestamp, prefix, data)
	}
}

func detectProtocolType(data string) string {
	data = strings.TrimSpace(data)
	
	// NATS protocol commands (client->server)
	if strings.HasPrefix(data, "CONNECT ") {
		return "CONNECT"
	}
	if strings.HasPrefix(data, "PUB ") {
		return "PUB"
	}
	if strings.HasPrefix(data, "SUB ") {
		return "SUB"
	}
	if strings.HasPrefix(data, "UNSUB ") {
		return "UNSUB"
	}
	if strings.HasPrefix(data, "PING") {
		return "PING"
	}
	if strings.HasPrefix(data, "PONG") {
		return "PONG"
	}
	
	// NATS protocol responses (server->client)
	if strings.HasPrefix(data, "INFO ") {
		return "INFO"
	}
	if strings.HasPrefix(data, "MSG ") {
		return "MSG"
	}
	if strings.HasPrefix(data, "+OK") {
		return "+OK"
	}
	if strings.HasPrefix(data, "-ERR ") {
		return "ERROR"
	}
	
	// Multi-line or partial data
	lines := strings.Split(data, "\n")
	if len(lines) > 1 {
		return "MULTI"
	}
	
	// Unknown or partial data
	if len(data) == 0 {
		return "EMPTY"
	}
	
	return "DATA"
}

func containsBinaryData(data []byte) bool {
	for _, b := range data {
		// Check for non-printable characters (except common whitespace)
		if b < 32 && b != '\t' && b != '\n' && b != '\r' {
			return true
		}
		if b > 126 {
			return true
		}
	}
	return false
}
package main

import (
    "bufio"
    "encoding/json"
    "fmt"
    "io"
    "net"
    "strings"
)

const (
    localPort     = 4223
    upstreamHost  = "127.0.0.1"
    upstreamPort  = 4222
)

func handleConnection(clientConn net.Conn) {
    defer clientConn.Close()

    upstreamConn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", upstreamHost, upstreamPort))
    if err != nil {
        fmt.Println("Failed to connect to upstream:", err)
        return
    }
    defer upstreamConn.Close()

    clientToUpstream := make(chan []byte)
    upstreamToClient := make(chan []byte)

    var username string

    go func() {
        reader := bufio.NewReader(clientConn)
        for {
            line, err := reader.ReadString('\n')
            if err != nil {
                if err != io.EOF {
                    fmt.Println("Read error from client:", err)
                }
                break
            }
            trimmed := strings.TrimSpace(line)
            if trimmed != "" {
                if strings.HasPrefix(trimmed, "CONNECT ") {
                    var obj map[string]interface{}
                    if err := json.Unmarshal([]byte(trimmed[8:]), &obj); err == nil {
                        if user, ok := obj["user"].(string); ok {
                            username = user
                            fmt.Printf("Authenticated user: %s\n", username)
                        }
                    } else {
                        fmt.Println("Failed to parse CONNECT line:", err)
                    }
                }
                if strings.HasPrefix(trimmed, "PUB ") && username != "" {
                    fmt.Printf("User \"%s\" sent message: %s\n", username, trimmed)
                } else {
                    fmt.Println("C->S:", trimmed)
                }
            }
            clientToUpstream <- []byte(line)
        }
        close(clientToUpstream)
    }()

    go func() {
        reader := bufio.NewReader(upstreamConn)
        for {
            line, err := reader.ReadString('\n')
            if err != nil {
                if err != io.EOF {
                    fmt.Println("Read error from upstream:", err)
                }
                break
            }
            trimmed := strings.TrimSpace(line)
            if trimmed != "" {
                fmt.Println("S->C:", trimmed)
            }
            upstreamToClient <- []byte(line)
        }
        close(upstreamToClient)
    }()

    for clientToUpstream != nil || upstreamToClient != nil {
        select {
        case data, ok := <-clientToUpstream:
            if !ok {
                clientToUpstream = nil
                continue
            }
            upstreamConn.Write(data)
        case data, ok := <-upstreamToClient:
            if !ok {
                upstreamToClient = nil
                continue
            }
            clientConn.Write(data)
        }
    }
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
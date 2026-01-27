package main

import (
	"context"
	"log"
	"os"

	"melange-lsp/internal/lsp"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

type stdioReadWriteCloser struct{}

func (stdioReadWriteCloser) Read(p []byte) (int, error) {
	return os.Stdin.Read(p)
}

func (stdioReadWriteCloser) Write(p []byte) (int, error) {
	return os.Stdout.Write(p)
}

func (stdioReadWriteCloser) Close() error {
	return nil
}

func main() {
	// Create LSP server
	server := lsp.NewServer()

	// Create JSON-RPC connection over stdin/stdout
	ctx := context.Background()
	stream := jsonrpc2.NewStream(stdioReadWriteCloser{})
	conn := jsonrpc2.NewConn(stream)

	// Create client from connection and set it on server
	client := protocol.ClientDispatcher(conn, nil)
	server.SetClient(client)

	// Create handler that routes LSP methods to server
	handler := protocol.ServerHandler(server, jsonrpc2.MethodNotFoundHandler)

	conn.Go(ctx, handler)

	// Wait for connection to close
	<-conn.Done()

	if err := conn.Err(); err != nil {
		log.Fatalf("connection error: %v", err)
	}
}

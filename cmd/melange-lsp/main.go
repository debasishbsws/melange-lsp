package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"melange-lsp/internal/lsp"

	"go.lsp.dev/jsonrpc2"
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
	// stdout carries the protocol; logs go to stderr.
	log.SetOutput(os.Stderr)
	log.SetFlags(0)

	for _, arg := range os.Args[1:] {
		switch arg {
		case "--version", "version":
			fmt.Println("melange-lsp", lsp.Version)
			return
		case "--stdio":
			// The default transport; accepted for editors that pass it.
		default:
			fmt.Fprintf(os.Stderr, "usage: melange-lsp [--stdio | --version]\n")
			os.Exit(2)
		}
	}

	ctx := context.Background()
	conn := jsonrpc2.NewConn(jsonrpc2.NewStream(stdioReadWriteCloser{}))
	lsp.NewServer().Serve(ctx, conn)
	<-conn.Done()
	if err := conn.Err(); err != nil && !errors.Is(err, io.EOF) {
		log.Fatalf("melange-lsp: connection error: %v", err)
	}
}

.PHONY: build test test-go test-lsp lint fmt clean

build:
	go build -o melange-lsp ./cmd/melange-lsp

test-go:
	go test ./... -race

test-lsp: build
	cd test/lsp-test && ./test.hs

test: test-go test-lsp

lint:
	golangci-lint run ./...

fmt:
	goimports -w .
	gofmt -w .

clean:
	rm -f melange-lsp

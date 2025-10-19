.PHONY: build test lint fmt clean

build:
	go build -o melange-lsp ./cmd/melange-lsp

test:
	go test ./... -race

lint:
	golangci-lint run ./...

fmt:
	goimports -w .
	gofmt -w .

clean:
	rm -f melange-lsp

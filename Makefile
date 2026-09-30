.PHONY: build install test test-go test-lsp lint fmt generate corpus clean

build:
	go build -o melange-lsp ./cmd/melange-lsp

install:
	go install ./cmd/melange-lsp

test-go:
	go test ./... -race

test-lsp: build
	cd test/lsp-test && ./test.hs

test: test-go test-lsp

lint:
	golangci-lint run ./...

fmt:
	golangci-lint fmt ./...

# Regenerate facts copied from the melange module (linter names).
generate:
	go generate ./...

# Run every feature over a real repository to find false positives:
#   make corpus ROOT=~/stereo GLOBS='os/*.yaml'
ROOT ?= .
GLOBS ?= *.yaml
corpus:
	go run ./hack/corpus -root $(ROOT) $(GLOBS)

clean:
	rm -f melange-lsp

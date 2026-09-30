.PHONY: build install zed-install vscode-package vscode-install test test-go test-lsp lint fmt generate corpus clean

build:
	go build -o melange-lsp ./cmd/melange-lsp

install:
	go install ./cmd/melange-lsp

# Build and install the Zed extension (see editors/zed).
zed-install: install
	editors/zed/install.sh

# Build the VS Code extension (editors/vscode) into a .vsix, and install it.
CODE ?= $(shell command -v code || echo "/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code")
vscode-package:
	cd editors/vscode && npm ci --no-audit --no-fund && npm run package

vscode-install: install vscode-package
	"$(CODE)" --install-extension editors/vscode/melange-*.vsix --force

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

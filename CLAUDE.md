# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is an LSP (Language Server Protocol) server for [melange](https://github.com/chainguard-dev/melange) YAML configuration files. Melange is a declarative APK package build system used with Wolfi and Alpine Linux.

The project is in early development — see `docs/PLAN.md` for the phased roadmap. Phases 1-2 (LSP skeleton, basic parsing, schema completions) are mostly complete.

## Build and Development Commands

```bash
make build      # Build the binary (outputs ./melange-lsp)
make test       # Run all tests (Go + LSP integration)
make test-go    # Run Go unit tests only (go test ./... -race)
make test-lsp   # Run LSP integration tests (requires Haskell stack + built binary)
make lint       # Run golangci-lint
make fmt        # Format code with goimports + gofmt
```

Running a single Go test:
```bash
go test ./internal/lsp -run TestName -v
```

LSP integration tests (`test/lsp-test/test.hs`) are a Haskell stack script using the `lsp-test` framework. There's no mechanism to run individual Haskell test cases without editing the file.

## Architecture

The LSP server communicates over stdin/stdout using JSON-RPC 2.0.

**Key packages:**

- `cmd/melange-lsp/main.go` — Entry point; creates stdio JSON-RPC connection and delegates to `Server`
- `internal/lsp/server.go` — Core `Server` struct with document cache (`map[DocumentURI]*Document`), LSP lifecycle methods, and document sync handlers. Calls into diagnostics/symbols/completion modules.
- `internal/lsp/completion.go` — Completion provider that determines the YAML path at cursor position and delegates to schema lookups
- `internal/lsp/diagnostics.go` — Validation using melange's own `config.ParseConfiguration` against an in-memory filesystem (`memfs`)
- `internal/lsp/symbols.go` — Document symbol extraction (package, pipeline steps, subpackages)
- `internal/lsp/stubs.go` — Empty implementations for ~30 unimplemented LSP methods
- `internal/parser/yaml.go` — Lightweight YAML unmarshaling into `MelangeConfig` struct
- `internal/schema/schema.go` — Reflection-based extraction of completion items from melange's Go types (uses YAML struct tags, checks `omitempty` for required fields)

**Key dependencies:**

- `go.lsp.dev/protocol` — LSP protocol types and client/server interfaces
- `go.lsp.dev/jsonrpc2` — JSON-RPC transport
- `chainguard.dev/melange/pkg/config` — Melange's config parser (used for validation and schema reflection)
- `github.com/psanford/memfs` — In-memory filesystem for validation without temp files

**Validation approach:** Documents are validated by writing content to an in-memory filesystem and running melange's actual parser against it. This ensures diagnostics match what melange would produce. Note: all diagnostic positions are currently reported at line 0, column 0.

**Completion approach:** `completion.go` walks backwards from the cursor to determine the YAML path (e.g., `["package"]`, `["pipeline", "0"]`), then `schema.go` uses reflection on melange's config types to return matching field names as completion items.

## Current Capabilities

- Document synchronization (open/change/close)
- Document symbols (package, pipeline steps, subpackages)
- Diagnostics via melange parser validation
- Completions for melange config keys (top-level, package, pipeline, subpackage sections)

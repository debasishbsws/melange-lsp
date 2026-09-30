# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is an LSP (Language Server Protocol) server for [melange](https://github.com/chainguard-dev/melange) YAML configuration files. Melange is a declarative APK package build system used with Wolfi and Alpine Linux.

It covers diagnostics, completion, hover, navigation, outline, links, inlay hints, code actions, semantic tokens and folding for package configurations and pipeline definitions. See `README.md` for the feature list and `docs/PLAN.md` for what is next.

## Build and Development Commands

```bash
make build      # Build the binary (outputs ./melange-lsp)
make test       # Run all tests (Go + LSP integration)
make test-go    # Run Go unit tests only (go test ./... -race)
make test-lsp   # Run LSP integration tests (requires Haskell stack + built binary)
make lint       # Run golangci-lint
make fmt        # Format code with goimports + gofmt
make generate   # Regenerate internal/known/linters_gen.go from melange
make corpus ROOT=~/stereo GLOBS='os/*.yaml'  # Run every feature over a real repo
```

Running a single Go test:
```bash
go test ./internal/lsp -run TestName -v
```

LSP integration tests (`test/lsp-test/test.hs`) are a Haskell stack script using the `lsp-test` framework. There's no mechanism to run individual Haskell test cases without editing the file.

## Architecture

The server speaks JSON-RPC 2.0 over stdio. It only serves melange files;
`document.Kind` decides that (a top-level `package:` with `name:`, or a
pipeline definition), and every request for other YAML gets an empty result.

**Packages, bottom up:**

- `internal/document` — One parsed snapshot of a file: text, lines, the
  yaml.v3 node tree, and its `Kind`. Converts between yaml.v3 positions
  (1-based line, rune column) and LSP positions (UTF-16), computes node end
  positions (yaml.v3 only records starts), and finds the node under a
  position (`Locate`). `Context` works out the completion context from
  indentation, because half-typed YAML usually does not parse.
- `internal/model` — melange semantics over a document: every pipeline step
  (main, subpackage, test, nested) with its path and enclosing subpackage,
  subpackages, vars (including `var-transforms` outputs), `data:` sets,
  pipeline-file inputs, and every `${{...}}` reference with its range.
- `internal/subst` — Resolves `${{...}}` using melange's
  `build.NewSubstitutionMap`, per subpackage, with a lenient fallback when
  melange rejects the file.
- `internal/pipeline` — Finds pipelines like melange: `--pipeline-dir(s)`
  from the nearest Makefile (or nearby `pipelines/` dirs), then
  `/usr/share/melange/pipelines`, then melange's embedded built-ins, which
  are extracted to the user cache dir so editors can open them. Parsed
  definitions are cached by modification time.
- `internal/schema` — melange's JSON Schema (`config.SchemaJSON`) as fields
  with types, enums and docs, corrected where it disagrees with YAML
  decoding (key casing, `timeout` type, required fields).
- `internal/known` — Facts melange does not export: linter names
  (`linters_gen.go`, generated from melange's source by `hack/genlinters`
  via `make generate`), capabilities, and variable documentation.
- `internal/lsp` — The server. `server.go` holds document snapshots and
  options; `router.go` maps methods to handlers (hand-written because
  `go.lsp.dev/protocol` v0.12.0 predates LSP 3.17 features such as inlay
  hints and `collapsedText`). One file per feature: `diagnostics.go`,
  `completion.go`, `hover.go`, `definition.go` (definition, references,
  rename), `symbols.go`, `links.go` (links, inlay hints, code lenses),
  `codeaction.go`, `semantic.go`, `folding.go`, `agentnotes.go`,
  `patches.go`, `workspace.go` (package index, workspace symbols and
  references).

**Diagnostics** mirror melange's own checks with precise positions (see
`docs/diagnostics.md`). melange's parser runs for its decode and validation
errors; when our checks find no errors, melange's compile step runs as a
backstop. Each diagnostic has a stable code; fixes travel in `Data` and are
turned into quick fixes by `codeaction.go`.

**Testing:** `features_test.go` covers each feature against a temporary
workspace; `e2e_test.go` drives the server over JSON-RPC. `hack/corpus`
runs every feature over a real repository; keep it free of false positives
(`make corpus ROOT=~/stereo GLOBS='os/*.yaml'`). `stereo_test.go` runs
against a stereo checkout when `STEREO` is set.

## Conventions

- Match melange's behaviour, and cite where in melange a rule comes from
  when it is not obvious.
- Prefer a warning over an error unless melange would fail, and prefer no
  diagnostic over a false one.
- Positions are UTF-16; convert with `document.Position`/`ByteToPosition`.

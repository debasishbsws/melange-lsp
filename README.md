# melange-lsp

Language Server Protocol (LSP) implementation for [melange](https://github.com/chainguard-dev/melange) configuration files.

## What is melange?

Melange is a declarative APK package build system using YAML-based pipeline configurations. It's commonly used with Wolfi and Alpine Linux ecosystems for building secure software artifacts.

## Goals

Provide IDE support for melange YAML configurations including:
- Autocompletion for pipeline names and configuration keys
- Validation of configuration schema
- Hover documentation for built-in pipelines
- Template variable support
- Diagnostics for common errors

## Building

```bash
make build
```

## Development

```bash
make fmt    # Format code
make lint   # Run linters
make test   # Run tests
```

## Status

Early development. See `docs/PLAN.md` for the phased roadmap.

Current capabilities:
- Document synchronization (open/change/close)
- Diagnostics via melange parser validation
- Completions for melange config keys and pipeline names
- Document symbols (package, pipeline steps, subpackages)
- Hover for `${{...}}` variable expressions (built-ins, user vars, range vars)

## Development History

This project has been developed entirely by LLMs, primarily [Claude](https://claude.ai) (Anthropic) with contributions from Qwen and others. Human involvement has been limited to direction, review, and testing.

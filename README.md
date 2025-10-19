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

Early development - basic project structure only.

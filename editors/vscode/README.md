# melange for VS Code

Language support for [melange](https://github.com/chainguard-dev/melange)
package and pipeline YAML, powered by
[melange-lsp](https://github.com/debasishbsws/melange-lsp).

It runs for YAML files; the server ignores YAML that is not a melange
package or pipeline, so other YAML tooling keeps working.

- Diagnostics that match melange's checks, with quick fixes
- Completion for keys, pipelines, inputs and `${{...}}` variables
- Hover with pipeline inputs, resolved values and update-check traces
- Go to definition, references, rename, outline and workspace symbols
- Inlay hints showing what lines with variables resolve to
- `@agent` notes folded every time a file opens

## Requirements

`melange-lsp` on your `PATH`:

```bash
go install ./cmd/melange-lsp   # from the melange-lsp repository
```

or set `melange.server.path`.

## Agent notes

`# @agent` comments are notes for AI agents and the next maintainer. They
fold whenever a file is opened; hover a folded note to read it.

| Command | Default key |
|---|---|
| Melange: Toggle Agent Notes | `cmd-alt-n` / `ctrl-alt-n` |
| Melange: Fold Agent Notes | |
| Melange: Unfold Agent Notes | |
| Melange: Restart Language Server | |

## Settings

| Setting | Default | |
|---|---|---|
| `melange.server.path` | `melange-lsp` | Path to the server binary |
| `melange.pipelineDirs` | `[]` | Extra pipeline directories, searched first |
| `melange.inlayHints` | `true` | Show resolved values inline |
| `melange.agentNotes.foldOnOpen` | `true` | Fold agent notes when a file opens |

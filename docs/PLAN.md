# melange-lsp plan

## Done

- Document model with real node ranges and UTF-16 positions; semantic model
  of steps, subpackages, vars, data, inputs and `${{...}}` references.
- Detection of melange files so other YAML is left alone.
- Diagnostics mirroring melange's parse and compile checks, calibrated
  against the stereo monorepo (see `docs/diagnostics.md`).
- Completion, hover, definition, references (in-file and workspace-wide),
  rename, document and workspace symbols, links, inlay hints, code lenses,
  code actions, semantic tokens, folding.
- Agent notes (`docs/agent-notes.md`).
- Claude Code plugin manifest.

## Next

- Editor extensions: a Zed extension that registers the server and enables
  LSP folding ranges, and a VS Code extension with a "Toggle agent notes"
  command.
- Zed: an upstream change so comment folding ranges can be folded as a
  group (Zed drops `FoldingRangeKind` today).
- Formatting that matches `yam` (`.yam.yaml`), so edits keep stereo's style.
- Epoch checks against git: warn when a change keeps the version but not
  the epoch, or bumps the version without resetting the epoch.
- Optional repository lint rules (stereo's fetch, git-ref and
  version-stream lints) behind an option, with `#nolint` support.

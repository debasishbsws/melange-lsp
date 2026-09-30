# melange-lsp

A language server for [melange](https://github.com/chainguard-dev/melange)
package configurations and pipeline definitions. It speaks LSP over stdio,
so it works in Zed, VS Code, Neovim, Helix, and with AI coding agents that
use language servers.

It is built on melange's own code: it parses with `config.ParseConfiguration`,
resolves variables with `build.NewSubstitutionMap`, reads melange's JSON
Schema and built-in pipelines, and falls back to melange's compile step, so
what it reports matches what `melange build` does.

## Which files it handles

The server only acts on melange files, so it can run alongside a general
YAML language server:

- **Package configuration:** a top-level `package:` mapping with a `name:`.
  A file keeps this status while you edit it, even if the YAML is briefly
  invalid.
- **Pipeline definition:** any YAML under a `pipelines/` directory, or YAML
  whose top-level keys are all pipeline fields (`name`, `inputs`, `needs`,
  `pipeline`, `runs`, ...).

Everything else, such as CI workflows or `cargobump-deps.yaml`, gets no
diagnostics, completions or folds.

## Features

**Diagnostics** with exact positions and did-you-mean suggestions, mirroring
melange's parse and compile checks: unknown fields, unknown pipelines,
unknown or missing `with:` inputs, checksum formats, undefined or
out-of-scope `${{...}}` variables, `if:` syntax, unknown linters, setcap
capabilities, invalid regular expressions, missing patch files, deprecated
fields, and more. See [docs/diagnostics.md](docs/diagnostics.md).

**Completion**
- Keys valid at the cursor, skipping ones already present, with snippets
  that lay out nested mappings and lists.
- `uses:` pipelines from the local pipeline directories and melange's
  built-ins, with their inputs documented. Choosing one inserts a `with:`
  block for its required inputs.
- `with:` inputs of the step's pipeline, required first, defaults as
  placeholders.
- `${{...}}` variables visible at the cursor, with their resolved values:
  `subpkg.*` only in subpackages, `range.*` only in `range:` subpackages,
  `inputs.*` in pipelines.
- Linter names for `checks.disabled`, capabilities, enum values, `range:`
  data sets, and package names from the workspace for dependency lists.
- Step snippets (`fetch`, `git-checkout`, `runs`) and a new-package skeleton.

**Hover**
- `${{...}}` shows its value, where it is defined, and what it means.
- `uses:` shows the pipeline's inputs and this step's effective values after
  defaults and substitution.
- Keys show melange's documentation, including conventions such as when to
  bump `epoch`.
- Values containing variables show the fully resolved string.

**Navigation**
- Go to definition for `uses:` (opens the pipeline, built-ins included),
  `with:` keys, `${{vars.*}}`, `${{inputs.*}}`, `range.*`, `range:`,
  `targets.package.*`, patch files, and any key's schema definition.
- References, highlights and rename for vars, pipeline inputs and data sets.
- Workspace references: every step using a pipeline, and every package
  depending on a package or subpackage.
- A document outline of the package, vars, data, pipeline steps (nested),
  subpackages with their pipelines and tests, and update settings.
- Workspace symbols for every package, subpackage and pipeline in the
  repository.

**Also:** document links (resolved source URLs, GitHub and
release-monitoring projects, patch files), inlay hints with resolved
variable values, code lenses for `var-transforms` results, semantic tokens
for `${{...}}` and agent notes, and folding.

**Code actions:** apply did-you-mean fixes, add missing required inputs,
increment `epoch`, and convert a comment block into an agent note.

**[Agent notes](docs/agent-notes.md):** `# @agent` comments addressed to AI
agents. Editors fold them away for humans; agents read them as written.

## Pipeline directories

`uses:` is resolved like melange does: pipeline directories in order, then
`/usr/share/melange/pipelines` if present, then melange's built-ins. The
directories come from the `--pipeline-dir` and `--pipeline-dirs` flags in
the nearest `Makefile`, which is how stereo and wolfi-dev/os configure
melange. Without a Makefile, the server uses `pipelines/` directories next
to the file and in its parent directories. The `pipelineDirs` option adds
directories searched first.

## Install

```bash
go install melange-lsp/cmd/melange-lsp@latest
```

Or from a checkout:

```bash
make install
```

## Editor setup

### Zed

Register the server for YAML in `settings.json`. LSP folding ranges are
needed for agent notes to fold:

```json
{
  "lsp": {
    "melange-lsp": {
      "binary": { "path": "melange-lsp" }
    }
  },
  "languages": {
    "YAML": {
      "language_servers": ["melange-lsp", "..."],
      "document_folding_ranges": "on"
    }
  }
}
```

A Zed extension that does this is planned.

### Neovim

```lua
vim.lsp.config("melange", {
  cmd = { "melange-lsp" },
  filetypes = { "yaml" },
  root_markers = { "Makefile", ".git" },
})
vim.lsp.enable("melange")
```

### Claude Code and other agents

This repository is a Claude Code plugin (`.claude-plugin/plugin.json`).
With `melange-lsp` on your `PATH`:

```bash
claude --plugin-dir /path/to/melange-lsp
```

Claude Code then gets diagnostics after every edit to a melange file, and
its LSP tool can use definitions, references, document symbols and
workspace symbols. Claude Code routes servers by file extension only, so
every `.yaml` file reaches the server; it ignores the ones that are not
melange.

Features that help agents in particular:

- Diagnostics name the fix: `"gretting" is not an input of pipeline
  "local/greet"; did you mean "greeting"?`, or the list of valid inputs.
- References work across the repository: on a `uses:` value (or anywhere in
  a pipeline file) they list every step that uses the pipeline; on a
  package or subpackage name they list every place it is a dependency or a
  build or test package.
- Workspace symbols find any package, subpackage or pipeline by name.
- [Agent notes](docs/agent-notes.md) carry guidance for future agents in
  the file itself.

## Options

Pass these as `initializationOptions` or through
`workspace/didChangeConfiguration`, either at the top level or under a
`melange` key:

| Option | Default | Meaning |
|---|---|---|
| `pipelineDirs` | `[]` | Extra pipeline directories, searched first. Relative paths are resolved against the workspace root. |
| `inlayHints` | `true` | Show resolved `${{...}}` values inline. |

## Development

```bash
make build      # ./melange-lsp
make test       # Go tests, plus the Haskell lsp-test suite if stack is installed
make lint
make generate   # refresh facts copied from melange (linter names)
make corpus ROOT=~/stereo GLOBS='os/*.yaml'   # run every feature over a real repo
```

`make corpus` is the main quality check: it reports every diagnostic by code
with examples, any panic, and slow files. New checks should keep it free of
false positives on a real repository before they land.

## Development history

This project has been developed entirely by LLMs, primarily
[Claude](https://claude.ai) (Anthropic) with contributions from Qwen and
others. Human involvement has been limited to direction, review, and
testing.

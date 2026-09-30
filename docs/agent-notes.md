# Agent notes

Agent notes are YAML comments meant for AI agents rather than human readers.
They are plain comments, so melange ignores them and agents read them exactly
as written. melange-lsp reports them as folding ranges, so editors can hide
them from humans until they want to read or edit them.

## Syntax

There is one trigger, `@agent`, at the start of a full-line comment. The colon
after it is optional.

One line:

```yaml
# @agent: upstream tags releases as vX.Y.Z-final; strip the suffix
# @agent upstream tags releases as vX.Y.Z-final; strip the suffix
```

Multiple lines, fenced with backticks like Markdown and closed by a bare
`` # ``` `` line:

```yaml
# @agent ```
# The CVE-2026-1234 patch is required until upstream ships 1.5.0.
# On version bump:
#   - check whether upstream merged PR #812
#   - if so, drop the patch and the extra test
# ```
```

Rules:

- A note applies to where it is placed: at the top of the file for the whole
  package, above a pipeline step for that step.
- Only full-line comments count. `version: 1.0 # @agent: ...` is not a note.
- Notes may appear inside block scalars such as `runs: |`, next to the shell
  they describe.
- Inside a fence every line must be a comment. The first non-comment line
  ends an unclosed fence, and the LSP warns about it.

## Writing notes

- Typing `# @` in a comment offers both forms as snippets.
- The "Convert comment to @agent note" code action wraps an existing
  comment block in a fence, so guidance already in a file can be folded
  without rewriting it.
- Semantic tokens mark `@agent` as a decorator and note text as comment
  documentation, so themes can style notes differently from other comments.

## Editor behaviour

melange-lsp returns each run of adjacent notes as one folding range of kind
`comment` with `collapsedText` set to `🤖 agent note` (or `🤖 N agent notes`).
It also returns structural YAML folds, because clients such as Zed use a
server's folding ranges in place of their own.

A single one-line note spans one line, which LSP clients cannot fold, so it
stays visible. Put two or more notes together, or use a fence, to fold them.

### Zed

Enable LSP folding ranges for YAML in `settings.json`:

```json
{
  "languages": {
    "YAML": {
      "language_servers": ["melange-lsp", "..."],
      "document_folding_ranges": "on"
    }
  }
}
```

Zed does not yet have a command that folds only comment ranges, so notes are
folded one at a time.

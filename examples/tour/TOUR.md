# melange-lsp tour

`hello.yaml` is a small package with a few deliberate mistakes. Open this
folder in Zed (`zed examples/tour`) after installing the dev extension from
`editors/zed`; `.zed/settings.json` turns on folding, inlay hints, code
lenses, links and semantic tokens for this project only.

Without an editor, `go run ./hack/tour` from the repository root prints what
each feature returns for this file.

## Things to try

| Where | Try | You should see |
|---|---|---|
| Lines 1–4 | Fold the `@agent` block (click the chevron) | It collapses to `🤖 agent note` |
| Line 14 `usrloca` | Hover the red squiggle, then open code actions (`cmd-.`) | "did you mean usrlocal" and a fix |
| Line 48 `gretting` | Code actions | Change to `greeting` |
| Line 50 `local/greet` | Code actions | Add missing inputs: `who` |
| Line 52 `autoconf/make-instal` | Code actions | Change to `autoconf/make-install` |
| Line 64 `vars.greting` | Code actions | Change to `vars.greeting` |
| Line 73 `tag-filter` | Hover | Deprecated, use `tag-filter-prefix` (struck through) |
| Line 45 `local/greet` | Hover | Inputs table and this step's resolved values |
| Line 45 `local/greet` | `cmd-click` (go to definition) | Opens `pipelines/local/greet.yaml` |
| Line 32 `fetch` | `cmd-click` | Opens melange's built-in `fetch.yaml` |
| Line 39 `fix-cflags.patch` | `cmd-click` | Opens the patch |
| Line 47 `${{vars.greeting}}` | Hover, `cmd-click`, find references, rename (`F2`) | Value, jump to `vars:`, both uses, rename both |
| Line 34 URI | Inlay hint and link | `= 2.12.1` after the variable; clicking opens the resolved URL |
| Line 29 `to: major-minor` | Code lens above it | `${{vars.major-minor}} → 2.12` |
| New line under `pipeline:` | Type `- uses: ` and pick `local/greet` | A `with:` block with `who:` is inserted |
| Inside `with:` of a step | Start a new key | That pipeline's inputs, required first |
| Inside `runs:` | Type `${{` | Variables in scope with their values |
| A new comment line | Type `# @` | `@agent` snippets |
| Any comment block | Code actions | Convert comment to @agent note |
| Anywhere | Outline panel (`cmd-shift-o`) | Package, vars, steps, subpackages, update |
| Anywhere | Project symbols (`cmd-t`) | Packages and pipelines by name |
| Anywhere | Code actions | Increment epoch |

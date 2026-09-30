package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/protocol"

	"melange-lsp/internal/document"
)

// workspace writes files under a temporary root and returns it. A Makefile
// points melange at ./pipelines, as stereo and wolfi-dev/os do.
func workspace(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	base := map[string]string{
		"Makefile": "MELANGE_OPTS += --pipeline-dir=./pipelines/\n",
		"pipelines/local/greet.yaml": `name: Greet someone
inputs:
  who:
    description: Who to greet
    required: true
  greeting:
    description: The greeting
    default: hello
pipeline:
  - runs: echo "${{inputs.greeting}} ${{inputs.who}}"
`,
	}
	for k, v := range files {
		base[k] = v
	}
	for name, content := range base {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// open parses text as root/name. "§" in text marks a cursor position,
// which is returned.
func open(t *testing.T, root, name, text string) (*Snapshot, protocol.Position) {
	t.Helper()
	var pos protocol.Position
	if i := strings.Index(text, "§"); i >= 0 {
		pos.Line = uint32(strings.Count(text[:i], "\n"))
		pos.Character = uint32(len([]rune(text[strings.LastIndex(text[:i], "\n")+1 : i])))
		text = text[:i] + text[i+len("§"):]
	}
	srv := NewTestServer(root)
	return srv.Open(document.URI(filepath.Join(root, name)), text), pos
}

const header = `package:
  name: hello
  version: "1.2.3"
  epoch: 0
  description: says hello
  copyright:
    - license: Apache-2.0
`

func codes(ds []protocol.Diagnostic, sev protocol.DiagnosticSeverity) map[string][]protocol.Diagnostic {
	out := map[string][]protocol.Diagnostic{}
	for _, d := range ds {
		if d.Severity <= sev {
			c, _ := d.Code.(string)
			out[c] = append(out[c], d)
		}
	}
	return out
}

func TestDiagnosticsClean(t *testing.T) {
	root := workspace(t, map[string]string{"hello/fix.patch": "--- a\n+++ b\n"})
	snap, _ := open(t, root, "hello.yaml", header+`
vars:
  greeting: hi
var-transforms:
  - from: ${{package.version}}
    match: ^(\d+)\.\d+\.\d+$
    replace: $1
    to: major
pipeline:
  - uses: fetch
    with:
      uri: https://example.com/hello-${{package.version}}.tar.gz
      expected-sha256: 0000000000000000000000000000000000000000000000000000000000000000
  - uses: patch
    with:
      patches: fix.patch
  - uses: local/greet
    with:
      who: ${{vars.greeting}} v${{vars.major}}
  - runs: install -D hello ${{targets.destdir}}/usr/bin/hello
subpackages:
  - name: hello-doc
    pipeline:
      - runs: mkdir -p ${{targets.subpkgdir}}
    test:
      pipeline:
        - runs: test -d ${{targets.subpkgdir}} && echo ${{subpkg.name}}
`)
	for _, d := range Diagnostics(snap) {
		t.Errorf("unexpected diagnostic at %d: %s (%v)", d.Range.Start.Line+1, d.Message, d.Code)
	}
}

func TestDiagnostics(t *testing.T) {
	root := workspace(t, nil)
	tests := []struct {
		name, body, code, contains string
		line                       uint32 // zero-based, relative to the whole file
	}{
		{"unknown pipeline with suggestion", "pipeline:\n  - uses: local/gret\n    with:\n      who: x\n", codeUnknownPipeline, `did you mean "local/greet"`, 8},
		{"unknown input", "pipeline:\n  - uses: local/greet\n    with:\n      who: x\n      gretting: hi\n", codeUnknownInput, `did you mean "greeting"`, 11},
		{"missing required input", "pipeline:\n  - uses: local/greet\n", codeMissingInput, "who", 8},
		{"bad checksum", "pipeline:\n  - uses: fetch\n    with:\n      uri: https://x\n      expected-sha256: abc\n", codeBadChecksum, "64 hex", 11},
		{"undefined var", "pipeline:\n  - runs: echo ${{vars.nope}}\n", codeUndefinedVar, "nope", 8},
		{"subpkg outside subpackage", "pipeline:\n  - runs: echo ${{subpkg.name}}\n", codeUndefinedVar, "only defined inside subpackages", 8},
		{"range outside range", "subpackages:\n  - name: x\n    pipeline:\n      - runs: echo ${{range.key}}\n", codeUndefinedVar, "range:", 10},
		{"template in vars", "vars:\n  a: ${{package.version}}\n", codeVarNotHere, "vars:", 8},
		{"step shape", "pipeline:\n  - uses: fetch\n    runs: echo\n", codeStepShape, "both uses: and runs:", 8},
		{"bad condition", "pipeline:\n  - if: ${{build.arch}} = 'x86_64'\n    runs: echo\n", codeBadCondition, "condition", 8},
		{"unknown linter", "subpackages:\n  - name: hello-dev\n    checks:\n      disabled:\n        - usrloca\n", codeUnknownLinter, `"usrlocal"`, 11},
		{"setcap capability", "  setcap:\n    - path: /usr/bin/x\n      add:\n        cap_sys_time: '+ep'\n      reason: needed\n", codeBadCapability, "not allowed", 8},
		{"bad regex", "var-transforms:\n  - from: ${{package.version}}\n    match: '('\n    replace: x\n    to: y\n", codeBadRegex, "regular expression", 9},
		{"unknown field", "pipeline:\n  - uses: fetch\n    wiht:\n      uri: x\n", codeUnknownField, `did you mean "with"`, 9},
		{"duplicate subpackage", "subpackages:\n  - name: hello-doc\n  - name: hello-doc\n", codeSubpackageName, "duplicate", 9},
		{"range spacing", "data:\n  - name: vs\n    items:\n      a: b\nsubpackages:\n  - name: x-${{range.key}}\n    range: vs\n    pipeline:\n      - runs: echo ${{ range.key }}\n", codeVarSpacing, "exactly", 15},
		{"enum", "update:\n  enabled: true\n  schedule:\n    period: hourly\n    reason: x\n", codeBadEnum, "daily", 10},
		{"deprecated", "update:\n  enabled: true\n  github:\n    identifier: a/b\n    tag-filter: v\n", codeDeprecated, "tag-filter-prefix", 11},
		{"agent note", "# @agent ```\n# never closed\npipeline: []\n", codeAgentNote, "unterminated", 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := tt.body
			text := header + body
			// Package-level keys go inside package:.
			if strings.HasPrefix(body, "  ") {
				text = strings.Replace(header, "  copyright:", body+"  copyright:", 1)
			}
			snap, _ := open(t, root, "hello.yaml", text)
			got := codes(Diagnostics(snap), protocol.DiagnosticSeverityHint)[tt.code]
			if len(got) == 0 {
				t.Fatalf("no %s diagnostic; got %v", tt.code, Diagnostics(snap))
			}
			if !strings.Contains(got[0].Message, tt.contains) {
				t.Errorf("message %q does not contain %q", got[0].Message, tt.contains)
			}
			if got[0].Range.Start.Line != tt.line {
				t.Errorf("line = %d, want %d (%s)", got[0].Range.Start.Line, tt.line, got[0].Message)
			}
		})
	}
}

func TestUnknownYAMLIsIgnored(t *testing.T) {
	root := workspace(t, nil)
	srv := NewTestServer(root)
	snap := srv.Open(document.URI(filepath.Join(root, "hello", "cargobump-deps.yaml")), "packages:\n  - name: x\n    version: 1\n")
	if snap.Active() {
		t.Fatal("cargobump deps should not be treated as melange")
	}
}

func TestPipelineFileDiagnostics(t *testing.T) {
	root := workspace(t, nil)
	snap, _ := open(t, root, "pipelines/local/bad.yaml", `name: bad
inputs:
  flavour:
    description: x
pipeline:
  - runs: echo ${{inputs.flavor}}
  - uses: local/greet
    with:
      whom: x
`)
	got := codes(Diagnostics(snap), protocol.DiagnosticSeverityError)
	if d := got[codeUndefinedVar]; len(d) != 1 || !strings.Contains(d[0].Message, `"flavour"`) {
		t.Errorf("undeclared input: %v", d)
	}
	if d := got[codeUnknownInput]; len(d) != 1 {
		t.Errorf("unknown input in nested step: %v", d)
	}
}

func labels(items []protocol.CompletionItem) map[string]protocol.CompletionItem {
	m := map[string]protocol.CompletionItem{}
	for _, i := range items {
		m[i.Label] = i
	}
	return m
}

func TestCompletion(t *testing.T) {
	root := workspace(t, nil)
	tests := []struct {
		name, text string
		want       []string
		notWant    []string
		check      func(t *testing.T, items map[string]protocol.CompletionItem)
	}{{
		name:    "package keys skip existing",
		text:    "package:\n  name: x\n  §\n",
		want:    []string{"version", "epoch", "copyright", "dependencies"},
		notWant: []string{"name"},
	}, {
		name: "uses values include local and built-in",
		text: header + "pipeline:\n  - uses: §\n",
		want: []string{"local/greet", "fetch", "git-checkout", "autoconf/configure"},
		check: func(t *testing.T, items map[string]protocol.CompletionItem) {
			// Choosing a pipeline adds its required inputs.
			if got := items["local/greet"].TextEdit.NewText; !strings.Contains(got, "with:\n      who: ${1}") {
				t.Errorf("local/greet insert = %q", got)
			}
		},
	}, {
		name:    "with inputs",
		text:    header + "pipeline:\n  - uses: local/greet\n    with:\n      who: x\n      §\n",
		want:    []string{"greeting"},
		notWant: []string{"who", "uri"},
		check: func(t *testing.T, items map[string]protocol.CompletionItem) {
			if got := items["greeting"].TextEdit.NewText; got != "greeting: ${1:hello}" {
				t.Errorf("default placeholder = %q", got)
			}
		},
	}, {
		name:    "step keys exclude runs next to uses",
		text:    header + "pipeline:\n  - uses: fetch\n    §\n",
		want:    []string{"with", "if", "working-directory"},
		notWant: []string{"runs", "uses"},
	}, {
		name:    "templates in scope",
		text:    header + "vars:\n  foo: bar\npipeline:\n  - runs: echo ${{§\n",
		want:    []string{"vars.foo", "package.version", "targets.destdir"},
		notWant: []string{"subpkg.name", "targets.subpkgdir"},
	}, {
		name: "templates in a subpackage",
		text: header + "subpackages:\n  - name: hello-dev\n    pipeline:\n      - runs: echo ${{sub§\n",
		want: []string{"subpkg.name", "targets.subpkgdir"},
	}, {
		name: "linters",
		text: header + "  checks:\n    disabled:\n      - §\n",
		want: []string{"usrlocal", "dev", "sbom"},
	}, {
		name: "agent note",
		text: header + "# @§\n",
		want: []string{"@agent", "@agent ```"},
	}, {
		name: "enum value",
		text: header + "update:\n  schedule:\n    period: §\n",
		want: []string{"daily", "weekly", "monthly"},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap, pos := open(t, root, "hello.yaml", tt.text)
			items := labels(Completion(snap, pos).Items)
			for _, w := range tt.want {
				if _, ok := items[w]; !ok {
					t.Errorf("missing %q in %d items", w, len(items))
				}
			}
			for _, w := range tt.notWant {
				if _, ok := items[w]; ok {
					t.Errorf("unexpected %q", w)
				}
			}
			if tt.check != nil {
				tt.check(t, items)
			}
		})
	}
}

func TestHover(t *testing.T) {
	root := workspace(t, nil)
	tests := []struct{ name, text, want string }{
		{"template value", header + "pipeline:\n  - runs: echo ${{package.ver§sion}}\n", "`1.2.3`"},
		{"var definition", header + "vars:\n  foo: bar\npipeline:\n  - runs: echo ${{vars.f§oo}}\n", "vars:"},
		{"uses docs and effective inputs", header + "pipeline:\n  - uses: local/gr§eet\n    with:\n      who: ${{package.name}}\n", "| `who` | `hello` | with |"},
		{"input", header + "pipeline:\n  - uses: local/greet\n    with:\n      wh§o: x\n", "Who to greet"},
		{"schema key", header + "update:\n  enab§led: true\n", "enabled"},
		{"epoch convention", "package:\n  name: x\n  ep§och: 0\n", "Increment by one"},
		{"resolved uri", header + "pipeline:\n  - uses: fetch\n    with:\n      uri: https://§x/${{package.name}}.tgz\n", "https://x/hello.tgz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap, pos := open(t, root, "hello.yaml", tt.text)
			h := Hover(snap, pos)
			if h == nil {
				t.Fatal("no hover")
			}
			if !strings.Contains(h.Contents.Value, tt.want) {
				t.Errorf("hover %q does not contain %q", h.Contents.Value, tt.want)
			}
		})
	}
}

func TestDefinitionAndRename(t *testing.T) {
	root := workspace(t, map[string]string{"hello/fix.patch": "x"})
	text := header + "vars:\n  foo: bar\npipeline:\n  - uses: local/greet\n    with:\n      who: ${{vars.foo}}\n  - uses: patch\n    with:\n      patches: fix.patch\n  - runs: echo ${{vars.foo}}\n"
	snap, _ := open(t, root, "hello.yaml", text)
	at := func(line, char uint32) protocol.Position { return protocol.Position{Line: line, Character: char} }

	// ${{vars.foo}} → vars key.
	locs := Definition(snap, at(12, 20))
	if len(locs) != 1 || locs[0].Range.Start.Line != 8 {
		t.Errorf("vars definition = %v", locs)
	}
	// uses: → pipeline file; with key → input.
	if locs := Definition(snap, at(10, 12)); len(locs) != 1 || !strings.HasSuffix(string(locs[0].URI), "local/greet.yaml") {
		t.Errorf("uses definition = %v", locs)
	}
	if locs := Definition(snap, at(12, 7)); len(locs) != 1 || locs[0].Range.Start.Line != 2 {
		t.Errorf("input definition = %v", locs)
	}
	if locs := Definition(snap, at(15, 17)); len(locs) != 1 || !strings.HasSuffix(string(locs[0].URI), "hello/fix.patch") {
		t.Errorf("patch definition = %v", locs)
	}

	refs := References(snap, at(8, 3), true)
	if len(refs) != 3 {
		t.Errorf("references = %d, want 3 (definition + 2 uses)", len(refs))
	}
	edit, err := Rename(snap, at(8, 3), "greeting")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edit.Changes[snap.Doc.URI] {
		if e.NewText != "greeting" || e.Range.End.Character-e.Range.Start.Character != 3 {
			t.Errorf("rename edit %+v should replace just the name", e)
		}
	}
	if _, err := Rename(snap, at(8, 3), "bad name"); err == nil {
		t.Error("rename to an invalid name should fail")
	}
}

func TestSymbols(t *testing.T) {
	root := workspace(t, nil)
	snap, _ := open(t, root, "hello.yaml", header+"pipeline:\n  - uses: fetch\n  - name: build it\n    runs: make\nsubpackages:\n  - name: hello-doc\n    pipeline:\n      - uses: split/manpages\n    test:\n      pipeline:\n        - runs: true\n")
	syms := Symbols(snap)
	names := map[string]protocol.DocumentSymbol{}
	for _, s := range syms {
		names[s.Name] = s
	}
	pkg, ok := names["hello"]
	if !ok || pkg.Detail != "1.2.3-r0" || pkg.Range.Start.Line != 0 {
		t.Errorf("package symbol = %+v", pkg)
	}
	pl := names["pipeline"]
	if len(pl.Children) != 2 || pl.Children[1].Name != "build it" || pl.Children[1].Range.Start.Line != 9 {
		t.Errorf("pipeline children = %+v", pl.Children)
	}
	sp := names["subpackages"].Children
	if len(sp) != 1 || sp[0].Name != "hello-doc" || len(sp[0].Children) != 2 {
		t.Errorf("subpackages = %+v", sp)
	}
	for _, s := range syms {
		if !document.Contains(s.Range, s.SelectionRange.Start) {
			t.Errorf("%s: selection %v outside range %v", s.Name, s.SelectionRange, s.Range)
		}
	}
}

func TestLinksAndHints(t *testing.T) {
	root := workspace(t, nil)
	snap, _ := open(t, root, "hello.yaml", header+"pipeline:\n  - uses: fetch\n    with:\n      uri: https://example.com/${{package.name}}-${{package.version}}.tgz\nupdate:\n  enabled: true\n  github:\n    identifier: org/hello\n")
	links := Links(snap)
	targets := map[string]bool{}
	for _, l := range links {
		targets[string(l.Target)] = true
	}
	for _, w := range []string{"https://example.com/hello-1.2.3.tgz", "https://github.com/org/hello"} {
		if !targets[w] {
			t.Errorf("missing link %s in %v", w, targets)
		}
	}
	hints := InlayHints(snap, protocol.Range{End: protocol.Position{Line: 100}})
	if len(hints) != 2 || hints[0].Label != "= hello" || hints[1].Label != "= 1.2.3" {
		t.Errorf("hints = %+v", hints)
	}
}

func TestCodeActions(t *testing.T) {
	root := workspace(t, nil)
	snap, _ := open(t, root, "hello.yaml", header+"pipeline:\n  - uses: local/gret\n# keep this\n# for later\n")
	all := protocol.Range{End: protocol.Position{Line: 100}}
	titles := map[string]protocol.CodeAction{}
	for _, a := range CodeActions(snap, all, protocol.CodeActionContext{}) {
		titles[a.Title] = a
	}
	if _, ok := titles[`Change to "local/greet"`]; !ok {
		t.Errorf("missing did-you-mean fix: %v", titles)
	}
	if a, ok := titles["Increment epoch (0 → 1)"]; !ok || a.Edit.Changes[snap.Doc.URI][0].NewText != "1" {
		t.Errorf("missing epoch bump: %v", titles)
	}

	snap, _ = open(t, root, "hello.yaml", header+"pipeline:\n  - uses: local/greet\n")
	var add *protocol.CodeAction
	for _, a := range CodeActions(snap, all, protocol.CodeActionContext{}) {
		if strings.HasPrefix(a.Title, "Add missing inputs") {
			add = &a
		}
	}
	if add == nil {
		t.Fatal("no add-missing-inputs action")
	}
	if got := add.Edit.Changes[snap.Doc.URI][0].NewText; got != "\n    with:\n      who: " {
		t.Errorf("inserted %q", got)
	}

	snap, _ = open(t, root, "hello.yaml", header+"# keep this\n# for later\npipeline: []\n")
	var conv *protocol.CodeAction
	for _, a := range CodeActions(snap, protocol.Range{Start: protocol.Position{Line: 7}, End: protocol.Position{Line: 7}}, protocol.CodeActionContext{}) {
		if a.Title == "Convert comment to @agent note" {
			conv = &a
		}
	}
	if conv == nil {
		t.Fatal("no convert-to-agent-note action")
	}
	if e := conv.Edit.Changes[snap.Doc.URI]; len(e) != 2 || e[0].NewText != "# @agent ```\n" || e[1].Range.Start.Line != 8 {
		t.Errorf("edits = %+v", e)
	}
}

func TestSemanticTokens(t *testing.T) {
	root := workspace(t, nil)
	snap, _ := open(t, root, "hello.yaml", header+"# @agent: note\npipeline:\n  - runs: echo ${{package.name}}\n")
	data := SemanticTokens(snap).Data
	if len(data)%5 != 0 || len(data) < 5*5 {
		t.Fatalf("tokens = %v", data)
	}
}

func TestCompletionWhileBroken(t *testing.T) {
	root := workspace(t, nil)
	// The half-typed key makes the YAML invalid; completion must still work.
	snap, pos := open(t, root, "hello.yaml", header+"pipeline:\n  - uses: local/greet\n    with:\n      who: x\n    wor§\n  - runs: [\n")
	if snap.Doc.ParseErr == nil {
		t.Fatal("expected the document not to parse")
	}
	if !snap.Active() {
		t.Fatal("a broken melange file must stay active")
	}
	items := labels(Completion(snap, pos).Items)
	if _, ok := items["working-directory"]; !ok {
		t.Errorf("missing working-directory in %d items", len(items))
	}
}

func TestWorkspaceSymbols(t *testing.T) {
	root := workspace(t, map[string]string{
		"hello.yaml": header + "subpackages:\n  - name: hello-dev\n",
		"other.yaml": "package:\n  name: other\n  version: 1\n",
		"ci.yaml":    "jobs: {}\n",
	})
	srv := NewTestServer(root)
	srv.workspace = &workspaceIndex{root: root}
	srv.workspace.build()
	syms := srv.workspaceSymbols(context.Background(), "hello")
	got := map[string]string{}
	for _, s := range syms {
		got[s.Name] = s.ContainerName
	}
	if got["hello"] != "1.2.3" || got["hello-dev"] != "subpackage of hello" {
		t.Errorf("symbols = %v", got)
	}
	if _, ok := got["other"]; ok {
		t.Error("query should filter")
	}
	if syms := srv.workspaceSymbols(context.Background(), "greet"); len(syms) == 0 {
		t.Error("pipelines should be workspace symbols")
	}
}

func TestDerivations(t *testing.T) {
	root := workspace(t, nil)
	text := header + `var-transforms:
  - from: ${{package.version}}
    match: ^(\d+)\.(\d+)\.\d+$
    replace: $1.$2
    to: major-minor
  - from: ${{package.version}}
    match: nomatch
    replace: x
    to: unchanged
pipeline:
  - uses: git-checkout
    with:
      repository: https://github.com/org/hello
      tag: hello-${{vars.underscored}}
      expected-commit: 0000000000000000000000000000000000000000
vars:
  underscored: 1_2_3
update:
  enabled: true
  github:
    identifier: org/hello
    strip-prefix: hello-
  version-separator: _
`
	snap, _ := open(t, root, "hello.yaml", text)

	// Hovering the to: name shows the computation.
	h := Hover(snap, protocol.Position{Line: 11, Character: 9})
	if h == nil || !strings.Contains(h.Contents.Value, "`${{vars.major-minor}}` = `1.2`") || !strings.Contains(h.Contents.Value, "| from | `1.2.3` |") {
		t.Errorf("to: hover = %v", h)
	}
	h = Hover(snap, protocol.Position{Line: 13, Character: 12})
	if h == nil || !strings.Contains(h.Contents.Value, "**no match**") {
		t.Errorf("non-matching transform hover = %v", h)
	}

	// The update block traces the checked-out tag to the version.
	u := traceUpdate(snap)
	if u == nil || !u.Actual || u.Input != "hello-1_2_3" || !u.Matches() {
		t.Fatalf("update trace = %+v", u)
	}
	if h := Hover(snap, protocol.Position{Line: 28, Character: 6}); h == nil || !strings.Contains(h.Contents.Value, "✓ matches") {
		t.Errorf("update hover = %v", h)
	}

	var hint bool
	for _, ih := range InlayHints(snap, protocol.Range{End: protocol.Position{Line: 100}}) {
		if ih.Position.Line == 11 && ih.Label == "= 1.2" {
			hint = true
		}
	}
	if !hint {
		t.Error("no inlay hint after to: major-minor")
	}
}

func TestUpdateReconstruction(t *testing.T) {
	root := workspace(t, nil)
	// No git-checkout: the tag is rebuilt from the version and settings.
	snap, _ := open(t, root, "hello.yaml", header+"update:\n  enabled: true\n  github:\n    identifier: org/hello\n    strip-prefix: release/v\n    tag-filter-prefix: release/\n")
	u := traceUpdate(snap)
	if u == nil || u.Actual || u.Input != "release/v1.2.3" || !u.Matches() {
		t.Errorf("trace = %+v", u)
	}
}

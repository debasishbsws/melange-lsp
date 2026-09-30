// Command tour prints what each language server feature returns for the
// example in examples/tour, as a quick look without an editor:
//
//	go run ./hack/tour
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.lsp.dev/protocol"

	"melange-lsp/internal/document"
	"melange-lsp/internal/lsp"
)

func main() {
	root, _ := filepath.Abs("examples/tour")
	file := filepath.Join(root, "hello.yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "run from the repository root:", err)
		os.Exit(1)
	}
	snap := lsp.NewTestServer(root).Open(document.URI(file), string(data))
	lines := strings.Split(string(data), "\n")

	// find returns the position of the n-th occurrence of needle, plus off.
	find := func(needle string, off int) protocol.Position {
		for i, l := range lines {
			if j := strings.Index(l, needle); j >= 0 {
				return protocol.Position{Line: uint32(i), Character: uint32(j + off)}
			}
		}
		panic("not found: " + needle)
	}
	at := func(p protocol.Position) string {
		return fmt.Sprintf("line %d: %s", p.Line+1, strings.TrimSpace(lines[p.Line]))
	}
	section := func(title string) { fmt.Printf("\n\033[1m== %s ==\033[0m\n", title) }
	short := func(s string, n int) string {
		s = strings.ReplaceAll(s, "\n", " ⏎ ")
		if len([]rune(s)) > n {
			return string([]rune(s)[:n]) + "…"
		}
		return s
	}

	section("Diagnostics")
	for _, d := range lsp.Diagnostics(snap) {
		sev := map[protocol.DiagnosticSeverity]string{1: "error", 2: "warning", 3: "info", 4: "hint"}[d.Severity]
		fmt.Printf("  %d:%d %-7s [%v] %s\n", d.Range.Start.Line+1, d.Range.Start.Character+1, sev, d.Code, short(d.Message, 110))
	}

	section("Code actions (quick fixes and refactorings)")
	for _, a := range lsp.CodeActions(snap, protocol.Range{End: protocol.Position{Line: uint32(len(lines))}}, protocol.CodeActionContext{}) {
		fmt.Printf("  %-17s %s\n", a.Kind, a.Title)
	}

	completion := func(label string, p protocol.Position, max int) {
		items := lsp.Completion(snap, p).Items
		var names []string
		for _, it := range items {
			names = append(names, it.Label)
		}
		if len(names) > max {
			names = append(names[:max], fmt.Sprintf("… %d more", len(items)-max))
		}
		fmt.Printf("  %s (%s)\n    %s\n", label, at(p), strings.Join(names, ", "))
	}
	section("Completion")
	completion("uses: values", find("uses: autoconf/configure", 6), 8)
	completion("${{ variables in a subpackage", find("echo ${{vars.greting}}", 8), 12)
	completion("checks.disabled linters", find("- usrloca", 2), 8)
	// Choosing local/greet on an empty step inserts its required inputs.
	for _, it := range lsp.Completion(snap, find("uses: autoconf/configure", 6)).Items {
		if it.Label == "local/greet" {
			fmt.Printf("  choosing local/greet inserts:\n    %s\n", strings.ReplaceAll(it.TextEdit.NewText, "\n", "\n    "))
		}
	}

	hover := func(label string, p protocol.Position) {
		h := lsp.Hover(snap, p)
		if h == nil {
			fmt.Printf("  %s: (none)\n", label)
			return
		}
		fmt.Printf("  %s (%s)\n", label, at(p))
		for _, l := range strings.Split(strings.TrimSpace(h.Contents.Value), "\n") {
			fmt.Printf("    │ %s\n", l)
		}
	}
	section("Hover")
	hover("variable", find("${{vars.major-minor}}", 8))
	hover("pipeline step", find("uses: local/greet", 8))
	hover("key docs", find("epoch: 0", 2))
	hover("deprecated key", find("tag-filter: v", 2))
	hover("var-transforms to:", find("to: major-minor", 5))
	hover("update block", find("strip-prefix: v", 2))

	section("Go to definition")
	for _, c := range []struct {
		label string
		p     protocol.Position
	}{
		{"${{vars.greeting}}", find("${{vars.greeting}}", 9)},
		{"uses: local/greet", find("uses: local/greet", 8)},
		{"uses: fetch (built-in)", find("uses: fetch", 7)},
		{"patches: fix-cflags.patch", find("fix-cflags.patch", 1)},
		{"with: who", find("who: ${{", 1)},
	} {
		for _, l := range lsp.Definition(snap, c.p) {
			fmt.Printf("  %-26s → %s:%d\n", c.label, relTo(root, document.Filename(l.URI)), l.Range.Start.Line+1)
		}
	}

	section("References and rename")
	p := find("  greeting: hi there", 3)
	fmt.Printf("  vars.greeting: %d references (definition + uses)\n", len(lsp.References(snap, p, true)))
	if e, err := lsp.Rename(snap, p, "salutation"); err == nil {
		fmt.Printf("  rename to salutation edits %d places\n", len(e.Changes[snap.Doc.URI]))
	}

	section("Outline")
	var walk func([]protocol.DocumentSymbol, string)
	walk = func(syms []protocol.DocumentSymbol, indent string) {
		for _, s := range syms {
			detail := ""
			if s.Detail != "" {
				detail = "  · " + short(s.Detail, 40)
			}
			fmt.Printf("  %s%s%s  (line %d)\n", indent, s.Name, detail, s.Range.Start.Line+1)
			walk(s.Children, indent+"  ")
		}
	}
	walk(lsp.Symbols(snap), "")

	section("Links")
	for _, l := range lsp.Links(snap) {
		fmt.Printf("  line %d → %s\n", l.Range.Start.Line+1, relTo(root, strings.TrimPrefix(string(l.Target), "file://")))
	}

	section("Inlay hints (resolved values shown inline)")
	for _, h := range lsp.InlayHints(snap, protocol.Range{End: protocol.Position{Line: uint32(len(lines))}}) {
		fmt.Printf("  line %d col %d: %s\n", h.Position.Line+1, h.Position.Character+1, h.Label)
	}

	section("Code lenses")
	for _, c := range lsp.CodeLenses(snap) {
		fmt.Printf("  line %d: %s\n", c.Range.Start.Line+1, c.Command.Title)
	}

	section("Folding: agent notes")
	for _, f := range lsp.FoldingRanges(snap) {
		if f.CollapsedText != "" {
			fmt.Printf("  lines %d-%d fold to %q\n", f.StartLine+1, f.EndLine+1, f.CollapsedText)
		}
	}
	fmt.Println()
}

func relTo(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	if i := strings.Index(p, "melange-lsp/pipelines-"); i >= 0 {
		return "(melange built-in) " + p[strings.Index(p[i:], "/")+i+1:]
	}
	return p
}

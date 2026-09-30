package lsp

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"melange-lsp/internal/document"
	"melange-lsp/internal/known"
	"melange-lsp/internal/model"
	"melange-lsp/internal/pipeline"
	"melange-lsp/internal/schema"
	"melange-lsp/internal/subst"

	"go.lsp.dev/protocol"
)

// Completion returns completions at pos.
func Completion(snap *Snapshot, pos protocol.Position) *protocol.CompletionList {
	c := &completer{snap: snap, doc: snap.Doc, pos: pos}
	c.run()
	if c.items == nil {
		c.items = []protocol.CompletionItem{}
	}
	return &protocol.CompletionList{IsIncomplete: c.incomplete, Items: c.items}
}

type completer struct {
	snap       *Snapshot
	doc        *document.Document
	pos        protocol.Position
	ctx        document.Context
	line       string
	prefix     string // line text before the cursor
	items      []protocol.CompletionItem
	incomplete bool
}

var unclosedTemplate = regexp.MustCompile(`\$\{\{\s*([A-Za-z0-9._-]*)$`)

func (c *completer) run() {
	c.line = c.doc.Line(int(c.pos.Line))
	c.prefix = c.line[:c.doc.ByteOffset(c.pos)]

	if m := unclosedTemplate.FindStringSubmatchIndex(c.prefix); m != nil {
		c.templates(c.prefix[m[2]:m[3]])
		return
	}

	c.ctx = c.doc.Context(c.pos)
	switch {
	case c.ctx.InComment:
		c.agentNotes()
	case c.ctx.InValue:
		c.value()
	case c.ctx.NewItem && c.scalarList():
		// handled
	default:
		c.keys()
	}
}

func (c *completer) root() string {
	if c.doc.Kind == document.KindPipeline {
		return schema.RootPipeline
	}
	return schema.RootConfig
}

// wordRange covers the partial word before the cursor and any word
// characters after it, so accepting a completion replaces the whole word.
func (c *completer) wordRange(word string) protocol.Range {
	start := c.doc.ByteOffset(c.pos) - len(word)
	end := c.doc.ByteOffset(c.pos)
	for end < len(c.line) && isWordByte(c.line[end]) {
		end++
	}
	return protocol.Range{Start: c.doc.ByteToPosition(int(c.pos.Line), start), End: c.doc.ByteToPosition(int(c.pos.Line), end)}
}

func isWordByte(b byte) bool {
	return b == '-' || b == '_' || b == '.' || b == '/' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func (c *completer) add(item protocol.CompletionItem) {
	c.items = append(c.items, item)
}

func markdown(s string) *protocol.MarkupContent {
	if s == "" {
		return nil
	}
	return &protocol.MarkupContent{Kind: protocol.Markdown, Value: s}
}

var snippetEscaper = strings.NewReplacer(`\`, `\\`, `$`, `\$`, `}`, `\}`)

func snippetEscape(s string) string { return snippetEscaper.Replace(s) }

// snippet is a completion that replaces r with a snippet, keeping the
// indentation we computed rather than letting the client re-indent it.
func snippet(label string, kind protocol.CompletionItemKind, r protocol.Range, text string) protocol.CompletionItem {
	return protocol.CompletionItem{
		Label:            label,
		Kind:             kind,
		TextEdit:         &protocol.TextEdit{Range: r, NewText: text},
		InsertTextFormat: protocol.InsertTextFormatSnippet,
		InsertTextMode:   protocol.InsertTextModeAsIs,
		FilterText:       label,
	}
}

// keys completes mapping keys at the cursor's indentation.
func (c *completer) keys() {
	path := c.ctx.Path
	r := c.wordRange(c.ctx.Word)
	col := int(r.Start.Character)
	indent := strings.Repeat(" ", col)

	// Inputs of the step's uses: pipeline.
	if path.Last() == "with" && len(path) > 0 && !path[len(path)-1].IsIdx {
		if c.withInputs(path, r) {
			return
		}
	}

	fields := schema.FieldsFrom(c.root(), path.Keys())
	def, _ := schema.DefAtFrom(c.root(), path.Keys())
	for _, f := range fields {
		if c.ctx.Siblings[f.Name] {
			continue
		}
		if def == "Pipeline" && !c.fitsStep(f.Name) {
			continue
		}
		item := snippet(f.Name, protocol.CompletionItemKindProperty, r, f.Name+keySuffix(f, indent))
		item.Detail = fieldDetail(f)
		item.Documentation = markdown(f.Description)
		item.SortText = "1" + f.Name
		if f.Required {
			item.Kind = protocol.CompletionItemKindField
			item.SortText = "0" + f.Name
		}
		c.add(item)
	}

	if def == "Pipeline" && c.ctx.NewItem {
		c.stepSnippets(r, col)
	}
	if def == "Configuration" && len(c.ctx.Siblings) == 0 {
		c.newConfigSnippet(r)
	}
}

// fitsStep hides keys that cannot be combined with what the step already
// has, following melange's validation.
func (c *completer) fitsStep(key string) bool {
	s := c.ctx.Siblings
	switch key {
	case "runs":
		return !s["uses"] && !s["with"]
	case "uses":
		return !s["runs"]
	case "with":
		return !s["runs"]
	}
	return true
}

func keySuffix(f schema.Field, indent string) string {
	switch {
	case f.Type == "array":
		return ":\n" + indent + "  - $0"
	case f.Type == "object" || f.Type == "map":
		return ":\n" + indent + "  $0"
	case len(f.Enum) > 0:
		return ": ${1|" + strings.Join(f.Enum, ",") + "|}"
	case f.Type == "boolean":
		return ": ${1|true,false|}"
	}
	return ": $0"
}

func fieldDetail(f schema.Field) string {
	t := f.Type
	if f.Type == "array" && f.ItemType != "" {
		t = "[]" + f.ItemType
	} else if f.Def != "" {
		t = f.Def
		if f.Type == "array" {
			t = "[]" + t
		}
	}
	if f.Required {
		return t + " (required)"
	}
	return t
}

// withInputs completes the inputs of the pipeline named by the step's uses:.
func (c *completer) withInputs(path document.Path, r protocol.Range) bool {
	step := c.stepAt(path[:len(path)-1])
	if step == nil || step.Uses == nil {
		return false
	}
	def := c.snap.Registry.Resolve(step.Uses.Value, c.snap.Dirs)
	if def == nil {
		return false
	}
	for i, in := range def.Inputs() {
		if c.ctx.Siblings[in.Name] {
			continue
		}
		val := ""
		if in.Default != nil {
			val = in.Default.Value
		}
		text := in.Name + ": ${1:" + snippetEscape(val) + "}"
		item := snippet(in.Name, protocol.CompletionItemKindProperty, r, text)
		item.Detail = inputDetail(in)
		item.Documentation = markdown(in.Description)
		item.SortText = fmt.Sprintf("1%03d", i)
		if in.Required && (in.Default == nil || in.Default.Value == "") {
			item.Kind = protocol.CompletionItemKindField
			item.SortText = fmt.Sprintf("0%03d", i)
		}
		c.add(item)
	}
	return true
}

func inputDetail(in *model.Input) string {
	var parts []string
	if in.Required {
		parts = append(parts, "required")
	}
	if in.Default != nil {
		d := in.Default.Value
		if len(d) > 40 {
			d = d[:37] + "..."
		}
		parts = append(parts, fmt.Sprintf("default %q", d))
	}
	if len(parts) == 0 {
		return "optional"
	}
	return strings.Join(parts, ", ")
}

// stepAt finds the step whose mapping is at path. The document may not
// parse mid-edit, so this falls back to nil.
func (c *completer) stepAt(path document.Path) *model.Step {
	for _, s := range c.snap.Model.Steps {
		if s.Path.String() == path.String() {
			return s
		}
	}
	return nil
}

// value completes the value after "key: ".
func (c *completer) value() {
	key := c.ctx.Key
	r := c.wordRange(c.ctx.Word)
	path := c.ctx.Path

	switch {
	case key == "uses":
		c.uses(r)
		return
	case key == "range" && c.inSubpackage():
		for _, name := range sortedKeys(c.snap.Model.Data) {
			item := protocol.CompletionItem{Label: name, Kind: protocol.CompletionItemKindEnum, TextEdit: &protocol.TextEdit{Range: r, NewText: name}}
			item.Detail = fmt.Sprintf("%d items", len(c.snap.Model.Data[name]))
			item.Documentation = markdown("`" + strings.Join(c.snap.Model.Data[name], "`, `") + "`")
			c.add(item)
		}
		return
	}

	// Input values: offer the default.
	if len(path) >= 2 && path[len(path)-2].Key == "with" {
		if step := c.stepAt(path[:len(path)-2]); step != nil && step.Uses != nil {
			if def := c.snap.Registry.Resolve(step.Uses.Value, c.snap.Dirs); def != nil {
				if in := def.Input(key); in != nil && in.Default != nil && in.Default.Value != "" {
					c.add(protocol.CompletionItem{
						Label: in.Default.Value, Kind: protocol.CompletionItemKindValue, Detail: "default",
						TextEdit: &protocol.TextEdit{Range: r, NewText: in.Default.Value},
					})
				}
			}
		}
		return
	}

	f, _, ok := schema.LookupFrom(c.root(), path.Keys())
	if !ok {
		return
	}
	vals := f.Enum
	if f.Type == "boolean" {
		vals = []string{"true", "false"}
	}
	for _, v := range vals {
		c.add(protocol.CompletionItem{Label: v, Kind: protocol.CompletionItemKindEnumMember, TextEdit: &protocol.TextEdit{Range: r, NewText: v}})
	}
}

func (c *completer) inSubpackage() bool {
	p := c.ctx.Path
	return len(p) >= 2 && p[0].Key == "subpackages" && p[1].IsIdx
}

func (c *completer) subpackage() *model.Subpackage {
	p := c.ctx.Path
	if len(p) >= 2 && p[0].Key == "subpackages" && p[1].IsIdx && p[1].Index < len(c.snap.Model.Subpackages) {
		return c.snap.Model.Subpackages[p[1].Index]
	}
	return nil
}

// uses completes pipeline names. Choosing one also inserts a with: block
// for its required inputs when the step has none yet.
func (c *completer) uses(r protocol.Range) {
	keyCol := strings.Index(c.line, "uses:")
	indent := strings.Repeat(" ", keyCol)
	hasWith := c.doc.Siblings(int(c.pos.Line), keyCol)["with"]

	for _, def := range c.snap.Registry.List(c.snap.Dirs) {
		text := def.Name
		if !hasWith {
			var req []string
			for _, in := range def.Inputs() {
				if in.Required && (in.Default == nil || in.Default.Value == "") {
					req = append(req, in.Name)
				}
			}
			if len(req) > 0 {
				text += "\n" + indent + "with:"
				for i, name := range req {
					text += fmt.Sprintf("\n%s  %s: ${%d}", indent, name, i+1)
				}
			}
		}
		item := snippet(def.Name, protocol.CompletionItemKindModule, r, text)
		item.Detail = def.Summary()
		if def.Builtin {
			item.Detail += " (built-in)"
		}
		item.Documentation = markdown(pipelineDoc(def))
		c.add(item)
	}
}

// scalarList completes items of lists with known values. It reports
// whether the list was one of those.
func (c *completer) scalarList() bool {
	path := c.ctx.Path
	if len(path) < 2 || !path[len(path)-1].IsIdx {
		return false
	}
	parent := path[len(path)-2].Key
	r := c.wordRange(c.ctx.Word)
	switch {
	case parent == "disabled" && path.Has("checks"):
		for _, l := range known.Linters {
			item := protocol.CompletionItem{Label: l.Name, Kind: protocol.CompletionItemKindEnumMember, TextEdit: &protocol.TextEdit{Range: r, NewText: l.Name}}
			item.Detail = "linter, " + l.Default + " by default"
			item.Documentation = markdown(l.Explain)
			c.add(item)
		}
	case (parent == "add" || parent == "drop") && path.Has("capabilities"):
		for _, cp := range known.RunnerCapabilities {
			c.add(protocol.CompletionItem{Label: cp, Kind: protocol.CompletionItemKindEnumMember, TextEdit: &protocol.TextEdit{Range: r, NewText: cp}})
		}
	case parent == "packages" || parent == "runtime" || parent == "provides" || parent == "replaces":
		c.packageNames(r)
	default:
		return false
	}
	return true
}

// packageNames completes package names: this file's own packages and
// those found in the workspace.
func (c *completer) packageNames(r protocol.Range) {
	seen := map[string]bool{}
	add := func(name, detail string) {
		if seen[name] || name == "" || strings.Contains(name, "${{") {
			return
		}
		seen[name] = true
		c.add(protocol.CompletionItem{Label: name, Kind: protocol.CompletionItemKindModule, Detail: detail, TextEdit: &protocol.TextEdit{Range: r, NewText: name}})
	}
	for _, sp := range c.snap.Model.Subpackages {
		add(sp.Name, "subpackage")
	}
	if c.snap.workspace == nil {
		return
	}
	word := c.ctx.Word
	n := 0
	for _, p := range c.snap.workspace.packages() {
		if word != "" && !strings.HasPrefix(p.Name, word) {
			continue
		}
		if n >= 200 {
			c.incomplete = true
			break
		}
		add(p.Name, p.Version)
		n++
	}
}

func (c *completer) stepSnippets(r protocol.Range, col int) {
	in := strings.Repeat(" ", col)
	type snip struct{ label, detail, body string }
	snips := []snip{
		{"fetch step", "download and extract a release tarball", "uses: fetch\n" + in + "with:\n" + in + "  uri: ${1:https://example.com/${{package.name}}-${{package.version}}.tar.gz}\n" + in + "  expected-sha256: ${2}"},
		{"git-checkout step", "clone a repository at a tag", "uses: git-checkout\n" + in + "with:\n" + in + "  repository: ${1:https://github.com/org/repo}\n" + in + "  tag: ${2:v${{package.version}}}\n" + in + "  expected-commit: ${3}"},
		{"runs step", "run a shell script", "runs: |\n" + in + "  ${0}"},
		{"named runs step", "run a shell script with a name", "name: ${1:description}\n" + in + "runs: |\n" + in + "  ${0}"},
	}
	for _, s := range snips {
		item := snippet(s.label, protocol.CompletionItemKindSnippet, r, s.body)
		item.Detail = s.detail
		item.SortText = "2" + s.label
		c.add(item)
	}
}

func (c *completer) newConfigSnippet(r protocol.Range) {
	name := strings.TrimSuffix(pathBase(document.Filename(c.doc.URI)), ".yaml")
	body := "package:\n  name: ${1:" + snippetEscape(name) + "}\n  version: \"${2:1.0.0}\"\n  epoch: 0\n  description: ${3}\n  copyright:\n    - license: ${4:Apache-2.0}\n\n" +
		"environment:\n  contents:\n    packages:\n      - build-base\n      - busybox\n\n" +
		"pipeline:\n  - uses: fetch\n    with:\n      uri: ${5}\n      expected-sha256: ${6}\n\n  - uses: autoconf/configure\n\n  - uses: autoconf/make\n\n  - uses: autoconf/make-install\n\n  - uses: strip\n\n" +
		"update:\n  enabled: true\n"
	item := snippet("new melange package", protocol.CompletionItemKindSnippet, r, body)
	item.Detail = "skeleton package configuration"
	item.SortText = "9"
	c.add(item)
}

func pathBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// agentNotes offers @agent note snippets inside comments.
func (c *completer) agentNotes() {
	i := strings.LastIndex(c.prefix, "#")
	after := c.prefix[i+1:]
	if !regexp.MustCompile(`^\s*@?[a-z]*$`).MatchString(after) || strings.TrimSpace(c.prefix[:i]) != "" {
		return
	}
	word := strings.TrimSpace(after)
	start := c.doc.ByteOffset(c.pos) - len(word)
	r := protocol.Range{Start: c.doc.ByteToPosition(int(c.pos.Line), start), End: c.pos}
	indent := c.prefix[:i]

	one := snippet("@agent", protocol.CompletionItemKindSnippet, r, "@agent: ${0}")
	one.Detail = "one-line note for AI agents"
	one.Documentation = markdown("A comment addressed to AI agents. Editors using melange-lsp can fold it away. See `docs/agent-notes.md`.")
	c.add(one)

	block := snippet("@agent ```", protocol.CompletionItemKindSnippet, r, "@agent ${1:heading} ```\n"+indent+"# ${0}\n"+indent+"# ```")
	block.Detail = "multi-line note for AI agents"
	block.FilterText = "@agent"
	c.add(block)
}

// templates completes ${{...}} variables visible at the cursor.
func (c *completer) templates(partial string) {
	start := c.doc.ByteOffset(c.pos) - len(partial)
	end := c.doc.ByteOffset(c.pos)
	for end < len(c.line) && isWordByte(c.line[end]) {
		end++
	}
	closing := "}}"
	if strings.HasPrefix(strings.TrimLeft(c.line[end:], " "), "}}") {
		closing = ""
	}
	r := protocol.Range{Start: c.doc.ByteToPosition(int(c.pos.Line), start), End: c.doc.ByteToPosition(int(c.pos.Line), end)}

	ctx := c.doc.Context(c.pos)
	c.ctx = ctx
	sp := c.subpackage()
	var step *model.Step
	if c.snap.Doc.Root != nil {
		if loc := c.doc.Locate(c.pos); loc != nil {
			step = c.snap.Model.StepAt(loc.Path)
			sp = c.snap.Model.SubpackageAt(loc.Path)
		}
	}
	sc := subst.Scope{Subpackage: sp, Inputs: stepInputs(c.snap, step)}

	names := map[string]string{} // name -> resolved value
	sm := c.snap.Subst()
	for _, n := range sm.Names(sc) {
		v, _ := sm.Lookup(n, sc)
		names[n] = v
	}
	for _, v := range c.snap.Model.VarOrder {
		if _, ok := names["vars."+v]; !ok {
			names["vars."+v] = ""
		}
	}
	if c.doc.Kind == document.KindPipeline {
		for _, in := range c.snap.Model.InputOrder {
			d := ""
			if def := c.snap.Model.Inputs[in].Default; def != nil {
				d = def.Value
			}
			names["inputs."+in] = d
		}
		for _, v := range known.Variables {
			if !strings.HasSuffix(v.Name, ".") && v.Scope == known.ScopePackage {
				if _, ok := names[v.Name]; !ok {
					names[v.Name] = ""
				}
			}
		}
	}
	if sp != nil && sp.Range != "" {
		names["range.key"] = strings.Join(c.snap.Model.Data[sp.Range], " | ")
		names["range.value"] = ""
	}

	keys := make([]string, 0, len(names))
	for n := range names {
		if strings.Contains(n, partial) {
			keys = append(keys, n)
		}
	}
	sort.Strings(keys)
	for _, n := range keys {
		item := protocol.CompletionItem{
			Label:    n,
			Kind:     protocol.CompletionItemKindVariable,
			TextEdit: &protocol.TextEdit{Range: r, NewText: n + closing},
			Detail:   names[n],
		}
		if doc, ok := known.VariableDoc(n); ok {
			item.Documentation = markdown(doc.Doc)
		}
		// Rank the common namespaces first.
		switch {
		case strings.HasPrefix(n, "vars."), strings.HasPrefix(n, "inputs."), strings.HasPrefix(n, "range."):
			item.SortText = "0" + n
		case strings.HasPrefix(n, "package."), strings.HasPrefix(n, "targets."):
			item.SortText = "1" + n
		default:
			item.SortText = "2" + n
		}
		c.add(item)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// pipelineDoc is Markdown documentation for a pipeline: its title, inputs,
// needed packages, and where it is defined.
func pipelineDoc(def *pipeline.Def) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**", def.Name)
	if def.Title != "" && def.Title != def.Name {
		fmt.Fprintf(&b, " — %s", strings.TrimSpace(def.Title))
	}
	b.WriteString("\n\n")
	if ins := def.Inputs(); len(ins) > 0 {
		b.WriteString("Inputs:\n\n")
		for _, in := range ins {
			fmt.Fprintf(&b, "- `%s`", in.Name)
			switch {
			case in.Required && (in.Default == nil || in.Default.Value == ""):
				b.WriteString(" (required)")
			case in.Default != nil && in.Default.Value != "":
				fmt.Fprintf(&b, " = `%s`", strings.ReplaceAll(truncate(in.Default.Value, 40), "`", "'"))
			}
			if desc := strings.Join(strings.Fields(in.Description), " "); desc != "" {
				b.WriteString(" — " + truncate(desc, 160))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	} else {
		b.WriteString("No inputs.\n\n")
	}
	if len(def.Needs) > 0 {
		fmt.Fprintf(&b, "Needs: `%s`\n\n", strings.Join(def.Needs, "`, `"))
	}
	if def.Builtin {
		b.WriteString("_melange built-in pipeline_")
	} else {
		fmt.Fprintf(&b, "_Defined in `%s`_", def.File)
	}
	return b.String()
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

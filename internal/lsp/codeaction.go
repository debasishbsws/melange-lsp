package lsp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"melange-lsp/internal/document"

	"go.lsp.dev/protocol"
)

// CodeActions returns quick fixes for diagnostics in r and refactorings
// that apply at r.
func CodeActions(snap *Snapshot, r protocol.Range, ctx protocol.CodeActionContext) []protocol.CodeAction {
	out := []protocol.CodeAction{}
	uri := snap.Doc.URI
	edit := func(title string, kind protocol.CodeActionKind, diag *protocol.Diagnostic, edits ...protocol.TextEdit) protocol.CodeAction {
		a := protocol.CodeAction{
			Title: title,
			Kind:  kind,
			Edit:  &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{uri: edits}},
		}
		if diag != nil {
			a.Diagnostics = []protocol.Diagnostic{*diag}
			a.IsPreferred = true
		}
		return a
	}

	// Recompute diagnostics rather than trusting the client's copy: some
	// clients drop the data field.
	for _, d := range Diagnostics(snap) {
		if !overlaps(d.Range, r) {
			continue
		}
		d := d
		code, _ := d.Code.(string)
		for i, rep := range fixReplacements(d) {
			a := edit(fmt.Sprintf("Change to %q", rep), protocol.QuickFix, &d, protocol.TextEdit{Range: d.Range, NewText: rep})
			a.IsPreferred = i == 0
			out = append(out, a)
		}
		if code == codeMissingInput {
			if a, ok := addMissingInputs(snap, d); ok {
				out = append(out, a)
			}
		}
	}

	if a, ok := bumpEpoch(snap); ok {
		out = append(out, a)
	}
	if a, ok := toAgentNote(snap, r); ok {
		out = append(out, a)
	}
	return out
}

func overlaps(a, b protocol.Range) bool {
	return !after(a.Start, b.End) && !after(b.Start, a.End)
}

func after(a, b protocol.Position) bool {
	return a.Line > b.Line || (a.Line == b.Line && a.Character > b.Character)
}

func fixReplacements(d protocol.Diagnostic) []string {
	if f, ok := d.Data.(fix); ok {
		return f.Replace
	}
	// From a client round trip the data arrives as a map.
	b, err := json.Marshal(d.Data)
	if err != nil {
		return nil
	}
	var f fix
	_ = json.Unmarshal(b, &f)
	return f.Replace
}

// addMissingInputs inserts the required inputs a step is missing.
func addMissingInputs(snap *Snapshot, d protocol.Diagnostic) (protocol.CodeAction, bool) {
	doc := snap.Doc
	for _, s := range snap.Model.Steps {
		if s.Uses == nil || doc.Range(s.Uses) != d.Range {
			continue
		}
		def := snap.Registry.Resolve(s.Uses.Value, snap.Dirs)
		if def == nil {
			return protocol.CodeAction{}, false
		}
		keyCol := s.UsesKey.Column - 1
		var missing []string
		for _, in := range def.Inputs() {
			if in.Required && (in.Default == nil || in.Default.Value == "") && document.MapValue(s.With, in.Name) == nil {
				missing = append(missing, in.Name)
			}
		}
		if len(missing) == 0 {
			return protocol.CodeAction{}, false
		}

		var text strings.Builder
		var at protocol.Position
		if s.With != nil && len(s.With.Content) > 0 {
			// Append after the last existing input.
			last := s.With.Content[len(s.With.Content)-2]
			at = doc.LineEnd(int(doc.Range(s.With.Content[len(s.With.Content)-1]).End.Line))
			for _, name := range missing {
				fmt.Fprintf(&text, "\n%s%s: ", strings.Repeat(" ", last.Column-1), name)
			}
		} else {
			if s.WithKey != nil {
				at = doc.LineEnd(s.WithKey.Line - 1)
			} else {
				at = doc.LineEnd(s.Uses.Line - 1)
				fmt.Fprintf(&text, "\n%swith:", strings.Repeat(" ", keyCol))
			}
			for _, name := range missing {
				fmt.Fprintf(&text, "\n%s  %s: ", strings.Repeat(" ", keyCol), name)
			}
		}
		d := d
		return protocol.CodeAction{
			Title:       "Add missing inputs: " + strings.Join(missing, ", "),
			Kind:        protocol.QuickFix,
			Diagnostics: []protocol.Diagnostic{d},
			IsPreferred: true,
			Edit: &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{
				doc.URI: {{Range: protocol.Range{Start: at, End: at}, NewText: text.String()}},
			}},
		}, true
	}
	return protocol.CodeAction{}, false
}

// bumpEpoch increments package.epoch, needed for any change that does not
// change the version.
func bumpEpoch(snap *Snapshot) (protocol.CodeAction, bool) {
	if snap.Doc.Kind != document.KindConfig {
		return protocol.CodeAction{}, false
	}
	e := document.MapValue(document.MapValue(snap.Doc.Root, "package"), "epoch")
	if e == nil {
		return protocol.CodeAction{}, false
	}
	n, err := strconv.Atoi(e.Value)
	if err != nil {
		return protocol.CodeAction{}, false
	}
	return protocol.CodeAction{
		Title: fmt.Sprintf("Increment epoch (%d → %d)", n, n+1),
		Kind:  protocol.Source,
		Edit: &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{
			snap.Doc.URI: {{Range: snap.Doc.Range(e), NewText: strconv.Itoa(n + 1)}},
		}},
	}, true
}

// toAgentNote wraps the comment block at r in an @agent fence.
func toAgentNote(snap *Snapshot, r protocol.Range) (protocol.CodeAction, bool) {
	d := snap.Doc
	isComment := func(i int) bool {
		return strings.HasPrefix(strings.TrimSpace(d.Line(i)), "#")
	}
	line := int(r.Start.Line)
	if !isComment(line) {
		return protocol.CodeAction{}, false
	}
	for _, n := range parseAgentNotes(d.Text) {
		if line >= n.StartLine && line <= n.EndLine {
			return protocol.CodeAction{}, false
		}
	}
	start, end := line, line
	for start > 0 && isComment(start-1) {
		start--
	}
	for end+1 < len(d.Lines) && isComment(end+1) {
		end++
	}
	// Do not swallow an adjacent agent note.
	for _, n := range parseAgentNotes(d.Text) {
		if n.EndLine >= start && n.EndLine < line {
			start = n.EndLine + 1
		}
		if n.StartLine <= end && n.StartLine > line {
			end = n.StartLine - 1
		}
	}
	indent := d.Line(start)[:document.IndentOf(d.Line(start))]
	open := protocol.Position{Line: uint32(start)}
	closeAt := d.LineEnd(end)
	return protocol.CodeAction{
		Title: "Convert comment to @agent note",
		Kind:  protocol.RefactorRewrite,
		Edit: &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{d.URI: {
			{Range: protocol.Range{Start: open, End: open}, NewText: indent + "# @agent ```\n"},
			{Range: protocol.Range{Start: closeAt, End: closeAt}, NewText: "\n" + indent + "# ```"},
		}}},
	}, true
}

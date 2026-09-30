package lsp

import (
	"fmt"
	"regexp"
	"strings"

	"melange-lsp/internal/document"
	"melange-lsp/internal/model"
	"melange-lsp/internal/subst"

	"go.lsp.dev/protocol"
	"gopkg.in/yaml.v3"
)

var httpURL = regexp.MustCompile(`^https?://\S+$`)

// Links returns clickable links: resolved source URLs, upstream project
// pages from update:, and patch files.
func Links(snap *Snapshot) []protocol.DocumentLink {
	d, m := snap.Doc, snap.Model
	out := []protocol.DocumentLink{}
	if d.Root == nil {
		return out
	}
	sm := snap.Subst()
	add := func(n *yaml.Node, target, tip string) {
		out = append(out, protocol.DocumentLink{Range: d.Range(n), Target: protocol.DocumentURI(target), Tooltip: tip})
	}

	for _, s := range m.Steps {
		sc := subst.Scope{Subpackage: s.Subpackage, Inputs: stepInputs(snap, s)}
		document.Entries(s.With, func(k, v *yaml.Node) {
			if v.Kind != yaml.ScalarNode {
				return
			}
			if k.Value == "patches" {
				return
			}
			resolved := sm.Resolve(v.Value, sc)
			if httpURL.MatchString(resolved) && !strings.Contains(resolved, "${{") {
				tip := ""
				if resolved != v.Value {
					tip = resolved
				}
				add(v, resolved, tip)
			}
		})
		for _, p := range patchRefs(snap, s) {
			if p.File != "" {
				out = append(out, protocol.DocumentLink{Range: p.Range, Target: document.URI(p.File)})
			}
		}
	}

	pkg := document.MapValue(d.Root, "package")
	if u := document.MapValue(pkg, "url"); u != nil {
		if resolved := sm.Resolve(u.Value, subst.Scope{}); httpURL.MatchString(resolved) {
			add(u, resolved, "")
		}
	}

	update := document.MapValue(d.Root, "update")
	if id := document.MapValue(document.MapValue(update, "github"), "identifier"); id != nil && strings.Count(id.Value, "/") == 1 {
		add(id, "https://github.com/"+id.Value, "Open on GitHub")
	}
	if id := document.MapValue(document.MapValue(update, "release-monitor"), "identifier"); id != nil && id.Value != "" {
		add(id, "https://release-monitoring.org/project/"+id.Value, "Open on release-monitoring.org")
	}
	return out
}

// InlayHint is an LSP 3.17 inlay hint; go.lsp.dev/protocol predates them.
type InlayHint struct {
	Position     protocol.Position `json:"position"`
	Label        string            `json:"label"`
	Kind         int               `json:"kind,omitempty"`
	PaddingLeft  bool              `json:"paddingLeft,omitempty"`
	PaddingRight bool              `json:"paddingRight,omitempty"`
	Tooltip      string            `json:"tooltip,omitempty"`
}

type inlayHintParams struct {
	TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
	Range        protocol.Range                  `json:"range"`
}

// hintWidth caps inlay hint labels; the tooltip has the full text.
const hintWidth = 80

// InlayHints shows, at the end of each line that uses variables, what
// that line resolves to: the whole value for single-line values, and the
// resolved script line inside block scalars such as runs:.
func InlayHints(snap *Snapshot, r protocol.Range) []InlayHint {
	out := []InlayHint{}
	if !snap.opts.inlayHints() || snap.Doc.Kind != document.KindConfig {
		return out
	}
	d, m := snap.Doc, snap.Model
	sm := snap.Subst()

	// The first template on each line decides the node and scope.
	done := map[uint32]bool{}
	for _, t := range m.Templates {
		line := t.Range.Start.Line
		if done[line] || line < r.Start.Line || line > r.End.Line {
			continue
		}
		done[line] = true
		if len(t.Path) > 0 && t.Path[0].Key == "var-transforms" {
			continue // explained by hover, the code lens, and the to: hint
		}
		n := d.NodeAtPath(t.Path)
		if n == nil {
			continue
		}
		sc := subst.Scope{Subpackage: t.Subpackage, Inputs: stepInputs(snap, t.Step)}
		resolve := func(s string) string {
			// range.* differs per item, so it stays as written.
			return model.TemplatePattern.ReplaceAllStringFunc(s, func(tpl string) string {
				name := model.TemplatePattern.FindStringSubmatch(tpl)[1]
				if strings.HasPrefix(name, "range.") {
					return tpl
				}
				if v, ok := sm.Lookup(name, sc); ok {
					return v
				}
				return tpl
			})
		}

		nr := d.Range(n)
		var raw string
		var pos protocol.Position
		switch {
		case n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 || nr.Start.Line != nr.End.Line:
			// One script line at a time.
			raw = strings.TrimSpace(d.Line(int(line)))
			pos = d.LineEnd(int(line))
		default:
			raw = n.Value
			pos = nr.End
		}
		resolved := resolve(raw)
		if resolved == raw {
			continue
		}
		out = append(out, InlayHint{
			Position:    pos,
			Label:       "= " + truncate(resolved, hintWidth),
			PaddingLeft: true,
			Tooltip:     resolved,
		})
	}
	// Each var-transforms to: shows the value it produces.
	for _, name := range m.VarOrder {
		v := m.Vars[name]
		if v.Transform == nil || v.Key.Line-1 < int(r.Start.Line) || v.Key.Line-1 > int(r.End.Line) {
			continue
		}
		if t := traceTransform(snap, v); t != nil && t.Err == nil {
			out = append(out, InlayHint{Position: d.Range(v.Key).End, Label: "= " + truncate(t.Result, 40), PaddingLeft: true,
				Tooltip: fmt.Sprintf("%s → %s", t.From, t.Result)})
		}
	}
	return out
}

// CodeLenses show the value each var-transforms entry produces.
func CodeLenses(snap *Snapshot) []protocol.CodeLens {
	out := []protocol.CodeLens{}
	if snap.Doc.Kind != document.KindConfig {
		return out
	}
	sm := snap.Subst()
	for _, name := range snap.Model.VarOrder {
		v := snap.Model.Vars[name]
		if v.Transform == nil {
			continue
		}
		val, ok := sm.Lookup("vars."+name, subst.Scope{})
		if !ok {
			continue
		}
		out = append(out, protocol.CodeLens{
			Range:   snap.Doc.Range(v.Key),
			Command: &protocol.Command{Title: fmt.Sprintf("${{vars.%s}} → %s", name, val)},
		})
	}
	if u := traceUpdate(snap); u != nil {
		k, _ := document.MapEntry(snap.Doc.Root, "update")
		out = append(out, protocol.CodeLens{Range: snap.Doc.Range(k), Command: &protocol.Command{Title: u.lens()}})
	}
	return out
}

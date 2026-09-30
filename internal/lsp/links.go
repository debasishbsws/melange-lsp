package lsp

import (
	"fmt"
	"regexp"
	"strings"

	"melange-lsp/internal/document"
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

// InlayHints shows the value of each ${{...}} in single-line values. Block
// scalars such as runs: are skipped to keep scripts readable.
func InlayHints(snap *Snapshot, r protocol.Range) []InlayHint {
	out := []InlayHint{}
	if !snap.opts.inlayHints() || snap.Doc.Kind != document.KindConfig {
		return out
	}
	d, m := snap.Doc, snap.Model
	sm := snap.Subst()
	for _, t := range m.Templates {
		if t.Range.Start.Line < r.Start.Line || t.Range.Start.Line > r.End.Line {
			continue
		}
		if n := d.NodeAtPath(t.Path); n != nil && n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
			continue
		}
		if len(t.Path) > 0 && t.Path[0].Key == "var-transforms" {
			continue // shown as a code lens
		}
		v, ok := sm.Lookup(t.Expr, subst.Scope{Subpackage: t.Subpackage, Inputs: stepInputs(snap, t.Step)})
		if !ok || v == "" || strings.HasPrefix(t.Expr, "range.") {
			continue
		}
		out = append(out, InlayHint{
			Position:    t.Range.End,
			Label:       "= " + truncate(v, 40),
			PaddingLeft: true,
			Tooltip:     fmt.Sprintf("${{%s}} = %s", t.Expr, v),
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

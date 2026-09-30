package lsp

import (
	"fmt"
	"strings"

	"melange-lsp/internal/document"
	"melange-lsp/internal/model"

	"go.lsp.dev/protocol"
	"gopkg.in/yaml.v3"
)

// Symbols returns the document outline.
func Symbols(snap *Snapshot) []protocol.DocumentSymbol {
	d, m := snap.Doc, snap.Model
	if d.Root == nil {
		return []protocol.DocumentSymbol{}
	}
	sb := symbolBuilder{doc: d, model: m}
	if d.Kind == document.KindPipeline {
		return sb.pipelineFile()
	}

	var out []protocol.DocumentSymbol
	document.Entries(d.Root, func(k, v *yaml.Node) {
		if s, ok := sb.top(k, v); ok {
			out = append(out, s)
		}
	})
	return out
}

type symbolBuilder struct {
	doc   *document.Document
	model *model.Model
}

func (sb symbolBuilder) sym(name, detail string, kind protocol.SymbolKind, k, v *yaml.Node, sel *yaml.Node) protocol.DocumentSymbol {
	if name == "" {
		name = k.Value
	}
	if sel == nil {
		sel = k
	}
	return protocol.DocumentSymbol{
		Name:           name,
		Detail:         detail,
		Kind:           kind,
		Range:          sb.doc.EntryRange(k, v),
		SelectionRange: sb.doc.Range(sel),
	}
}

func (sb symbolBuilder) top(k, v *yaml.Node) (protocol.DocumentSymbol, bool) {
	switch k.Value {
	case "package":
		name, version, epoch := "package", "", ""
		var sel *yaml.Node
		if n := document.MapValue(v, "name"); n != nil {
			name, sel = n.Value, n
		}
		if n := document.MapValue(v, "version"); n != nil {
			version = n.Value
		}
		if n := document.MapValue(v, "epoch"); n != nil {
			epoch = n.Value
		}
		detail := version
		if epoch != "" {
			detail += "-r" + epoch
		}
		return sb.sym(name, detail, protocol.SymbolKindPackage, k, v, sel), true

	case "vars":
		s := sb.sym("", fmt.Sprintf("%d", len(v.Content)/2), protocol.SymbolKindNamespace, k, v, nil)
		document.Entries(v, func(vk, vv *yaml.Node) {
			s.Children = append(s.Children, sb.sym("", vv.Value, protocol.SymbolKindVariable, vk, vv, nil))
		})
		return s, true

	case "var-transforms":
		s := sb.sym("", "", protocol.SymbolKindNamespace, k, v, nil)
		for _, t := range document.Items(v) {
			to := document.MapValue(t, "to")
			if to == nil {
				continue
			}
			from := ""
			if f := document.MapValue(t, "from"); f != nil {
				from = f.Value
			}
			s.Children = append(s.Children, protocol.DocumentSymbol{
				Name: to.Value, Detail: "from " + from, Kind: protocol.SymbolKindVariable,
				Range: sb.doc.ItemRange(t), SelectionRange: sb.doc.Range(to),
			})
		}
		return s, true

	case "data":
		s := sb.sym("", "", protocol.SymbolKindNamespace, k, v, nil)
		for _, set := range document.Items(v) {
			name := document.MapValue(set, "name")
			if name == nil {
				continue
			}
			items := document.MapValue(set, "items")
			child := protocol.DocumentSymbol{
				Name: name.Value, Detail: fmt.Sprintf("%d items", len(items.Content)/2), Kind: protocol.SymbolKindArray,
				Range: sb.doc.ItemRange(set), SelectionRange: sb.doc.Range(name),
			}
			document.Entries(items, func(ik, iv *yaml.Node) {
				child.Children = append(child.Children, sb.sym("", iv.Value, protocol.SymbolKindEnumMember, ik, iv, nil))
			})
			s.Children = append(s.Children, child)
		}
		return s, true

	case "environment":
		pkgs := document.Items(document.MapValue(document.MapValue(v, "contents"), "packages"))
		return sb.sym("", fmt.Sprintf("%d packages", len(pkgs)), protocol.SymbolKindModule, k, v, nil), true

	case "pipeline":
		s := sb.sym("", fmt.Sprintf("%d steps", len(v.Content)), protocol.SymbolKindNamespace, k, v, nil)
		s.Children = sb.steps(nil, false, nil)
		return s, true

	case "subpackages":
		s := sb.sym("", fmt.Sprintf("%d", len(v.Content)), protocol.SymbolKindNamespace, k, v, nil)
		for _, sp := range sb.model.Subpackages {
			s.Children = append(s.Children, sb.subpackage(sp))
		}
		return s, true

	case "test":
		s := sb.sym("", "", protocol.SymbolKindNamespace, k, v, nil)
		s.Children = sb.steps(nil, true, nil)
		return s, true

	case "update":
		detail := ""
		for _, p := range []string{"github", "release-monitor", "git", "oci"} {
			if pv := document.MapValue(v, p); pv != nil {
				detail = p
				if id := document.MapValue(pv, "identifier"); id != nil {
					detail += ": " + id.Value
				}
			}
		}
		if e := document.MapValue(v, "enabled"); e != nil && e.Value == "false" {
			detail = "disabled"
		}
		return sb.sym("", detail, protocol.SymbolKindEvent, k, v, nil), true
	}
	return protocol.DocumentSymbol{}, false
}

func (sb symbolBuilder) subpackage(sp *model.Subpackage) protocol.DocumentSymbol {
	name := sp.Name
	if name == "" {
		name = fmt.Sprintf("subpackages[%d]", sp.Index)
	}
	detail := ""
	if d := document.MapValue(sp.Node, "description"); d != nil {
		detail = d.Value
	}
	if sp.Range != "" {
		detail = "range: " + sp.Range
	}
	sel := sp.NameNode
	if sel == nil {
		sel = sp.Node
	}
	s := protocol.DocumentSymbol{
		Name: name, Detail: detail, Kind: protocol.SymbolKindModule,
		Range: sb.doc.ItemRange(sp.Node), SelectionRange: sb.doc.Range(sel),
	}
	s.Children = append(s.Children, sb.steps(sp, false, nil)...)
	if tk, tv := document.MapEntry(sp.Node, "test"); tk != nil {
		t := sb.sym("", "", protocol.SymbolKindNamespace, tk, tv, nil)
		t.Children = sb.steps(sp, true, nil)
		s.Children = append(s.Children, t)
	}
	return s
}

// steps returns symbols for the steps directly under parent.
func (sb symbolBuilder) steps(sp *model.Subpackage, test bool, parent *model.Step) []protocol.DocumentSymbol {
	var out []protocol.DocumentSymbol
	for _, s := range sb.model.Steps {
		if s.Subpackage != sp || s.Test != test || s.Parent != parent || s.Path == nil {
			continue
		}
		sel := s.Name
		if sel == nil {
			sel = s.Uses
		}
		if sel == nil {
			sel = s.Node.Content[0]
		}
		detail := ""
		if s.Uses != nil && s.Name != nil {
			detail = s.Uses.Value
		}
		sym := protocol.DocumentSymbol{
			Name:           strings.TrimSpace(s.Label()),
			Detail:         detail,
			Kind:           protocol.SymbolKindFunction,
			Range:          sb.doc.ItemRange(s.Node),
			SelectionRange: sb.doc.Range(sel),
		}
		sym.Children = sb.steps(sp, test, s)
		out = append(out, sym)
	}
	return out
}

func (sb symbolBuilder) pipelineFile() []protocol.DocumentSymbol {
	var out []protocol.DocumentSymbol
	root := sb.doc.Root
	if k, v := document.MapEntry(root, "inputs"); k != nil {
		s := sb.sym("", "", protocol.SymbolKindNamespace, k, v, nil)
		for _, name := range sb.model.InputOrder {
			in := sb.model.Inputs[name]
			_, iv := document.MapEntry(v, name)
			detail := "optional"
			if in.Required {
				detail = "required"
			}
			if in.Default != nil {
				detail += ", default " + in.Default.Value
			}
			s.Children = append(s.Children, sb.sym("", detail, protocol.SymbolKindField, in.Key, iv, nil))
		}
		out = append(out, s)
	}
	if k, v := document.MapEntry(root, "pipeline"); k != nil {
		s := sb.sym("", "", protocol.SymbolKindNamespace, k, v, nil)
		var fileStep *model.Step
		for _, st := range sb.model.Steps {
			if st.Path == nil {
				fileStep = st
			}
		}
		s.Children = sb.steps(nil, false, fileStep)
		out = append(out, s)
	}
	return out
}

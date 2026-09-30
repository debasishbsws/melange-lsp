package lsp

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"melange-lsp/internal/document"
	"melange-lsp/internal/model"
	"melange-lsp/internal/schema"

	"go.lsp.dev/protocol"
)

// symbolKind is what a renameable name refers to.
type symbolKind int

const (
	symVar   symbolKind = iota + 1 // vars: / var-transforms: to:
	symInput                       // a pipeline file's inputs:
	symData                        // a data: set
)

type symbol struct {
	kind symbolKind
	name string
	// at is the range of the name under the cursor.
	at protocol.Range
}

var templateNamespace = map[string]symbolKind{"vars.": symVar, "inputs.": symInput}

// symbolAt identifies a variable, input, or data set under the cursor.
func symbolAt(snap *Snapshot, pos protocol.Position) *symbol {
	m, d := snap.Model, snap.Doc
	if t := m.TemplateAt(pos); t != nil {
		for prefix, kind := range templateNamespace {
			if name, ok := strings.CutPrefix(t.Expr, prefix); ok {
				if kind == symInput && d.Kind != document.KindPipeline {
					return nil
				}
				return &symbol{kind: kind, name: name, at: nameRange(t, prefix)}
			}
		}
		return nil
	}
	loc := d.Locate(pos)
	if loc == nil {
		return nil
	}
	p := loc.Path
	r := d.Range(loc.Node)
	switch {
	case loc.OnKey && len(p) == 2 && p[0].Key == "vars":
		return &symbol{kind: symVar, name: loc.Node.Value, at: r}
	case !loc.OnKey && len(p) == 3 && p[0].Key == "var-transforms" && p[2].Key == "to":
		return &symbol{kind: symVar, name: loc.Node.Value, at: r}
	case loc.OnKey && len(p) == 2 && p[0].Key == "inputs" && d.Kind == document.KindPipeline:
		return &symbol{kind: symInput, name: loc.Node.Value, at: r}
	case !loc.OnKey && len(p) == 3 && p[0].Key == "data" && p[2].Key == "name":
		return &symbol{kind: symData, name: loc.Node.Value, at: r}
	case !loc.OnKey && len(p) == 3 && p[0].Key == "subpackages" && p[2].Key == "range":
		return &symbol{kind: symData, name: loc.Node.Value, at: r}
	}
	return nil
}

// nameRange is the part of a template after its namespace prefix.
func nameRange(t *model.Template, prefix string) protocol.Range {
	r := t.ExprRange
	r.Start.Character += uint32(len(prefix))
	return r
}

// occurrences returns the definition and uses of a symbol in this file.
func occurrences(snap *Snapshot, sym *symbol) (def *protocol.Range, uses []protocol.Range) {
	m, d := snap.Model, snap.Doc
	switch sym.kind {
	case symVar:
		if v := m.Vars[sym.name]; v != nil {
			r := d.Range(v.Key)
			def = &r
		}
		for _, t := range m.Templates {
			if t.Expr == "vars."+sym.name {
				uses = append(uses, nameRange(t, "vars."))
			}
		}
	case symInput:
		if in := m.Inputs[sym.name]; in != nil {
			r := d.Range(in.Key)
			def = &r
		}
		for _, t := range m.Templates {
			if t.Expr == "inputs."+sym.name {
				uses = append(uses, nameRange(t, "inputs."))
			}
		}
	case symData:
		if set := m.DataNodes[sym.name]; set != nil {
			r := d.Range(document.MapValue(set, "name"))
			def = &r
		}
		for _, sp := range m.Subpackages {
			if sp.Range == sym.name {
				uses = append(uses, d.Range(sp.RangeNode))
			}
		}
	}
	return def, uses
}

// Definition finds where the thing under the cursor is defined.
func Definition(snap *Snapshot, pos protocol.Position) []protocol.Location {
	d, m := snap.Doc, snap.Model
	here := func(r protocol.Range) []protocol.Location {
		return []protocol.Location{{URI: d.URI, Range: r}}
	}

	if sym := symbolAt(snap, pos); sym != nil {
		if def, _ := occurrences(snap, sym); def != nil {
			return here(*def)
		}
	}

	if t := m.TemplateAt(pos); t != nil {
		switch {
		case strings.HasPrefix(t.Expr, "range.") && t.Subpackage != nil:
			if set := m.DataNodes[t.Subpackage.Range]; set != nil {
				return here(d.Range(document.MapValue(set, "name")))
			}
		case strings.HasPrefix(t.Expr, "targets.package."):
			name := strings.TrimPrefix(t.Expr, "targets.package.")
			for _, sp := range m.Subpackages {
				if sp.Name == name {
					return here(d.Range(sp.NameNode))
				}
			}
			if n := document.MapValue(document.MapValue(d.Root, "package"), "name"); n != nil && n.Value == name {
				return here(d.Range(n))
			}
		case t.Expr == "subpkg.name" && t.Subpackage != nil && t.Subpackage.NameNode != nil:
			return here(d.Range(t.Subpackage.NameNode))
		}
		return nil
	}

	loc := d.Locate(pos)
	if loc == nil {
		return nil
	}
	step := m.StepAt(loc.Path)

	// uses: value → the pipeline file.
	if step != nil && !loc.OnKey && loc.Node == step.Uses {
		if def := snap.Registry.Resolve(step.Uses.Value, snap.Dirs); def != nil {
			return []protocol.Location{def.Location()}
		}
		return nil
	}
	// with: key → the input's declaration.
	if step != nil && loc.OnKey && step.Uses != nil && len(loc.Path) == len(step.Path)+2 && loc.Path[len(step.Path)].Key == "with" {
		if def := snap.Registry.Resolve(step.Uses.Value, snap.Dirs); def != nil {
			if in := def.Input(loc.Node.Value); in != nil {
				return []protocol.Location{{URI: def.Doc.URI, Range: def.Doc.Range(in.Key)}}
			}
			return []protocol.Location{def.Location()}
		}
	}
	// patches: a.patch b.patch → the patch files.
	if !loc.OnKey && loc.Path.Last() == "patches" && step != nil {
		if locs := patchLocations(snap, step, pos); len(locs) > 0 {
			return locs
		}
	}
	// Any key → its definition in melange's schema.
	if loc.OnKey {
		root := schema.RootConfig
		if d.Kind == document.KindPipeline {
			root = schema.RootPipeline
		}
		if f, _, ok := schema.LookupFrom(root, loc.Path.Keys()); ok && f.Line >= 0 {
			if p := schema.FilePath(); p != "" {
				line := uint32(f.Line)
				return []protocol.Location{{URI: document.URI(p), Range: protocol.Range{Start: protocol.Position{Line: line}, End: protocol.Position{Line: line}}}}
			}
		}
	}
	return nil
}

func patchLocations(snap *Snapshot, s *model.Step, pos protocol.Position) []protocol.Location {
	var out []protocol.Location
	for _, p := range patchRefs(snap, s) {
		if p.File == "" {
			continue
		}
		loc := protocol.Location{URI: document.URI(p.File)}
		if document.Contains(p.Range, pos) {
			return []protocol.Location{loc}
		}
		out = append(out, loc)
	}
	return out
}

// References lists uses of the variable, input, or data set under the cursor.
func References(snap *Snapshot, pos protocol.Position, includeDecl bool) []protocol.Location {
	sym := symbolAt(snap, pos)
	if sym == nil {
		return []protocol.Location{}
	}
	def, uses := occurrences(snap, sym)
	out := []protocol.Location{}
	if includeDecl && def != nil {
		out = append(out, protocol.Location{URI: snap.Doc.URI, Range: *def})
	}
	for _, r := range uses {
		out = append(out, protocol.Location{URI: snap.Doc.URI, Range: r})
	}
	return out
}

// Highlights marks every occurrence of the symbol under the cursor.
func Highlights(snap *Snapshot, pos protocol.Position) []protocol.DocumentHighlight {
	sym := symbolAt(snap, pos)
	if sym == nil {
		return []protocol.DocumentHighlight{}
	}
	def, uses := occurrences(snap, sym)
	out := []protocol.DocumentHighlight{}
	if def != nil {
		out = append(out, protocol.DocumentHighlight{Range: *def, Kind: protocol.DocumentHighlightKindWrite})
	}
	for _, r := range uses {
		out = append(out, protocol.DocumentHighlight{Range: r, Kind: protocol.DocumentHighlightKindRead})
	}
	return out
}

// PrepareRename reports the range of a renameable name under the cursor.
func PrepareRename(snap *Snapshot, pos protocol.Position) *protocol.Range {
	if sym := symbolAt(snap, pos); sym != nil {
		return &sym.at
	}
	return nil
}

var validName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Rename renames a variable, input, or data set and all its uses.
func Rename(snap *Snapshot, pos protocol.Position, newName string) (*protocol.WorkspaceEdit, error) {
	sym := symbolAt(snap, pos)
	if sym == nil {
		return nil, fmt.Errorf("nothing to rename here: only vars, pipeline inputs and data sets can be renamed")
	}
	if !validName.MatchString(newName) {
		return nil, fmt.Errorf("%q is not a valid name: use letters, digits, '_' and '-'", newName)
	}
	def, uses := occurrences(snap, sym)
	var edits []protocol.TextEdit
	if def != nil {
		edits = append(edits, protocol.TextEdit{Range: *def, NewText: newName})
	}
	for _, r := range uses {
		edits = append(edits, protocol.TextEdit{Range: r, NewText: newName})
	}
	return &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{snap.Doc.URI: edits}}, nil
}

// workspaceReferences answers references for names used across files: a
// pipeline (from a uses: value, or anywhere in its definition file) and a
// package or subpackage name (its dependents).
func (s *Server) workspaceReferences(snap *Snapshot, pos protocol.Position) []protocol.Location {
	d := snap.Doc
	if d.Kind == document.KindPipeline {
		if name := pipelineName(snap); name != "" {
			if loc := d.Locate(pos); loc == nil || loc.Path.Last() != "uses" {
				return s.usesOf(name)
			}
		}
	}
	loc := d.Locate(pos)
	if loc == nil || loc.OnKey {
		return []protocol.Location{}
	}
	if step := snap.Model.StepAt(loc.Path); step != nil && loc.Node == step.Uses {
		return s.usesOf(step.Uses.Value)
	}
	p := loc.Path
	isName := (len(p) == 2 && p[0].Key == "package" && p[1].Key == "name") ||
		(len(p) == 3 && p[0].Key == "subpackages" && p[2].Key == "name")
	if isName && !strings.Contains(loc.Node.Value, "${{") {
		return s.dependentsOf(loc.Node.Value)
	}
	return []protocol.Location{}
}

// pipelineName is the uses: name of a pipeline file: its path relative to
// the pipeline directory containing it.
func pipelineName(snap *Snapshot) string {
	file := document.Filename(snap.Doc.URI)
	for _, dir := range snap.Dirs {
		if rel, err := filepath.Rel(dir, file); err == nil && !strings.HasPrefix(rel, "..") {
			return strings.TrimSuffix(filepath.ToSlash(rel), ".yaml")
		}
	}
	if i := strings.LastIndex(file, "/pipelines/"); i >= 0 {
		return strings.TrimSuffix(file[i+len("/pipelines/"):], ".yaml")
	}
	return ""
}

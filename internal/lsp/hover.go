package lsp

import (
	"fmt"
	"strings"

	"melange-lsp/internal/document"
	"melange-lsp/internal/known"
	"melange-lsp/internal/model"
	"melange-lsp/internal/schema"
	"melange-lsp/internal/subst"

	"go.lsp.dev/protocol"
	"gopkg.in/yaml.v3"
)

// Hover returns documentation for what is under the cursor.
func Hover(snap *Snapshot, pos protocol.Position) *protocol.Hover {
	h := hoverer{snap: snap, doc: snap.Doc, model: snap.Model}
	md, r := h.at(pos)
	if md == "" {
		return nil
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: md}, Range: r}
}

type hoverer struct {
	snap  *Snapshot
	doc   *document.Document
	model *model.Model
}

func (h hoverer) at(pos protocol.Position) (string, *protocol.Range) {
	if t := h.model.TemplateAt(pos); t != nil {
		r := t.Range
		return h.variable(t), &r
	}
	if md, r := agentNoteHover(h.doc, int(pos.Line)); md != "" {
		return md, r
	}
	loc := h.doc.Locate(pos)
	if loc == nil {
		return "", nil
	}
	r := h.doc.Range(loc.Node)
	path := loc.Path
	step := h.model.StepAt(path)

	if !loc.OnKey && step != nil && loc.Node == step.Uses {
		return h.uses(step), &r
	}
	if loc.OnKey && step != nil && len(path) == len(step.Path)+2 && path[len(step.Path)].Key == "with" {
		if md := h.input(step, loc.Node.Value); md != "" {
			return md, &r
		}
	}
	if !loc.OnKey && len(path) >= 2 && path[len(path)-1].IsIdx && path[len(path)-2].Key == "disabled" {
		if l, ok := known.LinterByName(loc.Node.Value); ok {
			return fmt.Sprintf("**%s** linter\n\n%s\n\nRuns by default as: %s", l.Name, l.Explain, l.Default), &r
		}
	}
	if !loc.OnKey && path.Last() == "range" {
		if sp := h.model.SubpackageAt(path); sp != nil {
			return h.dataSet(sp.Range), &r
		}
	}
	// Derived values explain themselves: a var-transforms entry shows its
	// computation, the update block shows how upstream becomes the version.
	if trace := h.derivation(path); trace != "" {
		if loc.OnKey {
			if md := h.key(path); md != "" {
				trace += "\n\n---\n\n" + md
			}
		}
		return trace, &r
	}
	if loc.OnKey {
		if md := h.key(path); md != "" {
			return md, &r
		}
	}
	if !loc.OnKey && loc.Node.Kind == yaml.ScalarNode && strings.Contains(loc.Node.Value, "${{") {
		sc := subst.Scope{Subpackage: h.model.SubpackageAt(path), Inputs: stepInputs(h.snap, step)}
		resolved := h.snap.Subst().Resolve(loc.Node.Value, sc)
		if resolved != loc.Node.Value {
			return "Resolves to:\n\n```\n" + resolved + "\n```", &r
		}
	}
	return "", nil
}

func (h hoverer) root() string {
	if h.doc.Kind == document.KindPipeline {
		return schema.RootPipeline
	}
	return schema.RootConfig
}

// key documents a mapping key from the schema.
func (h hoverer) key(path document.Path) string {
	f, def, ok := schema.LookupFrom(h.root(), path.Keys())
	if !ok {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**: `%s`", f.Name, fieldDetail(f))
	if f.Description != "" {
		b.WriteString("\n\n" + f.Description)
	}
	if len(f.Enum) > 0 {
		fmt.Fprintf(&b, "\n\nOne of: `%s`", strings.Join(f.Enum, "`, `"))
	}
	if f.Def != "" && f.Description == "" {
		if d := schema.DefDoc(f.Def); d != "" {
			b.WriteString("\n\n" + d)
		}
	}
	fmt.Fprintf(&b, "\n\n_%s.%s_", def, f.Name)
	return b.String()
}

// uses documents the step's pipeline and its effective inputs.
func (h hoverer) uses(s *model.Step) string {
	def := h.snap.Registry.Resolve(s.Uses.Value, h.snap.Dirs)
	if def == nil {
		return fmt.Sprintf("Pipeline `%s` was not found.", s.Uses.Value)
	}
	var b strings.Builder
	b.WriteString(pipelineDoc(def))

	// Effective values for this step: with: over defaults, substituted.
	ins := def.Inputs()
	if len(ins) == 0 {
		return b.String()
	}
	sc := subst.Scope{Subpackage: s.Subpackage, Inputs: stepInputs(h.snap, s)}
	sm := h.snap.Subst()
	var rows []string
	for _, in := range ins {
		v := document.MapValue(s.With, in.Name)
		val, src := "", "default"
		if v != nil && v.Value != "" {
			val, src = v.Value, "with"
		} else if in.Default != nil {
			val = in.Default.Value
		}
		if val == "" {
			continue
		}
		resolved := sm.Resolve(val, sc)
		rows = append(rows, fmt.Sprintf("- `%s`: `%s` (%s)", in.Name, strings.ReplaceAll(truncate(resolved, 80), "`", "'"), src))
	}
	if len(rows) > 0 {
		b.WriteString("\n\n**This step:**\n\n")
		b.WriteString(strings.Join(rows, "\n"))
	}
	return b.String()
}

func (h hoverer) input(s *model.Step, name string) string {
	if s.Uses == nil {
		return ""
	}
	def := h.snap.Registry.Resolve(s.Uses.Value, h.snap.Dirs)
	if def == nil {
		return ""
	}
	in := def.Input(name)
	if in == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** — input of `%s` (%s)", in.Name, def.Name, inputDetail(in))
	if in.Description != "" {
		b.WriteString("\n\n" + in.Description)
	}
	if in.Default != nil && in.Default.Value != "" {
		fmt.Fprintf(&b, "\n\nDefault:\n```\n%s\n```", in.Default.Value)
	}
	return b.String()
}

// variable documents a ${{...}} reference.
func (h hoverer) variable(t *model.Template) string {
	name := t.Expr
	var b strings.Builder
	fmt.Fprintf(&b, "`${{%s}}`", name)

	sc := subst.Scope{Subpackage: t.Subpackage, Inputs: stepInputs(h.snap, t.Step)}
	if h.doc.Kind == document.KindPipeline {
		if in, ok := strings.CutPrefix(name, "inputs."); ok {
			if decl := h.model.Inputs[in]; decl != nil {
				fmt.Fprintf(&b, " — pipeline input (%s)", inputDetail(decl))
				if decl.Description != "" {
					b.WriteString("\n\n" + decl.Description)
				}
				return b.String()
			}
			return b.String() + "\n\nNot declared in `inputs:`."
		}
	}

	if v, ok := h.snap.Subst().Lookup(name, sc); ok {
		fmt.Fprintf(&b, " = `%s`", strings.ReplaceAll(truncate(v, 120), "`", "'"))
	}
	if doc, ok := known.VariableDoc(name); ok {
		b.WriteString("\n\n" + doc.Doc)
	}
	switch {
	case strings.HasPrefix(name, "vars."):
		if v := h.model.Vars[strings.TrimPrefix(name, "vars.")]; v != nil {
			if v.Transform != nil {
				from, match, repl := "", "", ""
				if n := document.MapValue(v.Transform, "from"); n != nil {
					from = n.Value
				}
				if n := document.MapValue(v.Transform, "match"); n != nil {
					match = n.Value
				}
				if n := document.MapValue(v.Transform, "replace"); n != nil {
					repl = n.Value
				}
				fmt.Fprintf(&b, "\n\nFrom `var-transforms` (line %d): `%s` with `s/%s/%s/`", v.Key.Line, from, match, repl)
			} else {
				fmt.Fprintf(&b, "\n\nDefined in `vars:` (line %d).", v.Key.Line)
			}
		}
	case strings.HasPrefix(name, "range.") && t.Subpackage != nil && t.Subpackage.Range != "":
		b.WriteString("\n\n" + h.dataSet(t.Subpackage.Range))
	}
	return b.String()
}

func (h hoverer) dataSet(name string) string {
	set := h.model.DataNodes[name]
	if set == nil {
		return fmt.Sprintf("Data set `%s` is not defined.", name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** — %d items\n\n", name, len(h.model.Data[name]))
	document.Entries(document.MapValue(set, "items"), func(k, v *yaml.Node) {
		fmt.Fprintf(&b, "- `%s`: %s\n", k.Value, truncate(v.Value, 80))
	})
	return b.String()
}

// derivation explains computed values for positions inside var-transforms
// entries and the update block.
func (h hoverer) derivation(path document.Path) string {
	if h.doc.Kind != document.KindConfig || len(path) == 0 {
		return ""
	}
	switch path[0].Key {
	case "var-transforms":
		if len(path) < 2 || !path[1].IsIdx {
			return ""
		}
		item := document.Items(document.MapValue(h.doc.Root, "var-transforms"))
		if path[1].Index >= len(item) {
			return ""
		}
		for _, v := range h.model.Vars {
			if v.Transform == item[path[1].Index] {
				if t := traceTransform(h.snap, v); t != nil {
					return t.markdown()
				}
			}
		}
	case "update":
		if u := traceUpdate(h.snap); u != nil {
			return u.markdown()
		}
	}
	return ""
}

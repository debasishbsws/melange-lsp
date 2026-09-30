// Package model extracts melange semantics from a parsed document: pipeline
// steps, subpackages, variables, pipeline inputs, and template references,
// each tied to its position in the file.
package model

import (
	"regexp"
	"strings"

	"melange-lsp/internal/document"

	"go.lsp.dev/protocol"
	"gopkg.in/yaml.v3"
)

// Step is one pipeline step anywhere in the file.
type Step struct {
	Node    *yaml.Node // the step mapping
	Path    document.Path
	Name    *yaml.Node
	UsesKey *yaml.Node
	Uses    *yaml.Node
	WithKey *yaml.Node
	With    *yaml.Node // mapping, or nil
	Runs    *yaml.Node

	// Subpackage is the enclosing subpackage, or nil for the main package.
	Subpackage *Subpackage
	// Test is true for steps under a test: section.
	Test bool
	// Parent is the enclosing step for nested pipeline: lists.
	Parent *Step
}

// Label is a short human description of the step.
func (s *Step) Label() string {
	switch {
	case s.Name != nil && s.Name.Value != "":
		return s.Name.Value
	case s.Uses != nil && s.Uses.Value != "":
		return s.Uses.Value
	case s.Runs != nil:
		first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(s.Runs.Value), "\n", 2)[0])
		if len(first) > 60 {
			first = first[:57] + "..."
		}
		if first == "" {
			return "runs"
		}
		return "runs: " + first
	}
	return "step"
}

// Subpackage is one entry of subpackages:.
type Subpackage struct {
	Index    int
	Node     *yaml.Node
	Path     document.Path
	NameNode *yaml.Node
	Name     string
	// Range is the data: set named by range:, if any.
	Range     string
	RangeNode *yaml.Node
}

// Var is a user variable from vars: or var-transforms: to:.
type Var struct {
	Name  string
	Key   *yaml.Node // the defining node (vars key, or var-transforms to: value)
	Value *yaml.Node // vars value; nil for var-transforms
	// Transform is the var-transforms entry that defines it, if any.
	Transform *yaml.Node
}

// Input is a declared input of a pipeline file.
type Input struct {
	Name        string
	Key         *yaml.Node
	Description string
	Required    bool
	Default     *yaml.Node
}

// Template is one ${{...}} reference.
type Template struct {
	// Expr is the trimmed expression, e.g. "package.version".
	Expr string
	// Range covers "${{ ... }}"; ExprRange covers just Expr.
	Range     protocol.Range
	ExprRange protocol.Range
	Path      document.Path
	// Subpackage and Step enclose the reference, if any.
	Subpackage *Subpackage
	Step       *Step
}

// Namespace returns the part before the first dot.
func (t *Template) Namespace() string {
	ns, _, _ := strings.Cut(t.Expr, ".")
	return ns
}

// Model is the semantic view of one document.
type Model struct {
	Doc         *document.Document
	Steps       []*Step
	Subpackages []*Subpackage
	Vars        map[string]*Var
	VarOrder    []string
	// Data maps data: set names to their item keys.
	Data      map[string][]string
	DataNodes map[string]*yaml.Node
	// Inputs are the declared inputs of a pipeline file.
	Inputs     map[string]*Input
	InputOrder []string
	Templates  []*Template
}

// TemplatePattern matches ${{ expr }}. It mirrors melange's substitution
// syntax.
var TemplatePattern = regexp.MustCompile(`\$\{\{\s*([^{}]*?)\s*\}\}`)

// Build analyzes d. It returns an empty model when d did not parse.
func Build(d *document.Document) *Model {
	m := &Model{
		Doc:       d,
		Vars:      map[string]*Var{},
		Data:      map[string][]string{},
		DataNodes: map[string]*yaml.Node{},
		Inputs:    map[string]*Input{},
	}
	if d.Root == nil {
		return m
	}
	root := d.Root

	m.collectVars(root)
	m.collectData(root)
	m.collectInputs(root)

	if d.Kind == document.KindPipeline {
		// A pipeline file is itself a step; its nested steps hang off it.
		m.collectStep(root, nil, nil, nil, false)
		m.collectTemplates()
		return m
	}
	m.collectSteps(document.MapValue(root, "pipeline"), document.Path{{Key: "pipeline"}}, nil, nil, false)
	if test := document.MapValue(root, "test"); test != nil {
		m.collectSteps(document.MapValue(test, "pipeline"), document.Path{{Key: "test"}, {Key: "pipeline"}}, nil, nil, true)
	}

	for i, sp := range document.Items(document.MapValue(root, "subpackages")) {
		path := document.Path{{Key: "subpackages"}, {Index: i, IsIdx: true}}
		s := &Subpackage{Index: i, Node: sp, Path: path}
		if n := document.MapValue(sp, "name"); n != nil {
			s.NameNode, s.Name = n, n.Value
		}
		if r := document.MapValue(sp, "range"); r != nil {
			s.RangeNode, s.Range = r, r.Value
		}
		m.Subpackages = append(m.Subpackages, s)

		m.collectSteps(document.MapValue(sp, "pipeline"), append(clone(path), document.PathElem{Key: "pipeline"}), s, nil, false)
		if test := document.MapValue(sp, "test"); test != nil {
			m.collectSteps(document.MapValue(test, "pipeline"), append(clone(path), document.PathElem{Key: "test"}, document.PathElem{Key: "pipeline"}), s, nil, true)
		}
	}

	m.collectTemplates()
	return m
}

func clone(p document.Path) document.Path {
	return append(document.Path(nil), p...)
}

func (m *Model) collectSteps(seq *yaml.Node, path document.Path, sp *Subpackage, parent *Step, test bool) {
	for i, item := range document.Items(seq) {
		m.collectStep(item, append(clone(path), document.PathElem{Index: i, IsIdx: true}), sp, parent, test)
	}
}

func (m *Model) collectStep(n *yaml.Node, path document.Path, sp *Subpackage, parent *Step, test bool) {
	if n.Kind != yaml.MappingNode {
		return
	}
	s := &Step{Node: n, Path: path, Subpackage: sp, Test: test, Parent: parent}
	s.Name = document.MapValue(n, "name")
	s.UsesKey, s.Uses = document.MapEntry(n, "uses")
	s.WithKey, s.With = document.MapEntry(n, "with")
	s.Runs = document.MapValue(n, "runs")
	m.Steps = append(m.Steps, s)
	m.collectSteps(document.MapValue(n, "pipeline"), append(clone(path), document.PathElem{Key: "pipeline"}), sp, s, test)
}

func (m *Model) collectVars(root *yaml.Node) {
	document.Entries(document.MapValue(root, "vars"), func(k, v *yaml.Node) {
		m.addVar(&Var{Name: k.Value, Key: k, Value: v})
	})
	for _, t := range document.Items(document.MapValue(root, "var-transforms")) {
		if to := document.MapValue(t, "to"); to != nil && to.Value != "" {
			m.addVar(&Var{Name: to.Value, Key: to, Transform: t})
		}
	}
}

func (m *Model) addVar(v *Var) {
	if _, ok := m.Vars[v.Name]; !ok {
		m.VarOrder = append(m.VarOrder, v.Name)
	}
	m.Vars[v.Name] = v
}

func (m *Model) collectData(root *yaml.Node) {
	for _, d := range document.Items(document.MapValue(root, "data")) {
		name := document.MapValue(d, "name")
		if name == nil {
			continue
		}
		m.DataNodes[name.Value] = d
		var keys []string
		document.Entries(document.MapValue(d, "items"), func(k, _ *yaml.Node) {
			keys = append(keys, k.Value)
		})
		m.Data[name.Value] = keys
	}
}

func (m *Model) collectInputs(root *yaml.Node) {
	if m.Doc.Kind != document.KindPipeline {
		return
	}
	document.Entries(document.MapValue(root, "inputs"), func(k, v *yaml.Node) {
		in := &Input{Name: k.Value, Key: k}
		if d := document.MapValue(v, "description"); d != nil {
			in.Description = strings.TrimSpace(d.Value)
		}
		if r := document.MapValue(v, "required"); r != nil {
			in.Required = r.Value == "true"
		}
		in.Default = document.MapValue(v, "default")
		m.Inputs[in.Name] = in
		m.InputOrder = append(m.InputOrder, in.Name)
	})
}

func (m *Model) collectTemplates() {
	d := m.Doc
	for line, text := range d.Lines {
		if !strings.Contains(text, "${{") {
			continue
		}
		for _, loc := range TemplatePattern.FindAllStringSubmatchIndex(text, -1) {
			start := d.ByteToPosition(line, loc[0])
			node := d.Locate(start)
			// Only scalar values are substituted. Comments between keys
			// locate to the enclosing mapping and are skipped here.
			if node == nil || node.OnKey || node.Node.Kind != yaml.ScalarNode || !document.Contains(d.Range(node.Node), start) {
				continue
			}
			t := &Template{
				Expr:      text[loc[2]:loc[3]],
				Range:     protocol.Range{Start: start, End: d.ByteToPosition(line, loc[1])},
				ExprRange: protocol.Range{Start: d.ByteToPosition(line, loc[2]), End: d.ByteToPosition(line, loc[3])},
				Path:      node.Path,
			}
			t.Subpackage = m.SubpackageAt(node.Path)
			t.Step = m.StepAt(node.Path)
			m.Templates = append(m.Templates, t)
		}
	}
}

// SubpackageAt returns the subpackage containing path, or nil.
func (m *Model) SubpackageAt(path document.Path) *Subpackage {
	if len(path) >= 2 && path[0].Key == "subpackages" && path[1].IsIdx && path[1].Index < len(m.Subpackages) {
		return m.Subpackages[path[1].Index]
	}
	return nil
}

// StepAt returns the innermost step containing path, or nil.
func (m *Model) StepAt(path document.Path) *Step {
	var best *Step
	for _, s := range m.Steps {
		if hasPrefix(path, s.Path) && (best == nil || len(s.Path) > len(best.Path)) {
			best = s
		}
	}
	return best
}

func hasPrefix(p, prefix document.Path) bool {
	if len(prefix) > len(p) {
		return false
	}
	for i := range prefix {
		if p[i] != prefix[i] {
			return false
		}
	}
	return true
}

// TemplateAt returns the template reference containing pos, or nil.
func (m *Model) TemplateAt(pos protocol.Position) *Template {
	for _, t := range m.Templates {
		if document.Contains(t.Range, pos) {
			return t
		}
	}
	return nil
}

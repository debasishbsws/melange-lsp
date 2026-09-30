package lsp

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"melange-lsp/internal/document"
	"melange-lsp/internal/known"
	"melange-lsp/internal/model"
	"melange-lsp/internal/pipeline"
	"melange-lsp/internal/schema"
	"melange-lsp/internal/subst"

	"chainguard.dev/melange/pkg/build"
	"chainguard.dev/melange/pkg/cond"
	"chainguard.dev/melange/pkg/config"
	"go.lsp.dev/protocol"
	"gopkg.in/yaml.v3"
)

// Diagnostic codes, shown by editors and used to match code actions.
const (
	codeYAML            = "yaml-syntax"
	codeUnknownField    = "unknown-field"
	codeMelange         = "melange"
	codeCompile         = "melange-compile"
	codeUnknownPipeline = "unknown-pipeline"
	codeInvalidUses     = "invalid-uses"
	codeUnknownInput    = "unknown-input"
	codeMissingInput    = "missing-input"
	codeBadChecksum     = "bad-checksum"
	codeStepShape       = "step-shape"
	codeUndefinedVar    = "undefined-variable"
	codeVarNotHere      = "variable-not-substituted"
	codeVarSpacing      = "variable-spacing"
	codeBadCondition    = "bad-condition"
	codeUnknownLinter   = "unknown-linter"
	codeBadCapability   = "bad-capability"
	codeSubpackageName  = "subpackage-name"
	codePackageName     = "package-name"
	codeBadRegex        = "bad-regex"
	codeBadEnum         = "bad-value"
	codeAgentNote       = "agent-note"
	codeDeprecated      = "deprecated"
	codeMissingPatch    = "missing-patch"
	// Lint rules share wolfictl's names so #nolint comments apply to both.
	codeUpdateReason   = "update-disabled-reason"
	codeScheduleReason = "update-schedule-reason"
)

// fix is attached to a diagnostic as Data: replacements for its range.
type fix struct {
	Replace []string `json:"replace,omitempty"`
}

type diagnoser struct {
	snap  *Snapshot
	doc   *document.Document
	model *model.Model
	out   []protocol.Diagnostic
}

func (dg *diagnoser) add(r protocol.Range, sev protocol.DiagnosticSeverity, code, msg string, replace ...string) {
	d := protocol.Diagnostic{
		Range:    r,
		Severity: sev,
		Code:     code,
		Source:   "melange",
		Message:  msg,
	}
	if len(replace) > 0 {
		d.Data = fix{Replace: replace}
	}
	switch code {
	case codeDeprecated:
		d.Tags = []protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated}
	}
	dg.out = append(dg.out, d)
}

func (dg *diagnoser) node(n *yaml.Node, sev protocol.DiagnosticSeverity, code, msg string, replace ...string) {
	dg.add(dg.doc.Range(n), sev, code, msg, replace...)
}

func (dg *diagnoser) line(line int, sev protocol.DiagnosticSeverity, code, msg string) {
	l := dg.doc.Line(line)
	start := document.IndentOf(l)
	dg.add(protocol.Range{Start: dg.doc.ByteToPosition(line, start), End: dg.doc.LineEnd(line)}, sev, code, msg)
}

// Diagnostics computes all diagnostics for a melange document.
func Diagnostics(snap *Snapshot) []protocol.Diagnostic {
	// Never nil: publishDiagnostics must send an array.
	dg := &diagnoser{snap: snap, doc: snap.Doc, model: snap.Model, out: []protocol.Diagnostic{}}

	if err := snap.Doc.ParseErr; err != nil {
		dg.line(yamlErrorLine(err.Error()), protocol.DiagnosticSeverityError, codeYAML, strings.TrimPrefix(err.Error(), "yaml: "))
	}

	switch snap.Doc.Kind {
	case document.KindConfig:
		if snap.Doc.Root != nil {
			dg.config()
		}
	case document.KindPipeline:
		if snap.Doc.Root != nil {
			dg.pipelineFile()
		}
	}

	dg.out = append(dg.out, agentNoteDiagnostics(snap.Doc)...)
	sort.SliceStable(dg.out, func(i, j int) bool {
		a, b := dg.out[i].Range.Start, dg.out[j].Range.Start
		return a.Line < b.Line || (a.Line == b.Line && a.Character < b.Character)
	})
	return dg.out
}

var yamlLine = regexp.MustCompile(`line (\d+)`)

func yamlErrorLine(msg string) int {
	if m := yamlLine.FindStringSubmatch(msg); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n - 1
	}
	return 0
}

func (dg *diagnoser) config() {
	sm := dg.snap.Subst()

	errsBefore := len(dg.out)
	dg.packageFields()
	dg.subpackageNames()
	for _, s := range dg.model.Steps {
		dg.step(s)
	}
	dg.templates()
	dg.checksDisabled()
	dg.capabilities()
	dg.varTransforms()
	dg.enums()
	dg.patches()
	dg.updateLint()
	ownErrors := hasErrors(dg.out[errsBefore:])

	if sm.ParseErr != nil {
		dg.melangeParseError(sm.ParseErr, ownErrors)
		return
	}
	// Our checks mirror melange's compile step. When they find nothing,
	// run the real compile so anything they miss (shell syntax in runs:,
	// nested pipeline errors) still surfaces.
	if !ownErrors && dg.doc.ParseErr == nil {
		dg.compile()
	}
}

func hasErrors(ds []protocol.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == protocol.DiagnosticSeverityError {
			return true
		}
	}
	return false
}

// unmarshal errors look like "line 12: field foo not found in type config.Pipeline".
var (
	unmarshalLine = regexp.MustCompile(`line (\d+): (.*)`)
	unknownField  = regexp.MustCompile(`field (\S+) not found in type config\.(\w+)`)
	validateErr   = regexp.MustCompile(`^validating configuration "[^"]*": build configuration is invalid: `)
)

// melangeParseError reports config.ParseConfiguration's error at the right
// place. Validation problems we already reported ourselves are skipped.
func (dg *diagnoser) melangeParseError(err error, ownErrors bool) {
	msg := err.Error()
	if dg.doc.ParseErr != nil && strings.Contains(msg, "yaml: line") && !strings.Contains(msg, "unmarshal errors") {
		return // already reported as a syntax error
	}

	if strings.Contains(msg, "unmarshal errors") {
		for _, m := range unmarshalLine.FindAllStringSubmatch(msg, -1) {
			line, _ := strconv.Atoi(m[1])
			dg.unmarshalError(line-1, m[2])
		}
		return
	}

	if validateErr.MatchString(msg) {
		if ownErrors {
			return // the same problem, reported with a precise position
		}
		problem := validateErr.ReplaceAllString(msg, "")
		dg.at(dg.locateProblem(problem), protocol.DiagnosticSeverityError, codeMelange, problem)
		return
	}

	msg = strings.TrimPrefix(msg, `unable to decode configuration file "melange.yaml": `)
	dg.at(dg.locateProblem(msg), protocol.DiagnosticSeverityError, codeMelange, msg)
}

func (dg *diagnoser) at(n *yaml.Node, sev protocol.DiagnosticSeverity, code, msg string) {
	if n == nil {
		n, _ = document.MapEntry(dg.doc.Root, "package")
	}
	if n == nil {
		dg.line(0, sev, code, msg)
		return
	}
	dg.node(n, sev, code, msg)
}

var (
	quotedSubpackage = regexp.MustCompile(`subpackages\[(\d+)\]`)
	quotedName       = regexp.MustCompile(`"([^"]+)"`)
)

// locateProblem guesses the node a melange message is about.
func (dg *diagnoser) locateProblem(msg string) *yaml.Node {
	pkg := document.MapValue(dg.doc.Root, "package")
	switch {
	case strings.Contains(msg, "package name"):
		return document.MapValue(pkg, "name")
	case strings.Contains(msg, "package version"):
		if v := document.MapValue(pkg, "version"); v != nil {
			return v
		}
	case strings.Contains(msg, "CPE"):
		k, _ := document.MapEntry(pkg, "cpe")
		return k
	case strings.Contains(msg, "capability"):
		k, _ := document.MapEntry(pkg, "setcap")
		return k
	}
	if m := quotedSubpackage.FindStringSubmatch(msg); m != nil {
		i, _ := strconv.Atoi(m[1])
		if i < len(dg.model.Subpackages) {
			return dg.model.Subpackages[i].NameNode
		}
	}
	if strings.Contains(msg, "subpackage") {
		for _, q := range quotedName.FindAllStringSubmatch(msg, -1) {
			for _, sp := range dg.model.Subpackages {
				if sp.Name == q[1] {
					return sp.NameNode
				}
			}
		}
	}
	return nil
}

// typeNames maps melange Go types in decode errors to what users write.
var typeNames = map[string]string{
	"Configuration": "the top level", "Package": "package", "Pipeline": "pipeline step",
	"Subpackage": "subpackage", "Test": "test", "Update": "update", "Dependencies": "dependencies",
	"GitHubMonitor": "update.github", "ReleaseMonitor": "update.release-monitor", "GitMonitor": "update.git",
	"Copyright": "copyright", "Needs": "needs", "Input": "input", "Scriptlets": "scriptlets",
	"VarTransforms": "var-transforms", "RangeData": "data", "Checks": "checks", "CPE": "cpe",
	"Resources": "resources", "PackageOption": "options", "Capabilities": "capabilities",
}

func (dg *diagnoser) unmarshalError(line int, msg string) {
	m := unknownField.FindStringSubmatch(msg)
	if m == nil {
		dg.line(line, protocol.DiagnosticSeverityError, codeMelange, msg)
		return
	}
	field, typ := m[1], m[2]
	where := typeNames[typ]
	if where == "" {
		where = typ
	}
	var names []string
	for _, f := range schema.FieldsOf(typ) {
		names = append(names, f.Name)
	}
	text := fmt.Sprintf("unknown field %q in %s", field, where)
	sugg := suggest(field, names)
	if len(sugg) > 0 {
		text += fmt.Sprintf("; did you mean %q?", sugg[0])
	}

	// Point at the key itself.
	l := dg.doc.Line(line)
	if i := strings.Index(l, field+":"); i >= 0 {
		dg.add(protocol.Range{Start: dg.doc.ByteToPosition(line, i), End: dg.doc.ByteToPosition(line, i+len(field))},
			protocol.DiagnosticSeverityError, codeUnknownField, text, sugg...)
		return
	}
	dg.line(line, protocol.DiagnosticSeverityError, codeUnknownField, text)
}

var namePattern = regexp.MustCompile(`^[a-zA-Z\d][a-zA-Z\d+_.-]*$`)

func (dg *diagnoser) packageFields() {
	pkg := document.MapValue(dg.doc.Root, "package")
	if n := document.MapValue(pkg, "name"); n != nil && !strings.Contains(n.Value, "${{") && !namePattern.MatchString(n.Value) {
		dg.node(n, protocol.DiagnosticSeverityError, codePackageName, fmt.Sprintf("package name must match %s", namePattern))
	}
	k, v := document.MapEntry(pkg, "version")
	switch {
	case k == nil:
		pk, _ := document.MapEntry(dg.doc.Root, "package")
		dg.node(pk, protocol.DiagnosticSeverityError, codeMelange, "package version must not be empty")
	case v.Value == "" && v.Kind == yaml.ScalarNode:
		dg.node(k, protocol.DiagnosticSeverityError, codeMelange, "package version must not be empty")
	}
}

func (dg *diagnoser) subpackageNames() {
	main := ""
	if n := document.MapValue(document.MapValue(dg.doc.Root, "package"), "name"); n != nil {
		main = n.Value
	}
	seen := map[string]*model.Subpackage{}
	for _, sp := range dg.model.Subpackages {
		if sp.NameNode == nil {
			dg.node(sp.Node, protocol.DiagnosticSeverityError, codeSubpackageName, "subpackage has no name")
			continue
		}
		name := sp.Name
		switch {
		case name == main:
			dg.node(sp.NameNode, protocol.DiagnosticSeverityError, codeSubpackageName, fmt.Sprintf("subpackage has the same name as the main package: %q", name))
		case seen[name] != nil && sp.Range == "":
			dg.node(sp.NameNode, protocol.DiagnosticSeverityError, codeSubpackageName, fmt.Sprintf("duplicate subpackage name %q (also subpackages[%d])", name, seen[name].Index))
		case !strings.Contains(name, "${{") && !namePattern.MatchString(name):
			dg.node(sp.NameNode, protocol.DiagnosticSeverityError, codeSubpackageName, fmt.Sprintf("subpackage name must match %s", namePattern))
		}
		if seen[name] == nil {
			seen[name] = sp
		}
		if sp.Range != "" {
			if _, ok := dg.model.Data[sp.Range]; !ok {
				var names []string
				for n := range dg.model.Data {
					names = append(names, n)
				}
				msg := fmt.Sprintf("range %q is not defined in data:", sp.Range)
				sugg := suggest(sp.Range, names)
				if len(sugg) > 0 {
					msg += fmt.Sprintf("; did you mean %q?", sugg[0])
				}
				dg.node(sp.RangeNode, protocol.DiagnosticSeverityError, codeMelange, msg, sugg...)
			}
		}
	}
}

// step checks one pipeline step: its shape, uses:, and with: inputs.
func (dg *diagnoser) step(s *model.Step) {
	// melange validates the shape of main and subpackage pipelines only;
	// in tests the same shapes are accepted but easy to misread.
	sev, suffix := protocol.DiagnosticSeverityError, ""
	if s.Test {
		sev, suffix = protocol.DiagnosticSeverityInformation, " (accepted in test pipelines, rejected in build pipelines; consider separate steps)"
	}
	switch {
	case s.With != nil && s.Uses == nil && document.MapValue(s.Node, "pipeline") == nil:
		dg.node(s.WithKey, sev, codeStepShape, "step has with: but no uses:"+suffix)
	case s.Uses != nil && s.Runs != nil:
		dg.node(s.UsesKey, sev, codeStepShape, "step has both uses: and runs:"+suffix)
	case s.With != nil && s.Runs != nil:
		dg.node(s.WithKey, sev, codeStepShape, "step has both with: and runs:"+suffix)
	}
	if s.Uses != nil && document.MapValue(s.Node, "pipeline") != nil {
		dg.node(s.UsesKey, protocol.DiagnosticSeverityWarning, codeStepShape, "step has both uses: and pipeline:; melange runs the inline pipeline: and ignores uses:")
	}

	if cond := document.MapValue(s.Node, "if"); cond != nil {
		dg.condition(cond)
	}

	if s.Uses == nil || document.MapValue(s.Node, "pipeline") != nil {
		return
	}
	uses := s.Uses.Value
	if strings.Contains(uses, "${{") {
		return
	}
	if strings.HasPrefix(uses, "/") || strings.Contains(uses, "..") {
		dg.node(s.Uses, protocol.DiagnosticSeverityError, codeInvalidUses, "uses: must not be an absolute path or contain '..'")
		return
	}
	def := dg.snap.Registry.Resolve(uses, dg.snap.Dirs)
	if def == nil {
		var names []string
		for _, d := range dg.snap.Registry.List(dg.snap.Dirs) {
			names = append(names, d.Name)
		}
		msg := fmt.Sprintf("pipeline %q not found", uses)
		sugg := suggest(uses, names)
		if len(sugg) > 0 {
			msg += fmt.Sprintf("; did you mean %q?", sugg[0])
		}
		msg += "\nSearched: " + strings.Join(append(pipeline.Search(dg.snap.Dirs), "melange built-ins"), ", ")
		dg.node(s.Uses, protocol.DiagnosticSeverityError, codeUnknownPipeline, msg, sugg...)
		return
	}
	dg.inputs(s, def)
}

// inputs mirrors melange's validateWith for one step.
func (dg *diagnoser) inputs(s *model.Step, def *pipeline.Def) {
	set := map[string]*yaml.Node{}
	var declared []string
	for _, in := range def.Inputs() {
		declared = append(declared, in.Name)
	}
	document.Entries(s.With, func(k, v *yaml.Node) {
		set[k.Value] = v
		if def.Input(k.Value) == nil {
			msg := fmt.Sprintf("%q is not an input of pipeline %q", k.Value, def.Name)
			sugg := suggest(k.Value, declared)
			if len(sugg) > 0 {
				msg += fmt.Sprintf("; did you mean %q?", sugg[0])
			} else if len(declared) > 0 {
				msg += "\nInputs: " + strings.Join(declared, ", ")
			} else {
				msg += "; it takes no inputs"
			}
			dg.node(k, protocol.DiagnosticSeverityError, codeUnknownInput, msg, sugg...)
		}
	})

	var missing []string
	for _, in := range def.Inputs() {
		v := set[in.Name]
		val := ""
		if v != nil {
			val = v.Value
		}
		hasDefault := in.Default != nil && in.Default.Value != ""
		if in.Required && val == "" && !hasDefault {
			missing = append(missing, in.Name)
			continue
		}
		if v == nil || val == "" {
			continue
		}
		dg.checksum(in.Name, v, s)
	}
	if len(missing) > 0 {
		noun := "input"
		if len(missing) > 1 {
			noun = "inputs"
		}
		dg.node(s.Uses, protocol.DiagnosticSeverityError, codeMissingInput,
			fmt.Sprintf("pipeline %q is missing required %s: %s", def.Name, noun, strings.Join(missing, ", ")))
	}
}

var shaLength = map[string]int{"expected-sha256": 64, "expected-sha512": 128, "expected-commit": 40}

// checksum validates hash inputs as melange does, after its parse-time
// substitution of exact ${{package.*}} and ${{vars.*}} references.
func (dg *diagnoser) checksum(name string, v *yaml.Node, s *model.Step) {
	want, ok := shaLength[name]
	if !ok {
		return
	}
	val := v.Value
	if strings.Contains(val, "${{range.") {
		// Each range item substitutes a different value: check them all.
		if val == "${{range.value}}" && s.Subpackage != nil {
			if set := dg.model.DataNodes[s.Subpackage.Range]; set != nil {
				document.Entries(document.MapValue(set, "items"), func(k, iv *yaml.Node) {
					if !validHex(iv.Value, want) {
						dg.node(iv, protocol.DiagnosticSeverityError, codeBadChecksum,
							fmt.Sprintf("%s for %q (used as %s in subpackage %q) must be %d hex characters, got %d", "value", k.Value, name, s.Subpackage.Name, want, len(iv.Value)))
					}
				})
			}
		}
		return
	}
	if strings.Contains(val, "${{") {
		val = dg.snap.Subst().Resolve(val, subst.Scope{Subpackage: s.Subpackage})
		if strings.Contains(val, "${{") {
			// Only parse-time substitution happens before this check, and
			// only for the exact form, so anything left makes melange fail.
			if !exactForm(v.Value) {
				dg.node(v, protocol.DiagnosticSeverityError, codeBadChecksum, fmt.Sprintf("%s is checked before full substitution: write variables without spaces, like ${{vars.name}}", name))
			}
			return
		}
	}
	if !validHex(val, want) {
		kind := "hex characters"
		if name == "expected-commit" {
			kind = "hex characters (a full commit SHA)"
		}
		dg.node(v, protocol.DiagnosticSeverityError, codeBadChecksum, fmt.Sprintf("%s must be %d %s, got %d characters", name, want, kind, len(val)))
	}
}

func validHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

func exactForm(s string) bool {
	for _, m := range model.TemplatePattern.FindAllStringSubmatch(s, -1) {
		if m[0] != "${{"+m[1]+"}}" {
			return false
		}
	}
	return true
}

// condition checks an if: expression with melange's own parser.
func (dg *diagnoser) condition(n *yaml.Node) {
	if n.Kind != yaml.ScalarNode {
		return
	}
	_, err := cond.Evaluate(n.Value, func(string) (string, error) { return "", nil })
	if err != nil {
		dg.node(n, protocol.DiagnosticSeverityError, codeBadCondition, "invalid if: condition: "+err.Error())
	}
}

// fieldKind classifies where a template appears, which decides whether
// melange substitutes it.
type fieldKind int

const (
	fieldCompiled        fieldKind = iota // step runs/with/if/working-directory/needs: strict
	fieldParseTime                        // substituted by exact text at parse time, else left as is
	fieldParseStrict                      // parse-time and strict: dependencies, environment packages
	fieldVars                             // vars: values are never substituted
	fieldTransformFrom                    // var-transforms from: parse-time and strict
	fieldStepEnvironment                  // step environment: parse-time only
	fieldSubpackageIf                     // resolved at parse time only
	fieldDeferred                         // var-transforms replace: kept and substituted where the variable is used
)

func (dg *diagnoser) fieldKind(t *model.Template) fieldKind {
	p := t.Path
	if len(p) > 0 && p[0].Key == "vars" {
		return fieldVars
	}
	if len(p) >= 3 && p[0].Key == "var-transforms" {
		if p[2].Key == "from" {
			return fieldTransformFrom
		}
		// The result is inlined where ${{vars.X}} is used, then
		// substituted again at compile time.
		return fieldDeferred
	}
	if t.Step != nil && len(p) > len(t.Step.Path) {
		switch p[len(t.Step.Path)].Key {
		case "runs", "with", "if", "working-directory", "needs":
			return fieldCompiled
		case "environment":
			return fieldStepEnvironment
		}
		return fieldParseTime
	}
	if p.Has("dependencies") || (p.Has("environment") && p.Has("packages")) {
		return fieldParseStrict
	}
	if t.Subpackage != nil && len(p) == 3 && p[2].Key == "if" {
		return fieldSubpackageIf
	}
	return fieldParseTime
}

// parseTimeVar reports whether melange's parse-time replacer knows name.
func (dg *diagnoser) parseTimeVar(name string, t *model.Template) bool {
	switch name {
	case "package.name", "package.version", "package.description", "package.epoch", "package.full-version":
		return true
	case "range.key", "range.value":
		return t.Subpackage != nil && t.Subpackage.Range != ""
	}
	if v, ok := strings.CutPrefix(name, "vars."); ok {
		_, defined := dg.model.Vars[v]
		return defined
	}
	return false
}

var varChars = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func (dg *diagnoser) templates() {
	sm := dg.snap.Subst()
	if len(sm.Global) == 0 {
		return // could not resolve anything; avoid a flood of false errors
	}
	for _, t := range dg.model.Templates {
		name := t.Expr
		if name == "" {
			dg.add(t.Range, protocol.DiagnosticSeverityError, codeUndefinedVar, "empty variable name")
			continue
		}
		if !varChars.MatchString(name) {
			// melange has no expressions inside ${{ }}. Compiled fields fail;
			// elsewhere the text is kept literally, which is rarely intended.
			sev, tail := protocol.DiagnosticSeverityError, ""
			if dg.fieldKind(t) != fieldCompiled {
				sev, tail = protocol.DiagnosticSeverityWarning, "; melange leaves it in the output as literal text"
			}
			dg.add(t.ExprRange, sev, codeUndefinedVar, fmt.Sprintf("%q is not a variable name: ${{ }} holds a single name of letters, digits, '.', '_' and '-'%s", name, tail))
			continue
		}
		exact := dg.doc.Line(int(t.Range.Start.Line))[dg.doc.ByteOffset(t.Range.Start):dg.doc.ByteOffset(t.Range.End)] == "${{"+name+"}}"
		parseTime := dg.parseTimeVar(name, t)

		switch dg.fieldKind(t) {
		case fieldDeferred:
			continue
		case fieldVars:
			dg.add(t.Range, protocol.DiagnosticSeverityError, codeVarNotHere, "variables are not substituted inside vars: values; use var-transforms: to derive a variable")
			continue
		case fieldTransformFrom, fieldParseStrict:
			if !parseTime || (!exact && strings.HasPrefix(name, "range.")) {
				dg.add(t.ExprRange, protocol.DiagnosticSeverityError, codeUndefinedVar,
					fmt.Sprintf("variable %q is not defined here: only ${{package.*}} and ${{vars.*}} are available at parse time", name))
			}
			continue
		case fieldStepEnvironment, fieldParseTime, fieldSubpackageIf:
			if parseTime && exact {
				continue
			}
			if parseTime {
				dg.add(t.Range, protocol.DiagnosticSeverityWarning, codeVarSpacing, fmt.Sprintf("only substituted when written exactly ${{%s}}, without spaces", name), "${{"+name+"}}")
				continue
			}
			where, sev := "here", protocol.DiagnosticSeverityWarning
			switch {
			case dg.fieldKind(t) == fieldStepEnvironment:
				where = "in a step's environment:"
			case t.Step != nil && len(t.Path) == len(t.Step.Path)+1 && t.Path[len(t.Step.Path)].Key == "name":
				// Only the build log shows step names.
				where, sev = "in step names", protocol.DiagnosticSeverityInformation
			}
			_, defined := dg.lookup(name, t)
			if v, isVar := strings.CutPrefix(name, "vars."); isVar {
				_, defined = dg.model.Vars[v]
			}
			if defined || (isVarLike(name) && !strings.HasPrefix(name, "vars.")) {
				dg.add(t.ExprRange, sev, codeVarNotHere,
					fmt.Sprintf("${{%s}} is not substituted %s; it will be left as literal text", name, where))
			} else {
				// Not fatal here: melange keeps the text as it is.
				dg.undefinedAt(t, name, protocol.DiagnosticSeverityWarning, "; melange leaves it as literal text")
			}
			continue
		}

		// Compiled fields: exact-form range.* was already replaced at parse
		// time; everything else must be in the compile-time map.
		if strings.HasPrefix(name, "range.") {
			if !parseTime {
				dg.add(t.ExprRange, protocol.DiagnosticSeverityError, codeUndefinedVar, fmt.Sprintf("%s is only defined in subpackages with range:", name))
			} else if !exact {
				dg.add(t.Range, protocol.DiagnosticSeverityError, codeVarSpacing, fmt.Sprintf("range variables are only substituted when written exactly ${{%s}}", name), "${{"+name+"}}")
			}
			continue
		}
		if name == "package.description" {
			if !exact {
				dg.add(t.Range, protocol.DiagnosticSeverityError, codeVarSpacing, "${{package.description}} is only substituted when written exactly, without spaces", "${{package.description}}")
			}
			continue
		}
		if _, ok := dg.lookup(name, t); !ok {
			dg.undefined(t, name)
		}
	}
}

// isVarLike reports whether name is in a namespace melange defines, so a
// miss is about where it is used rather than a typo.
func isVarLike(name string) bool {
	_, ok := known.VariableDoc(name)
	return ok
}

// lookup resolves name as melange would when compiling the template's step.
func (dg *diagnoser) lookup(name string, t *model.Template) (string, bool) {
	return dg.snap.Subst().Lookup(name, subst.Scope{Subpackage: t.Subpackage, Inputs: stepInputs(dg.snap, t.Step)})
}

// stepInputs returns the ${{inputs.*}} visible inside step: the with: of
// every enclosing step, with pipeline defaults filled in.
func stepInputs(snap *Snapshot, s *model.Step) map[string]string {
	if s == nil {
		return nil
	}
	var in map[string]string
	for p := s.Parent; p != nil; p = p.Parent {
		if p.With == nil {
			continue
		}
		if in == nil {
			in = map[string]string{}
		}
		if p.Uses != nil {
			if def := snap.Registry.Resolve(p.Uses.Value, snap.Dirs); def != nil {
				for _, i := range def.Inputs() {
					if _, ok := in[i.Name]; !ok && i.Default != nil {
						in[i.Name] = i.Default.Value
					}
				}
			}
		}
		document.Entries(p.With, func(k, v *yaml.Node) {
			if _, ok := in[k.Value]; !ok {
				in[k.Value] = v.Value
			}
		})
	}
	return in
}

func (dg *diagnoser) undefined(t *model.Template, name string) {
	dg.undefinedAt(t, name, protocol.DiagnosticSeverityError, "")
}

func (dg *diagnoser) undefinedAt(t *model.Template, name string, sev protocol.DiagnosticSeverity, tail string) {
	sc := subst.Scope{Subpackage: t.Subpackage, Inputs: stepInputs(dg.snap, t.Step)}
	msg := fmt.Sprintf("variable %q is not defined", name)
	switch {
	case (name == "subpkg.name" || name == "targets.subpkgdir") && t.Subpackage == nil:
		msg = fmt.Sprintf("${{%s}} is only defined inside subpackages:", name)
	case strings.HasPrefix(name, "inputs.") && sc.Inputs == nil:
		msg = fmt.Sprintf("${{%s}} is only defined in pipeline definitions and in steps nested under a step with with:", name)
	case strings.HasPrefix(name, "vars."):
		msg = fmt.Sprintf("variable %q is not defined in vars: or var-transforms:", strings.TrimPrefix(name, "vars."))
	}
	msg += tail
	sugg := suggest(name, dg.snap.Subst().Names(sc))
	if len(sugg) > 0 {
		msg += fmt.Sprintf("; did you mean ${{%s}}?", sugg[0])
	}
	dg.add(t.ExprRange, sev, codeUndefinedVar, msg, sugg...)
}

func (dg *diagnoser) checksDisabled() {
	check := func(checks *yaml.Node) {
		var names []string
		for _, l := range known.Linters {
			names = append(names, l.Name)
		}
		for _, n := range document.Items(document.MapValue(checks, "disabled")) {
			if _, ok := known.LinterByName(n.Value); ok {
				continue
			}
			msg := fmt.Sprintf("unknown linter %q; melange fails the build at lint time", n.Value)
			sugg := suggest(n.Value, names)
			if len(sugg) > 0 {
				msg += fmt.Sprintf("; did you mean %q?", sugg[0])
			}
			dg.node(n, protocol.DiagnosticSeverityError, codeUnknownLinter, msg, sugg...)
		}
	}
	check(document.MapValue(document.MapValue(dg.doc.Root, "package"), "checks"))
	for _, sp := range dg.model.Subpackages {
		check(document.MapValue(sp.Node, "checks"))
	}
}

func (dg *diagnoser) capabilities() {
	for _, sc := range document.Items(document.MapValue(document.MapValue(dg.doc.Root, "package"), "setcap")) {
		document.Entries(document.MapValue(sc, "add"), func(k, _ *yaml.Node) {
			for _, c := range strings.Split(k.Value, ",") {
				c = strings.TrimSpace(c)
				if !contains(known.SetcapCapabilities, c) {
					dg.node(k, protocol.DiagnosticSeverityError, codeBadCapability,
						fmt.Sprintf("capability %q is not allowed in setcap:; allowed: %s", c, strings.Join(known.SetcapCapabilities, ", ")))
				}
			}
		})
		if r := document.MapValue(sc, "reason"); r == nil || strings.TrimSpace(r.Value) == "" {
			dg.node(sc, protocol.DiagnosticSeverityError, codeBadCapability, "setcap entries need a reason: explaining why the capability is required")
		}
	}

	checkRunner := func(seq *yaml.Node) {
		for _, n := range document.Items(seq) {
			if !contains(known.RunnerCapabilities, n.Value) {
				msg := fmt.Sprintf("%q is not a Linux capability name (expected an uppercase CAP_* name)", n.Value)
				sugg := suggest(strings.ToUpper(n.Value), known.RunnerCapabilities)
				dg.node(n, protocol.DiagnosticSeverityWarning, codeBadCapability, msg, sugg...)
			}
		}
	}
	for _, c := range []*yaml.Node{document.MapValue(dg.doc.Root, "capabilities"), document.MapValue(document.MapValue(dg.doc.Root, "test"), "capabilities")} {
		checkRunner(document.MapValue(c, "add"))
		checkRunner(document.MapValue(c, "drop"))
	}
	for _, s := range dg.model.Steps {
		checkRunner(document.MapValue(document.MapValue(document.MapValue(s.Node, "needs"), "capabilities"), "add"))
	}
}

func (dg *diagnoser) varTransforms() {
	for _, t := range document.Items(document.MapValue(dg.doc.Root, "var-transforms")) {
		if m := document.MapValue(t, "match"); m != nil {
			if _, err := regexp.Compile(m.Value); err != nil {
				dg.node(m, protocol.DiagnosticSeverityError, codeBadRegex, "match is not a valid regular expression: "+err.Error())
			}
		}
	}
	for _, vt := range document.Items(document.MapValue(document.MapValue(dg.doc.Root, "update"), "version-transform")) {
		if m := document.MapValue(vt, "match"); m != nil {
			if _, err := regexp.Compile(m.Value); err != nil {
				dg.node(m, protocol.DiagnosticSeverityError, codeBadRegex, "match is not a valid regular expression: "+err.Error())
			}
		}
	}
	for _, p := range document.Items(document.MapValue(document.MapValue(dg.doc.Root, "update"), "ignore-regex-patterns")) {
		if _, err := regexp.Compile(p.Value); err != nil {
			dg.node(p, protocol.DiagnosticSeverityError, codeBadRegex, "not a valid regular expression: "+err.Error())
		}
	}
}

// enums checks scalar values against fixed sets from the schema.
func (dg *diagnoser) enums() {
	var walk func(n *yaml.Node, path []string)
	walk = func(n *yaml.Node, path []string) {
		switch n.Kind {
		case yaml.MappingNode:
			document.Entries(n, func(k, v *yaml.Node) {
				p := append(append([]string(nil), path...), k.Value)
				if f, _, ok := schema.Lookup(p); ok && f.Deprecated != "" {
					msg := fmt.Sprintf("%s is deprecated", k.Value)
					var fixes []string
					if f.Deprecated != "-" {
						msg += fmt.Sprintf("; use %s instead", f.Deprecated)
						fixes = append(fixes, f.Deprecated)
					}
					dg.node(k, protocol.DiagnosticSeverityInformation, codeDeprecated, msg, fixes...)
				}
				if v.Kind == yaml.ScalarNode && v.Value != "" && !strings.Contains(v.Value, "${{") {
					if f, _, ok := schema.Lookup(p); ok {
						if len(f.Enum) > 0 && !contains(f.Enum, v.Value) {
							dg.node(v, protocol.DiagnosticSeverityError, codeBadEnum,
								fmt.Sprintf("%s must be one of: %s", k.Value, strings.Join(f.Enum, ", ")), suggest(v.Value, f.Enum)...)
						}
						if f.Type == "boolean" && v.Tag != "!!bool" {
							dg.node(v, protocol.DiagnosticSeverityError, codeBadEnum, fmt.Sprintf("%s must be true or false", k.Value), "true", "false")
						}
					}
				}
				walk(v, p)
			})
		case yaml.SequenceNode:
			for _, it := range n.Content {
				walk(it, append(append([]string(nil), path...), "0"))
			}
		}
	}
	walk(dg.doc.Root, nil)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

var (
	compileSubpackage = regexp.MustCompile(`compiling subpackage "([^"]+)"`)
	compileStep       = regexp.MustCompile(`Pipeline\[(\d+)\]`)
)

// compile runs melange's compile step as a backstop.
func (dg *diagnoser) compile() {
	cfg, err := subst.Parse(dg.doc.Text)
	if err != nil {
		return
	}
	b := &build.Build{
		Configuration: cfg,
		PipelineDirs:  pipeline.Search(dg.snap.Dirs),
		Arch:          subst.Arch,
	}
	err = b.Compile(context.Background())
	if err == nil {
		return
	}
	msg := err.Error()

	// Point at the subpackage and step melange names, if it does.
	var steps []*model.Step
	var at *yaml.Node
	if m := compileSubpackage.FindStringSubmatch(msg); m != nil {
		for _, sp := range dg.model.Subpackages {
			if sp.Name == m[1] {
				at = sp.NameNode
				steps = dg.topSteps(sp, strings.Contains(msg, "test"))
			}
		}
	} else {
		steps = dg.topSteps(nil, strings.Contains(msg, "test pipelines"))
	}
	if m := compileStep.FindStringSubmatch(msg); m != nil {
		if i, _ := strconv.Atoi(m[1]); i < len(steps) {
			at = steps[i].Node.Content[0]
		}
	}
	dg.at(at, protocol.DiagnosticSeverityError, codeCompile, "melange compile: "+msg)
}

// topSteps returns the top-level steps of a pipeline list, in order.
func (dg *diagnoser) topSteps(sp *model.Subpackage, test bool) []*model.Step {
	var out []*model.Step
	for _, s := range dg.model.Steps {
		if s.Parent == nil && s.Subpackage == sp && s.Test == test {
			out = append(out, s)
		}
	}
	return out
}

// pipelineFile checks a pipeline definition.
func (dg *diagnoser) pipelineFile() {
	var p config.Pipeline
	if err := pipeline.DecodeStrict(dg.doc.Text, &p); err != nil {
		for _, m := range unmarshalLine.FindAllStringSubmatch(err.Error(), -1) {
			line, _ := strconv.Atoi(m[1])
			if f := unknownField.FindStringSubmatch(m[2]); f != nil {
				// A common convention for documenting pipelines and steps.
				if f[1] == "description" {
					continue
				}
				var names []string
				for _, fl := range schema.FieldsOf(f[2]) {
					names = append(names, fl.Name)
				}
				msg := fmt.Sprintf("field %q is not part of melange's pipeline schema; melange ignores it", f[1])
				sugg := suggest(f[1], names)
				if len(sugg) > 0 {
					msg += fmt.Sprintf("; did you mean %q?", sugg[0])
				}
				l := dg.doc.Line(line - 1)
				if i := strings.Index(l, f[1]+":"); i >= 0 {
					dg.add(protocol.Range{Start: dg.doc.ByteToPosition(line-1, i), End: dg.doc.ByteToPosition(line-1, i+len(f[1]))},
						protocol.DiagnosticSeverityInformation, codeUnknownField, msg, sugg...)
					continue
				}
			}
			dg.line(line-1, protocol.DiagnosticSeverityWarning, codeMelange, m[2])
		}
	}

	for _, s := range dg.model.Steps {
		if s.Path == nil {
			continue // the file itself
		}
		dg.step(s)
	}
	if c := document.MapValue(dg.doc.Root, "if"); c != nil {
		dg.condition(c)
	}

	// ${{inputs.*}} must be declared.
	for _, t := range dg.model.Templates {
		name, ok := strings.CutPrefix(t.Expr, "inputs.")
		if !ok {
			continue
		}
		if _, declared := dg.model.Inputs[name]; declared {
			continue
		}
		msg := fmt.Sprintf("input %q is not declared in inputs:", name)
		sev := protocol.DiagnosticSeverityError
		if t.Step != nil && len(t.Path) == len(t.Step.Path)+1 && t.Path[len(t.Step.Path)].Key == "name" {
			sev = protocol.DiagnosticSeverityInformation
			msg += "; step names are not substituted, so the log shows it literally"
		}
		sugg := suggest(name, dg.model.InputOrder)
		if len(sugg) > 0 {
			msg += fmt.Sprintf("; did you mean %q?", sugg[0])
		}
		var fixes []string
		for _, s := range sugg {
			fixes = append(fixes, "inputs."+s)
		}
		dg.add(t.ExprRange, sev, codeUndefinedVar, msg, fixes...)
	}
}

// patches warns about patch files that do not exist. A patch named in
// some runs: script is probably generated during the build.
func (dg *diagnoser) patches() {
	var scripts strings.Builder
	for _, s := range dg.model.Steps {
		if s.Runs != nil {
			scripts.WriteString(s.Runs.Value)
		}
	}
	for _, s := range dg.model.Steps {
		for _, p := range patchRefs(dg.snap, s) {
			// Paths with directories may point into the upstream source
			// tree, which only exists at build time.
			if p.File == "" && len(p.Tried) > 0 && !strings.Contains(p.Name, "/") && workingDir(s) == "" && !strings.Contains(scripts.String(), p.Name) {
				dg.add(p.Range, protocol.DiagnosticSeverityWarning, codeMissingPatch,
					fmt.Sprintf("patch %q not found; looked for %s", p.Name, strings.Join(p.Tried, ", ")))
			}
		}
	}
}

var nolint = regexp.MustCompile(`(?m)^\s*#\s*nolint:([\w,-]+)`)

// suppressed reports whether a #nolint comment disables rule.
func (dg *diagnoser) suppressed(rule string) bool {
	for _, m := range nolint.FindAllStringSubmatch(dg.doc.Text, -1) {
		for _, r := range strings.Split(m[1], ",") {
			if strings.TrimSpace(r) == rule {
				return true
			}
		}
	}
	return false
}

// updateLint checks update: settings that tooling relies on.
func (dg *diagnoser) updateLint() {
	update := document.MapValue(dg.doc.Root, "update")
	if update == nil {
		return
	}
	if e := document.MapValue(update, "enabled"); e != nil && e.Value == "false" && document.MapValue(update, "exclude-reason") == nil && !dg.suppressed(codeUpdateReason) {
		dg.node(e, protocol.DiagnosticSeverityInformation, codeUpdateReason, "update is disabled without an exclude-reason: explaining why")
	}
	if sk, sv := document.MapEntry(update, "schedule"); sk != nil && !dg.suppressed(codeScheduleReason) {
		if r := document.MapValue(sv, "reason"); r == nil || strings.TrimSpace(r.Value) == "" {
			dg.node(sk, protocol.DiagnosticSeverityWarning, codeScheduleReason, "update schedule needs a reason:")
		}
	}
}

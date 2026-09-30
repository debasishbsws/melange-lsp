// Package subst resolves melange ${{...}} variables for a document, using
// melange's own substitution map where possible.
package subst

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing/fstest"

	"melange-lsp/internal/document"
	"melange-lsp/internal/model"

	apko_types "chainguard.dev/apko/pkg/build/types"
	"chainguard.dev/melange/pkg/build"
	"chainguard.dev/melange/pkg/config"
	"gopkg.in/yaml.v3"
)

// Map holds resolved variables for one document.
type Map struct {
	// Global are variables valid anywhere in the package.
	Global map[string]string
	// Sub holds subpackage-only variables by subpackage index.
	Sub map[int]map[string]string
	// Config is the parsed configuration, or nil if parsing failed.
	Config *config.Configuration
	// ParseErr is melange's parse error, if any.
	ParseErr error
}

// Scope selects which variables are visible.
type Scope struct {
	Subpackage *model.Subpackage
	// Inputs are ${{inputs.*}} visible at this point.
	Inputs map[string]string
}

// Lookup resolves a variable in scope. ok is false if it is not defined.
func (m *Map) Lookup(name string, sc Scope) (string, bool) {
	if m == nil {
		return "", false
	}
	if strings.HasPrefix(name, "inputs.") && sc.Inputs != nil {
		v, ok := sc.Inputs[strings.TrimPrefix(name, "inputs.")]
		return v, ok
	}
	if sc.Subpackage != nil {
		if v, ok := m.Sub[sc.Subpackage.Index][name]; ok {
			return v, true
		}
	}
	v, ok := m.Global[name]
	return v, ok
}

// Names returns every variable visible in scope, sorted.
func (m *Map) Names(sc Scope) []string {
	seen := map[string]bool{}
	var out []string
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	if m != nil {
		for k := range m.Global {
			add(k)
		}
		if sc.Subpackage != nil {
			for k := range m.Sub[sc.Subpackage.Index] {
				add(k)
			}
		}
	}
	for k := range sc.Inputs {
		add("inputs." + k)
	}
	sort.Strings(out)
	return out
}

// Resolve replaces every ${{...}} in s that is defined in scope.
func (m *Map) Resolve(s string, sc Scope) string {
	return model.TemplatePattern.ReplaceAllStringFunc(s, func(t string) string {
		name := model.TemplatePattern.FindStringSubmatch(t)[1]
		if v, ok := m.Lookup(name, sc); ok {
			return v
		}
		return t
	})
}

// Arch is the architecture used to resolve build.* variables.
var Arch = apko_types.ParseArchitecture(runtime.GOARCH)

// Build computes the substitution map for a config document. It returns an
// empty map for other kinds.
func Build(d *document.Document, mdl *model.Model) *Map {
	m := &Map{Global: map[string]string{}, Sub: map[int]map[string]string{}}
	if d.Kind != document.KindConfig {
		return m
	}

	cfg, err := Parse(d.Text)
	m.Config, m.ParseErr = cfg, err
	if err != nil {
		// Fall back to a lenient decode so hovers still resolve while the
		// file has errors melange rejects.
		cfg = &config.Configuration{}
		if yerr := yaml.Unmarshal([]byte(d.Text), cfg); yerr != nil {
			cfg = nil
		}
	}
	if cfg == nil {
		return m
	}

	if err == nil {
		if sm, serr := build.NewSubstitutionMap(cfg, Arch, "gnu", nil); serr == nil {
			m.Global = clean(sm.Substitutions)
			m.addSubpackages(mdl, cfg, sm)
			return m
		}
	}
	m.Global = lenient(cfg)
	m.addSubpackages(mdl, cfg, nil)
	return m
}

// Parse runs melange's configuration parser on text.
func Parse(text string) (*config.Configuration, error) {
	fsys := fstest.MapFS{"melange.yaml": &fstest.MapFile{Data: []byte(text)}}
	return config.ParseConfiguration(context.Background(), "melange.yaml", config.WithFS(fsys))
}

// addSubpackages records subpkg.* and friends for each subpackage in the
// document. Range subpackages are expanded by melange, so their parsed
// counterpart is matched on the first expansion.
func (m *Map) addSubpackages(mdl *model.Model, cfg *config.Configuration, sm *build.SubstitutionMap) {
	for _, sp := range mdl.Subpackages {
		vars := map[string]string{}
		var parsed *config.Subpackage
		for i := range cfg.Subpackages {
			if cfg.Subpackages[i].Name == sp.Name {
				parsed = &cfg.Subpackages[i]
				break
			}
		}
		if parsed == nil && sp.Range != "" && len(mdl.Data[sp.Range]) > 0 {
			key := mdl.Data[sp.Range][0]
			want := strings.ReplaceAll(sp.Name, "${{range.key}}", key)
			for i := range cfg.Subpackages {
				if cfg.Subpackages[i].Name == want {
					parsed = &cfg.Subpackages[i]
				}
			}
		}
		name := sp.Name
		if parsed != nil {
			name = parsed.Name
		}
		if sm != nil && parsed != nil {
			full := clean(sm.Subpackage(parsed).Substitutions)
			for k, v := range full {
				if m.Global[k] != v {
					vars[k] = v
				}
			}
		} else {
			vars["subpkg.name"] = name
			vars["targets.subpkgdir"] = "/home/build/melange-out/" + name
			vars["context.name"] = name
			vars["targets.contextdir"] = "/home/build/melange-out/" + name
		}
		if sp.Range != "" {
			keys := mdl.Data[sp.Range]
			vars["range.key"] = strings.Join(keys, " | ")
			vars["range.value"] = fmt.Sprintf("(%d items from data %q)", len(keys), sp.Range)
		}
		m.Sub[sp.Index] = vars
	}
}

func clean(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[strings.TrimSuffix(strings.TrimPrefix(k, "${{"), "}}")] = v
	}
	return out
}

// lenient reproduces the compile-time map from a configuration melange
// could not fully validate.
func lenient(cfg *config.Configuration) map[string]string {
	pkg := cfg.Package
	m := map[string]string{
		"${{package.name}}":         pkg.Name,
		"${{package.version}}":      pkg.Version,
		"${{package.epoch}}":        strconv.FormatUint(pkg.Epoch, 10),
		"${{package.full-version}}": fmt.Sprintf("%s-r%d", pkg.Version, pkg.Epoch),
		"${{package.srcdir}}":       "/home/build",
		"${{targets.outdir}}":       "/home/build/melange-out",
		"${{targets.destdir}}":      "/home/build/melange-out/" + pkg.Name,
		"${{targets.contextdir}}":   "/home/build/melange-out/" + pkg.Name,
		"${{context.name}}":         pkg.Name,
		"${{build.arch}}":           Arch.ToAPK(),
		"${{build.goarch}}":         string(Arch),
	}
	for k, v := range cfg.Vars {
		m["${{vars."+k+"}}"] = v
	}
	m["${{targets.package."+pkg.Name+"}}"] = "/home/build/melange-out/" + pkg.Name
	for _, sp := range cfg.Subpackages {
		m["${{targets.package."+sp.Name+"}}"] = "/home/build/melange-out/" + sp.Name
	}
	for k := range cfg.Options {
		m["${{options."+k+".enabled}}"] = "false"
	}
	_ = cfg.PerformVarSubstitutions(m)
	return clean(m)
}

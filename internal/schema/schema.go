// Package schema answers questions about melange's configuration schema:
// which keys are valid at a path, their types, and their documentation. It
// reads the JSON Schema melange generates from its Go types
// (config.SchemaJSON) and corrects the places where that schema disagrees
// with how melange actually decodes YAML.
package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"

	"chainguard.dev/melange/pkg/config"
)

// Field is one key of a schema object.
type Field struct {
	// Name is the YAML key.
	Name        string
	Description string
	// Type is "object", "array", "map", "string", "integer", "boolean",
	// or "" when unknown.
	Type string
	// Def is the schema definition of an object value or of array items.
	Def string
	// ItemType is the element type of arrays of scalars.
	ItemType string
	// Enum lists the allowed scalar values, if fixed.
	Enum     []string
	Required bool
	// Deprecated is set for deprecated fields: the replacement key, or
	// "-" when there is none.
	Deprecated string
	// Line is the zero-based line of the property in schema.json.
	Line int
}

// IsContainer reports whether the value is written on following lines.
func (f Field) IsContainer() bool {
	return f.Type == "object" || f.Type == "array" || f.Type == "map"
}

type rawDef struct {
	Properties           map[string]rawProp `json:"properties"`
	AdditionalProperties json.RawMessage    `json:"additionalProperties"`
	Description          string             `json:"description"`
}

type rawProp struct {
	Ref                  string          `json:"$ref"`
	Type                 string          `json:"type"`
	Description          string          `json:"description"`
	Items                *rawProp        `json:"items"`
	Enum                 []string        `json:"enum"`
	AdditionalProperties json.RawMessage `json:"additionalProperties"`
}

var (
	schemaJSON = config.SchemaJSON

	loadOnce sync.Once
	defs     map[string]map[string]Field
	defDocs  map[string]string
)

// required are the keys melange rejects a file without. The generated
// schema marks many optional fields required, so it is not used for this.
var required = map[string][]string{
	"Configuration": {"package"},
	"Package":       {"name", "version", "epoch"},
	"Subpackage":    {"name"},
	"VarTransforms": {"from", "match", "replace", "to"},
	"RangeData":     {"name", "items"},
	"Copyright":     {"license"},
	"Schedule":      {"period"},
}

// enums the schema does not declare.
var enums = map[string][]string{
	"Schedule.period": {"daily", "weekly", "monthly"},
	"CPE.part":        {"a", "o"},
}

// typeFixes corrects types the schema gets wrong for YAML.
var typeFixes = map[string]string{
	// time.Duration: written as "1h30m" in YAML.
	"Package.timeout": "string",
}

// extraDocs adds melange conventions the Go comments do not state.
var extraDocs = map[string]string{
	"Package.epoch":        "Increment by one for every change that does not change `version`, so the new build is published. Reset to `0` when `version` changes.",
	"Package.version":      "The upstream version. Changing it means `epoch` should be reset to `0`.",
	"Subpackage.range":     "Name of a `data:` set. The subpackage is repeated once per item, with `${{range.key}}` and `${{range.value}}` substituted (written without spaces).",
	"Configuration.data":   "Named sets of key/value items that `range:` subpackages iterate over.",
	"Pipeline.uses":        "A reusable pipeline, looked up in the pipeline directories and then melange's built-ins. Its inputs are set with `with:`.",
	"Pipeline.with":        "Inputs for the `uses:` pipeline. Unknown inputs are an error; required inputs without a default must be set.",
	"Pipeline.if":          "Condition deciding whether the step runs, e.g. `${{build.arch}} == 'x86_64'`. Supports `==`, `!=`, `&&`, `||` and parentheses; `&&` and `||` have equal precedence and bind left to right.",
	"Pipeline.environment": "Environment variables for this step and its children. Values only get `${{package.*}}` and `${{vars.*}}` substitution, written without spaces.",
	"Checks.disabled":      "Linters to skip for this package. Unknown names fail the build.",
}

func load() {
	var s struct {
		Defs map[string]rawDef `json:"$defs"`
	}
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		panic("schema: parsing melange schema.json: " + err.Error())
	}
	lines := propertyLines()

	defs = map[string]map[string]Field{}
	defDocs = map[string]string{}
	for name, d := range s.Defs {
		defDocs[name] = d.Description
		req := map[string]bool{}
		for _, r := range required[name] {
			req[r] = true
		}
		fields := map[string]Field{}
		for raw, p := range d.Properties {
			key := yamlKey(raw, d.Properties)
			f := Field{
				Name:        key,
				Description: p.Description,
				Required:    req[key],
				Line:        -1,
			}
			if l, ok := lines[name+"."+raw]; ok {
				f.Line = l
			}
			switch {
			case p.Ref != "":
				f.Type, f.Def = "object", refName(p.Ref)
			case p.Type == "array" && p.Items != nil:
				f.Type = "array"
				if p.Items.Ref != "" {
					f.Def = refName(p.Items.Ref)
				} else {
					f.ItemType = p.Items.Type
				}
			case p.Type == "object" && len(p.AdditionalProperties) > 0:
				f.Type = "map"
				var ap rawProp
				if json.Unmarshal(p.AdditionalProperties, &ap) == nil && ap.Ref != "" {
					f.Def = refName(ap.Ref)
				}
			default:
				f.Type = p.Type
			}
			f.Enum = p.Enum
			if e, ok := enums[name+"."+key]; ok {
				f.Enum = e
			}
			if t, ok := typeFixes[name+"."+key]; ok {
				f.Type = t
			}
			if extra, ok := extraDocs[name+"."+key]; ok {
				if f.Description != "" {
					f.Description += "\n\n"
				}
				f.Description += extra
			}
			if m := deprecatedUse.FindStringSubmatch(f.Description); m != nil {
				f.Deprecated = kebab(m[1])
			} else if strings.Contains(f.Description, "Deprecated") {
				f.Deprecated = "-"
			}
			f.Description = cleanDoc(f.Description)
			fields[key] = f
		}
		defs[name] = fields
	}
}

// yamlKey maps a schema property to its YAML key. Fields without JSON tags
// appear capitalised in the schema (e.g. Needs.Packages) but yaml.v3
// lowercases untagged field names.
func yamlKey(raw string, props map[string]rawProp) string {
	if raw == "" || !unicode.IsUpper(rune(raw[0])) {
		return raw
	}
	return strings.ToLower(raw)
}

var (
	optionalPrefix = regexp.MustCompile(`^(Optional|Required): `)
	deprecatedUse  = regexp.MustCompile(`Deprecated: Use (\w+) instead`)
)

// kebab converts a Go field name to melange's YAML key style.
func kebab(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('-')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

func cleanDoc(s string) string {
	return optionalPrefix.ReplaceAllString(strings.TrimSpace(s), "")
}

func refName(ref string) string {
	return strings.TrimPrefix(ref, "#/$defs/")
}

// propertyLines maps "Def.prop" to the zero-based line of prop in
// schema.json, for go-to-definition.
func propertyLines() map[string]int {
	idx := map[string]int{}
	lines := strings.Split(string(schemaJSON), "\n")
	defLine := regexp.MustCompile(`^    "([A-Za-z]+)": \{`)
	propLine := regexp.MustCompile(`^        "([^"]+)": \{`)
	cur := ""
	for i, l := range lines {
		if m := defLine.FindStringSubmatch(l); m != nil {
			cur = m[1]
			continue
		}
		if m := propLine.FindStringSubmatch(l); m != nil && cur != "" {
			idx[cur+"."+m[1]] = i
		}
	}
	return idx
}

// Root definitions for the two kinds of melange file.
const (
	RootConfig   = "Configuration"
	RootPipeline = "Pipeline"
)

// DefAt returns the definition whose keys are valid at path in a package
// configuration. path holds mapping keys and "0" for sequence items.
func DefAt(path []string) (string, bool) {
	return DefAtFrom(RootConfig, path)
}

// DefAtFrom is DefAt starting from the given root definition.
func DefAtFrom(root string, path []string) (string, bool) {
	loadOnce.Do(load)
	def := root
	for i := 0; i < len(path); i++ {
		seg := path[i]
		if seg == "0" {
			continue
		}
		f, ok := defs[def][seg]
		if !ok || f.Def == "" {
			return "", false
		}
		switch f.Type {
		case "array":
			// An array must be followed by an index to reach its items.
			if i+1 >= len(path) || path[i+1] != "0" {
				return "", false
			}
		case "map":
			// The next segment is a map key, e.g. an input name.
			if i+1 >= len(path) {
				return "", false
			}
			i++
		}
		def = f.Def
	}
	return def, true
}

// Fields returns the keys valid at path in a package configuration.
func Fields(path []string) []Field {
	return FieldsFrom(RootConfig, path)
}

// FieldsFrom returns the keys valid at path from root, sorted by name.
func FieldsFrom(root string, path []string) []Field {
	def, ok := DefAtFrom(root, path)
	if !ok {
		return nil
	}
	return FieldsOf(def)
}

// FieldsOf returns the fields of a definition, sorted by name.
func FieldsOf(def string) []Field {
	loadOnce.Do(load)
	var out []Field
	for _, f := range defs[def] {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup returns the field a configuration path ends at, e.g.
// ["package", "epoch"], and the definition holding it.
func Lookup(path []string) (Field, string, bool) {
	return LookupFrom(RootConfig, path)
}

// LookupFrom is Lookup starting from root.
func LookupFrom(root string, path []string) (Field, string, bool) {
	// Drop trailing indices: the field is the array itself.
	for len(path) > 0 && path[len(path)-1] == "0" {
		path = path[:len(path)-1]
	}
	if len(path) == 0 {
		return Field{}, "", false
	}
	def, ok := DefAtFrom(root, path[:len(path)-1])
	if !ok {
		return Field{}, "", false
	}
	loadOnce.Do(load)
	f, ok := defs[def][path[len(path)-1]]
	return f, def, ok
}

// DefDoc is a definition's own description.
func DefDoc(def string) string {
	loadOnce.Do(load)
	return cleanDoc(defDocs[def])
}

var (
	fileOnce sync.Once
	filePath string
)

// FilePath returns schema.json on disk, writing it to the user cache
// directory on first use so editors can open it.
func FilePath() string {
	fileOnce.Do(func() {
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		dir := filepath.Join(base, "melange-lsp")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		p := filepath.Join(dir, "schema.json")
		if old, err := os.ReadFile(p); err != nil || string(old) != string(schemaJSON) {
			if err := os.WriteFile(p, schemaJSON, 0o644); err != nil {
				return
			}
		}
		filePath = p
	})
	return filePath
}

// Package document holds a parsed melange YAML document: its text, its YAML
// node tree, what kind of melange file it is, and conversions between YAML
// node positions and LSP positions.
package document

import (
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"go.lsp.dev/protocol"
	"gopkg.in/yaml.v3"
)

// Kind is the kind of melange file a document holds.
type Kind int

const (
	// KindUnknown is any YAML that is not a melange file. The server
	// provides no features for it so other YAML tooling is undisturbed.
	KindUnknown Kind = iota
	// KindConfig is a package configuration: a top-level `package:` mapping
	// with a `name:`.
	KindConfig
	// KindPipeline is a reusable pipeline definition, as found in a
	// pipelines/ directory and referenced with `uses:`.
	KindPipeline
)

func (k Kind) String() string {
	switch k {
	case KindConfig:
		return "config"
	case KindPipeline:
		return "pipeline"
	default:
		return "unknown"
	}
}

// Document is an immutable snapshot of one open file.
type Document struct {
	URI     protocol.DocumentURI
	Version int32
	Text    string
	Lines   []string

	// Root is the top-level mapping node, or nil if the text is not a
	// YAML mapping or failed to parse.
	Root *yaml.Node
	// ParseErr is the YAML syntax error, if any.
	ParseErr error

	Kind Kind
}

// New parses text into a Document. It never fails: syntax errors are
// recorded in ParseErr and the kind falls back to a textual check so that
// a half-typed file keeps its features.
func New(uri protocol.DocumentURI, version int32, text string) *Document {
	d := &Document{
		URI:     uri,
		Version: version,
		Text:    text,
		Lines:   splitLines(text),
	}

	var root yaml.Node
	if err := yaml.Unmarshal([]byte(text), &root); err != nil {
		d.ParseErr = err
	} else if len(root.Content) > 0 && root.Content[0].Kind == yaml.MappingNode {
		d.Root = root.Content[0]
	}

	d.Kind = d.classify()
	return d
}

func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// pipelineKeys are the fields of config.Pipeline, which is also the schema
// of a pipeline definition file.
var pipelineKeys = map[string]bool{
	"name": true, "uses": true, "with": true, "runs": true, "pipeline": true,
	"inputs": true, "needs": true, "label": true, "if": true, "assertions": true,
	"working-directory": true, "environment": true,
}

var (
	textPackageKey = regexp.MustCompile(`(?m)^package:[ \t]*(#.*)?$`)
	textNameKey    = regexp.MustCompile(`^[ \t]+name:`)
)

func (d *Document) classify() Kind {
	if d.Root != nil {
		if pkg := MapValue(d.Root, "package"); pkg != nil && pkg.Kind == yaml.MappingNode && MapValue(pkg, "name") != nil {
			return KindConfig
		}
		// Anything in a pipelines/ directory is a pipeline; elsewhere the
		// keys must look like one.
		if d.inPipelinesDir() || d.looksLikePipeline() {
			return KindPipeline
		}
		return KindUnknown
	}

	// Unparseable, usually mid-edit: a top-level `package:` followed by an
	// indented `name:` is enough to keep treating it as a config.
	if loc := textPackageKey.FindStringIndex(d.Text); loc != nil {
		startLine := strings.Count(d.Text[:loc[0]], "\n")
		for _, l := range d.Lines[startLine+1:] {
			if strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "#") {
				continue
			}
			if !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
				break
			}
			if textNameKey.MatchString(l) {
				return KindConfig
			}
		}
	}
	if d.inPipelinesDir() {
		return KindPipeline
	}
	return KindUnknown
}

// looksLikePipeline reports whether every top-level key is a pipeline field
// and the document does something (runs, uses, or nested pipeline).
func (d *Document) looksLikePipeline() bool {
	acts := false
	for i := 0; i+1 < len(d.Root.Content); i += 2 {
		k := d.Root.Content[i].Value
		if !pipelineKeys[k] {
			return false
		}
		if k == "runs" || k == "uses" || k == "pipeline" {
			acts = true
		}
	}
	return acts
}

func (d *Document) inPipelinesDir() bool {
	return strings.Contains(Filename(d.URI), "/pipelines/")
}

// Line returns line i, or "" if out of range.
func (d *Document) Line(i int) string {
	if i < 0 || i >= len(d.Lines) {
		return ""
	}
	return d.Lines[i]
}

// Position converts a zero-based line and rune column to an LSP position,
// whose character offset counts UTF-16 code units.
func (d *Document) Position(line, runeCol int) protocol.Position {
	return protocol.Position{Line: uint32(line), Character: uint32(UTF16Col(d.Line(line), runeCol))}
}

// ByteToPosition converts a zero-based line and byte offset within it.
func (d *Document) ByteToPosition(line, byteCol int) protocol.Position {
	l := d.Line(line)
	if byteCol > len(l) {
		byteCol = len(l)
	}
	return d.Position(line, utf8.RuneCountInString(l[:byteCol]))
}

// ByteOffset converts an LSP position to a byte offset within its line.
func (d *Document) ByteOffset(pos protocol.Position) int {
	l := d.Line(int(pos.Line))
	units := 0
	for i, r := range l {
		if units >= int(pos.Character) {
			return i
		}
		units++
		if r >= 0x10000 {
			units++
		}
	}
	return len(l)
}

// UTF16Col returns the UTF-16 offset of rune column runeCol in line.
func UTF16Col(line string, runeCol int) int {
	units, n := 0, 0
	for _, r := range line {
		if n >= runeCol {
			break
		}
		units++
		if r >= 0x10000 {
			units++
		}
		n++
	}
	return units
}

// LineEnd is the position at the end of line i.
func (d *Document) LineEnd(i int) protocol.Position {
	return d.Position(i, utf8.RuneCountInString(d.Line(i)))
}

// IndentOf returns the number of leading spaces and tabs.
func IndentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// Filename returns the local path of a file:// URI, or "" for other schemes.
func Filename(uri protocol.DocumentURI) string {
	u, err := url.Parse(string(uri))
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return filepath.FromSlash(u.Path)
}

// URI returns the file:// URI for a local path.
func URI(path string) protocol.DocumentURI {
	return protocol.DocumentURI((&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
}

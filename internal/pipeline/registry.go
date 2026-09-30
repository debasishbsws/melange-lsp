// Package pipeline finds and parses melange pipeline definitions: the
// built-in ones embedded in melange and local ones in pipeline directories.
package pipeline

import (
	"bytes"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"melange-lsp/internal/document"
	"melange-lsp/internal/model"

	"go.lsp.dev/protocol"
	"gopkg.in/yaml.v3"
)

// BuiltinDir is where melange's apk installs pipelines; melange searches
// it after --pipeline-dirs and before its embedded copies.
const BuiltinDir = "/usr/share/melange/pipelines"

// Def is a parsed pipeline definition.
type Def struct {
	// Name is the uses: name, e.g. "go/build".
	Name string
	// Title is the pipeline's own name: field.
	Title string
	// File is the definition's path on disk. Built-ins are extracted to a
	// cache directory so editors can open them.
	File    string
	Dir     string
	Builtin bool

	Doc   *document.Document
	Model *model.Model
	Needs []string
}

// Inputs returns the declared inputs in file order.
func (d *Def) Inputs() []*model.Input {
	var out []*model.Input
	for _, n := range d.Model.InputOrder {
		out = append(out, d.Model.Inputs[n])
	}
	return out
}

// Input returns the named input, or nil.
func (d *Def) Input(name string) *model.Input {
	return d.Model.Inputs[name]
}

// Location is the start of the definition file.
func (d *Def) Location() protocol.Location {
	return protocol.Location{URI: d.Doc.URI}
}

type cached struct {
	def     *Def
	modTime time.Time
}

// Registry resolves uses: names. It is safe for concurrent use.
type Registry struct {
	embedded fs.FS
	prefix   string

	extractOnce sync.Once
	extractDir  string

	mu    sync.Mutex
	defs  map[string]cached  // by file path
	lists map[string][]entry // by directory
}

type entry struct {
	name, file string
}

// NewRegistry creates a registry whose built-ins come from embedded under
// prefix (melange's build.PipelinesFS and "pipelines").
func NewRegistry(embedded fs.FS, prefix string) *Registry {
	return &Registry{
		embedded: embedded,
		prefix:   prefix,
		defs:     map[string]cached{},
		lists:    map[string][]entry{},
	}
}

// Invalidate drops cached listings, e.g. after pipeline files change.
// Parsed definitions are revalidated by modification time anyway.
func (r *Registry) Invalidate() {
	r.mu.Lock()
	r.lists = map[string][]entry{}
	r.mu.Unlock()
}

// Search returns dirs followed by melange's own fallbacks, in the order
// melange searches them.
func Search(dirs []string) []string {
	out := append([]string(nil), dirs...)
	if fi, err := os.Stat(BuiltinDir); err == nil && fi.IsDir() {
		out = append(out, BuiltinDir)
	}
	return out
}

// Resolve finds the pipeline for a uses: name, searching dirs first and the
// built-ins last, like melange. It returns nil if there is none.
func (r *Registry) Resolve(uses string, dirs []string) *Def {
	if !validUses(uses) {
		return nil
	}
	for _, dir := range Search(dirs) {
		file := filepath.Join(dir, filepath.FromSlash(uses)+".yaml")
		if def := r.load(uses, dir, file, false); def != nil {
			return def
		}
	}
	dir := r.builtinDir()
	if dir == "" {
		return nil
	}
	return r.load(uses, dir, filepath.Join(dir, filepath.FromSlash(uses)+".yaml"), true)
}

// validUses mirrors melange's rejection of absolute and parent paths.
func validUses(uses string) bool {
	return uses != "" && !strings.HasPrefix(uses, "/") && !strings.Contains(uses, "..") && !strings.Contains(uses, "${{")
}

func (r *Registry) load(uses, dir, file string, builtin bool) *Def {
	fi, err := os.Stat(file)
	if err != nil || fi.IsDir() {
		return nil
	}
	r.mu.Lock()
	c, ok := r.defs[file]
	r.mu.Unlock()
	if ok && c.modTime.Equal(fi.ModTime()) {
		return c.def
	}

	data, err := os.ReadFile(file) // #nosec G304 -- pipeline files the user asked for
	if err != nil {
		return nil
	}
	doc := document.New(document.URI(file), 0, string(data))
	// Pipeline files are pipelines by definition, whatever they contain.
	doc.Kind = document.KindPipeline
	def := &Def{
		Name:    uses,
		File:    file,
		Dir:     dir,
		Builtin: builtin,
		Doc:     doc,
		Model:   model.Build(doc),
	}
	if doc.Root != nil {
		if t := document.MapValue(doc.Root, "name"); t != nil {
			def.Title = t.Value
		}
		for _, p := range document.Items(document.MapValue(document.MapValue(doc.Root, "needs"), "packages")) {
			def.Needs = append(def.Needs, p.Value)
		}
	}

	r.mu.Lock()
	r.defs[file] = cached{def: def, modTime: fi.ModTime()}
	r.mu.Unlock()
	return def
}

// List returns every pipeline visible from dirs, first match per name, in
// melange's search order, sorted by name.
func (r *Registry) List(dirs []string) []*Def {
	seen := map[string]bool{}
	var out []*Def
	add := func(dir string, builtin bool) {
		for _, e := range r.listDir(dir) {
			if seen[e.name] {
				continue
			}
			seen[e.name] = true
			if def := r.load(e.name, dir, e.file, builtin); def != nil {
				out = append(out, def)
			}
		}
	}
	for _, dir := range Search(dirs) {
		add(dir, false)
	}
	if dir := r.builtinDir(); dir != "" {
		add(dir, true)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *Registry) listDir(dir string) []entry {
	r.mu.Lock()
	if es, ok := r.lists[dir]; ok {
		r.mu.Unlock()
		return es
	}
	r.mu.Unlock()

	var es []entry
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(p, ".yaml") {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		es = append(es, entry{name: filepath.ToSlash(strings.TrimSuffix(rel, ".yaml")), file: p})
		return nil
	})

	r.mu.Lock()
	r.lists[dir] = es
	r.mu.Unlock()
	return es
}

// builtinDir extracts the embedded pipelines once per process into the user
// cache directory and returns that directory.
func (r *Registry) builtinDir() string {
	r.extractOnce.Do(func() {
		if r.embedded == nil {
			return
		}
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		dir := filepath.Join(base, "melange-lsp", "pipelines-"+melangeVersion())
		err = fs.WalkDir(r.embedded, r.prefix, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".yaml") {
				return err
			}
			data, err := fs.ReadFile(r.embedded, p)
			if err != nil {
				return err
			}
			rel := strings.TrimPrefix(p, r.prefix+"/")
			dst := filepath.Join(dir, filepath.FromSlash(rel))
			if old, err := os.ReadFile(dst); err == nil && bytes.Equal(old, data) {
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			return os.WriteFile(dst, data, 0o444)
		})
		if err != nil {
			log.Printf("melange-lsp: extracting built-in pipelines: %v", err)
			return
		}
		r.extractDir = dir
	})
	return r.extractDir
}

func melangeVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "chainguard.dev/melange" {
				if dep.Replace != nil {
					return "replaced"
				}
				return dep.Version
			}
		}
	}
	return "unknown"
}

var pipelineDirFlag = regexp.MustCompile(`--pipeline-dirs?[= ]+([^\s"'\\]+)`)

// DiscoverDirs finds the pipeline directories used to build the file at
// path. It reads --pipeline-dir(s) flags from the nearest Makefile, which
// is how stereo and wolfi-dev/os configure melange; failing that it uses
// pipelines/ directories next to the file and in its ancestors. root
// bounds the search.
func DiscoverDirs(file, root string) []string {
	if file == "" {
		return nil
	}
	dir := filepath.Dir(file)
	stop := stopDir(dir, root)

	for d := dir; ; d = filepath.Dir(d) {
		if dirs := makefileDirs(filepath.Join(d, "Makefile")); len(dirs) > 0 {
			return dirs
		}
		if d == stop || filepath.Dir(d) == d {
			break
		}
	}

	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		if !seen[d] && isDir(d) {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	for d := dir; ; d = filepath.Dir(d) {
		// A file inside a pipelines tree resolves uses: from its root.
		if path.Base(filepath.ToSlash(d)) == "pipelines" {
			add(d)
		}
		add(filepath.Join(d, "pipelines"))
		if d == stop || filepath.Dir(d) == d {
			break
		}
	}
	return dirs
}

// stopDir is root if dir is inside it, else the nearest ancestor holding
// .git, else dir itself.
func stopDir(dir, root string) string {
	if root != "" {
		if rel, err := filepath.Rel(root, dir); err == nil && !strings.HasPrefix(rel, "..") {
			return root
		}
	}
	for d := dir; ; d = filepath.Dir(d) {
		if isDir(filepath.Join(d, ".git")) || isFile(filepath.Join(d, ".git")) {
			return d
		}
		if filepath.Dir(d) == d {
			return dir
		}
	}
}

func makefileDirs(makefile string) []string {
	data, err := os.ReadFile(makefile) // #nosec G304
	if err != nil {
		return nil
	}
	var dirs []string
	seen := map[string]bool{}
	for _, m := range pipelineDirFlag.FindAllStringSubmatch(string(data), -1) {
		d := m[1]
		if strings.Contains(d, "$") {
			continue
		}
		if !filepath.IsAbs(d) {
			d = filepath.Join(filepath.Dir(makefile), d)
		}
		d = filepath.Clean(d)
		if !seen[d] && isDir(d) {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	return dirs
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// Uses reports the uses: names referenced inside a pipeline definition.
func (d *Def) Uses() []string {
	var out []string
	for _, s := range d.Model.Steps {
		if s.Uses != nil {
			out = append(out, s.Uses.Value)
		}
	}
	return out
}

// Summary is the first line of the pipeline's title, for completion lists.
func (d *Def) Summary() string {
	if d.Title != "" {
		return strings.TrimSpace(strings.SplitN(d.Title, "\n", 2)[0])
	}
	return d.Name
}

// decodeStrict is used for pipeline file diagnostics.
func decodeStrict(text string, out any) error {
	dec := yaml.NewDecoder(strings.NewReader(text))
	dec.KnownFields(true)
	return dec.Decode(out)
}

// DecodeStrict decodes a pipeline file rejecting unknown fields. melange
// itself ignores unknown fields in pipeline files, so callers report
// these as warnings.
func DecodeStrict(text string, out any) error { return decodeStrict(text, out) }

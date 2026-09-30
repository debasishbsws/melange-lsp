package lsp

import (
	"bufio"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"melange-lsp/internal/document"

	"go.lsp.dev/protocol"
)

// workspaceIndex lists the packages and subpackages defined under the
// workspace root, for workspace/symbol and dependency completion. It is
// built in the background and refreshed when older than a minute.
type workspaceIndex struct {
	root string
	// ready is closed when the first build finishes.
	ready     chan struct{}
	readyOnce sync.Once

	mu       sync.Mutex
	built    time.Time
	building bool
	pkgs     []indexedPackage
	// files are the melange package configurations found.
	files []string
}

type indexedPackage struct {
	Name    string
	Version string
	File    string
	Line    int
	// Parent is the main package for subpackages, "" for main packages.
	Parent string
}

func newWorkspaceIndex(root string) *workspaceIndex {
	w := &workspaceIndex{root: root, ready: make(chan struct{})}
	go w.build()
	return w
}

// wait blocks until the first build has finished, so early requests get
// complete answers rather than empty ones.
func (w *workspaceIndex) wait() {
	if w.ready == nil {
		w.mu.Lock()
		never := w.built.IsZero()
		w.mu.Unlock()
		if never {
			w.build()
		}
		return
	}
	select {
	case <-w.ready:
	case <-time.After(30 * time.Second):
	}
}

// packages returns the current index, starting a refresh if it is stale.
func (w *workspaceIndex) packages() []indexedPackage {
	if w == nil {
		return nil
	}
	w.wait()
	w.mu.Lock()
	stale := time.Since(w.built) > time.Minute && !w.building
	pkgs := w.pkgs
	w.mu.Unlock()
	if stale {
		go w.build()
	}
	return pkgs
}

// skipDirs are never melange package directories.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "pipelines": true,
	"testdata": true, ".github": true, "charts": true,
}

func (w *workspaceIndex) build() {
	w.mu.Lock()
	if w.building {
		w.mu.Unlock()
		return
	}
	w.building = true
	w.mu.Unlock()

	var pkgs []indexedPackage
	var files []string
	_ = filepath.WalkDir(w.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(w.root, p)
			if p != w.root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") || strings.Count(rel, string(filepath.Separator)) >= 3) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml") {
			if found := scanPackageFile(p); len(found) > 0 {
				pkgs = append(pkgs, found...)
				files = append(files, p)
			}
		}
		return nil
	})
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })

	w.mu.Lock()
	w.pkgs, w.files, w.built, w.building = pkgs, files, time.Now(), false
	w.mu.Unlock()
	if w.ready != nil {
		w.readyOnce.Do(func() { close(w.ready) })
	}
}

var (
	indexName       = regexp.MustCompile(`^  name:\s*["']?([^"'#\s]+)`)
	indexVersion    = regexp.MustCompile(`^  version:\s*["']?([^"'#\s]+)`)
	indexSubpackage = regexp.MustCompile(`^\s*- name:\s*["']?([^"'#\s]+)`)
)

// scanPackageFile extracts package and subpackage names without a full
// YAML parse, so indexing thousands of files stays fast.
func scanPackageFile(path string) []indexedPackage {
	f, err := os.Open(path) // #nosec G304 -- files under the workspace root
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []indexedPackage
	var main *indexedPackage
	section := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for line := 0; sc.Scan(); line++ {
		t := sc.Text()
		if t != "" && t[0] != ' ' && t[0] != '#' && t[0] != '-' {
			section = strings.TrimSuffix(strings.Fields(t)[0], ":")
			if line == 0 && section != "package" && !strings.HasPrefix(t, "package:") {
				// Melange configs start with package:; skip other YAML fast.
				if !strings.HasPrefix(t, "#") {
					return nil
				}
			}
			continue
		}
		switch section {
		case "package":
			if m := indexName.FindStringSubmatch(t); m != nil && main == nil {
				out = append(out, indexedPackage{Name: m[1], File: path, Line: line})
				main = &out[len(out)-1]
			} else if m := indexVersion.FindStringSubmatch(t); m != nil && main != nil {
				main.Version = m[1]
			}
		case "subpackages":
			if m := indexSubpackage.FindStringSubmatch(t); m != nil && main != nil && !strings.Contains(m[1], "${{") {
				out = append(out, indexedPackage{Name: m[1], File: path, Line: line, Parent: main.Name, Version: main.Version})
			}
		}
	}
	if main == nil {
		return nil
	}
	return out
}

// workspaceSymbols answers workspace/symbol with packages, subpackages, and
// pipelines whose names contain query.
func (s *Server) workspaceSymbols(_ context.Context, query string) []protocol.SymbolInformation {
	s.mu.Lock()
	idx, root := s.workspace, s.root
	s.mu.Unlock()

	q := strings.ToLower(query)
	out := []protocol.SymbolInformation{}
	for _, p := range idx.packages() {
		if q != "" && !strings.Contains(strings.ToLower(p.Name), q) {
			continue
		}
		kind, container := protocol.SymbolKindPackage, p.Version
		if p.Parent != "" {
			kind, container = protocol.SymbolKindModule, "subpackage of "+p.Parent
		}
		out = append(out, protocol.SymbolInformation{
			Name:          p.Name,
			Kind:          kind,
			ContainerName: container,
			Location: protocol.Location{
				URI:   document.URI(p.File),
				Range: protocol.Range{Start: protocol.Position{Line: uint32(p.Line)}, End: protocol.Position{Line: uint32(p.Line)}},
			},
		})
		if len(out) >= 500 {
			return out
		}
	}

	// Pipelines visible from the workspace root.
	for _, def := range s.registry.List(discoverFor(root)) {
		if q != "" && !strings.Contains(strings.ToLower(def.Name), q) {
			continue
		}
		container := "pipeline"
		if def.Builtin {
			container = "melange built-in pipeline"
		}
		out = append(out, protocol.SymbolInformation{
			Name: def.Name, Kind: protocol.SymbolKindFunction, ContainerName: container,
			Location: def.Location(),
		})
	}
	return out
}

var (
	discoverMu    sync.Mutex
	discoverCache = map[string][]string{}
)

// discoverFor lists pipeline directories under a workspace root: root's
// own pipelines/ and those one level down (stereo's os/pipelines,
// enterprise-packages/pipelines, ...). Results are cached per root.
func discoverFor(root string) []string {
	if root == "" {
		return nil
	}
	discoverMu.Lock()
	defer discoverMu.Unlock()
	if dirs, ok := discoverCache[root]; ok {
		return dirs
	}
	var dirs []string
	for _, pattern := range []string{"pipelines", "*/pipelines"} {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil && fi.IsDir() {
				dirs = append(dirs, m)
			}
		}
	}
	discoverCache[root] = dirs
	return dirs
}

// configFiles returns the indexed package configurations, building the
// index synchronously if it has never been built.
func (w *workspaceIndex) configFiles() []string {
	if w == nil {
		return nil
	}
	w.wait()
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.files
}

// grep returns every line in the workspace's package configurations (and
// pipeline files under dirs) matching re, as locations of the first group.
func (w *workspaceIndex) grep(re *regexp.Regexp, dirs []string) []protocol.Location {
	files := append([]string(nil), w.configFiles()...)
	for _, d := range dirs {
		_ = filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err == nil && !e.IsDir() && strings.HasSuffix(p, ".yaml") {
				files = append(files, p)
			}
			return nil
		})
	}
	seen := map[string]bool{}
	var unique []string
	for _, f := range files {
		if !seen[f] {
			seen[f] = true
			unique = append(unique, f)
		}
	}

	// Read in parallel; only matching files are split into lines.
	results := make([][]protocol.Location, len(unique))
	var wg sync.WaitGroup
	work := make(chan int)
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				results[i] = grepFile(unique[i], re)
			}
		}()
	}
	for i := range unique {
		work <- i
	}
	close(work)
	wg.Wait()

	var out []protocol.Location
	for _, r := range results {
		out = append(out, r...)
	}
	return out
}

// usesOf finds every step in the workspace that uses the named pipeline.
func (s *Server) usesOf(name string) []protocol.Location {
	s.mu.Lock()
	idx, root := s.workspace, s.root
	s.mu.Unlock()
	re := regexp.MustCompile(`(?m)^\s*(?:- )?uses:\s*(` + regexp.QuoteMeta(name) + `)\s*(?:#.*)?$`)
	return idx.grep(re, discoverFor(root))
}

// dependentsOf finds every list item in the workspace naming pkg, which
// covers runtime dependencies and build and test environments.
func (s *Server) dependentsOf(pkg string) []protocol.Location {
	s.mu.Lock()
	idx := s.workspace
	s.mu.Unlock()
	re := regexp.MustCompile(`(?m)^\s*- ["']?(` + regexp.QuoteMeta(pkg) + `)["']?\s*(?:#.*)?$`)
	return idx.grep(re, nil)
}

func grepFile(f string, re *regexp.Regexp) []protocol.Location {
	data, err := os.ReadFile(f) // #nosec G304 -- workspace files
	if err != nil || !re.Match(data) {
		return nil
	}
	uri := document.URI(f)
	var out []protocol.Location
	for i, l := range strings.Split(string(data), "\n") {
		m := re.FindStringSubmatchIndex(l)
		if m == nil {
			continue
		}
		pos := func(b int) protocol.Position {
			return protocol.Position{Line: uint32(i), Character: uint32(document.UTF16Col(l, utf8.RuneCountInString(l[:b])))}
		}
		out = append(out, protocol.Location{URI: uri, Range: protocol.Range{Start: pos(m[2]), End: pos(m[3])}})
	}
	return out
}

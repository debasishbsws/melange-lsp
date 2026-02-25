package pipeline

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// InputInfo describes a single pipeline input parameter.
type InputInfo struct {
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
	Default     string `yaml:"default"`
}

// PipelineInfo describes a parsed pipeline YAML file.
type PipelineInfo struct {
	Name   string               `yaml:"name"`
	Inputs map[string]InputInfo `yaml:"inputs"`

	// Raw is the full YAML source of the pipeline file.
	Raw string `yaml:"-"`
}

// Registry holds the embedded (built-in) pipeline filesystem and a cache for
// parsed embedded pipelines. On-disk pipelines are resolved per-request via
// dirs passed to each method.
type Registry struct {
	mu sync.Mutex

	// embeddedFS is the embedded filesystem (e.g. melange's PipelinesFS).
	embeddedFS fs.FS
	// embeddedPrefix is the path prefix within embeddedFS (e.g. "pipelines").
	embeddedPrefix string

	// embeddedDiskDir is the on-disk path to the embedded pipelines in the Go
	// module cache, used for go-to-definition on built-in pipelines.
	embeddedDiskDir string

	// cache stores parsed embedded pipelines by uses-name.
	cache map[string]*PipelineInfo
}

// NewRegistry creates a registry backed by an embedded filesystem.
// modulePath is the Go module path (e.g. "chainguard.dev/melange") used to
// locate the pipelines on disk in the module cache for go-to-definition.
func NewRegistry(embeddedFS fs.FS, prefix string) *Registry {
	return &Registry{
		embeddedFS:      embeddedFS,
		embeddedPrefix:  prefix,
		embeddedDiskDir: findEmbeddedDiskDir(),
		cache:           make(map[string]*PipelineInfo),
	}
}

// findEmbeddedDiskDir uses runtime build info to locate the melange module's
// pipelines directory in the Go module cache.
func findEmbeddedDiskDir() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, dep := range info.Deps {
		if dep.Path == "chainguard.dev/melange" {
			gopath := os.Getenv("GOPATH")
			if gopath == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return ""
				}
				gopath = filepath.Join(home, "go")
			}
			dir := filepath.Join(gopath, "pkg", "mod", dep.Path+"@"+dep.Version, "pkg", "build", "pipelines")
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				return dir
			}
		}
	}
	return ""
}

// ResolvePipelinePath returns the absolute filesystem path for a pipeline YAML
// file. It searches the given dirs first, then falls back to the embedded
// pipelines' location in the Go module cache (for go-to-definition on built-ins).
func (r *Registry) ResolvePipelinePath(name string, dirs []string) (string, error) {
	filename := name + ".yaml"
	for _, dir := range dirs {
		path := filepath.Join(dir, filename)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	// Fallback: check the Go module cache for embedded pipelines
	if r.embeddedDiskDir != "" {
		path := filepath.Join(r.embeddedDiskDir, filename)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("pipeline %q not found on disk", name)
}

// LoadPipeline finds and parses a pipeline by its uses-name (e.g. "go/build").
// It searches the given dirs first, then falls back to the embedded FS.
func (r *Registry) LoadPipeline(name string, dirs []string) (*PipelineInfo, error) {
	// Try on-disk dirs first (not cached — scoped per request)
	filename := name + ".yaml"
	for _, dir := range dirs {
		path := filepath.Join(dir, filename)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var info PipelineInfo
		if err := yaml.Unmarshal(data, &info); err != nil {
			return nil, fmt.Errorf("parsing pipeline %s: %w", path, err)
		}
		info.Raw = string(data)
		return &info, nil
	}

	// Fallback: embedded FS (cached)
	if r.embeddedFS != nil {
		r.mu.Lock()
		if info, ok := r.cache[name]; ok {
			r.mu.Unlock()
			return info, nil
		}
		r.mu.Unlock()

		embeddedPath := r.embeddedPrefix + "/" + name + ".yaml"
		data, err := fs.ReadFile(r.embeddedFS, embeddedPath)
		if err == nil {
			var info PipelineInfo
			if err := yaml.Unmarshal(data, &info); err != nil {
				return nil, fmt.Errorf("parsing embedded pipeline %s: %w", name, err)
			}
			info.Raw = string(data)
			r.mu.Lock()
			r.cache[name] = &info
			r.mu.Unlock()
			return &info, nil
		}
	}

	return nil, fmt.Errorf("pipeline %q not found", name)
}

// ListPipelines returns pipeline uses-names from the given dirs and the
// embedded FS combined.
func (r *Registry) ListPipelines(dirs []string) []string {
	seen := make(map[string]bool)
	var names []string

	for _, dir := range dirs {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() || !strings.HasSuffix(info.Name(), ".yaml") {
				return nil
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return nil
			}
			name := strings.TrimSuffix(rel, ".yaml")
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
			return nil
		})
		if err != nil {
			continue
		}
	}

	if r.embeddedFS != nil {
		fs.WalkDir(r.embeddedFS, r.embeddedPrefix, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".yaml") {
				return nil
			}
			rel, err := filepath.Rel(r.embeddedPrefix, path)
			if err != nil {
				return nil
			}
			name := strings.TrimSuffix(rel, ".yaml")
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
			return nil
		})
	}

	return names
}

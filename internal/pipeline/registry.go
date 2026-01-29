package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
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
}

// Registry discovers and caches pipeline definitions from configured directories.
type Registry struct {
	mu   sync.Mutex
	dirs []string

	// cache stores parsed pipelines by uses-name (e.g. "go/build")
	cache map[string]*PipelineInfo
}

// NewRegistry creates a registry that searches the given directories in order.
func NewRegistry(dirs []string) *Registry {
	return &Registry{
		dirs:  dirs,
		cache: make(map[string]*PipelineInfo),
	}
}

// LoadPipeline finds and parses a pipeline by its uses-name (e.g. "go/build").
// It searches directories in order; first match wins.
func (r *Registry) LoadPipeline(name string) (*PipelineInfo, error) {
	r.mu.Lock()
	if info, ok := r.cache[name]; ok {
		r.mu.Unlock()
		return info, nil
	}
	r.mu.Unlock()

	filename := name + ".yaml"
	for _, dir := range r.dirs {
		path := filepath.Join(dir, filename)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var info PipelineInfo
		if err := yaml.Unmarshal(data, &info); err != nil {
			return nil, fmt.Errorf("parsing pipeline %s: %w", path, err)
		}

		r.mu.Lock()
		r.cache[name] = &info
		r.mu.Unlock()
		return &info, nil
	}

	return nil, fmt.Errorf("pipeline %q not found", name)
}

// ListPipelines scans all directories for *.yaml files and returns their
// uses-names (e.g. "go/build" for go/build.yaml).
func (r *Registry) ListPipelines() []string {
	seen := make(map[string]bool)
	var names []string

	for _, dir := range r.dirs {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip inaccessible paths
			}
			if info.IsDir() || !strings.HasSuffix(info.Name(), ".yaml") {
				return nil
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return nil
			}
			// Strip .yaml suffix to get uses-name
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

	return names
}

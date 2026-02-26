package schema

import (
	"testing"
)

func findItem(items []CompletionItem, label string) *CompletionItem {
	for _, item := range items {
		if item.Label == label {
			return &item
		}
	}
	return nil
}

func TestTopLevelCompletions(t *testing.T) {
	items := GetCompletionsForPath(nil)
	if len(items) == 0 {
		t.Fatal("expected top-level completions, got none")
	}

	expectedKeys := []string{"package", "pipeline", "subpackages", "environment", "update", "vars", "test"}
	for _, key := range expectedKeys {
		if findItem(items, key) == nil {
			t.Errorf("expected top-level key %q not found", key)
		}
	}
}

func TestPackageCompletions(t *testing.T) {
	items := GetCompletionsForPath([]string{"package"})
	if len(items) == 0 {
		t.Fatal("expected package completions, got none")
	}

	name := findItem(items, "name")
	if name == nil {
		t.Fatal("expected 'name' in package completions")
	}
	if name.Description == "" {
		t.Error("expected description for 'name', got empty string")
	}
	if !name.Required {
		t.Error("expected 'name' to be required")
	}

	version := findItem(items, "version")
	if version == nil {
		t.Fatal("expected 'version' in package completions")
	}
	if version.Description == "" {
		t.Error("expected description for 'version'")
	}

	epoch := findItem(items, "epoch")
	if epoch == nil {
		t.Fatal("expected 'epoch' in package completions")
	}
	if !epoch.Required {
		t.Error("expected 'epoch' to be required")
	}
}

func TestPipelineCompletions(t *testing.T) {
	items := GetCompletionsForPath([]string{"pipeline", "0"})
	if len(items) == 0 {
		t.Fatal("expected pipeline completions, got none")
	}

	expectedKeys := []string{"uses", "runs", "with", "name", "pipeline", "working-directory"}
	for _, key := range expectedKeys {
		if findItem(items, key) == nil {
			t.Errorf("expected pipeline key %q not found", key)
		}
	}

	runs := findItem(items, "runs")
	if runs == nil {
		t.Fatal("expected 'runs' in pipeline completions")
	}
	if runs.Description == "" {
		t.Error("expected description for 'runs'")
	}
}

func TestRequiredFields(t *testing.T) {
	// Configuration: "package" is required
	topLevel := GetCompletionsForPath(nil)
	pkg := findItem(topLevel, "package")
	if pkg == nil {
		t.Fatal("expected 'package' in top-level")
	}
	if !pkg.Required {
		t.Error("expected 'package' to be required at top level")
	}

	// "pipeline" at top level is not in required array
	pipeline := findItem(topLevel, "pipeline")
	if pipeline != nil && pipeline.Required {
		t.Error("expected 'pipeline' to not be required at top level")
	}

	// Package: name, version, epoch are required
	pkgItems := GetCompletionsForPath([]string{"package"})
	for _, key := range []string{"name", "version", "epoch"} {
		item := findItem(pkgItems, key)
		if item == nil {
			t.Errorf("expected %q in package completions", key)
			continue
		}
		if !item.Required {
			t.Errorf("expected %q to be required in package", key)
		}
	}

	// Package: "description" is not required
	desc := findItem(pkgItems, "description")
	if desc != nil && desc.Required {
		t.Error("expected 'description' to not be required in package")
	}
}

func TestDeepNesting(t *testing.T) {
	tests := []struct {
		name     string
		path     []string
		wantKeys []string
	}{
		{
			name:     "update properties",
			path:     []string{"update"},
			wantKeys: []string{"enabled", "github", "release-monitor", "git"},
		},
		{
			name:     "environment (ImageConfiguration)",
			path:     []string{"environment"},
			wantKeys: []string{"contents", "accounts", "archs"},
		},
		{
			name:     "package copyright item",
			path:     []string{"package", "copyright", "0"},
			wantKeys: []string{"license", "paths", "attestation"},
		},
		{
			name:     "package dependencies",
			path:     []string{"package", "dependencies"},
			wantKeys: []string{"runtime", "provides", "replaces"},
		},
		{
			name:     "pipeline assertions",
			path:     []string{"pipeline", "0", "assertions"},
			wantKeys: []string{"required-steps"},
		},
		{
			name:     "subpackage item",
			path:     []string{"subpackages", "0"},
			wantKeys: []string{"name", "pipeline", "dependencies"},
		},
		{
			name:     "test properties",
			path:     []string{"test"},
			wantKeys: []string{"environment", "pipeline"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := GetCompletionsForPath(tt.path)
			if len(items) == 0 {
				t.Fatalf("expected completions for path %v, got none", tt.path)
			}
			for _, key := range tt.wantKeys {
				if findItem(items, key) == nil {
					t.Errorf("expected key %q in completions for path %v", key, tt.path)
				}
			}
		})
	}
}

func TestUnknownPath(t *testing.T) {
	items := GetCompletionsForPath([]string{"nonexistent"})
	if len(items) != 0 {
		t.Errorf("expected empty completions for unknown path, got %d items", len(items))
	}

	items = GetCompletionsForPath([]string{"package", "nonexistent"})
	if len(items) != 0 {
		t.Errorf("expected empty completions for unknown nested path, got %d items", len(items))
	}
}

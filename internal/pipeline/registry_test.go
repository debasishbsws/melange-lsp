package pipeline

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func setupTestPipelines(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Create go/build.yaml
	goDir := filepath.Join(dir, "go")
	if err := os.MkdirAll(goDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goDir, "build.yaml"), []byte(`
name: Go Build
inputs:
  packages:
    description: "Go packages to build"
    required: true
  output:
    description: "Output binary name"
    required: false
    default: "app"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create fetch.yaml at top level
	if err := os.WriteFile(filepath.Join(dir, "fetch.yaml"), []byte(`
name: Fetch source
inputs:
  uri:
    description: "URI to fetch"
    required: true
`), 0o644); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestListPipelines(t *testing.T) {
	dir := setupTestPipelines(t)
	r := NewRegistry([]string{dir})

	names := r.ListPipelines()
	sort.Strings(names)

	want := []string{"fetch", "go/build"}
	if len(names) != len(want) {
		t.Fatalf("ListPipelines() = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("ListPipelines()[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

func TestLoadPipeline(t *testing.T) {
	dir := setupTestPipelines(t)
	r := NewRegistry([]string{dir})

	info, err := r.LoadPipeline("go/build")
	if err != nil {
		t.Fatalf("LoadPipeline(go/build) error: %v", err)
	}
	if info.Name != "Go Build" {
		t.Errorf("Name = %q, want %q", info.Name, "Go Build")
	}
	if len(info.Inputs) != 2 {
		t.Fatalf("Inputs count = %d, want 2", len(info.Inputs))
	}
	pkg := info.Inputs["packages"]
	if !pkg.Required {
		t.Error("packages input should be required")
	}
	out := info.Inputs["output"]
	if out.Required {
		t.Error("output input should not be required")
	}
	if out.Default != "app" {
		t.Errorf("output default = %q, want %q", out.Default, "app")
	}
}

func TestLoadPipelineNotFound(t *testing.T) {
	r := NewRegistry([]string{t.TempDir()})
	_, err := r.LoadPipeline("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing pipeline")
	}
}

func TestLoadPipelineCaching(t *testing.T) {
	dir := setupTestPipelines(t)
	r := NewRegistry([]string{dir})

	info1, _ := r.LoadPipeline("fetch")
	info2, _ := r.LoadPipeline("fetch")
	if info1 != info2 {
		t.Error("expected cached pointer to be returned")
	}
}

func TestMultipleDirsFirstWins(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	// Write different content in each dir
	os.WriteFile(filepath.Join(dir1, "test.yaml"), []byte("name: from-dir1\n"), 0o644)
	os.WriteFile(filepath.Join(dir2, "test.yaml"), []byte("name: from-dir2\n"), 0o644)

	r := NewRegistry([]string{dir1, dir2})
	info, err := r.LoadPipeline("test")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "from-dir1" {
		t.Errorf("Name = %q, want %q (first dir should win)", info.Name, "from-dir1")
	}
}

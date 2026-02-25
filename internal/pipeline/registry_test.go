package pipeline

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"testing/fstest"
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
	r := NewRegistry(nil, "")

	names := r.ListPipelines([]string{dir})
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

func TestLoadPipelineFromDisk(t *testing.T) {
	dir := setupTestPipelines(t)
	r := NewRegistry(nil, "")

	info, err := r.LoadPipeline("go/build", []string{dir})
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

func TestLoadPipelineFromEmbedded(t *testing.T) {
	embedded := fstest.MapFS{
		"pipelines/git-checkout.yaml": &fstest.MapFile{
			Data: []byte("name: Git Checkout\ninputs:\n  repository:\n    description: \"Repo URL\"\n    required: true\n"),
		},
	}
	r := NewRegistry(embedded, "pipelines")

	info, err := r.LoadPipeline("git-checkout", nil)
	if err != nil {
		t.Fatalf("LoadPipeline(git-checkout) error: %v", err)
	}
	if info.Name != "Git Checkout" {
		t.Errorf("Name = %q, want %q", info.Name, "Git Checkout")
	}
	if _, ok := info.Inputs["repository"]; !ok {
		t.Error("expected 'repository' input")
	}
}

func TestLoadPipelineDiskOverridesEmbedded(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "fetch.yaml"), []byte("name: local-fetch\n"), 0o644)

	embedded := fstest.MapFS{
		"pipelines/fetch.yaml": &fstest.MapFile{
			Data: []byte("name: embedded-fetch\n"),
		},
	}
	r := NewRegistry(embedded, "pipelines")

	info, err := r.LoadPipeline("fetch", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "local-fetch" {
		t.Errorf("Name = %q, want %q (disk should override embedded)", info.Name, "local-fetch")
	}
}

func TestLoadPipelineNotFound(t *testing.T) {
	r := NewRegistry(nil, "")
	_, err := r.LoadPipeline("nonexistent", []string{t.TempDir()})
	if err == nil {
		t.Fatal("expected error for missing pipeline")
	}
}

func TestLoadPipelineEmbeddedCaching(t *testing.T) {
	embedded := fstest.MapFS{
		"p/test.yaml": &fstest.MapFile{Data: []byte("name: cached\n")},
	}
	r := NewRegistry(embedded, "p")

	info1, _ := r.LoadPipeline("test", nil)
	info2, _ := r.LoadPipeline("test", nil)
	if info1 != info2 {
		t.Error("expected cached pointer to be returned for embedded pipeline")
	}
}

func TestResolvePipelinePath(t *testing.T) {
	dir := setupTestPipelines(t)
	r := NewRegistry(nil, "")

	path, err := r.ResolvePipelinePath("go/build", []string{dir})
	if err != nil {
		t.Fatalf("ResolvePipelinePath(go/build) error: %v", err)
	}
	want := filepath.Join(dir, "go", "build.yaml")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

func TestResolvePipelinePathNotFound(t *testing.T) {
	r := NewRegistry(nil, "")
	_, err := r.ResolvePipelinePath("nonexistent", []string{t.TempDir()})
	if err == nil {
		t.Fatal("expected error for missing pipeline")
	}
}

func TestListPipelinesIncludesEmbedded(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "local.yaml"), []byte("name: local\n"), 0o644)

	embedded := fstest.MapFS{
		"p/builtin.yaml": &fstest.MapFile{Data: []byte("name: builtin\n")},
	}
	r := NewRegistry(embedded, "p")

	names := r.ListPipelines([]string{dir})
	sort.Strings(names)

	want := []string{"builtin", "local"}
	if len(names) != len(want) {
		t.Fatalf("ListPipelines() = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("ListPipelines()[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

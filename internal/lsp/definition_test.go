package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"melange-lsp/internal/pipeline"

	"go.lsp.dev/protocol"
)

func setupTestRegistry(t *testing.T) (reg *pipeline.Registry, dirs []string, pipelinesDir string) {
	t.Helper()
	dir := t.TempDir()

	goDir := filepath.Join(dir, "go")
	if err := os.MkdirAll(goDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goDir, "build.yaml"), []byte(`name: Go Build
inputs:
  packages:
    description: "Go packages to build"
    required: true
  output:
    description: "Output binary name"
    required: false
    default: "app"
pipeline:
  - runs: |
      go build -o ${{inputs.output}} ${{inputs.packages}}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "fetch.yaml"), []byte(`name: Fetch source
inputs:
  uri:
    description: "URI to fetch"
    required: true
  expected-sha256:
    description: "Expected SHA256 hash"
    required: false
pipeline:
  - runs: |
      wget ${{inputs.uri}}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// A pipeline that uses ${{context.name}} (like split/bin)
	splitDir := filepath.Join(dir, "split")
	if err := os.MkdirAll(splitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(splitDir, "bin.yaml"), []byte(`name: Split bin
pipeline:
  - runs: |
      pkg="${{context.name}}"
      echo $pkg
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// A metapackage-like pipeline that recursively uses other pipelines
	testDir := filepath.Join(dir, "test")
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "metapackage.yaml"), []byte(`name: Meta Package
pipeline:
  - uses: split/bin
  - runs: echo done
`), 0o644); err != nil {
		t.Fatal(err)
	}

	return pipeline.NewRegistry(nil, ""), []string{dir}, dir
}

func TestDefinitionUses(t *testing.T) {
	registry, dirs, pipelinesDir := setupTestRegistry(t)

	content := `pipeline:
  - uses: go/build
    with:
      packages: ./cmd/app
`
	pos := protocol.Position{Line: 1, Character: 12}
	locs := getDefinition(content, pos, registry, dirs)
	if len(locs) == 0 {
		t.Fatal("expected definition location, got none")
	}

	wantPath := filepath.Join(pipelinesDir, "go", "build.yaml")
	wantURI := "file://" + wantPath
	if string(locs[0].URI) != wantURI {
		t.Errorf("URI = %q, want %q", locs[0].URI, wantURI)
	}
	if locs[0].Range.Start.Line != 0 {
		t.Errorf("expected line 0, got %d", locs[0].Range.Start.Line)
	}
}

func TestDefinitionUsesWithListPrefix(t *testing.T) {
	registry, dirs, pipelinesDir := setupTestRegistry(t)

	content := `pipeline:
  - uses: fetch
    with:
      uri: https://example.com
`
	pos := protocol.Position{Line: 1, Character: 12}
	locs := getDefinition(content, pos, registry, dirs)
	if len(locs) == 0 {
		t.Fatal("expected definition location, got none")
	}

	wantPath := filepath.Join(pipelinesDir, "fetch.yaml")
	wantURI := "file://" + wantPath
	if string(locs[0].URI) != wantURI {
		t.Errorf("URI = %q, want %q", locs[0].URI, wantURI)
	}
}

func TestDefinitionWithKey(t *testing.T) {
	registry, dirs, pipelinesDir := setupTestRegistry(t)

	content := `pipeline:
  - uses: go/build
    with:
      packages: ./cmd/app
      output: mybin
`
	// Cursor on "packages" key
	pos := protocol.Position{Line: 3, Character: 8}
	locs := getDefinition(content, pos, registry, dirs)
	if len(locs) == 0 {
		t.Fatal("expected definition location for with key, got none")
	}

	wantPath := filepath.Join(pipelinesDir, "go", "build.yaml")
	wantURI := "file://" + wantPath
	if string(locs[0].URI) != wantURI {
		t.Errorf("URI = %q, want %q", locs[0].URI, wantURI)
	}

	// The "packages:" input should be at line 2 in the pipeline file
	if locs[0].Range.Start.Line != 2 {
		t.Errorf("expected line 2 for 'packages' input, got %d", locs[0].Range.Start.Line)
	}
}

func TestDefinitionWithKeyOutput(t *testing.T) {
	registry, dirs, pipelinesDir := setupTestRegistry(t)

	content := `pipeline:
  - uses: go/build
    with:
      output: mybin
`
	// Cursor on "output" key
	pos := protocol.Position{Line: 3, Character: 8}
	locs := getDefinition(content, pos, registry, dirs)
	if len(locs) == 0 {
		t.Fatal("expected definition location for 'output' key, got none")
	}

	wantPath := filepath.Join(pipelinesDir, "go", "build.yaml")
	wantURI := "file://" + wantPath
	if string(locs[0].URI) != wantURI {
		t.Errorf("URI = %q, want %q", locs[0].URI, wantURI)
	}

	// The "output:" input should be at line 5 in the pipeline file
	if locs[0].Range.Start.Line != 5 {
		t.Errorf("expected line 5 for 'output' input, got %d", locs[0].Range.Start.Line)
	}
}

func TestDefinitionSchemaFallback(t *testing.T) {
	registry, dirs, _ := setupTestRegistry(t)

	content := `package:
  name: test
  version: 1.0.0
`
	// Cursor on "name" key under package — should jump to schema.json
	pos := protocol.Position{Line: 1, Character: 4}
	locs := getDefinition(content, pos, registry, dirs)
	if len(locs) == 0 {
		t.Fatal("expected schema definition for 'name' under package, got none")
	}
	if !strings.Contains(string(locs[0].URI), "schema.json") {
		t.Errorf("expected schema.json URI, got %q", locs[0].URI)
	}
}

func TestDefinitionSchemaTopLevel(t *testing.T) {
	content := `package:
  name: test
pipeline:
  - runs: echo hi
`
	// Cursor on "package:" at top level — should jump to schema.json
	pos := protocol.Position{Line: 0, Character: 2}
	locs := getDefinition(content, pos, nil, nil)
	if len(locs) == 0 {
		t.Fatal("expected schema definition for top-level 'package', got none")
	}
	if !strings.Contains(string(locs[0].URI), "schema.json") {
		t.Errorf("expected schema.json URI, got %q", locs[0].URI)
	}
}

func TestDefinitionNilRegistry(t *testing.T) {
	content := `pipeline:
  - uses: go/build
`
	// Cursor on "uses:" value — with nil registry, pipeline definition won't work
	// but schema fallback should fire since cursor is on the "uses" key
	pos := protocol.Position{Line: 1, Character: 6}
	locs := getDefinition(content, pos, nil, nil)
	// "uses" is a property of Pipeline, so schema fallback should work
	if len(locs) == 0 {
		t.Fatal("expected schema fallback for 'uses' key with nil registry, got none")
	}
	if !strings.Contains(string(locs[0].URI), "schema.json") {
		t.Errorf("expected schema.json URI, got %q", locs[0].URI)
	}
}

func TestDefinitionUnknownPipeline(t *testing.T) {
	registry, dirs, _ := setupTestRegistry(t)

	content := `pipeline:
  - uses: nonexistent/pipeline
`
	pos := protocol.Position{Line: 1, Character: 12}
	locs := getDefinition(content, pos, registry, dirs)
	if len(locs) != 0 {
		t.Errorf("expected no definition for unknown pipeline, got %d", len(locs))
	}
}

func TestDefinitionFromPipelineFile(t *testing.T) {
	registry, _, pipelinesDir := setupTestRegistry(t)

	// Simulate a pipeline file that uses another pipeline.
	// The file is "inside" the pipelines dir, so pipelineDirsForURI should
	// find pipelinesDir as an ancestor named "pipelines".
	// Here we test getDefinition directly with dirs = [pipelinesDir].
	content := `name: Meta Pipeline
pipeline:
  - uses: go/build
    with:
      packages: ./cmd/app
  - uses: fetch
    with:
      uri: https://example.com
`
	// Cursor on "uses: go/build" (line 2)
	pos := protocol.Position{Line: 2, Character: 12}
	locs := getDefinition(content, pos, registry, []string{pipelinesDir})
	if len(locs) == 0 {
		t.Fatal("expected definition from within pipeline file, got none")
	}

	wantPath := filepath.Join(pipelinesDir, "go", "build.yaml")
	wantURI := "file://" + wantPath
	if string(locs[0].URI) != wantURI {
		t.Errorf("URI = %q, want %q", locs[0].URI, wantURI)
	}

	// Also test "uses: fetch" (line 5)
	pos = protocol.Position{Line: 5, Character: 12}
	locs = getDefinition(content, pos, registry, []string{pipelinesDir})
	if len(locs) == 0 {
		t.Fatal("expected definition for 'fetch' from within pipeline file, got none")
	}

	wantPath = filepath.Join(pipelinesDir, "fetch.yaml")
	wantURI = "file://" + wantPath
	if string(locs[0].URI) != wantURI {
		t.Errorf("URI = %q, want %q", locs[0].URI, wantURI)
	}
}

func TestPipelineDirsForURI(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		wantDirs []string
	}{
		{
			name:     "melange config file",
			uri:      "file:///project/mypackage.yaml",
			wantDirs: []string{"/project/pipelines"},
		},
		{
			name:     "pipeline file in pipelines dir",
			uri:      "file:///project/pipelines/test/metapackage.yaml",
			wantDirs: []string{"/project/pipelines/test/pipelines", "/project/pipelines"},
		},
		{
			name:     "pipeline file at pipelines root",
			uri:      "file:///project/pipelines/fetch.yaml",
			wantDirs: []string{"/project/pipelines/pipelines", "/project/pipelines"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dirs := pipelineDirsForURI(protocol.DocumentURI(tt.uri))
			if len(dirs) != len(tt.wantDirs) {
				t.Fatalf("got %d dirs %v, want %d dirs %v", len(dirs), dirs, len(tt.wantDirs), tt.wantDirs)
			}
			for i, want := range tt.wantDirs {
				if dirs[i] != want {
					t.Errorf("dirs[%d] = %q, want %q", i, dirs[i], want)
				}
			}
		})
	}
}

func TestHoverWithKey(t *testing.T) {
	registry, dirs, _ := setupTestRegistry(t)

	content := `pipeline:
  - uses: go/build
    with:
      packages: ./cmd/app
      output: mybin
`
	// Cursor on "packages" key (line 3)
	pos := protocol.Position{Line: 3, Character: 8}
	hover := getHover(content, pos, registry, dirs)
	if hover == nil {
		t.Fatal("expected hover result for with key, got nil")
	}

	val := hover.Contents.Value
	if !strings.Contains(val, "packages") {
		t.Errorf("expected key name in hover, got: %s", val)
	}
	if !strings.Contains(val, "Go packages to build") {
		t.Errorf("expected input description in hover, got: %s", val)
	}
	if !strings.Contains(val, "Required") {
		t.Errorf("expected Required in hover, got: %s", val)
	}
}

func TestHoverWithKeyOptionalDefault(t *testing.T) {
	registry, dirs, _ := setupTestRegistry(t)

	content := `pipeline:
  - uses: go/build
    with:
      output: mybin
`
	// Cursor on "output" key (line 3)
	pos := protocol.Position{Line: 3, Character: 8}
	hover := getHover(content, pos, registry, dirs)
	if hover == nil {
		t.Fatal("expected hover result for with key, got nil")
	}

	val := hover.Contents.Value
	if !strings.Contains(val, "Output binary name") {
		t.Errorf("expected input description in hover, got: %s", val)
	}
	if !strings.Contains(val, "Optional") {
		t.Errorf("expected Optional in hover, got: %s", val)
	}
	if !strings.Contains(val, "Default: `app`") {
		t.Errorf("expected default value in hover, got: %s", val)
	}
}

func TestHoverWithKeyNilRegistry(t *testing.T) {
	content := `pipeline:
  - uses: go/build
    with:
      packages: ./cmd/app
`
	// With nil registry, should fall through to normal hover behavior
	pos := protocol.Position{Line: 3, Character: 8}
	hover := getHover(content, pos, nil, nil)
	// "packages: ./cmd/app" has no templates, so should be nil
	if hover != nil {
		t.Errorf("expected nil hover with nil registry, got: %+v", hover)
	}
}

func TestHoverWithKeyResolved(t *testing.T) {
	registry, dirs, _ := setupTestRegistry(t)

	// With a template value in the with: block, hover should show resolved value
	content := `package:
  name: myapp
  version: 2.0.0
  epoch: 0
pipeline:
  - uses: go/build
    with:
      packages: ./cmd/${{package.name}}
      output: ${{package.name}}
`
	// Cursor on "packages" key (line 7)
	pos := protocol.Position{Line: 7, Character: 8}
	hover := getHover(content, pos, registry, dirs)
	if hover == nil {
		t.Fatal("expected hover result for with key with template, got nil")
	}

	val := hover.Contents.Value
	t.Logf("hover output:\n%s", val)
	if !strings.Contains(val, "Go packages to build") {
		t.Errorf("expected input description, got: %s", val)
	}
	if !strings.Contains(val, "./cmd/myapp") {
		t.Errorf("expected resolved value './cmd/myapp' in hover, got: %s", val)
	}
}

func TestHoverUses(t *testing.T) {
	registry, dirs, _ := setupTestRegistry(t)

	// Use a valid melange config so Build.Compile() can process it
	content := `package:
  name: test-pkg
  version: 1.0.0
  epoch: 0
pipeline:
  - uses: go/build
    with:
      packages: ./cmd/app
`
	// Cursor on "uses: go/build" line (line 5)
	pos := protocol.Position{Line: 5, Character: 12}
	hover := getHover(content, pos, registry, dirs)
	if hover == nil {
		t.Fatal("expected hover result for uses line, got nil")
	}

	val := hover.Contents.Value
	t.Logf("hover output:\n%s", val)
	if !strings.Contains(val, "go/build") {
		t.Errorf("expected pipeline uses-name in hover, got: %s", val)
	}
	if !strings.Contains(val, "```yaml") {
		t.Errorf("expected yaml code block in hover, got: %s", val)
	}
	// After compilation, inputs should be resolved
	if !strings.Contains(val, "go build") {
		t.Errorf("expected script content in hover, got: %s", val)
	}
	// The with: value "./cmd/app" should appear in the compiled output
	if !strings.Contains(val, "./cmd/app") {
		t.Errorf("expected compiled with value in hover, got: %s", val)
	}
	// Should show "(compiled)" in the header
	if !strings.Contains(val, "compiled") {
		t.Errorf("expected '(compiled)' in header, got: %s", val)
	}
}

func TestHoverUsesContextName(t *testing.T) {
	registry, dirs, _ := setupTestRegistry(t)

	content := `package:
  name: myapp
  version: 1.0.0
  epoch: 0
pipeline:
  - uses: test/metapackage
`
	// Cursor on "uses: test/metapackage" (line 5)
	pos := protocol.Position{Line: 5, Character: 12}
	hover := getHover(content, pos, registry, dirs)
	if hover == nil {
		t.Fatal("expected hover result for metapackage, got nil")
	}

	val := hover.Contents.Value
	t.Logf("hover output:\n%s", val)

	// Should be compiled and show resolved context.name
	if !strings.Contains(val, "compiled") {
		t.Errorf("expected compiled output, got: %s", val)
	}
	// ${{context.name}} should be resolved to "myapp"
	if strings.Contains(val, "${{context.name}}") {
		t.Errorf("expected ${{context.name}} to be resolved, but it wasn't: %s", val)
	}
	if !strings.Contains(val, "myapp") {
		t.Errorf("expected 'myapp' (resolved context.name) in hover, got: %s", val)
	}
}

func TestHoverUsesFromPipelineFile(t *testing.T) {
	registry, dirs, _ := setupTestRegistry(t)

	// Pipeline file (not valid melange config) — should still compile
	// via Compiled.CompilePipelines fallback, resolving inputs
	content := `pipeline:
  - uses: go/build
    with:
      packages: ./cmd/app
`
	pos := protocol.Position{Line: 1, Character: 12}
	hover := getHover(content, pos, registry, dirs)
	if hover == nil {
		t.Fatal("expected hover result for uses line, got nil")
	}

	val := hover.Contents.Value
	if !strings.Contains(val, "go/build") {
		t.Errorf("expected pipeline uses-name in hover, got: %s", val)
	}
	if !strings.Contains(val, "compiled") {
		t.Errorf("expected compiled output, got: %s", val)
	}
	// Inputs should be resolved even from pipeline file context
	if !strings.Contains(val, "./cmd/app") {
		t.Errorf("expected resolved input in hover, got: %s", val)
	}
}

func TestHoverUsesInTestStanza(t *testing.T) {
	registry, dirs, _ := setupTestRegistry(t)

	content := `package:
  name: test-pkg
  version: 1.0.0
  epoch: 0
pipeline:
  - runs: echo build
test:
  pipeline:
    - uses: go/build
      with:
        packages: ./cmd/app
`
	// Cursor on "uses: go/build" inside test: stanza (line 8)
	pos := protocol.Position{Line: 8, Character: 15}
	hover := getHover(content, pos, registry, dirs)
	if hover == nil {
		t.Fatal("expected hover result for uses in test stanza, got nil")
	}

	val := hover.Contents.Value
	t.Logf("hover output:\n%s", val)
	if !strings.Contains(val, "go/build") {
		t.Errorf("expected pipeline name in hover, got: %s", val)
	}
	if !strings.Contains(val, "compiled") {
		t.Errorf("expected '(compiled)' in header, got: %s", val)
	}
	if !strings.Contains(val, "./cmd/app") {
		t.Errorf("expected compiled with value in hover, got: %s", val)
	}
}

func TestHoverUsesUnknown(t *testing.T) {
	registry, dirs, _ := setupTestRegistry(t)

	content := `pipeline:
  - uses: nonexistent/pipeline
`
	pos := protocol.Position{Line: 1, Character: 12}
	hover := getHover(content, pos, registry, dirs)
	// Unknown pipeline should return nil
	if hover != nil {
		t.Errorf("expected nil hover for unknown pipeline, got: %+v", hover)
	}
}

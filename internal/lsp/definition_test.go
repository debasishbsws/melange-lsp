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

func TestDefinitionOutsidePipeline(t *testing.T) {
	registry, dirs, _ := setupTestRegistry(t)

	content := `package:
  name: test
  version: 1.0.0
`
	pos := protocol.Position{Line: 1, Character: 4}
	locs := getDefinition(content, pos, registry, dirs)
	if len(locs) != 0 {
		t.Errorf("expected no definition outside pipeline, got %d", len(locs))
	}
}

func TestDefinitionNilRegistry(t *testing.T) {
	content := `pipeline:
  - uses: go/build
`
	pos := protocol.Position{Line: 1, Character: 12}
	locs := getDefinition(content, pos, nil, nil)
	if len(locs) != 0 {
		t.Errorf("expected no definition with nil registry, got %d", len(locs))
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

func TestHoverUses(t *testing.T) {
	registry, dirs, _ := setupTestRegistry(t)

	content := `pipeline:
  - uses: go/build
    with:
      packages: ./cmd/app
`
	// Cursor on "uses: go/build" line
	pos := protocol.Position{Line: 1, Character: 12}
	hover := getHover(content, pos, registry, dirs)
	if hover == nil {
		t.Fatal("expected hover result for uses line, got nil")
	}

	val := hover.Contents.Value
	if !strings.Contains(val, "Go Build") {
		t.Errorf("expected pipeline name in hover, got: %s", val)
	}
	if !strings.Contains(val, "go/build") {
		t.Errorf("expected pipeline uses-name in hover, got: %s", val)
	}
	if !strings.Contains(val, "```yaml") {
		t.Errorf("expected yaml code block in hover, got: %s", val)
	}
	if !strings.Contains(val, "packages:") {
		t.Errorf("expected 'packages' input in hover, got: %s", val)
	}
	if !strings.Contains(val, "go build") {
		t.Errorf("expected script content in hover, got: %s", val)
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

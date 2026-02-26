package lsp

import (
	"testing"

	"go.lsp.dev/protocol"
)

func TestWithCompletionTopLevel(t *testing.T) {
	reg, dirs, _ := setupTestRegistry(t)

	content := `package:
  name: test
pipeline:
  - uses: go/build
    with:
      p`

	// Cursor on "p" inside with: block (line 5, char 6)
	pos := protocol.Position{Line: 5, Character: 6}
	items := getCompletions(content, pos, reg, dirs)

	if len(items) == 0 {
		t.Fatal("expected with: completions, got none")
	}

	found := map[string]bool{}
	for _, item := range items {
		found[item.Label] = true
	}
	if !found["packages"] {
		t.Errorf("expected 'packages' in with: completions, got: %v", items)
	}
	if !found["output"] {
		t.Errorf("expected 'output' in with: completions, got: %v", items)
	}
}

func TestWithCompletionInSubpackage(t *testing.T) {
	reg, dirs, _ := setupTestRegistry(t)

	content := `package:
  name: test
subpackages:
  - name: test-doc
    pipeline:
      - uses: go/build
        with:
          p`

	// Cursor on "p" inside with: block in a subpackage (line 7, char 10)
	pos := protocol.Position{Line: 7, Character: 10}
	items := getCompletions(content, pos, reg, dirs)

	if len(items) == 0 {
		t.Fatal("expected with: completions in subpackage, got none")
	}

	found := map[string]bool{}
	for _, item := range items {
		found[item.Label] = true
	}
	if !found["packages"] {
		t.Errorf("expected 'packages' in with: completions, got labels: %v", found)
	}
}

func TestWithCompletionShowsDetails(t *testing.T) {
	reg, dirs, _ := setupTestRegistry(t)

	content := `pipeline:
  - uses: go/build
    with:
      `

	pos := protocol.Position{Line: 3, Character: 6}
	items := getCompletions(content, pos, reg, dirs)

	for _, item := range items {
		if item.Label == "output" {
			if item.Detail == "" {
				t.Error("expected detail/description for 'output' input")
			}
			if item.Kind != protocol.CompletionItemKindProperty {
				t.Errorf("expected optional input to be Property kind, got %v", item.Kind)
			}
			return
		}
	}
	t.Error("'output' not found in completions")
}

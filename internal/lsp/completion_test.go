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

func TestTemplateCompletionNamespaces(t *testing.T) {
	content := `package:
  name: test
  version: 1.0.0
pipeline:
  - runs: echo ${{`

	// Cursor right after ${{ on line 4
	pos := protocol.Position{Line: 4, Character: 20}
	items := getCompletions(content, pos, nil, nil)

	if len(items) == 0 {
		t.Fatal("expected namespace completions, got none")
	}

	found := map[string]bool{}
	for _, item := range items {
		found[item.Label] = true
	}

	for _, ns := range []string{"package", "vars", "targets", "build", "host", "cross", "subpkg", "context"} {
		if !found[ns] {
			t.Errorf("expected namespace %q in completions, got: %v", ns, found)
		}
	}
}

func TestTemplateCompletionNamespaceTextEdit(t *testing.T) {
	content := `pipeline:
  - runs: echo ${{r`

	// Cursor after "r" on line 1: "${{r" starts at col 17, "${{" is 3 chars, so col 20 is after "r"
	pos := protocol.Position{Line: 1, Character: 20}
	items := getCompletions(content, pos, nil, nil)

	found := false
	for _, item := range items {
		if item.Label == "range" {
			found = true
			if item.TextEdit == nil {
				t.Fatal("expected TextEdit for 'range', got nil")
			}
			// The edit should replace from right after "${{" (col 18) to cursor (col 19)
			if item.TextEdit.Range.Start.Character != 18 {
				t.Errorf("expected edit start at col 18, got %d", item.TextEdit.Range.Start.Character)
			}
			if item.TextEdit.Range.End.Character != 19 {
				t.Errorf("expected edit end at col 19, got %d", item.TextEdit.Range.End.Character)
			}
			if item.TextEdit.NewText != "range" {
				t.Errorf("expected NewText %q, got %q", "range", item.TextEdit.NewText)
			}
		}
	}
	if !found {
		t.Error("'range' not found in completions")
	}
}

func TestTemplateCompletionPackageDot(t *testing.T) {
	content := `package:
  name: test
  version: 1.0.0
pipeline:
  - runs: echo ${{package.`

	pos := protocol.Position{Line: 4, Character: 28}
	items := getCompletions(content, pos, nil, nil)

	if len(items) == 0 {
		t.Fatal("expected package key completions, got none")
	}

	found := map[string]bool{}
	for _, item := range items {
		found[item.Label] = true
	}

	for _, key := range []string{"name", "version", "epoch", "full-version"} {
		if !found[key] {
			t.Errorf("expected %q in package completions, got: %v", key, found)
		}
	}
}

func TestTemplateCompletionVars(t *testing.T) {
	content := `package:
  name: test
  version: 1.0.0
vars:
  py-version: "3.12"
  mandir: /usr/share/man
pipeline:
  - runs: echo ${{vars.`

	pos := protocol.Position{Line: 7, Character: 23}
	items := getCompletions(content, pos, nil, nil)

	if len(items) == 0 {
		t.Fatal("expected vars completions, got none")
	}

	found := map[string]bool{}
	for _, item := range items {
		found[item.Label] = true
	}

	if !found["py-version"] {
		t.Errorf("expected 'py-version' in vars completions, got: %v", found)
	}
	if !found["mandir"] {
		t.Errorf("expected 'mandir' in vars completions, got: %v", found)
	}
}

func TestTemplateCompletionPartial(t *testing.T) {
	content := `package:
  name: test
  version: 1.0.0
pipeline:
  - runs: echo ${{package.n`

	pos := protocol.Position{Line: 4, Character: 29}
	items := getCompletions(content, pos, nil, nil)

	if len(items) == 0 {
		t.Fatal("expected filtered completions, got none")
	}

	found := map[string]bool{}
	for _, item := range items {
		found[item.Label] = true
		// Verify TextEdit replaces from after "${{" to cursor, inserting full "package.name"
		if item.Label == "name" && item.TextEdit != nil {
			if item.TextEdit.NewText != "package.name" {
				t.Errorf("expected NewText %q, got %q", "package.name", item.TextEdit.NewText)
			}
		}
	}

	if !found["name"] {
		t.Errorf("expected 'name' in filtered completions, got: %v", found)
	}
	if found["version"] {
		t.Errorf("'version' should be filtered out by partial 'n', got: %v", found)
	}
}

func TestTemplateCompletionResolved(t *testing.T) {
	content := `package:
  name: mypackage
  version: 2.5.0
pipeline:
  - runs: echo ${{package.`

	pos := protocol.Position{Line: 4, Character: 28}
	items := getCompletions(content, pos, nil, nil)

	for _, item := range items {
		if item.Label == "name" {
			if item.Detail == "" {
				t.Error("expected detail with resolved value for 'name'")
			}
			return
		}
	}
	t.Error("'name' not found in completions")
}

func TestTemplateCompletionSubpkg(t *testing.T) {
	content := `package:
  name: test
  version: 1.0.0
subpackages:
  - name: test-dev
    pipeline:
      - runs: echo ${{subpkg.`

	pos := protocol.Position{Line: 6, Character: 29}
	items := getCompletions(content, pos, nil, nil)

	if len(items) == 0 {
		t.Fatal("expected subpkg completions, got none")
	}

	found := map[string]bool{}
	for _, item := range items {
		found[item.Label] = true
	}

	if !found["name"] {
		t.Errorf("expected 'name' in subpkg completions, got: %v", found)
	}
}

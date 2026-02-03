package lsp

import (
	"testing"

	"go.lsp.dev/protocol"
)

func TestHoverBuiltinVar(t *testing.T) {
	content := `package:
  name: test
  version: ${{package.version}}
`
	// Cursor on "package.version" inside ${{...}}
	pos := protocol.Position{Line: 2, Character: 16}
	hover := getHover(content, pos)
	if hover == nil {
		t.Fatal("expected hover result, got nil")
	}
	if !contains(hover.Contents.Value, "The package version") {
		t.Errorf("expected built-in description, got: %s", hover.Contents.Value)
	}
}

func TestHoverUserVar(t *testing.T) {
	content := `vars:
  foo: bar-value
pipeline:
  - runs: echo ${{vars.foo}}
`
	// Cursor on "vars.foo" inside ${{...}}
	pos := protocol.Position{Line: 3, Character: 22}
	hover := getHover(content, pos)
	if hover == nil {
		t.Fatal("expected hover result, got nil")
	}
	if !contains(hover.Contents.Value, "bar-value") {
		t.Errorf("expected var value in hover, got: %s", hover.Contents.Value)
	}
}

func TestHoverRangeVar(t *testing.T) {
	content := `data:
  - name: accounts
    items:
      alpha: one
      beta: two
subpackages:
  - range: accounts
    name: ${{range.key}}
`
	// Cursor on "range.key" inside ${{...}}
	pos := protocol.Position{Line: 7, Character: 14}
	hover := getHover(content, pos)
	if hover == nil {
		t.Fatal("expected hover result, got nil")
	}
	if !contains(hover.Contents.Value, "accounts") {
		t.Errorf("expected range data name in hover, got: %s", hover.Contents.Value)
	}
	if !contains(hover.Contents.Value, "alpha") || !contains(hover.Contents.Value, "beta") {
		t.Errorf("expected data item keys in hover, got: %s", hover.Contents.Value)
	}
}

func TestHoverNoVariable(t *testing.T) {
	content := `package:
  name: test
`
	pos := protocol.Position{Line: 1, Character: 5}
	hover := getHover(content, pos)
	if hover != nil {
		t.Errorf("expected nil hover when not on a variable, got: %+v", hover)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

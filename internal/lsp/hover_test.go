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
	hover := getHover(content, pos, nil, nil)
	if hover == nil {
		t.Fatal("expected hover result, got nil")
	}
	if !contains(hover.Contents.Value, "The package version") {
		t.Errorf("expected built-in description, got: %s", hover.Contents.Value)
	}
}

func TestHoverBuiltinVarResolved(t *testing.T) {
	content := `package:
  name: mypackage
  version: 1.2.3
  epoch: 5
  description: A cool package
pipeline:
  - runs: echo ${{package.name}}
`
	// Cursor on "package.name" inside ${{...}}
	pos := protocol.Position{Line: 6, Character: 20}
	hover := getHover(content, pos, nil, nil)
	if hover == nil {
		t.Fatal("expected hover result, got nil")
	}
	// Should show resolved value prominently
	if !contains(hover.Contents.Value, "**mypackage**") {
		t.Errorf("expected resolved package name, got: %s", hover.Contents.Value)
	}
	if !contains(hover.Contents.Value, "The package name") {
		t.Errorf("expected description, got: %s", hover.Contents.Value)
	}
}

func TestHoverVarTransformResolved(t *testing.T) {
	content := `package:
  name: test-pkg
  version: 1.2.3
vars:
  mangled-ver: 1.2.3
var-transforms:
  - from: ${{vars.mangled-ver}}
    match: \.
    replace: _
    to: mangled-ver
pipeline:
  - runs: echo ${{vars.mangled-ver}}
`
	// Cursor on "vars.mangled-ver"
	pos := protocol.Position{Line: 11, Character: 20}
	hover := getHover(content, pos, nil, nil)
	if hover == nil {
		t.Fatal("expected hover result, got nil")
	}
	// After var-transform, dots should be replaced with underscores
	if !contains(hover.Contents.Value, "1_2_3") {
		t.Errorf("expected var-transform resolved value '1_2_3', got: %s", hover.Contents.Value)
	}
}

func TestHoverFallbackOnInvalidConfig(t *testing.T) {
	// Invalid YAML that our lightweight parser can still handle for vars
	content := `vars:
  foo: bar-value
this is not valid: [yaml
pipeline:
  - runs: echo ${{vars.foo}}
`
	// Cursor on "vars.foo"
	pos := protocol.Position{Line: 4, Character: 22}
	hover := getHover(content, pos, nil, nil)
	if hover == nil {
		t.Fatal("expected hover result, got nil")
	}
	// Should still show something useful even if ParseConfiguration fails
	if !contains(hover.Contents.Value, "User-defined variable") {
		t.Errorf("expected fallback hover info, got: %s", hover.Contents.Value)
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
	hover := getHover(content, pos, nil, nil)
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
	hover := getHover(content, pos, nil, nil)
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

func TestHoverKeyResolved(t *testing.T) {
	content := `package:
  name: mypackage
  version: 1.2.3
subpackages:
  - name: ${{package.name}}-dev
`
	// Cursor on "name" key at the subpackage level (line 4)
	pos := protocol.Position{Line: 4, Character: 6}
	hover := getHover(content, pos, nil, nil)
	if hover == nil {
		t.Fatal("expected hover result, got nil")
	}
	if !contains(hover.Contents.Value, "mypackage-dev") {
		t.Errorf("expected resolved value 'mypackage-dev', got: %s", hover.Contents.Value)
	}
	if !contains(hover.Contents.Value, "Resolved value of `name`") {
		t.Errorf("expected 'Resolved value of' text, got: %s", hover.Contents.Value)
	}
}

func TestHoverKeyInRange(t *testing.T) {
	content := `package:
  name: mypackage
  version: 1.2.3
data:
  - name: accounts
    items:
      alpha: one
      beta: two
subpackages:
  - range: accounts
    name: ${{package.name}}-${{range.key}}
`
	// Cursor on "name" key inside range subpackage (line 10)
	pos := protocol.Position{Line: 10, Character: 6}
	hover := getHover(content, pos, nil, nil)
	if hover == nil {
		t.Fatal("expected hover result, got nil")
	}
	if !contains(hover.Contents.Value, "mypackage-alpha") {
		t.Errorf("expected 'mypackage-alpha' in hover, got: %s", hover.Contents.Value)
	}
	if !contains(hover.Contents.Value, "mypackage-beta") {
		t.Errorf("expected 'mypackage-beta' in hover, got: %s", hover.Contents.Value)
	}
	if !contains(hover.Contents.Value, "Resolved values of `name`") {
		t.Errorf("expected 'Resolved values of' header, got: %s", hover.Contents.Value)
	}
}

func TestHoverKeyBlockScalar(t *testing.T) {
	content := `package:
  name: mypackage
  version: 1.2.3
pipeline:
  - runs: |
      echo ${{package.name}}
      echo done
`
	// Cursor on "runs" key (line 4)
	pos := protocol.Position{Line: 4, Character: 6}
	hover := getHover(content, pos, nil, nil)
	if hover == nil {
		t.Fatal("expected hover result, got nil")
	}
	if !contains(hover.Contents.Value, "mypackage") {
		t.Errorf("expected resolved package name in block scalar, got: %s", hover.Contents.Value)
	}
	if !contains(hover.Contents.Value, "Resolved value of `runs`") {
		t.Errorf("expected 'Resolved value of' text, got: %s", hover.Contents.Value)
	}
}

func TestHoverKeyBlockScalarInRange(t *testing.T) {
	content := `package:
  name: mypkg
  version: 1.0.0
data:
  - name: pyversions
    items:
      "3.11": "3.11"
      "3.12": "3.12"
subpackages:
  - range: pyversions
    name: ${{package.name}}-${{range.key}}
    pipeline:
      - runs: |
          python${{range.key}} <<'EOF'
          import sys
          EOF
`
	// Cursor on "runs" key (line 12)
	pos := protocol.Position{Line: 12, Character: 8}
	hover := getHover(content, pos, nil, nil)
	if hover == nil {
		t.Fatal("expected hover result, got nil")
	}
	if !contains(hover.Contents.Value, "python3.11") {
		t.Errorf("expected 'python3.11' in range block scalar, got: %s", hover.Contents.Value)
	}
	if !contains(hover.Contents.Value, "python3.12") {
		t.Errorf("expected 'python3.12' in range block scalar, got: %s", hover.Contents.Value)
	}
}

func TestHoverKeyLiteralValue(t *testing.T) {
	content := `package:
  name: mypackage
  version: 1.2.3
`
	// Cursor on "name" key with a plain literal value — should return nil
	pos := protocol.Position{Line: 1, Character: 4}
	hover := getHover(content, pos, nil, nil)
	if hover != nil {
		t.Errorf("expected nil hover for literal value key, got: %+v", hover)
	}
}

func TestHoverValueSideOfKey(t *testing.T) {
	content := `package:
  name: mypackage
  version: 1.2.3
subpackages:
  - name: ${{package.name}}-dev
`
	// Cursor on the VALUE side, but NOT inside the ${{...}} span.
	// "${{package.name}}-dev" — the "-dev" suffix starts after "}}"
	// Line 4: "  - name: ${{package.name}}-dev"
	// Position just after the closing }} on the "-dev" part (char ~30)
	pos := protocol.Position{Line: 4, Character: 30}
	hover := getHover(content, pos, nil, nil)
	if hover == nil {
		t.Fatal("expected hover result, got nil")
	}
	if !contains(hover.Contents.Value, "mypackage-dev") {
		t.Errorf("expected resolved value 'mypackage-dev', got: %s", hover.Contents.Value)
	}
}

func TestHoverBareListItem(t *testing.T) {
	content := `package:
  name: mypackage
  version: 1.2.3
environment:
  contents:
    packages:
      - ${{package.name}}-dev
`
	// Cursor on bare list item "- ${{package.name}}-dev" (line 6)
	// Place cursor past the ${{}} span, on the "-dev" suffix
	pos := protocol.Position{Line: 6, Character: 28}
	hover := getHover(content, pos, nil, nil)
	if hover == nil {
		t.Fatal("expected hover result, got nil")
	}
	if !contains(hover.Contents.Value, "mypackage-dev") {
		t.Errorf("expected resolved value, got: %s", hover.Contents.Value)
	}
}

func TestHoverBareListItemInRange(t *testing.T) {
	content := `package:
  name: mypkg
  version: 1.0.0
data:
  - name: modules
    items:
      foo: one
      bar: two
subpackages:
  - range: modules
    name: ${{package.name}}-${{range.key}}
    dependencies:
      provides:
        - lib-${{range.key}}
`
	// Cursor on bare list item with range (line 13), on "lib-" prefix before ${{
	pos := protocol.Position{Line: 13, Character: 10}
	hover := getHover(content, pos, nil, nil)
	if hover == nil {
		t.Fatal("expected hover result, got nil")
	}
	if !contains(hover.Contents.Value, "lib-bar") {
		t.Errorf("expected 'lib-bar' in hover, got: %s", hover.Contents.Value)
	}
	if !contains(hover.Contents.Value, "lib-foo") {
		t.Errorf("expected 'lib-foo' in hover, got: %s", hover.Contents.Value)
	}
}

func TestHoverBareListItemLiteral(t *testing.T) {
	content := `package:
  name: mypackage
  version: 1.2.3
environment:
  contents:
    packages:
      - build-base
`
	// Cursor on bare list item with no templates — should return nil
	pos := protocol.Position{Line: 6, Character: 10}
	hover := getHover(content, pos, nil, nil)
	if hover != nil {
		t.Errorf("expected nil hover for literal list item, got: %+v", hover)
	}
}

func TestHoverNoVariable(t *testing.T) {
	content := `package:
  name: test
`
	pos := protocol.Position{Line: 1, Character: 5}
	hover := getHover(content, pos, nil, nil)
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

package lsp

import (
	"strings"
	"testing"
)

func TestCodeLensVarTransform(t *testing.T) {
	content := `package:
  name: test
  version: 1.2.3
  epoch: 0
  description: test package
vars:
  foo: bar
var-transforms:
  - from: ${{package.version}}
    match: ^(\d+\.\d+)\.\d+$
    replace: $1
    to: major-minor
pipeline:
  - runs: echo hello
`
	lenses := getCodeLenses(content)
	if len(lenses) != 1 {
		t.Fatalf("expected 1 code lens, got %d", len(lenses))
	}

	lens := lenses[0]
	if lens.Command == nil {
		t.Fatal("expected command on code lens")
	}
	if !strings.Contains(lens.Command.Title, "→") {
		t.Errorf("expected arrow in title, got: %s", lens.Command.Title)
	}
	// The transform extracts major.minor from 1.2.3 → "1.2"
	if !strings.Contains(lens.Command.Title, "1.2") {
		t.Errorf("expected resolved value containing '1.2', got: %s", lens.Command.Title)
	}
	if lens.Command.Command != "" {
		t.Errorf("expected empty command string for display-only lens, got: %s", lens.Command.Command)
	}
}

func TestCodeLensMultipleTransforms(t *testing.T) {
	content := `package:
  name: test
  version: 8.4.5
  epoch: 0
  description: test package
var-transforms:
  - from: ${{package.version}}
    match: ^(\d+)\..*
    replace: $1
    to: major-version
  - from: ${{package.version}}
    match: ^(\d+\.\d+)\..*
    replace: $1
    to: major-minor-version
pipeline:
  - runs: echo hello
`
	lenses := getCodeLenses(content)
	if len(lenses) != 2 {
		t.Fatalf("expected 2 code lenses, got %d", len(lenses))
	}

	// First transform: major-version should be "8"
	if !strings.Contains(lenses[0].Command.Title, "8") {
		t.Errorf("expected major-version '8', got: %s", lenses[0].Command.Title)
	}

	// Second transform: major-minor-version should be "8.4"
	if !strings.Contains(lenses[1].Command.Title, "8.4") {
		t.Errorf("expected major-minor-version '8.4', got: %s", lenses[1].Command.Title)
	}
}

func TestCodeLensNoTransforms(t *testing.T) {
	content := `package:
  name: test
  version: 1.0.0
  epoch: 0
  description: test package
pipeline:
  - runs: echo hello
`
	lenses := getCodeLenses(content)
	if len(lenses) != 0 {
		t.Errorf("expected 0 code lenses for config without var-transforms, got %d", len(lenses))
	}
}

func TestCodeLensFromPackageVersion(t *testing.T) {
	content := `package:
  name: test
  version: 8.4.5
  epoch: 2
  description: test package
var-transforms:
  - from: ${{package.version}}
    match: \.(\d+)$
    replace: "-$1"
    to: mangled-package-version
pipeline:
  - runs: echo hello
`
	lenses := getCodeLenses(content)
	if len(lenses) != 1 {
		t.Fatalf("expected 1 code lens, got %d", len(lenses))
	}

	// The transform replaces the last .N with -N: 8.4.5 → 8.4-5
	if !strings.Contains(lenses[0].Command.Title, "8.4-5") {
		t.Errorf("expected '8.4-5' in lens title, got: %s", lenses[0].Command.Title)
	}
}

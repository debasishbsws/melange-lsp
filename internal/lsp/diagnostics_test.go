package lsp

import (
	"testing"
)

func TestParseLineFromError(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want uint32
	}{
		{"syntax error", `yaml: line 5: could not find expected ':'`, 4},
		{"unmarshal error", `line 4: cannot unmarshal !!str into uint64`, 3},
		{"no line info", `something went wrong`, 0},
		{"multiple lines takes first", `line 10: error1\nline 20: error2`, 9},
		{"line 1 maps to 0", `yaml: line 1: bad`, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLineFromError(tt.msg)
			if got != tt.want {
				t.Errorf("parseLineFromError(%q) = %d, want %d", tt.msg, got, tt.want)
			}
		})
	}
}

func TestValidateDocumentLineNumbers(t *testing.T) {
	// Invalid YAML that will produce an error with a line number
	content := `package:
  name: test
  version: 1.0.0
  epoch: not_a_number
`
	diags := ValidateDocument("file:///test.yaml", content)
	if len(diags) == 0 {
		t.Fatal("expected diagnostics for invalid YAML")
	}
	d := diags[0]
	// The error should point to line 3 (0-indexed) where epoch has a bad value
	if d.Range.Start.Line == 0 && d.Range.End.Line == 0 && d.Range.End.Character == 0 {
		t.Errorf("diagnostic still at 0:0, expected a parsed line number; message: %s", d.Message)
	}
}

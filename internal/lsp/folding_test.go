package lsp

import (
	"encoding/json"
	"strings"
	"testing"

	"melange-lsp/internal/document"

	"go.lsp.dev/protocol"
)

type fold struct {
	start, end uint32
	agent      bool
}

func folds(ranges []FoldingRange) []fold {
	var out []fold
	for _, r := range ranges {
		out = append(out, fold{r.StartLine, r.EndLine, r.Kind == protocol.CommentFoldingRange})
	}
	return out
}

func TestStructuralFoldingRanges(t *testing.T) {
	content := `package:
  name: foo
  version: 1.0.0
# section comment
pipeline:
  - uses: fetch
    with:
      uri: https://example.com

  - runs: |
      make
      # shell comment
      make install
subpackages:
- name: foo-doc
  pipeline:
  - uses: split/manpages
`
	want := []fold{
		{0, 2, false},   // package
		{4, 12, false},  // pipeline
		{5, 7, false},   // - uses: fetch
		{6, 7, false},   // with
		{9, 12, false},  // - runs
		{13, 16, false}, // subpackages with same-indent items
		{14, 16, false}, // - name: foo-doc
		{15, 16, false}, // pipeline with same-indent items
	}
	got := folds(getFoldingRanges(document.New("file:///x.yaml", 1, content)))
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("fold %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestAgentNoteFoldingRanges(t *testing.T) {
	content := `# @agent: lone one-liner cannot fold
package:
  name: foo

  # @agent: first
  # @agent ` + "```" + `
  # body
  # ` + "```" + `
  version: 1.0.0

# @agent: a
# @agent: b
`
	var agent []FoldingRange
	for _, r := range getFoldingRanges(document.New("file:///x.yaml", 1, content)) {
		if r.Kind == protocol.CommentFoldingRange {
			agent = append(agent, r)
		}
	}
	if len(agent) != 2 {
		t.Fatalf("got %d agent folds %+v, want 2", len(agent), agent)
	}

	// A one-liner directly followed by a fence merges into one fold.
	if a := agent[0]; a.StartLine != 4 || a.EndLine != 7 || a.CollapsedText != "🤖 2 agent notes" {
		t.Errorf("fold 0 = %d-%d %q, want 4-7 %q", a.StartLine, a.EndLine, a.CollapsedText, "🤖 2 agent notes")
	}
	if a := agent[0]; a.StartCharacter == nil || *a.StartCharacter != 2 {
		t.Errorf("fold 0 should start at the '#' (character 2), got %v", a.StartCharacter)
	}
	if a := agent[1]; a.StartLine != 10 || a.EndLine != 11 {
		t.Errorf("fold 1 = %d-%d, want 10-11", a.StartLine, a.EndLine)
	}
}

func TestFoldingRangeJSON(t *testing.T) {
	start := uint32(0)
	b, err := json.Marshal(FoldingRange{StartLine: 1, StartCharacter: &start, EndLine: 3, Kind: protocol.CommentFoldingRange, CollapsedText: "🤖 agent note"})
	if err != nil {
		t.Fatal(err)
	}
	// startCharacter 0 must be sent, not dropped by omitempty.
	for _, want := range []string{`"startCharacter":0`, `"collapsedText":"🤖 agent note"`, `"kind":"comment"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON %s missing %s", b, want)
		}
	}
}

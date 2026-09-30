package lsp

import (
	"strings"
	"testing"

	"melange-lsp/internal/document"
)

func TestParseAgentNotes(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []AgentNote
	}{{
		name:    "one-liner with colon",
		content: "# @agent: strip the -final suffix\n",
		want:    []AgentNote{{StartLine: 0, EndLine: 0, Terminated: true, Text: "strip the -final suffix"}},
	}, {
		name:    "one-liner without colon",
		content: "  # @agent strip the -final suffix\n",
		want:    []AgentNote{{StartLine: 0, EndLine: 0, Indent: 2, Terminated: true, Text: "strip the -final suffix"}},
	}, {
		name:    "no space after hash",
		content: "#@agent: note\n",
		want:    []AgentNote{{StartLine: 0, EndLine: 0, Terminated: true, Text: "note"}},
	}, {
		name: "fenced",
		content: "package:\n" +
			"  # @agent ```\n" +
			"  # line one\n" +
			"  #   indented\n" +
			"  # ```\n" +
			"  name: foo\n",
		want: []AgentNote{{StartLine: 1, EndLine: 4, Indent: 2, Fenced: true, Terminated: true, Text: "line one\n  indented"}},
	}, {
		name:    "fenced with colon and info string",
		content: "# @agent: ```md\n# body\n# ```\n",
		want:    []AgentNote{{StartLine: 0, EndLine: 2, Fenced: true, Terminated: true, Text: "body"}},
	}, {
		name:    "unterminated fence stops at first non-comment",
		content: "# @agent ```\n# body\nname: foo\n# ```\n",
		want:    []AgentNote{{StartLine: 0, EndLine: 1, Fenced: true, Terminated: false, Text: "body"}},
	}, {
		name:    "one-liner inside fence is body text",
		content: "# @agent ```\n# @agent: not a new note\n# ```\n",
		want:    []AgentNote{{StartLine: 0, EndLine: 2, Fenced: true, Terminated: true, Text: "@agent: not a new note"}},
	}, {
		name:    "not a note",
		content: "# @agents are cool\n# see @agent docs\nname: x # @agent: trailing comments are ignored\n",
		want:    nil,
	}, {
		name:    "CRLF line endings",
		content: "# @agent: note\r\n# @agent ```\r\n# body\r\n# ```\r\n",
		want: []AgentNote{
			{StartLine: 0, EndLine: 0, Terminated: true, Text: "note"},
			{StartLine: 1, EndLine: 3, Fenced: true, Terminated: true, Text: "body"},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAgentNotes(tt.content)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d notes %+v, want %d", len(got), got, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("note %d:\n got  %+v\n want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestAgentNoteDiagnostics(t *testing.T) {
	content := "# @agent: fine\n" +
		"# @agent\n" +
		"# @agent ```\n" +
		"# never closed\n" +
		"package:\n"

	diags := agentNoteDiagnostics(document.New("file:///x.yaml", 1, content))
	if len(diags) != 2 {
		t.Fatalf("got %d diagnostics %+v, want 2", len(diags), diags)
	}
	if diags[0].Range.Start.Line != 1 || !strings.Contains(diags[0].Message, "empty") {
		t.Errorf("diag 0 = line %d %q, want empty note on line 1", diags[0].Range.Start.Line, diags[0].Message)
	}
	if diags[1].Range.Start.Line != 2 || !strings.Contains(diags[1].Message, "unterminated") {
		t.Errorf("diag 1 = line %d %q, want unterminated on line 2", diags[1].Range.Start.Line, diags[1].Message)
	}
}

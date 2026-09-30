package lsp

import (
	"regexp"
	"strings"
	"unicode/utf16"

	"melange-lsp/internal/document"

	"go.lsp.dev/protocol"
)

// Agent notes are YAML comments addressed to AI agents rather than humans.
// They are ordinary comments, so melange ignores them and agents read them
// verbatim; editors can fold them away via the folding ranges we report.
//
// One-liner (the colon is optional):
//
//	# @agent: upstream tags releases as vX.Y.Z-final
//	# @agent upstream tags releases as vX.Y.Z-final
//
// Multi-line, fenced with backticks and closed by a bare "# ```" line:
//
//	# @agent ```
//	# The CVE-2026-1234 patch is required until 1.5.0.
//	# ```
var (
	agentNoteStart = regexp.MustCompile(`^(\s*)#\s*@agent\b:?\s*(.*)$`)
	commentLine    = regexp.MustCompile(`^\s*#`)
)

const agentFence = "```"

// AgentNote is a single agent note found in a document.
type AgentNote struct {
	// StartLine and EndLine are zero-based and inclusive.
	StartLine int
	EndLine   int
	// Indent is the UTF-16 column of the leading '#'.
	Indent int
	// Fenced is true for the multi-line ``` form.
	Fenced bool
	// Terminated is false for a fenced note missing its closing fence.
	Terminated bool
	// Text is the note body with comment markers and fences removed.
	Text string
}

// parseAgentNotes scans content line by line for agent notes. It matches
// full-line comments only, including comments inside block scalars such as
// `runs: |`, so notes can sit next to the shell they describe.
func parseAgentNotes(content string) []AgentNote {
	lines := strings.Split(content, "\n")
	var notes []AgentNote

	for i := 0; i < len(lines); i++ {
		m := agentNoteStart.FindStringSubmatch(strings.TrimRight(lines[i], "\r"))
		if m == nil {
			continue
		}
		rest := strings.TrimSpace(m[2])
		note := AgentNote{
			StartLine:  i,
			EndLine:    i,
			Indent:     utf16Len(m[1]),
			Terminated: true,
		}

		if !strings.HasPrefix(rest, agentFence) {
			note.Text = rest
			notes = append(notes, note)
			continue
		}

		// Fenced note: anything after the opening fence on the same line is
		// treated as an info string and ignored, like Markdown.
		note.Fenced = true
		note.Terminated = false
		var body []string
		j := i + 1
		for ; j < len(lines); j++ {
			line := strings.TrimRight(lines[j], "\r")
			if !commentLine.MatchString(line) {
				break
			}
			text := stripComment(line)
			if strings.TrimSpace(text) == agentFence {
				note.Terminated = true
				break
			}
			body = append(body, text)
		}
		if note.Terminated {
			note.EndLine = j
		} else {
			note.EndLine = j - 1
		}
		note.Text = strings.Join(dedent(body), "\n")
		notes = append(notes, note)
		i = note.EndLine
	}

	return notes
}

// stripComment removes leading whitespace, the '#', and one following space.
func stripComment(line string) string {
	s := strings.TrimLeft(line, " \t")
	s = strings.TrimPrefix(s, "#")
	return strings.TrimPrefix(s, " ")
}

// dedent removes the common leading whitespace from non-blank lines.
func dedent(lines []string) []string {
	common := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if common < 0 || n < common {
			common = n
		}
	}
	if common <= 0 {
		return lines
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if len(l) >= common {
			out[i] = l[common:]
		} else {
			out[i] = strings.TrimLeft(l, " \t")
		}
	}
	return out
}

// agentNoteDiagnostics reports malformed agent notes.
func agentNoteDiagnostics(d *document.Document) []protocol.Diagnostic {
	var diags []protocol.Diagnostic
	for _, n := range parseAgentNotes(d.Text) {
		var msg string
		switch {
		case n.Fenced && !n.Terminated:
			msg = "unterminated @agent block: close it with a \"# ```\" line"
		case strings.TrimSpace(n.Text) == "":
			msg = "empty @agent note"
		default:
			continue
		}
		diags = append(diags, protocol.Diagnostic{
			Range: protocol.Range{
				Start: protocol.Position{Line: uint32(n.StartLine), Character: uint32(n.Indent)},
				End:   d.LineEnd(n.StartLine),
			},
			Severity: protocol.DiagnosticSeverityWarning,
			Code:     codeAgentNote,
			Source:   "melange",
			Message:  msg,
		})
	}
	return diags
}

func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

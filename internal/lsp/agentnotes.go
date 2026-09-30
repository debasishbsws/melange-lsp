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
// Multi-line: a line ending in ``` opens the note and a bare "# ```" line
// closes it. Text between @agent and the fence is an optional heading:
//
//	# @agent Keep the CVE-2026-1234 patch ```
//	# The patch is required until 1.5.0.
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
	// Heading is the text between @agent and the opening fence.
	Heading string
	// Text is the note body with comment markers and fences removed.
	Text string
}

// Title is how a note is labelled when folded: its heading, else its
// first non-empty line.
func (n AgentNote) Title() string {
	if n.Heading != "" {
		return n.Heading
	}
	for _, l := range strings.Split(n.Text, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

// Markdown renders the note for hovers.
func (n AgentNote) Markdown() string {
	if n.Heading != "" {
		return "**" + n.Heading + "**\n\n" + n.Text
	}
	return n.Text
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

		// A line ending in the fence opens a multi-line note; any text
		// between @agent and the fence is its heading.
		if !strings.HasSuffix(rest, agentFence) {
			note.Text = rest
			notes = append(notes, note)
			continue
		}
		note.Fenced = true
		note.Heading = strings.TrimSpace(strings.TrimSuffix(rest, agentFence))
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
		case strings.TrimSpace(n.Text) == "" && n.Heading == "":
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

// noteTitleWidth is how much of a note's title a fold shows.
const noteTitleWidth = 60

// truncateWords shortens s to at most n characters, cutting at a word
// boundary and adding an ellipsis.
func truncateWords(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:") + "…"
}

// agentNoteGroups splits notes into runs of adjacent notes, which fold and
// hover together.
func agentNoteGroups(notes []AgentNote) [][]AgentNote {
	var groups [][]AgentNote
	for i := 0; i < len(notes); {
		j := i + 1
		for j < len(notes) && notes[j].StartLine == notes[j-1].EndLine+1 {
			j++
		}
		groups = append(groups, notes[i:j])
		i = j
	}
	return groups
}

// agentNoteHover shows the full note(s) at line, so a folded note can be
// read without opening it.
func agentNoteHover(d *document.Document, line int) (string, *protocol.Range) {
	for _, g := range agentNoteGroups(parseAgentNotes(d.Text)) {
		first, last := g[0], g[len(g)-1]
		if line < first.StartLine || line > last.EndLine {
			continue
		}
		r := protocol.Range{Start: protocol.Position{Line: uint32(first.StartLine), Character: uint32(first.Indent)}, End: d.LineEnd(last.EndLine)}
		if len(g) == 1 {
			return "🤖 **Agent note**\n\n" + first.Markdown(), &r
		}
		// Several adjacent notes read as a list.
		var b strings.Builder
		b.WriteString("🤖 **Agent notes**\n\n")
		for _, n := range g {
			b.WriteString("- " + strings.ReplaceAll(strings.TrimSpace(n.Markdown()), "\n", "\n  ") + "\n")
		}
		return b.String(), &r
	}
	return "", nil
}

package lsp

import (
	"fmt"
	"sort"
	"strings"

	"melange-lsp/internal/document"

	"go.lsp.dev/protocol"
)

// FoldingRange mirrors protocol.FoldingRange and adds CollapsedText (LSP
// 3.17), which go.lsp.dev/protocol v0.12.0 does not model. Clients that
// support it show this text in place of the folded lines.
type FoldingRange struct {
	StartLine      uint32                    `json:"startLine"`
	StartCharacter *uint32                   `json:"startCharacter,omitempty"`
	EndLine        uint32                    `json:"endLine"`
	EndCharacter   *uint32                   `json:"endCharacter,omitempty"`
	Kind           protocol.FoldingRangeKind `json:"kind,omitempty"`
	CollapsedText  string                    `json:"collapsedText,omitempty"`
}

// getFoldingRanges returns structural YAML folds plus agent-note folds.
//
// Structural folds matter because editors such as Zed use LSP folding
// ranges instead of their own when a server provides them, so returning
// only agent notes would break folding of pipelines and subpackages.
func getFoldingRanges(d *document.Document) []FoldingRange {
	lines := d.Lines
	ranges := structuralFoldingRanges(lines)
	ranges = append(ranges, agentNoteFoldingRanges(lines, parseAgentNotes(d.Text))...)

	sort.SliceStable(ranges, func(i, j int) bool {
		if ranges[i].StartLine != ranges[j].StartLine {
			return ranges[i].StartLine < ranges[j].StartLine
		}
		return ranges[i].EndLine > ranges[j].EndLine
	})
	return ranges
}

// agentNoteFoldingRanges folds each run of adjacent agent notes into one
// range of kind "comment". A lone one-line note spans a single line, which
// clients cannot fold, so it is not reported.
func agentNoteFoldingRanges(lines []string, notes []AgentNote) []FoldingRange {
	var ranges []FoldingRange
	for i := 0; i < len(notes); {
		first, last := notes[i], notes[i]
		count := 1
		for i+count < len(notes) && notes[i+count].StartLine == last.EndLine+1 {
			last = notes[i+count]
			count++
		}
		i += count

		if last.EndLine <= first.StartLine {
			continue
		}
		label := "🤖 agent note"
		if count > 1 {
			label = fmt.Sprintf("🤖 %d agent notes", count)
		}
		// Start the fold at the '#' rather than the end of the line so the
		// whole note, including the "@agent" marker, is hidden.
		startChar := uint32(first.Indent)
		endChar := uint32(utf16Len(lines[last.EndLine]))
		ranges = append(ranges, FoldingRange{
			StartLine:      uint32(first.StartLine),
			StartCharacter: &startChar,
			EndLine:        uint32(last.EndLine),
			EndCharacter:   &endChar,
			Kind:           protocol.CommentFoldingRange,
			CollapsedText:  label,
		})
	}
	return ranges
}

// structuralFoldingRanges derives folds from indentation. A mapping key or
// sequence item folds over every following line that is indented deeper,
// which also covers block scalars such as `runs: |`. Blank lines and
// comments no deeper than the opener are ignored, so a comment introducing
// the next section is not swallowed by the previous one.
func structuralFoldingRanges(lines []string) []FoldingRange {
	var ranges []FoldingRange
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" {
			continue
		}
		indent := indentOf(line)
		// A key with no inline value may hold a sequence written at the same
		// indent ("pipeline:\n- uses: ..."), which YAML allows.
		sameIndentItems := strings.HasSuffix(stripTrailingComment(trimmed), ":") && !strings.HasPrefix(trimmed, "- ")

		end := i
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if t == "" {
				continue
			}
			ind := indentOf(lines[j])
			if ind > indent || (sameIndentItems && ind == indent && (t == "-" || strings.HasPrefix(t, "- "))) {
				end = j
				continue
			}
			if strings.HasPrefix(t, "#") {
				continue
			}
			break
		}
		if end > i {
			ranges = append(ranges, FoldingRange{StartLine: uint32(i), EndLine: uint32(end)})
		}
	}
	return ranges
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// stripTrailingComment removes a " #..." suffix. It is only used to detect
// keys ending in ':', so it does not need to understand quoting.
func stripTrailingComment(s string) string {
	if i := strings.Index(s, " #"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

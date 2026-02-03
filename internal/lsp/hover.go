package lsp

import (
	"fmt"
	"sort"
	"strings"

	"melange-lsp/internal/parser"

	"go.lsp.dev/protocol"
)

// builtinVarDescriptions maps well-known ${{...}} variable paths to descriptions.
var builtinVarDescriptions = map[string]string{
	"package.name":        "The package name",
	"package.version":     "The package version",
	"package.epoch":       "The package epoch (version bump counter)",
	"package.full-version": "The full version string (version-repoch)",
	"package.description": "The package description",

	"targets.contextdir": "The build context directory",
	"targets.destdir":    "The package install destination directory",

	"build.arch":       "The build architecture (e.g. x86_64, aarch64)",
	"build.goarch":     "The Go architecture name (e.g. amd64, arm64)",

	"host.triplet.gnu":   "The GNU host triplet",
	"host.triplet.rust":  "The Rust host triplet",

	"cross.triplet.gnu.glibc":  "The GNU glibc cross-compilation triplet",
	"cross.triplet.gnu.musl":   "The GNU musl cross-compilation triplet",
	"cross.triplet.rust":       "The Rust cross-compilation triplet",

	"subpkg.name": "The subpackage name",

	"context.os": "The build OS (e.g. linux)",
}

// getHover returns hover information for the given document content and position.
// It provides info when the cursor is on a ${{...}} expression.
func getHover(content string, position protocol.Position) *protocol.Hover {
	lines := strings.Split(content, "\n")
	lineIdx := int(position.Line)
	if lineIdx >= len(lines) {
		return nil
	}

	line := lines[lineIdx]
	col := int(position.Character)
	if col > len(line) {
		col = len(line)
	}

	// Find ${{...}} expression at cursor position
	expr, startCol, endCol := findExpressionAt(line, col)
	if expr == "" {
		return nil
	}

	// Look up hover info
	markdown := lookupHoverInfo(expr, content, lines, lineIdx)
	if markdown == "" {
		return nil
	}

	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown,
		},
		Range: &protocol.Range{
			Start: protocol.Position{Line: uint32(lineIdx), Character: uint32(startCol)},
			End:   protocol.Position{Line: uint32(lineIdx), Character: uint32(endCol)},
		},
	}
}

// findExpressionAt finds a ${{...}} expression at the given column in a line.
// Returns the inner expression text, and the start/end columns of the full ${{...}} token.
func findExpressionAt(line string, col int) (expr string, start, end int) {
	// Search backwards from col for "${{" and forwards for "}}"
	// The cursor can be anywhere within the ${{...}} span.

	// Find all ${{...}} spans in the line and check if col falls within one
	searchFrom := 0
	for {
		openIdx := strings.Index(line[searchFrom:], "${{")
		if openIdx < 0 {
			break
		}
		openIdx += searchFrom
		closeIdx := strings.Index(line[openIdx:], "}}")
		if closeIdx < 0 {
			break
		}
		closeIdx += openIdx

		spanEnd := closeIdx + 2 // past the "}}"
		if col >= openIdx && col <= spanEnd {
			inner := strings.TrimSpace(line[openIdx+3 : closeIdx])
			return inner, openIdx, spanEnd
		}
		searchFrom = spanEnd
	}
	return "", 0, 0
}

// lookupHoverInfo returns markdown hover text for a melange variable expression.
func lookupHoverInfo(expr, content string, lines []string, lineIdx int) string {
	// Check built-in variables
	if desc, ok := builtinVarDescriptions[expr]; ok {
		return fmt.Sprintf("**`${{%s}}`**\n\nBuilt-in: %s", expr, desc)
	}

	// Check vars.* — user-defined variables
	if strings.HasPrefix(expr, "vars.") {
		varName := strings.TrimPrefix(expr, "vars.")
		cfg, err := parser.Parse(content)
		if err != nil {
			return fmt.Sprintf("`${{%s}}` — User-defined variable", expr)
		}
		if val, ok := cfg.Vars[varName]; ok {
			// Show value prominently
			return fmt.Sprintf("**%s**\n\n`${{%s}}` — User-defined variable", val, expr)
		}
		return fmt.Sprintf("`${{%s}}` — User-defined variable (not found in `vars:`)", expr)
	}

	// Check range.key / range.value
	if expr == "range.key" || expr == "range.value" {
		return lookupRangeHover(expr, content, lines, lineIdx)
	}

	return ""
}

// lookupRangeHover resolves range variable hover info by finding the enclosing
// range: directive and matching it to a data: entry.
func lookupRangeHover(expr, content string, lines []string, lineIdx int) string {
	// Walk backwards to find a "range:" directive
	rangeName := ""
	for i := lineIdx; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		// Handle both "range: X" and "- range: X" (list item)
		check := trimmed
		if strings.HasPrefix(check, "- ") {
			check = strings.TrimPrefix(check, "- ")
		}
		if strings.HasPrefix(check, "range:") {
			rangeName = strings.TrimSpace(strings.TrimPrefix(check, "range:"))
			break
		}
	}

	if rangeName == "" {
		return fmt.Sprintf("`${{%s}}` — Range iteration variable", expr)
	}

	// Parse the document to find the matching data entry
	cfg, err := parser.Parse(content)
	if err != nil {
		return fmt.Sprintf("`${{%s}}` — Iterates over `%s`", expr, rangeName)
	}

	for _, d := range cfg.Data {
		if d.Name == rangeName {
			keys := make([]string, 0, len(d.Items))
			for k := range d.Items {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			keyList := strings.Join(keys, ", ")
			// Show keys prominently
			return fmt.Sprintf("**Iterates over:** %s\n\n`${{%s}}` — Range key from `%s` data", keyList, expr, rangeName)
		}
	}

	return fmt.Sprintf("`${{%s}}` — Iterates over `%s`", expr, rangeName)
}

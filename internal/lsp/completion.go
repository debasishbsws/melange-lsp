package lsp

import (
	"strings"

	"melange-lsp/internal/schema"

	"go.lsp.dev/protocol"
)

// getCompletions returns completions for the given document and position.
func getCompletions(content string, position protocol.Position) []protocol.CompletionItem {
	lines := strings.Split(content, "\n")

	// Determine context from cursor position
	path := getYAMLPath(lines, int(position.Line), int(position.Character))

	// Get schema completions for this path
	schemaItems := schema.GetCompletionsForPath(path)

	var items []protocol.CompletionItem
	for _, item := range schemaItems {
		kind := protocol.CompletionItemKindProperty
		if item.Required {
			kind = protocol.CompletionItemKindField
		}

		// Add ": " suffix for key completions
		insertText := item.Label + ": "

		items = append(items, protocol.CompletionItem{
			Label:      item.Label,
			Kind:       kind,
			Detail:     item.Description,
			InsertText: insertText,
		})
	}

	return items
}

// getYAMLPath determines the YAML path at the given position.
// Returns a path like ["package"] or ["pipeline", "0", "uses"].
func getYAMLPath(lines []string, line, character int) []string {
	if line >= len(lines) {
		return nil
	}

	// Calculate indentation at cursor
	currentLine := ""
	if line < len(lines) {
		currentLine = lines[line]
	}
	cursorIndent := getIndentation(currentLine)

	// If we're at the start of a line (or just whitespace), use current indent
	// to determine what level we're completing at
	lineContent := strings.TrimSpace(currentLine)
	if lineContent == "" || !strings.Contains(lineContent, ":") {
		// We're starting a new key, find parent context
		return findParentPath(lines, line, cursorIndent)
	}

	// We're on a line with content, parse the path
	return findParentPath(lines, line, cursorIndent)
}

// findParentPath walks backwards to find the YAML path to the current position.
func findParentPath(lines []string, line, cursorIndent int) []string {
	var path []string

	// Track if we're inside a list item
	inListItem := false
	listIndex := 0

	// Walk backwards through lines to build path
	for i := line - 1; i >= 0; i-- {
		l := lines[i]
		indent := getIndentation(l)
		content := strings.TrimSpace(l)

		// Skip empty lines and comments
		if content == "" || strings.HasPrefix(content, "#") {
			continue
		}

		// Only consider lines with less indentation (parents)
		if indent >= cursorIndent {
			// Check if this is a list item at same level
			if strings.HasPrefix(content, "- ") && indent == cursorIndent-2 {
				inListItem = true
			}
			continue
		}

		// This line is a parent
		cursorIndent = indent

		// Check if it's a list item
		if strings.HasPrefix(content, "- ") {
			inListItem = true
			content = strings.TrimPrefix(content, "- ")
			listIndex++
		}

		// Extract the key name
		if colonIdx := strings.Index(content, ":"); colonIdx > 0 {
			key := content[:colonIdx]
			path = append([]string{key}, path...)
		}

		// If we're at root level (indent 0), stop
		if indent == 0 {
			break
		}
	}

	// If we detected we're in a list, add a placeholder index
	if inListItem && len(path) > 0 {
		path = append(path, "0")
	}

	return path
}

// getIndentation returns the number of leading spaces in a line.
func getIndentation(line string) int {
	count := 0
	for _, ch := range line {
		if ch == ' ' {
			count++
		} else if ch == '\t' {
			count += 2 // Treat tabs as 2 spaces
		} else {
			break
		}
	}
	return count
}

package lsp

import (
	"strings"

	"melange-lsp/internal/pipeline"
	"melange-lsp/internal/schema"

	"go.lsp.dev/protocol"
)

// getCompletions returns completions for the given document and position.
func getCompletions(content string, position protocol.Position, registry *pipeline.Registry) []protocol.CompletionItem {
	lines := strings.Split(content, "\n")
	line := int(position.Line)

	// Determine context from cursor position
	path := getYAMLPath(lines, line, int(position.Character))

	// Check for pipeline-specific completions
	if registry != nil {
		if items := getPipelineCompletions(lines, line, path, registry); items != nil {
			return items
		}
	}

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

// getPipelineCompletions returns completions for uses: values and with: keys,
// or nil if the cursor is not in a pipeline-specific context.
func getPipelineCompletions(lines []string, line int, path []string, registry *pipeline.Registry) []protocol.CompletionItem {
	if len(path) < 2 || path[0] != "pipeline" {
		return nil
	}

	// Check if cursor is on a "uses:" value line
	currentLine := ""
	if line < len(lines) {
		currentLine = strings.TrimSpace(lines[line])
	}

	// uses: value completion — path is ["pipeline", "0"] and line has "uses:"
	if strings.HasPrefix(currentLine, "uses:") || strings.HasPrefix(currentLine, "- uses:") {
		names := registry.ListPipelines()
		var items []protocol.CompletionItem
		for _, name := range names {
			items = append(items, protocol.CompletionItem{
				Label:  name,
				Kind:   protocol.CompletionItemKindValue,
				Detail: "Pipeline",
			})
		}
		return items
	}

	// with: key completion — path ends with "with" (i.e. ["pipeline", "0", "with"])
	if path[len(path)-1] == "with" {
		// Find the sibling "uses:" value in this pipeline step
		usesValue := findSiblingUses(lines, line)
		if usesValue == "" {
			return nil
		}
		info, err := registry.LoadPipeline(usesValue)
		if err != nil {
			return nil
		}
		return pipelineInputsToCompletions(info.Inputs)
	}

	return nil
}

// findSiblingUses searches for a "uses:" key at the same indentation level
// as the current with: block, by walking backwards from the given line.
func findSiblingUses(lines []string, line int) string {
	if line >= len(lines) {
		return ""
	}

	// Find the indentation of the with: block's parent
	// Walk backwards to find "with:" key, then continue to find "uses:" at same level
	withIndent := -1
	for i := line; i >= 0; i-- {
		content := strings.TrimSpace(lines[i])
		indent := getIndentation(lines[i])

		if content == "with:" || strings.HasPrefix(content, "with:") {
			withIndent = indent
			break
		}

		// If we hit something at lower indent, stop
		if indent < getIndentation(lines[line]) && content != "" {
			withIndent = indent
			break
		}
	}

	if withIndent < 0 {
		return ""
	}

	// Now walk backwards from with: to find uses: at same indent
	for i := line; i >= 0; i-- {
		content := strings.TrimSpace(lines[i])
		indent := getIndentation(lines[i])

		// Account for list item prefix
		rawContent := content
		if strings.HasPrefix(rawContent, "- ") {
			rawContent = strings.TrimPrefix(rawContent, "- ")
			// List item prefix means the key is 2 chars further right
			indent += 2
		}

		if indent == withIndent && strings.HasPrefix(rawContent, "uses:") {
			val := strings.TrimSpace(strings.TrimPrefix(rawContent, "uses:"))
			return val
		}

		// Stop if we go above the pipeline step
		if indent < withIndent && content != "" && !strings.HasPrefix(content, "#") {
			break
		}
	}

	return ""
}

// pipelineInputsToCompletions converts pipeline inputs to completion items.
func pipelineInputsToCompletions(inputs map[string]pipeline.InputInfo) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	for name, input := range inputs {
		kind := protocol.CompletionItemKindProperty
		if input.Required {
			kind = protocol.CompletionItemKindField
		}

		detail := input.Description
		if input.Default != "" {
			detail += " (default: " + input.Default + ")"
		}

		items = append(items, protocol.CompletionItem{
			Label:      name,
			Kind:       kind,
			Detail:     detail,
			InsertText: name + ": ",
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

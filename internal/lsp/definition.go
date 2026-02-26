package lsp

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"melange-lsp/internal/pipeline"
	"melange-lsp/internal/schema"

	"go.lsp.dev/protocol"
)

// getDefinition returns go-to-definition locations for pipeline uses: values,
// with: keys, and generic YAML keys (via schema.json fallback).
func getDefinition(content string, position protocol.Position, registry *pipeline.Registry, dirs []string) []protocol.Location {
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

	if registry != nil {
		// Case 1: cursor on a uses: line — jump to the pipeline file
		if loc := getUsesDefinition(line, lines, lineIdx, registry, dirs); loc != nil {
			return []protocol.Location{*loc}
		}

		// Case 2: cursor on a with: key — jump to that input's definition in the pipeline file
		if loc := getWithKeyDefinition(line, col, lines, lineIdx, registry, dirs); loc != nil {
			return []protocol.Location{*loc}
		}
	}

	// Case 3: cursor on any YAML key — jump to its definition in schema.json
	if loc := getSchemaDefinition(line, col, lines, lineIdx); loc != nil {
		return []protocol.Location{*loc}
	}

	return nil
}

// getSchemaDefinition checks if the cursor is on a YAML key and returns a
// location pointing to that property's definition in the embedded schema.json.
func getSchemaDefinition(line string, col int, lines []string, lineIdx int) *protocol.Location {
	schemaPath := schema.SchemaFilePath()
	if schemaPath == "" {
		return nil
	}

	// Extract the key name from the current line
	stripped := strings.TrimLeft(line, " \t")
	if stripped == "" || strings.HasPrefix(stripped, "#") {
		return nil
	}

	work := stripped
	if strings.HasPrefix(work, "- ") {
		work = work[2:]
	}

	colonIdx := strings.Index(work, ":")
	if colonIdx <= 0 {
		return nil
	}
	keyName := work[:colonIdx]

	// Cursor must be on or before the key portion
	origKeyStart := strings.Index(line, keyName)
	origColonPos := origKeyStart + len(keyName)
	if col > origColonPos {
		return nil
	}

	// Determine the YAML path to the parent context
	cursorIndent := getIndentation(line)
	path := findParentPath(lines, lineIdx, cursorIndent)

	// Look up the line number in schema.json
	schemaLine := schema.FindPropertyLine(path, keyName)
	if schemaLine < 0 {
		return nil
	}

	return &protocol.Location{
		URI: protocol.DocumentURI(fmt.Sprintf("file://%s", schemaPath)),
		Range: protocol.Range{
			Start: protocol.Position{Line: uint32(schemaLine), Character: 0},
			End:   protocol.Position{Line: uint32(schemaLine), Character: 0},
		},
	}
}

// getUsesDefinition checks if the current line is a uses: line and returns
// a location pointing to the top of the pipeline YAML file.
func getUsesDefinition(line string, lines []string, lineIdx int, registry *pipeline.Registry, dirs []string) *protocol.Location {
	stripped := strings.TrimSpace(line)
	if strings.HasPrefix(stripped, "- ") {
		stripped = stripped[2:]
	}
	if !strings.HasPrefix(stripped, "uses:") {
		return nil
	}

	usesValue := strings.TrimSpace(strings.TrimPrefix(stripped, "uses:"))
	if usesValue == "" {
		return nil
	}

	path, err := registry.ResolvePipelinePath(usesValue, dirs)
	if err != nil {
		return nil
	}

	return &protocol.Location{
		URI: protocol.DocumentURI(fmt.Sprintf("file://%s", path)),
		Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End:   protocol.Position{Line: 0, Character: 0},
		},
	}
}

// getWithKeyDefinition checks if the cursor is on a key inside a with: block
// and returns a location pointing to that input's definition in the pipeline file.
func getWithKeyDefinition(line string, col int, lines []string, lineIdx int, registry *pipeline.Registry, dirs []string) *protocol.Location {
	stripped := strings.TrimLeft(line, " \t")
	if stripped == "" || strings.HasPrefix(stripped, "#") {
		return nil
	}

	work := stripped
	if strings.HasPrefix(work, "- ") {
		work = work[2:]
	}

	colonIdx := strings.Index(work, ":")
	if colonIdx <= 0 {
		return nil
	}

	keyName := work[:colonIdx]

	// Cursor must be on the key portion
	origKeyStart := strings.Index(line, keyName)
	origColonPos := origKeyStart + len(keyName)
	if col > origColonPos {
		return nil
	}

	// Check if we're inside a with: block
	lineIndent := getIndentation(line)
	inWithBlock := false
	for i := lineIdx - 1; i >= 0; i-- {
		l := lines[i]
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := getIndentation(l)
		if indent < lineIndent {
			parentKey := trimmed
			if strings.HasPrefix(parentKey, "- ") {
				parentKey = parentKey[2:]
			}
			if strings.HasPrefix(parentKey, "with:") {
				inWithBlock = true
			}
			break
		}
	}

	if !inWithBlock {
		return nil
	}

	usesValue := findSiblingUses(lines, lineIdx)
	if usesValue == "" {
		return nil
	}

	path, err := registry.ResolvePipelinePath(usesValue, dirs)
	if err != nil {
		return nil
	}

	// Find the line number of this input in the pipeline file
	inputLine := findInputLineInFile(path, keyName)

	return &protocol.Location{
		URI: protocol.DocumentURI(fmt.Sprintf("file://%s", path)),
		Range: protocol.Range{
			Start: protocol.Position{Line: uint32(inputLine), Character: 0},
			End:   protocol.Position{Line: uint32(inputLine), Character: 0},
		},
	}
}

// findInputLineInFile scans a pipeline YAML file for an input key definition
// under the inputs: section and returns its 0-based line number.
// Returns 0 if the key is not found.
func findInputLineInFile(path, keyName string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	inInputs := false
	inputsIndent := -1

	target := keyName + ":"

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		indent := getIndentation(line)

		if trimmed == "inputs:" || strings.HasPrefix(trimmed, "inputs:") {
			inInputs = true
			inputsIndent = indent
			lineNum++
			continue
		}

		if inInputs {
			// If we hit a line at same or lower indent as inputs:, we've left the section
			if trimmed != "" && indent <= inputsIndent {
				break
			}
			// Check if this is our target key (direct child of inputs:)
			if indent == inputsIndent+2 && (trimmed == target || strings.HasPrefix(trimmed, target)) {
				return lineNum
			}
		}

		lineNum++
	}

	return 0
}

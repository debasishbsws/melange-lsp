package lsp

import (
	"strings"

	"go.lsp.dev/protocol"
)

// getCodeLenses returns CodeLens items for var-transforms `to:` lines,
// showing the resolved value of each transformed variable.
func getCodeLenses(content string) []protocol.CodeLens {
	subMap := buildSubstitutionMap(content)
	if subMap == nil {
		return nil
	}

	lines := strings.Split(content, "\n")

	// Find `to:` lines inside var-transforms sections and look up resolved values
	var lenses []protocol.CodeLens
	inVarTransforms := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Detect var-transforms section
		if trimmed == "var-transforms:" {
			inVarTransforms = true
			continue
		}

		// Exit var-transforms when we hit a non-indented, non-empty line
		// that isn't a list continuation
		if inVarTransforms && trimmed != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			inVarTransforms = false
		}

		if !inVarTransforms {
			continue
		}

		// Match `to: <name>` lines (with optional leading `- `)
		if strings.HasPrefix(trimmed, "to:") || strings.HasPrefix(trimmed, "- to:") {
			varName := trimmed
			if strings.HasPrefix(varName, "- ") {
				varName = varName[2:]
			}
			varName = strings.TrimPrefix(varName, "to:")
			varName = strings.TrimSpace(varName)

			if varName == "" {
				continue
			}

			key := "vars." + varName
			if val, ok := subMap[key]; ok {
				lineLen := uint32(len(line))
				lenses = append(lenses, protocol.CodeLens{
					Range: protocol.Range{
						Start: protocol.Position{Line: uint32(i), Character: 0},
						End:   protocol.Position{Line: uint32(i), Character: lineLen},
					},
					Command: &protocol.Command{
						Title:   "→ " + val,
						Command: "",
					},
				})
			}
		}
	}

	return lenses
}

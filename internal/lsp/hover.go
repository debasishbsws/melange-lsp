package lsp

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"

	"melange-lsp/internal/parser"
	"melange-lsp/internal/pipeline"

	"chainguard.dev/melange/pkg/config"
	"github.com/psanford/memfs"
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
// It provides info when the cursor is on a ${{...}} expression, a pipeline
// with: key, or a YAML key/value containing template expressions.
func getHover(content string, position protocol.Position, registry *pipeline.Registry, dirs []string) *protocol.Hover {
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

	// Check for pipeline-specific hovers before template resolution
	if registry != nil {
		if markdown := getUsesHover(line, registry, dirs); markdown != "" {
			return &protocol.Hover{
				Contents: protocol.MarkupContent{
					Kind:  protocol.Markdown,
					Value: markdown,
				},
			}
		}
		if markdown := getPipelineInputHover(line, col, lines, lineIdx, registry, dirs); markdown != "" {
			return &protocol.Hover{
				Contents: protocol.MarkupContent{
					Kind:  protocol.Markdown,
					Value: markdown,
				},
			}
		}
	}

	// Build substitution map from melange's parser for resolved values
	subMap := buildSubstitutionMap(content)

	// Find ${{...}} expression at cursor position
	expr, startCol, endCol := findExpressionAt(line, col)
	if expr != "" {
		// Look up hover info for the expression
		markdown := lookupHoverInfo(expr, content, lines, lineIdx, subMap)
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

	// Fallback: check if cursor is on a YAML key or value containing templates
	markdown := getKeyValueHover(line, col, content, lines, lineIdx, subMap)
	if markdown == "" {
		markdown = getValueHover(line, col, content, lines, lineIdx, subMap)
	}
	if markdown == "" {
		return nil
	}

	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown,
		},
	}
}

// getUsesHover returns hover documentation when the cursor is on a uses: line
// in a pipeline step. It loads the pipeline definition and shows the name,
// inputs summary, and runs script.
func getUsesHover(line string, registry *pipeline.Registry, dirs []string) string {
	stripped := strings.TrimSpace(line)
	if strings.HasPrefix(stripped, "- ") {
		stripped = stripped[2:]
	}
	if !strings.HasPrefix(stripped, "uses:") {
		return ""
	}

	usesValue := strings.TrimSpace(strings.TrimPrefix(stripped, "uses:"))
	if usesValue == "" {
		return ""
	}

	info, err := registry.LoadPipeline(usesValue, dirs)
	if err != nil {
		return ""
	}

	header := fmt.Sprintf("**`%s`**", usesValue)
	if info.Name != "" {
		header = fmt.Sprintf("**%s** (`%s`)", info.Name, usesValue)
	}

	raw := strings.TrimSpace(info.Raw)
	if raw == "" {
		return header
	}

	return header + "\n\n```yaml\n" + raw + "\n```"
}

// getPipelineInputHover returns hover documentation for a with: key in a
// pipeline step. It detects if the cursor is on a key inside a with: block,
// looks up the sibling uses: value, loads the pipeline, and returns the input's
// description, required/optional status, and default value.
func getPipelineInputHover(line string, col int, lines []string, lineIdx int, registry *pipeline.Registry, dirs []string) string {
	// Check if this line looks like a key: value inside a with: block
	stripped := strings.TrimLeft(line, " \t")
	if stripped == "" || strings.HasPrefix(stripped, "#") {
		return ""
	}

	// Strip list prefix if present
	work := stripped
	if strings.HasPrefix(work, "- ") {
		work = work[2:]
	}

	colonIdx := strings.Index(work, ":")
	if colonIdx <= 0 {
		return ""
	}

	keyName := work[:colonIdx]

	// Cursor must be on the key portion
	origKeyStart := strings.Index(line, keyName)
	origColonPos := origKeyStart + len(keyName)
	if col > origColonPos {
		return ""
	}

	// Check if we're inside a with: block by walking backwards
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
			// This is a parent — check if it's "with:"
			parentKey := strings.TrimSpace(l)
			if strings.HasPrefix(parentKey, "- ") {
				parentKey = parentKey[2:]
			}
			if strings.HasPrefix(parentKey, "with:") {
				inWithBlock = true
			}
			break
		}
		if indent == lineIndent {
			// Sibling line, keep looking
			continue
		}
	}

	if !inWithBlock {
		return ""
	}

	// Find the sibling uses: value
	usesValue := findSiblingUses(lines, lineIdx)
	if usesValue == "" {
		return ""
	}

	info, err := registry.LoadPipeline(usesValue, dirs)
	if err != nil {
		return ""
	}

	input, ok := info.Inputs[keyName]
	if !ok {
		return ""
	}

	// Build markdown documentation
	var parts []string
	parts = append(parts, fmt.Sprintf("**`%s`** — %s pipeline input", keyName, usesValue))
	if input.Description != "" {
		parts = append(parts, input.Description)
	}
	if input.Required {
		parts = append(parts, "*Required*")
	} else {
		parts = append(parts, "*Optional*")
	}
	if input.Default != "" {
		parts = append(parts, fmt.Sprintf("Default: `%s`", input.Default))
	}

	return strings.Join(parts, "\n\n")
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

// buildSubstitutionMap uses melange's ParseConfiguration to build a map of
// variable names to their resolved values. Returns nil if parsing fails.
func buildSubstitutionMap(content string) map[string]string {
	fs := memfs.New()
	filename := "melange.yaml"
	if err := fs.WriteFile(filename, []byte(content), 0644); err != nil {
		log.Printf("melange-lsp: hover: failed to write to memfs: %v", err)
		return nil
	}

	ctx := context.Background()
	cfg, err := config.ParseConfiguration(ctx, filename, config.WithFS(fs))
	if err != nil {
		log.Printf("melange-lsp: hover: ParseConfiguration failed: %v", err)
		return nil
	}

	result := map[string]string{
		"package.name":         cfg.Package.Name,
		"package.version":      cfg.Package.Version,
		"package.epoch":        strconv.FormatUint(cfg.Package.Epoch, 10),
		"package.full-version": fmt.Sprintf("%s-r%d", cfg.Package.Version, cfg.Package.Epoch),
		"package.description":  cfg.Package.Description,
	}

	// Get vars (including cross-var references)
	varsMap, err := cfg.GetVarsFromConfig()
	if err != nil {
		log.Printf("melange-lsp: hover: GetVarsFromConfig failed: %v", err)
		return result
	}

	// Apply var-transforms
	if err := cfg.PerformVarSubstitutions(varsMap); err != nil {
		log.Printf("melange-lsp: hover: PerformVarSubstitutions failed: %v", err)
	}

	// Convert from "${{vars.foo}}" keys to "vars.foo" keys
	for k, v := range varsMap {
		clean := strings.TrimPrefix(k, "${{")
		clean = strings.TrimSuffix(clean, "}}")
		result[clean] = v
	}

	return result
}

// lookupHoverInfo returns markdown hover text for a melange variable expression.
func lookupHoverInfo(expr, content string, lines []string, lineIdx int, subMap map[string]string) string {
	// Check built-in variables
	if desc, ok := builtinVarDescriptions[expr]; ok {
		if val, hasVal := subMap[expr]; hasVal && val != "" {
			return fmt.Sprintf("**%s**\n\n`${{%s}}` — %s", val, expr, desc)
		}
		return fmt.Sprintf("**`${{%s}}`**\n\nBuilt-in: %s", expr, desc)
	}

	// Check vars.* — user-defined variables
	if strings.HasPrefix(expr, "vars.") {
		if val, hasVal := subMap[expr]; hasVal {
			return fmt.Sprintf("**%s**\n\n`${{%s}}` — User-defined variable", val, expr)
		}
		// Fallback to lightweight parser
		varName := strings.TrimPrefix(expr, "vars.")
		cfg, err := parser.Parse(content)
		if err != nil {
			return fmt.Sprintf("`${{%s}}` — User-defined variable", expr)
		}
		if val, ok := cfg.Vars[varName]; ok {
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

// resolveTemplateString replaces all ${{...}} tokens in raw with values from subMap.
func resolveTemplateString(raw string, subMap map[string]string) string {
	result := raw
	for {
		openIdx := strings.Index(result, "${{")
		if openIdx < 0 {
			break
		}
		closeIdx := strings.Index(result[openIdx:], "}}")
		if closeIdx < 0 {
			break
		}
		closeIdx += openIdx
		key := strings.TrimSpace(result[openIdx+3 : closeIdx])
		if val, ok := subMap[key]; ok {
			result = result[:openIdx] + val + result[closeIdx+2:]
		} else {
			// Leave unresolved expressions as-is; skip past to avoid infinite loop
			result = result[:openIdx] + "${{" + key + "}}" + result[closeIdx+2:]
			break
		}
	}
	return result
}

// getKeyValueHover returns hover markdown when the cursor is on a YAML key
// whose value contains ${{...}} template expressions.
func getKeyValueHover(line string, col int, content string, lines []string, lineIdx int, subMap map[string]string) string {
	// Strip leading whitespace and optional list prefix "- "
	stripped := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(stripped, "- ") {
		stripped = strings.TrimPrefix(stripped, "- ")
	}

	// Must be a key: value line
	colonIdx := strings.Index(stripped, ":")
	if colonIdx < 0 {
		return ""
	}

	key := stripped[:colonIdx]
	if key == "" {
		return ""
	}

	// Find the colon position in the original line
	origColonIdx := strings.Index(line, key+":") + len(key)
	// Only trigger if cursor is on/before the colon (on the key portion)
	if col > origColonIdx {
		return ""
	}

	// Extract raw value (everything after "key: ")
	rawValue := strings.TrimSpace(stripped[colonIdx+1:])
	if rawValue == "" {
		return ""
	}

	// Handle YAML block scalars (| or >) — collect indented continuation lines
	if rawValue == "|" || rawValue == ">" || rawValue == "|-" || rawValue == ">-" {
		// Determine the indentation of the block content
		var blockLines []string
		blockIndent := -1
		for i := lineIdx + 1; i < len(lines); i++ {
			l := lines[i]
			if strings.TrimSpace(l) == "" {
				blockLines = append(blockLines, "")
				continue
			}
			indent := len(l) - len(strings.TrimLeft(l, " \t"))
			if blockIndent < 0 {
				blockIndent = indent
			}
			if indent < blockIndent {
				break
			}
			blockLines = append(blockLines, l[blockIndent:])
		}
		rawValue = strings.Join(blockLines, "\n")
	}

	// Only show hover if value contains template expressions
	if !strings.Contains(rawValue, "${{") {
		return ""
	}

	// Check if we're inside a range: context
	rangeName := findEnclosingRange(lines, lineIdx)

	if rangeName != "" {
		// In a range context: expand for each data item
		cfg, err := parser.Parse(content)
		if err == nil {
			for _, d := range cfg.Data {
				if d.Name == rangeName {
					keys := make([]string, 0, len(d.Items))
					for k := range d.Items {
						keys = append(keys, k)
					}
					sort.Strings(keys)

					var items []string
					for _, k := range keys {
						// Build a per-item subMap with range.key and range.value
						localMap := make(map[string]string, len(subMap)+2)
						for mk, mv := range subMap {
							localMap[mk] = mv
						}
						localMap["range.key"] = k
						localMap["range.value"] = d.Items[k]
						resolved := resolveTemplateString(rawValue, localMap)
						items = append(items, formatResolvedItem(resolved))
					}
					return fmt.Sprintf("**Resolved values of `%s`:**\n\n%s", key, strings.Join(items, "\n"))
				}
			}
		}
	}

	// Not in range context: resolve directly
	resolved := resolveTemplateString(rawValue, subMap)
	return formatResolvedHover(key, resolved)
}

// getValueHover returns hover markdown when the cursor is on a YAML value
// (the value side of "key: value", or a bare "- value" list item) that
// contains ${{...}} template expressions.
func getValueHover(line string, col int, content string, lines []string, lineIdx int, subMap map[string]string) string {
	stripped := strings.TrimLeft(line, " \t")

	var rawValue string
	var label string

	// Check if this is a "- value" bare list item (no colon in the value)
	if strings.HasPrefix(stripped, "- ") {
		afterDash := stripped[2:]
		// If there's a colon, it's a key:value — handled below
		if !strings.Contains(afterDash, ":") {
			rawValue = strings.TrimSpace(afterDash)
			label = "" // bare list item, no key name
		}
	}

	// Check if cursor is on the value side of a "key: value" line
	if rawValue == "" {
		work := stripped
		if strings.HasPrefix(work, "- ") {
			work = work[2:]
		}
		colonIdx := strings.Index(work, ":")
		if colonIdx < 0 {
			return ""
		}
		key := work[:colonIdx]
		// Find colon position in original line
		origColonIdx := strings.Index(line, key+":") + len(key)
		// Only trigger if cursor is AFTER the colon (on the value portion)
		if col <= origColonIdx {
			return ""
		}
		rawValue = strings.TrimSpace(work[colonIdx+1:])
		label = key
	}

	if rawValue == "" {
		return ""
	}

	// Handle block scalars — cursor must be on the key: | line itself
	if rawValue == "|" || rawValue == ">" || rawValue == "|-" || rawValue == ">-" {
		return "" // block scalar value hover is handled via getKeyValueHover on the key
	}

	if !strings.Contains(rawValue, "${{") {
		return ""
	}

	// Find enclosing range context
	rangeName := findEnclosingRange(lines, lineIdx)

	if rangeName != "" {
		cfg, err := parser.Parse(content)
		if err == nil {
			for _, d := range cfg.Data {
				if d.Name == rangeName {
					keys := make([]string, 0, len(d.Items))
					for k := range d.Items {
						keys = append(keys, k)
					}
					sort.Strings(keys)

					var items []string
					for _, k := range keys {
						localMap := make(map[string]string, len(subMap)+2)
						for mk, mv := range subMap {
							localMap[mk] = mv
						}
						localMap["range.key"] = k
						localMap["range.value"] = d.Items[k]
						resolved := resolveTemplateString(rawValue, localMap)
						items = append(items, formatResolvedItem(resolved))
					}
					if label != "" {
						return fmt.Sprintf("**Resolved values of `%s`:**\n\n%s", label, strings.Join(items, "\n"))
					}
					return fmt.Sprintf("**Resolved values:**\n\n%s", strings.Join(items, "\n"))
				}
			}
		}
	}

	resolved := resolveTemplateString(rawValue, subMap)
	if label != "" {
		return formatResolvedHover(label, resolved)
	}
	return fmt.Sprintf("**`%s`**\n\nResolved value", resolved)
}

// findEnclosingRange walks backwards from lineIdx to find a "range:" directive.
// Returns the range name or "" if not in a range context.
func findEnclosingRange(lines []string, lineIdx int) string {
	for i := lineIdx; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		check := trimmed
		if strings.HasPrefix(check, "- ") {
			check = check[2:]
		}
		if strings.HasPrefix(check, "range:") {
			return strings.TrimSpace(strings.TrimPrefix(check, "range:"))
		}
	}
	return ""
}

// formatResolvedHover formats a single resolved value for hover display.
func formatResolvedHover(key, resolved string) string {
	if strings.Contains(resolved, "\n") {
		return fmt.Sprintf("**Resolved value of `%s`:**\n\n```\n%s\n```", key, resolved)
	}
	return fmt.Sprintf("**%s**\n\nResolved value of `%s`", resolved, key)
}

// formatResolvedItem formats a resolved value as a list item for range hover.
func formatResolvedItem(resolved string) string {
	if strings.Contains(resolved, "\n") {
		return fmt.Sprintf("- ```\n%s\n```", resolved)
	}
	return fmt.Sprintf("- `%s`", resolved)
}

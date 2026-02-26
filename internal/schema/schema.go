package schema

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed schema.json
var schemaJSON []byte

// CompletionItem represents a completion suggestion.
type CompletionItem struct {
	Label       string
	Description string
	Required    bool
}

// jsonSchema represents the top-level JSON Schema structure.
type jsonSchema struct {
	Defs map[string]jsonSchemaDef `json:"$defs"`
}

// jsonSchemaDef represents a single type definition in the schema.
type jsonSchemaDef struct {
	Properties map[string]jsonSchemaProperty `json:"properties"`
	Required   []string                      `json:"required"`
}

// jsonSchemaProperty represents a property within a type definition.
type jsonSchemaProperty struct {
	Ref         string             `json:"$ref"`
	Description string             `json:"description"`
	Type        string             `json:"type"`
	Items       *jsonSchemaItemRef `json:"items"`
}

// jsonSchemaItemRef represents the items field for array types.
type jsonSchemaItemRef struct {
	Ref string `json:"$ref"`
}

var parsed jsonSchema

// lineIndex maps "DefinitionName.propertyName" to a 0-based line number in schema.json.
var lineIndex map[string]int

func init() {
	if err := json.Unmarshal(schemaJSON, &parsed); err != nil {
		panic("schema: failed to parse embedded schema.json: " + err.Error())
	}
	lineIndex = buildLineIndex()
}

// buildLineIndex scans the raw schema.json text to map each property to its
// line number. It looks for patterns like `"propName": {` within `"DefName": {`
// → `"properties": {` blocks.
func buildLineIndex() map[string]int {
	idx := make(map[string]int)
	lines := strings.Split(string(schemaJSON), "\n")

	var currentDef string
	inProperties := false
	braceDepth := 0

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track definition names: top-level keys inside $defs
		// Pattern: "DefinitionName": {
		if currentDef == "" && strings.HasSuffix(trimmed, ": {") {
			name := strings.Trim(strings.TrimSuffix(trimmed, ": {"), "\"")
			if name != "$schema" && name != "$id" && name != "$ref" && name != "$defs" {
				currentDef = name
				braceDepth = 1
				inProperties = false
				continue
			}
		}

		if currentDef == "" {
			continue
		}

		// Count braces to know when we leave the definition
		for _, ch := range trimmed {
			if ch == '{' {
				braceDepth++
			} else if ch == '}' {
				braceDepth--
			}
		}

		if braceDepth <= 0 {
			currentDef = ""
			inProperties = false
			continue
		}

		// Detect "properties": {
		if !inProperties && (trimmed == "\"properties\": {") {
			inProperties = true
			continue
		}

		// Inside properties, look for property keys at the right depth
		if inProperties && braceDepth == 3 {
			// Pattern: "propertyName": { or "propertyName": "..."
			if colonIdx := strings.Index(trimmed, "\": "); colonIdx > 0 && strings.HasPrefix(trimmed, "\"") {
				propName := trimmed[1:colonIdx]
				idx[currentDef+"."+propName] = i
			}
		}
	}

	return idx
}

var (
	schemaFileOnce sync.Once
	schemaFilePath string
)

// SchemaFilePath returns the on-disk path to schema.json, writing the embedded
// content to a cache directory if needed. This allows go-to-definition to open
// the file in the editor.
func SchemaFilePath() string {
	schemaFileOnce.Do(func() {
		dir := filepath.Join(os.TempDir(), "melange-lsp")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		p := filepath.Join(dir, "schema.json")
		if err := os.WriteFile(p, schemaJSON, 0o644); err != nil {
			return
		}
		schemaFilePath = p
	})
	return schemaFilePath
}

// FindPropertyLine resolves a YAML path and key name to a 0-based line number
// in schema.json. Returns -1 if the property cannot be found.
func FindPropertyLine(path []string, key string) int {
	// Resolve the path to a definition name.
	defName := resolvePathToDefinition(path)
	if defName == "" {
		return -1
	}

	line, ok := lineIndex[defName+"."+key]
	if !ok {
		return -1
	}
	return line
}

// resolvePathToDefinition walks the YAML path and returns the schema definition
// name that contains the properties at that level.
func resolvePathToDefinition(path []string) string {
	defName := "Configuration"
	def, ok := parsed.Defs[defName]
	if !ok {
		return ""
	}

	for _, segment := range path {
		if isNumeric(segment) {
			continue
		}

		prop, ok := def.Properties[segment]
		if !ok {
			return ""
		}

		var targetDef string
		if prop.Ref != "" {
			targetDef = resolveRef(prop.Ref)
		} else if prop.Type == "array" && prop.Items != nil && prop.Items.Ref != "" {
			targetDef = resolveRef(prop.Items.Ref)
		} else {
			return ""
		}

		if targetDef == "" {
			return ""
		}

		next, ok := parsed.Defs[targetDef]
		if !ok {
			return ""
		}
		defName = targetDef
		def = next
	}

	return defName
}

// resolveRef extracts the definition name from a $ref like "#/$defs/Package".
func resolveRef(ref string) string {
	const prefix = "#/$defs/"
	if strings.HasPrefix(ref, prefix) {
		return ref[len(prefix):]
	}
	return ""
}

// isNumeric reports whether s consists entirely of digits.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// GetCompletionsForPath returns completions for a given YAML path.
// Path is a list of keys from root, e.g., ["package"] or ["pipeline", "0"].
func GetCompletionsForPath(path []string) []CompletionItem {
	def, ok := parsed.Defs["Configuration"]
	if !ok {
		return nil
	}

	// Walk the path to resolve to the target definition.
	for i := 0; i < len(path); i++ {
		segment := path[i]

		// Skip numeric indices — they were already handled when we
		// resolved the array's items ref in the previous iteration.
		if isNumeric(segment) {
			continue
		}

		prop, ok := def.Properties[segment]
		if !ok {
			return nil
		}

		// Determine the target definition name.
		var targetDef string

		if prop.Ref != "" {
			// Direct $ref (e.g., "package" → "#/$defs/Package")
			targetDef = resolveRef(prop.Ref)
		} else if prop.Type == "array" && prop.Items != nil && prop.Items.Ref != "" {
			// Array with $ref items (e.g., "pipeline" → items.$ref)
			targetDef = resolveRef(prop.Items.Ref)
		} else {
			return nil
		}

		if targetDef == "" {
			return nil
		}

		next, ok := parsed.Defs[targetDef]
		if !ok {
			return nil
		}
		def = next
	}

	return defToCompletions(def)
}

// defToCompletions converts a schema definition's properties into CompletionItems.
func defToCompletions(def jsonSchemaDef) []CompletionItem {
	requiredSet := make(map[string]bool, len(def.Required))
	for _, r := range def.Required {
		requiredSet[r] = true
	}

	items := make([]CompletionItem, 0, len(def.Properties))
	for name, prop := range def.Properties {
		items = append(items, CompletionItem{
			Label:       name,
			Description: prop.Description,
			Required:    requiredSet[name],
		})
	}
	return items
}

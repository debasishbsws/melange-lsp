package schema

import (
	_ "embed"
	"encoding/json"
	"strings"
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

func init() {
	if err := json.Unmarshal(schemaJSON, &parsed); err != nil {
		panic("schema: failed to parse embedded schema.json: " + err.Error())
	}
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

package schema

import (
	"reflect"
	"strings"

	"chainguard.dev/melange/pkg/config"
)

// CompletionItem represents a completion suggestion.
type CompletionItem struct {
	Label       string
	Description string
	Required    bool
}

// FieldInfo contains information about a struct field.
type FieldInfo struct {
	Name        string
	YAMLName    string
	Description string
	Required    bool
	Type        reflect.Type
}

// GetTopLevelKeys returns completion items for top-level configuration keys.
func GetTopLevelKeys() []CompletionItem {
	return getStructFields(reflect.TypeOf(config.Configuration{}))
}

// GetPackageKeys returns completion items for package section keys.
func GetPackageKeys() []CompletionItem {
	return getStructFields(reflect.TypeOf(config.Package{}))
}

// GetPipelineKeys returns completion items for pipeline section keys.
func GetPipelineKeys() []CompletionItem {
	return getStructFields(reflect.TypeOf(config.Pipeline{}))
}

// GetSubpackageKeys returns completion items for subpackage section keys.
func GetSubpackageKeys() []CompletionItem {
	return getStructFields(reflect.TypeOf(config.Subpackage{}))
}

// getStructFields extracts field information from a struct type.
func getStructFields(t reflect.Type) []CompletionItem {
	var items []CompletionItem

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// Skip unexported fields
		if !field.IsExported() {
			continue
		}

		yamlTag := field.Tag.Get("yaml")
		if yamlTag == "" || yamlTag == "-" {
			continue
		}

		// Parse yaml tag (format: "name,omitempty")
		parts := strings.Split(yamlTag, ",")
		yamlName := parts[0]

		// Check if field is required (no omitempty)
		required := true
		for _, part := range parts[1:] {
			if part == "omitempty" {
				required = false
				break
			}
		}

		// Get description from json tag comments or field name
		description := getFieldDescription(field)

		items = append(items, CompletionItem{
			Label:       yamlName,
			Description: description,
			Required:    required,
		})
	}

	return items
}

// getFieldDescription returns a description for a field.
func getFieldDescription(field reflect.StructField) string {
	// Use the field name as a basic description
	// In the future, we could parse comments from source or use a separate doc file
	name := field.Name

	// Add type info for complex types
	typeName := field.Type.String()
	if strings.HasPrefix(typeName, "[]") {
		return name + " (list)"
	}
	if strings.HasPrefix(typeName, "map[") {
		return name + " (map)"
	}
	if field.Type.Kind() == reflect.Struct || field.Type.Kind() == reflect.Ptr {
		return name + " (object)"
	}

	return name
}

// GetCompletionsForPath returns completions for a given YAML path.
// Path is a list of keys from root, e.g., ["package"] or ["pipeline", "0"].
func GetCompletionsForPath(path []string) []CompletionItem {
	if len(path) == 0 {
		return GetTopLevelKeys()
	}

	switch path[0] {
	case "package":
		return GetPackageKeys()
	case "pipeline":
		// Inside a pipeline item
		if len(path) >= 2 {
			return GetPipelineKeys()
		}
		return nil
	case "subpackages":
		// Inside a subpackage item
		if len(path) >= 2 {
			return GetSubpackageKeys()
		}
		return nil
	default:
		return nil
	}
}

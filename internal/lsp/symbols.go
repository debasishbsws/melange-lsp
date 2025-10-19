package lsp

import (
	"melange-lsp/internal/parser"

	"go.lsp.dev/protocol"
)

// parseDocumentSymbols parses the document content and returns symbols for the outline view.
func parseDocumentSymbols(content string) []interface{} {
	config, err := parser.Parse(content)
	if err != nil {
		// If parsing fails, return empty symbols
		return nil
	}

	var symbols []interface{}

	// Add package symbol
	if config.Package.Name != "" {
		symbols = append(symbols, protocol.DocumentSymbol{
			Name:   config.Package.Name,
			Kind:   protocol.SymbolKindPackage,
			Range:  protocol.Range{}, // TODO: track actual positions
			SelectionRange: protocol.Range{},
			Detail: config.Package.Version,
		})
	}

	// Add pipeline symbols
	for i, step := range config.Pipeline {
		name := step.Name
		if name == "" && step.Uses != "" {
			name = step.Uses
		} else if name == "" {
			name = "pipeline step"
		}

		symbols = append(symbols, protocol.DocumentSymbol{
			Name:           name,
			Kind:           protocol.SymbolKindFunction,
			Range:          protocol.Range{},
			SelectionRange: protocol.Range{},
			Detail:         step.Uses,
		})
		_ = i // Use i if we implement proper ranges later
	}

	// Add subpackage symbols
	for _, subpkg := range config.Subpackages {
		symbols = append(symbols, protocol.DocumentSymbol{
			Name:           subpkg.Name,
			Kind:           protocol.SymbolKindModule,
			Range:          protocol.Range{},
			SelectionRange: protocol.Range{},
		})
	}

	return symbols
}

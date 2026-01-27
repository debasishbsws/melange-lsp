package lsp

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"chainguard.dev/melange/pkg/config"
	"github.com/psanford/memfs"
	"go.lsp.dev/protocol"
)

// ValidateDocument validates a melange YAML document and returns diagnostics.
func ValidateDocument(uri protocol.DocumentURI, content string) (diagnostics []protocol.Diagnostic) {
	// Recover from any panics that melange might throw
	defer func() {
		if r := recover(); r != nil {
			log.Printf("melange-lsp: panic during validation: %v", r)
			diagnostics = append(diagnostics, protocol.Diagnostic{
				Range: protocol.Range{
					Start: protocol.Position{Line: 0, Character: 0},
					End:   protocol.Position{Line: 0, Character: 0},
				},
				Severity: protocol.DiagnosticSeverityError,
				Source:   "melange-lsp",
				Message:  fmt.Sprintf("Internal error during validation: %v", r),
			})
		}
	}()

	log.Printf("melange-lsp: validating document %s", uri)

	// Create an in-memory filesystem with the document content
	fs := memfs.New()
	filename := "melange.yaml"
	if err := fs.WriteFile(filename, []byte(content), 0644); err != nil {
		log.Printf("melange-lsp: failed to write to memfs: %v", err)
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 0},
			},
			Severity: protocol.DiagnosticSeverityError,
			Source:   "melange-lsp",
			Message:  "Failed to validate document",
		})
		return diagnostics
	}

	// Try to parse with melange
	ctx := context.Background()
	_, err := config.ParseConfiguration(
		ctx,
		filename,
		config.WithFS(fs),
	)

	if err != nil {
		log.Printf("melange-lsp: validation error: %v", err)
		// Convert melange error to LSP diagnostic
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 0},
			},
			Severity: protocol.DiagnosticSeverityError,
			Source:   "melange",
			Message:  err.Error(),
		})
	} else {
		log.Printf("melange-lsp: validation successful")
	}

	return diagnostics
}

// ValidateDocumentFromFile validates a melange YAML file and returns diagnostics.
// This is useful when we have an actual file path.
func ValidateDocumentFromFile(uri protocol.DocumentURI) []protocol.Diagnostic {
	var diagnostics []protocol.Diagnostic

	// Extract file path from URI
	filePath := uri.Filename()
	if filePath == "" {
		return diagnostics
	}

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return diagnostics
	}

	// Try to parse with melange
	ctx := context.Background()
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return diagnostics
	}

	_, err = config.ParseConfiguration(ctx, absPath)

	if err != nil {
		// Convert melange error to LSP diagnostic
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 0},
			},
			Severity: protocol.DiagnosticSeverityError,
			Source:   "melange",
			Message:  err.Error(),
		})
	}

	return diagnostics
}

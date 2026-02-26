package lsp

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"chainguard.dev/melange/pkg/config"
	"github.com/psanford/memfs"
	"go.lsp.dev/protocol"
	"gopkg.in/yaml.v3"
)

var linePattern = regexp.MustCompile(`line (\d+)`)

// parseLineFromError extracts a 0-indexed line number from an error message.
// Melange/YAML errors embed 1-indexed line numbers like "yaml: line 5: ...".
// Returns 0 if no line number is found.
func parseLineFromError(errMsg string) uint32 {
	m := linePattern.FindStringSubmatch(errMsg)
	if m == nil {
		return 0
	}
	n, err := strconv.ParseUint(m[1], 10, 32)
	if err != nil || n == 0 {
		return 0
	}
	return uint32(n - 1) // convert to 0-indexed
}

// diagnosticForError creates a diagnostic highlighting the full line extracted from the error message.
func diagnosticForError(source string, err error) protocol.Diagnostic {
	line := parseLineFromError(err.Error())
	return protocol.Diagnostic{
		Range: protocol.Range{
			Start: protocol.Position{Line: line, Character: 0},
			End:   protocol.Position{Line: line, Character: 1000},
		},
		Severity: protocol.DiagnosticSeverityError,
		Source:   source,
		Message:  err.Error(),
	}
}

// ValidateDocument validates a melange YAML document and returns diagnostics.
func ValidateDocument(uri protocol.DocumentURI, content string) []protocol.Diagnostic {
	diagnostics := []protocol.Diagnostic{}

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
		// Fall back to parsing as a pipeline definition
		if isPipelineFile([]byte(content)) {
			log.Printf("melange-lsp: validation: parsed as pipeline")
			return diagnostics
		}

		log.Printf("melange-lsp: validation error: %v", err)
		diagnostics = append(diagnostics, diagnosticForError("melange", err))
	} else {
		log.Printf("melange-lsp: validation successful")
	}

	return diagnostics
}

// isPipelineFile checks if the content parses as a valid pipeline definition.
// A valid pipeline must have at least one pipeline-specific field populated.
func isPipelineFile(content []byte) bool {
	var p config.Pipeline
	if err := yaml.Unmarshal(content, &p); err != nil {
		return false
	}
	// Must look like an actual pipeline, not just any YAML that happens to parse
	return p.Runs != "" || p.Uses != "" || len(p.Pipeline) > 0 || len(p.Inputs) > 0
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
		// Fall back to parsing as a pipeline definition
		b, readErr := os.ReadFile(absPath) // #nosec G304
		if readErr == nil && isPipelineFile(b) {
			return diagnostics
		}

		diagnostics = append(diagnostics, diagnosticForError("melange", err))
	}

	return diagnostics
}

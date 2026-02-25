package lsp

import (
	"context"
	"net/url"
	"path/filepath"
	"sync"

	"melange-lsp/internal/pipeline"

	"chainguard.dev/melange/pkg/build"
	"go.lsp.dev/protocol"
)

// Server implements the LSP server for melange configuration files.
type Server struct {
	mu sync.Mutex

	// documents tracks open documents by URI
	documents map[protocol.DocumentURI]*Document

	// client is used to send notifications to the LSP client
	client protocol.Client

	// registry provides pipeline discovery (embedded FS for built-ins)
	registry *pipeline.Registry
}

// Document represents an open melange configuration file.
type Document struct {
	URI     protocol.DocumentURI
	Content string
	Version int32
}

// NewServer creates a new LSP server instance.
func NewServer() *Server {
	return &Server{
		documents: make(map[protocol.DocumentURI]*Document),
	}
}

// Initialize handles the initialize request from the client.
func (s *Server) Initialize(ctx context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	s.mu.Lock()
	s.registry = pipeline.NewRegistry(build.PipelinesFS, "pipelines")
	s.mu.Unlock()

	return &protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync: protocol.TextDocumentSyncOptions{
				OpenClose: true,
				Change:    protocol.TextDocumentSyncKindFull,
			},
			HoverProvider:          true,
			DefinitionProvider:     true,
			DocumentSymbolProvider: true,
			CompletionProvider: &protocol.CompletionOptions{
				TriggerCharacters: []string{"\n", " ", ":"},
			},
		},
		ServerInfo: &protocol.ServerInfo{
			Name:    "melange-lsp",
			Version: "0.1.0",
		},
	}, nil
}

// Initialized handles the initialized notification from the client.
func (s *Server) Initialized(ctx context.Context, params *protocol.InitializedParams) error {
	return nil
}

// Shutdown handles the shutdown request from the client.
func (s *Server) Shutdown(ctx context.Context) error {
	return nil
}

// Exit handles the exit notification from the client.
func (s *Server) Exit(ctx context.Context) error {
	return nil
}

// DidOpen handles the textDocument/didOpen notification.
func (s *Server) DidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	s.mu.Lock()
	s.documents[params.TextDocument.URI] = &Document{
		URI:     params.TextDocument.URI,
		Content: params.TextDocument.Text,
		Version: params.TextDocument.Version,
	}
	s.mu.Unlock()

	// Validate and publish diagnostics
	diagnostics := ValidateDocument(params.TextDocument.URI, params.TextDocument.Text)
	s.publishDiagnostics(ctx, params.TextDocument.URI, diagnostics)

	return nil
}

// DidChange handles the textDocument/didChange notification.
func (s *Server) DidChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) error {
	s.mu.Lock()
	doc, exists := s.documents[params.TextDocument.URI]
	if !exists {
		s.mu.Unlock()
		return nil
	}

	// Full sync - replace entire content
	if len(params.ContentChanges) > 0 {
		doc.Content = params.ContentChanges[0].Text
		doc.Version = params.TextDocument.Version
	}
	s.mu.Unlock()

	// Validate and publish diagnostics
	diagnostics := ValidateDocument(params.TextDocument.URI, doc.Content)
	s.publishDiagnostics(ctx, params.TextDocument.URI, diagnostics)

	return nil
}

// DidClose handles the textDocument/didClose notification.
func (s *Server) DidClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	s.mu.Lock()
	delete(s.documents, params.TextDocument.URI)
	s.mu.Unlock()

	// Clear diagnostics when document closes
	s.publishDiagnostics(ctx, params.TextDocument.URI, []protocol.Diagnostic{})

	return nil
}

// DocumentSymbol handles the textDocument/documentSymbol request.
func (s *Server) DocumentSymbol(ctx context.Context, params *protocol.DocumentSymbolParams) ([]interface{}, error) {
	s.mu.Lock()
	doc, exists := s.documents[params.TextDocument.URI]
	s.mu.Unlock()

	if !exists {
		return []interface{}{}, nil
	}

	// Parse the document and return symbols
	symbols := parseDocumentSymbols(doc.Content)
	return symbols, nil
}

// Completion handles the textDocument/completion request.
func (s *Server) Completion(ctx context.Context, params *protocol.CompletionParams) (*protocol.CompletionList, error) {
	s.mu.Lock()
	doc, exists := s.documents[params.TextDocument.URI]
	s.mu.Unlock()

	if !exists {
		return &protocol.CompletionList{Items: []protocol.CompletionItem{}}, nil
	}

	dirs := pipelineDirsForURI(params.TextDocument.URI)
	items := getCompletions(doc.Content, params.Position, s.registry, dirs)

	return &protocol.CompletionList{
		IsIncomplete: false,
		Items:        items,
	}, nil
}

// Hover handles the textDocument/hover request.
func (s *Server) Hover(ctx context.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	s.mu.Lock()
	doc, exists := s.documents[params.TextDocument.URI]
	s.mu.Unlock()

	if !exists {
		return nil, nil
	}

	dirs := pipelineDirsForURI(params.TextDocument.URI)
	return getHover(doc.Content, params.Position, s.registry, dirs), nil
}

// SetClient sets the LSP client for sending notifications.
func (s *Server) SetClient(client protocol.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = client
}

// publishDiagnostics sends diagnostics for a document to the client.
func (s *Server) publishDiagnostics(ctx context.Context, uri protocol.DocumentURI, diagnostics []protocol.Diagnostic) {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()

	if client == nil {
		return
	}

	client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnostics,
	})
}

// pipelineDirsForURI returns the on-disk pipeline directories to search for a
// given document URI. This is the pipelines/ directory that is a sibling of the
// file being edited.
func pipelineDirsForURI(uri protocol.DocumentURI) []string {
	u, err := url.Parse(string(uri))
	if err != nil || u.Path == "" {
		return nil
	}
	dir := filepath.Join(filepath.Dir(u.Path), "pipelines")
	return []string{dir}
}

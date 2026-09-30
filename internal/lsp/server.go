// Package lsp implements the melange language server.
package lsp

import (
	"context"
	"encoding/json"
	"log"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"melange-lsp/internal/document"
	"melange-lsp/internal/model"
	"melange-lsp/internal/pipeline"
	"melange-lsp/internal/subst"

	"chainguard.dev/melange/pkg/build"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

// Version is reported to clients in initialize.
const Version = "0.2.0"

// Options are read from initializationOptions and
// workspace/didChangeConfiguration (under a "melange" key).
type Options struct {
	// PipelineDirs are extra pipeline directories searched before the
	// ones discovered next to each file. Relative paths are resolved
	// against the workspace root.
	PipelineDirs []string `json:"pipelineDirs,omitempty"`
	// InlayHints shows resolved ${{...}} values inline. Default true.
	InlayHints *bool `json:"inlayHints,omitempty"`
}

func (o Options) inlayHints() bool { return o.InlayHints == nil || *o.InlayHints }

// Server is the melange language server.
type Server struct {
	conn jsonrpc2.Conn

	mu       sync.Mutex
	docs     map[protocol.DocumentURI]*Snapshot
	root     string
	opts     Options
	registry *pipeline.Registry
	// workspace indexes packages under root; nil without a root.
	workspace *workspaceIndex
	shutdown  bool
}

// NewServer creates a server using melange's built-in pipelines.
func NewServer() *Server {
	return &Server{
		docs:     map[protocol.DocumentURI]*Snapshot{},
		registry: pipeline.NewRegistry(build.PipelinesFS, "pipelines"),
	}
}

// Snapshot is one version of an open document plus lazily computed
// analysis. It is immutable once created, so features can use it without
// holding the server lock.
type Snapshot struct {
	Doc      *document.Document
	Model    *model.Model
	Dirs     []string
	Registry *pipeline.Registry
	opts     Options

	workspace *workspaceIndex

	substOnce sync.Once
	subst     *subst.Map
}

// Subst returns the substitution map, computed on first use.
func (s *Snapshot) Subst() *subst.Map {
	s.substOnce.Do(func() {
		s.subst = subst.Build(s.Doc, s.Model)
	})
	return s.subst
}

// Active reports whether the document is a melange file we serve.
func (s *Snapshot) Active() bool {
	return s != nil && s.Doc.Kind != document.KindUnknown
}

func (s *Server) snapshot(uri protocol.DocumentURI) *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs[uri]
}

// update stores a new version of a document and returns its snapshot.
func (s *Server) update(uri protocol.DocumentURI, version int32, text string) *Snapshot {
	d := document.New(uri, version, text)

	s.mu.Lock()
	prev := s.docs[uri]
	// Keep serving a melange file through edits that break its YAML.
	if d.ParseErr != nil && d.Kind == document.KindUnknown && prev != nil {
		d.Kind = prev.Doc.Kind
	}
	snap := &Snapshot{
		Doc:      d,
		Model:    model.Build(d),
		Dirs:     s.pipelineDirs(uri),
		Registry: s.registry,
		opts:     s.opts,

		workspace: s.workspace,
	}
	s.docs[uri] = snap
	s.mu.Unlock()
	return snap
}

// pipelineDirs returns the directories searched for `uses:` pipelines for
// a file, in melange's search order. Must be called with s.mu held.
func (s *Server) pipelineDirs(uri protocol.DocumentURI) []string {
	var dirs []string
	for _, d := range s.opts.PipelineDirs {
		if !filepath.IsAbs(d) && s.root != "" {
			d = filepath.Join(s.root, d)
		}
		dirs = append(dirs, d)
	}
	dirs = append(dirs, pipeline.DiscoverDirs(document.Filename(uri), s.root)...)
	// A shared pipeline runs in whichever tree uses it, so search them all.
	if strings.Contains(document.Filename(uri), "/pipelines/") {
		for _, d := range discoverFor(s.root) {
			if !slices.Contains(dirs, d) {
				dirs = append(dirs, d)
			}
		}
	}
	return dirs
}

func (s *Server) notify(ctx context.Context, method string, params any) {
	if s.conn == nil {
		return
	}
	if err := s.conn.Notify(ctx, method, params); err != nil {
		log.Printf("melange-lsp: notify %s: %v", method, err)
	}
}

func (s *Server) publishDiagnostics(ctx context.Context, snap *Snapshot) {
	diags := []protocol.Diagnostic{}
	if snap.Active() {
		diags = Diagnostics(snap)
	}
	s.notify(ctx, protocol.MethodTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         snap.Doc.URI,
		Version:     uint32(snap.Doc.Version),
		Diagnostics: diags,
	})
}

// initializeParams is the subset of InitializeParams we read.
type initializeParams struct {
	RootURI               protocol.DocumentURI `json:"rootUri"`
	WorkspaceFolders      []protocol.WorkspaceFolder
	InitializationOptions json.RawMessage `json:"initializationOptions"`
}

func (s *Server) initialize(_ context.Context, p *initializeParams) (any, error) {
	s.mu.Lock()
	if p.RootURI != "" {
		s.root = document.Filename(p.RootURI)
	} else if len(p.WorkspaceFolders) > 0 {
		s.root = document.Filename(protocol.DocumentURI(p.WorkspaceFolders[0].URI))
	}
	if len(p.InitializationOptions) > 0 {
		s.opts = parseOptions(p.InitializationOptions)
	}
	if s.root != "" {
		s.workspace = newWorkspaceIndex(s.root)
	}
	s.mu.Unlock()

	return map[string]any{
		"capabilities": map[string]any{
			"positionEncoding": "utf-16",
			"textDocumentSync": map[string]any{
				"openClose": true,
				"change":    protocol.TextDocumentSyncKindFull,
				"save":      map[string]any{"includeText": false},
			},
			"completionProvider": map[string]any{
				"triggerCharacters": []string{"{", ".", "/", " ", "@", "-"},
			},
			"hoverProvider":             true,
			"definitionProvider":        true,
			"referencesProvider":        true,
			"documentHighlightProvider": true,
			"renameProvider":            map[string]any{"prepareProvider": true},
			"documentSymbolProvider":    true,
			"workspaceSymbolProvider":   true,
			"foldingRangeProvider":      true,
			"documentLinkProvider":      map[string]any{},
			"inlayHintProvider":         true,
			"codeLensProvider":          map[string]any{},
			"codeActionProvider": map[string]any{
				"codeActionKinds": []string{"quickfix", "refactor.rewrite", "source"},
			},
			"semanticTokensProvider": map[string]any{
				"legend": semanticLegend,
				"full":   true,
			},
		},
		"serverInfo": map[string]any{"name": "melange-lsp", "version": Version},
	}, nil
}

func parseOptions(raw json.RawMessage) Options {
	var o Options
	// Accept both {"pipelineDirs": ...} and {"melange": {"pipelineDirs": ...}}.
	var wrapped struct {
		Melange *Options `json:"melange"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Melange != nil {
		return *wrapped.Melange
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		log.Printf("melange-lsp: ignoring bad options: %v", err)
	}
	return o
}

func (s *Server) didChangeConfiguration(ctx context.Context, p *struct {
	Settings json.RawMessage `json:"settings"`
}) error {
	if len(p.Settings) == 0 || string(p.Settings) == "null" {
		return nil
	}
	s.mu.Lock()
	s.opts = parseOptions(p.Settings)
	var uris []protocol.DocumentURI
	for u := range s.docs {
		uris = append(uris, u)
	}
	s.mu.Unlock()
	s.refresh(ctx, uris)
	return nil
}

// refresh re-analyzes open documents, e.g. after options or pipeline files
// change.
func (s *Server) refresh(ctx context.Context, uris []protocol.DocumentURI) {
	for _, u := range uris {
		if old := s.snapshot(u); old != nil {
			s.publishDiagnostics(ctx, s.update(u, old.Doc.Version, old.Doc.Text))
		}
	}
}

func (s *Server) didOpen(ctx context.Context, p *protocol.DidOpenTextDocumentParams) error {
	s.publishDiagnostics(ctx, s.update(p.TextDocument.URI, p.TextDocument.Version, p.TextDocument.Text))
	return nil
}

func (s *Server) didChange(ctx context.Context, p *protocol.DidChangeTextDocumentParams) error {
	if len(p.ContentChanges) == 0 {
		return nil
	}
	// Full sync: the last change holds the whole document.
	text := p.ContentChanges[len(p.ContentChanges)-1].Text
	s.publishDiagnostics(ctx, s.update(p.TextDocument.URI, p.TextDocument.Version, text))
	return nil
}

func (s *Server) didSave(ctx context.Context, p *protocol.DidSaveTextDocumentParams) error {
	// A saved pipeline file changes what other documents resolve to.
	if snap := s.snapshot(p.TextDocument.URI); snap != nil && snap.Doc.Kind == document.KindPipeline {
		s.registry.Invalidate()
		s.refreshAll(ctx)
	}
	return nil
}

func (s *Server) didChangeWatchedFiles(ctx context.Context, _ *protocol.DidChangeWatchedFilesParams) error {
	s.registry.Invalidate()
	s.refreshAll(ctx)
	return nil
}

func (s *Server) refreshAll(ctx context.Context) {
	s.mu.Lock()
	var uris []protocol.DocumentURI
	for u := range s.docs {
		uris = append(uris, u)
	}
	s.mu.Unlock()
	s.refresh(ctx, uris)
}

func (s *Server) didClose(ctx context.Context, p *protocol.DidCloseTextDocumentParams) error {
	s.mu.Lock()
	delete(s.docs, p.TextDocument.URI)
	s.mu.Unlock()
	s.notify(ctx, protocol.MethodTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         p.TextDocument.URI,
		Diagnostics: []protocol.Diagnostic{},
	})
	return nil
}

// Root returns the workspace root, for tests.
func (s *Server) Root() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root
}

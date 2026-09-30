package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime/debug"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

// Serve runs the server on conn until the connection closes.
func (s *Server) Serve(ctx context.Context, conn jsonrpc2.Conn) {
	s.conn = conn
	conn.Go(ctx, s.handle)
}

// handler adapts a typed method to the JSON-RPC layer.
type handler func(ctx context.Context, params json.RawMessage) (any, error)

func request[P any](fn func(context.Context, *P) (any, error)) handler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		p := new(P)
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, p); err != nil {
				return nil, fmt.Errorf("%w: %s", jsonrpc2.ErrInvalidParams, err)
			}
		}
		return fn(ctx, p)
	}
}

func notification[P any](fn func(context.Context, *P) error) handler {
	return request(func(ctx context.Context, p *P) (any, error) { return nil, fn(ctx, p) })
}

// docRequest wraps a feature that needs the current snapshot of an active
// melange document. Requests for other documents get empty results.
func docRequest[P any, R any](s *Server, uri func(*P) protocol.DocumentURI, empty R, fn func(*Snapshot, *P) R) handler {
	return request(func(_ context.Context, p *P) (any, error) {
		snap := s.snapshot(uri(p))
		if !snap.Active() {
			return empty, nil
		}
		return fn(snap, p), nil
	})
}

func (s *Server) methods() map[string]handler {
	td := func(p *protocol.TextDocumentPositionParams) protocol.DocumentURI { return p.TextDocument.URI }
	return map[string]handler{
		"initialize":  request(s.initialize),
		"initialized": notification(func(context.Context, *struct{}) error { return nil }),
		"shutdown": request(func(context.Context, *struct{}) (any, error) {
			s.mu.Lock()
			s.shutdown = true
			s.mu.Unlock()
			return nil, nil
		}),
		"exit": notification(func(context.Context, *struct{}) error {
			s.mu.Lock()
			code := 1
			if s.shutdown {
				code = 0
			}
			s.mu.Unlock()
			os.Exit(code)
			return nil
		}),
		"$/cancelRequest":                  notification(func(context.Context, *struct{}) error { return nil }),
		"$/setTrace":                       notification(func(context.Context, *struct{}) error { return nil }),
		"textDocument/didOpen":             notification(s.didOpen),
		"textDocument/didChange":           notification(s.didChange),
		"textDocument/didSave":             notification(s.didSave),
		"textDocument/didClose":            notification(s.didClose),
		"workspace/didChangeConfiguration": notification(s.didChangeConfiguration),
		"workspace/didChangeWatchedFiles":  notification(s.didChangeWatchedFiles),

		"textDocument/completion": docRequest(s, func(p *protocol.CompletionParams) protocol.DocumentURI { return p.TextDocument.URI },
			&protocol.CompletionList{Items: []protocol.CompletionItem{}}, func(snap *Snapshot, p *protocol.CompletionParams) *protocol.CompletionList {
				return Completion(snap, p.Position)
			}),
		"textDocument/hover": docRequest(s, func(p *protocol.HoverParams) protocol.DocumentURI { return p.TextDocument.URI },
			(*protocol.Hover)(nil), func(snap *Snapshot, p *protocol.HoverParams) *protocol.Hover {
				return Hover(snap, p.Position)
			}),
		"textDocument/definition": docRequest(s, func(p *protocol.DefinitionParams) protocol.DocumentURI { return p.TextDocument.URI },
			[]protocol.Location{}, func(snap *Snapshot, p *protocol.DefinitionParams) []protocol.Location {
				return Definition(snap, p.Position)
			}),
		"textDocument/references": docRequest(s, func(p *protocol.ReferenceParams) protocol.DocumentURI { return p.TextDocument.URI },
			[]protocol.Location{}, func(snap *Snapshot, p *protocol.ReferenceParams) []protocol.Location {
				if refs := References(snap, p.Position, p.Context.IncludeDeclaration); len(refs) > 0 {
					return refs
				}
				return s.workspaceReferences(snap, p.Position)
			}),
		"textDocument/documentHighlight": docRequest(s, func(p *protocol.DocumentHighlightParams) protocol.DocumentURI { return p.TextDocument.URI },
			[]protocol.DocumentHighlight{}, func(snap *Snapshot, p *protocol.DocumentHighlightParams) []protocol.DocumentHighlight {
				return Highlights(snap, p.Position)
			}),
		"textDocument/prepareRename": docRequest(s, td, (*protocol.Range)(nil), func(snap *Snapshot, p *protocol.TextDocumentPositionParams) *protocol.Range {
			return PrepareRename(snap, p.Position)
		}),
		"textDocument/rename": request(func(_ context.Context, p *protocol.RenameParams) (any, error) {
			snap := s.snapshot(p.TextDocument.URI)
			if !snap.Active() {
				return nil, nil
			}
			return Rename(snap, p.Position, p.NewName)
		}),
		"textDocument/documentSymbol": docRequest(s, func(p *protocol.DocumentSymbolParams) protocol.DocumentURI { return p.TextDocument.URI },
			[]protocol.DocumentSymbol{}, func(snap *Snapshot, _ *protocol.DocumentSymbolParams) []protocol.DocumentSymbol {
				return Symbols(snap)
			}),
		"workspace/symbol": request(func(ctx context.Context, p *protocol.WorkspaceSymbolParams) (any, error) {
			return s.workspaceSymbols(ctx, p.Query), nil
		}),
		"textDocument/foldingRange": docRequest(s, func(p *protocol.FoldingRangeParams) protocol.DocumentURI { return p.TextDocument.URI },
			[]FoldingRange(nil), func(snap *Snapshot, _ *protocol.FoldingRangeParams) []FoldingRange {
				return getFoldingRanges(snap.Doc)
			}),
		"textDocument/documentLink": docRequest(s, func(p *protocol.DocumentLinkParams) protocol.DocumentURI { return p.TextDocument.URI },
			[]protocol.DocumentLink{}, func(snap *Snapshot, _ *protocol.DocumentLinkParams) []protocol.DocumentLink {
				return Links(snap)
			}),
		"textDocument/inlayHint": docRequest(s, func(p *inlayHintParams) protocol.DocumentURI { return p.TextDocument.URI },
			[]InlayHint{}, func(snap *Snapshot, p *inlayHintParams) []InlayHint {
				return InlayHints(snap, p.Range)
			}),
		"textDocument/codeLens": docRequest(s, func(p *protocol.CodeLensParams) protocol.DocumentURI { return p.TextDocument.URI },
			[]protocol.CodeLens{}, func(snap *Snapshot, _ *protocol.CodeLensParams) []protocol.CodeLens {
				return CodeLenses(snap)
			}),
		"textDocument/codeAction": docRequest(s, func(p *protocol.CodeActionParams) protocol.DocumentURI { return p.TextDocument.URI },
			[]protocol.CodeAction{}, func(snap *Snapshot, p *protocol.CodeActionParams) []protocol.CodeAction {
				return CodeActions(snap, p.Range, p.Context)
			}),
		"textDocument/semanticTokens/full": docRequest(s, func(p *protocol.SemanticTokensParams) protocol.DocumentURI { return p.TextDocument.URI },
			&protocol.SemanticTokens{Data: []uint32{}}, func(snap *Snapshot, _ *protocol.SemanticTokensParams) *protocol.SemanticTokens {
				return SemanticTokens(snap)
			}),
	}
}

func (s *Server) handle(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) (err error) {
	h, ok := s.methods()[req.Method()]
	if !ok {
		_, isCall := req.(*jsonrpc2.Call)
		if !isCall {
			return nil // unknown notifications are ignored
		}
		return reply(ctx, nil, fmt.Errorf("%w: %s", jsonrpc2.ErrMethodNotFound, req.Method()))
	}

	// A bug in one feature must not take down the editor's language server.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("melange-lsp: panic in %s: %v\n%s", req.Method(), r, debug.Stack())
			err = reply(ctx, nil, fmt.Errorf("%w: internal error in %s", jsonrpc2.ErrInternal, req.Method()))
		}
	}()

	result, herr := h(ctx, req.Params())
	return reply(ctx, result, herr)
}

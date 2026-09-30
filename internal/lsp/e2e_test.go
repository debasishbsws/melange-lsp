package lsp

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"melange-lsp/internal/document"
)

// TestEndToEnd drives the server over JSON-RPC like an editor would.
func TestEndToEnd(t *testing.T) {
	root := workspace(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	serverSide, clientSide := net.Pipe()
	srv := NewServer()
	srv.Serve(ctx, jsonrpc2.NewConn(jsonrpc2.NewStream(serverSide)))

	diags := make(chan protocol.PublishDiagnosticsParams, 10)
	client := jsonrpc2.NewConn(jsonrpc2.NewStream(clientSide))
	client.Go(ctx, func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		if req.Method() == protocol.MethodTextDocumentPublishDiagnostics {
			var p protocol.PublishDiagnosticsParams
			_ = json.Unmarshal(req.Params(), &p)
			diags <- p
		}
		return reply(ctx, nil, nil)
	})

	call := func(method string, params, result any) {
		t.Helper()
		if _, err := client.Call(ctx, method, params, result); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}

	var init struct {
		Capabilities map[string]any `json:"capabilities"`
		ServerInfo   struct{ Name string }
	}
	call("initialize", map[string]any{"rootUri": document.URI(root)}, &init)
	for _, c := range []string{"foldingRangeProvider", "inlayHintProvider", "completionProvider", "semanticTokensProvider", "renameProvider"} {
		if init.Capabilities[c] == nil {
			t.Errorf("capability %s missing", c)
		}
	}
	_ = client.Notify(ctx, "initialized", map[string]any{})

	uri := document.URI(filepath.Join(root, "hello.yaml"))
	text := header + "# @agent ```\n# keep the greet step\n# ```\npipeline:\n  - uses: local/greet\n    with:\n      who: ${{package.name}}\n  - uses: nope\n"
	_ = client.Notify(ctx, "textDocument/didOpen", protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: uri, LanguageID: "yaml", Version: 1, Text: text},
	})

	select {
	case p := <-diags:
		if len(p.Diagnostics) != 1 || !strings.Contains(p.Diagnostics[0].Message, `pipeline "nope" not found`) {
			t.Errorf("diagnostics = %+v", p.Diagnostics)
		}
	case <-ctx.Done():
		t.Fatal("no diagnostics published")
	}

	var folds []FoldingRange
	call("textDocument/foldingRange", map[string]any{"textDocument": map[string]any{"uri": uri}}, &folds)
	found := false
	for _, f := range folds {
		if f.CollapsedText == "🤖 NOTE: keep the greet step" && f.StartLine == 7 && f.EndLine == 9 {
			found = true
		}
	}
	if !found {
		t.Errorf("no agent note fold in %+v", folds)
	}

	var hints []InlayHint
	call("textDocument/inlayHint", map[string]any{"textDocument": map[string]any{"uri": uri}, "range": protocol.Range{End: protocol.Position{Line: 50}}}, &hints)
	if len(hints) != 1 || hints[0].Label != "= hello" {
		t.Errorf("hints = %+v", hints)
	}

	var list protocol.CompletionList
	call("textDocument/completion", protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}, Position: protocol.Position{Line: 11, Character: 12}},
	}, &list)
	if len(list.Items) == 0 {
		t.Error("no completions for uses:")
	}

	// A clean file publishes an empty array, not null.
	clean := document.URI(filepath.Join(root, "clean.yaml"))
	_ = client.Notify(ctx, "textDocument/didOpen", protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: clean, LanguageID: "yaml", Version: 1, Text: header},
	})
	if p := <-diags; p.Diagnostics == nil || len(p.Diagnostics) != 0 {
		t.Errorf("clean file diagnostics = %#v, want an empty array", p.Diagnostics)
	}

	// Non-melange YAML gets nothing, so other YAML tooling is undisturbed.
	other := document.URI(filepath.Join(root, "ci.yaml"))
	_ = client.Notify(ctx, "textDocument/didOpen", protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: other, LanguageID: "yaml", Version: 1, Text: "jobs:\n  build: {}\n"},
	})
	if p := <-diags; len(p.Diagnostics) != 0 {
		t.Errorf("non-melange diagnostics = %+v", p.Diagnostics)
	}
	var noFolds []FoldingRange
	call("textDocument/foldingRange", map[string]any{"textDocument": map[string]any{"uri": other}}, &noFolds)
	if noFolds != nil {
		t.Errorf("non-melange folds = %+v, want null so the editor folds itself", noFolds)
	}

	// Unknown methods are rejected, not ignored.
	if _, err := client.Call(ctx, "textDocument/nope", map[string]any{}, nil); err == nil {
		t.Error("unknown method should fail")
	}
	call("shutdown", nil, nil)
}

package lsp

import (
	"go.lsp.dev/protocol"
)

// NewTestServer returns a server rooted at root without a connection, for
// tests and tools that call features directly.
func NewTestServer(root string) *Server {
	s := NewServer()
	s.root = root
	return s
}

// Open stores a document and returns its snapshot.
func (s *Server) Open(uri protocol.DocumentURI, text string) *Snapshot {
	return s.update(uri, 1, text)
}

// ExerciseAll runs every read-only feature over snap, including hover and
// definition at every template and uses:, so tools can find panics.
func ExerciseAll(snap *Snapshot) {
	Symbols(snap)
	getFoldingRanges(snap.Doc)
	Links(snap)
	InlayHints(snap, protocol.Range{End: protocol.Position{Line: uint32(len(snap.Doc.Lines))}})
	CodeLenses(snap)
	SemanticTokens(snap)
	for _, t := range snap.Model.Templates {
		Hover(snap, t.ExprRange.Start)
		Definition(snap, t.ExprRange.Start)
		References(snap, t.ExprRange.Start, true)
	}
	for _, s := range snap.Model.Steps {
		if s.Uses != nil {
			p := snap.Doc.Start(s.Uses)
			Hover(snap, p)
			Definition(snap, p)
			Completion(snap, p)
		}
	}
	CodeActions(snap, protocol.Range{End: protocol.Position{Line: uint32(len(snap.Doc.Lines))}}, protocol.CodeActionContext{})
}

// FoldingRanges returns the folding ranges for a snapshot.
func FoldingRanges(snap *Snapshot) []FoldingRange {
	return getFoldingRanges(snap.Doc)
}

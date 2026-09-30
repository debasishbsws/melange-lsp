package lsp

import (
	"sort"
	"strings"

	"melange-lsp/internal/document"

	"go.lsp.dev/protocol"
)

// Semantic token types and modifiers. Editors color ${{...}} references
// inside plain YAML strings and mark @agent notes distinctly.
var semanticLegend = map[string][]string{
	"tokenTypes":     {"variable", "macro", "decorator", "comment"},
	"tokenModifiers": {"defaultLibrary", "documentation"},
}

const (
	tokVariable = iota
	tokMacro
	tokDecorator
	tokComment
)

const (
	modDefaultLibrary = 1 << iota
	modDocumentation
)

type token struct {
	line, char, length uint32
	typ, mods          uint32
}

// SemanticTokens returns tokens for template references and agent notes.
func SemanticTokens(snap *Snapshot) *protocol.SemanticTokens {
	d := snap.Doc
	var toks []token
	span := func(r protocol.Range, typ, mods uint32) {
		if r.Start.Line == r.End.Line && r.End.Character > r.Start.Character {
			toks = append(toks, token{r.Start.Line, r.Start.Character, r.End.Character - r.Start.Character, typ, mods})
		}
	}

	for _, t := range snap.Model.Templates {
		open := protocol.Range{Start: t.Range.Start, End: protocol.Position{Line: t.Range.Start.Line, Character: t.Range.Start.Character + 3}}
		closeR := protocol.Range{Start: protocol.Position{Line: t.Range.End.Line, Character: t.Range.End.Character - 2}, End: t.Range.End}
		span(open, tokMacro, 0)
		var mods uint32
		if !strings.HasPrefix(t.Expr, "vars.") && !strings.HasPrefix(t.Expr, "inputs.") {
			mods = modDefaultLibrary
		}
		span(t.ExprRange, tokVariable, mods)
		span(closeR, tokMacro, 0)
	}

	for _, n := range parseAgentNotes(d.Text) {
		for line := n.StartLine; line <= n.EndLine; line++ {
			text := d.Line(line)
			start := document.IndentOf(text)
			r := protocol.Range{Start: d.ByteToPosition(line, start), End: d.LineEnd(line)}
			if line == n.StartLine {
				if i := strings.Index(text, "@agent"); i >= 0 {
					span(protocol.Range{Start: d.ByteToPosition(line, start), End: d.ByteToPosition(line, i)}, tokComment, modDocumentation)
					span(protocol.Range{Start: d.ByteToPosition(line, i), End: d.ByteToPosition(line, i+len("@agent"))}, tokDecorator, 0)
					r.Start = d.ByteToPosition(line, i+len("@agent"))
				}
			}
			span(r, tokComment, modDocumentation)
		}
	}

	sort.Slice(toks, func(i, j int) bool {
		if toks[i].line != toks[j].line {
			return toks[i].line < toks[j].line
		}
		return toks[i].char < toks[j].char
	})

	data := make([]uint32, 0, len(toks)*5)
	var pl, pc uint32
	for _, t := range toks {
		dl := t.line - pl
		dc := t.char
		if dl == 0 {
			dc = t.char - pc
		}
		data = append(data, dl, dc, t.length, t.typ, t.mods)
		pl, pc = t.line, t.char
	}
	return &protocol.SemanticTokens{Data: data}
}

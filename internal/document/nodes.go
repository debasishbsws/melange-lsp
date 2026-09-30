package document

import (
	"strings"
	"unicode/utf8"

	"go.lsp.dev/protocol"
	"gopkg.in/yaml.v3"
)

// MapValue returns the value for key in mapping node m, or nil.
func MapValue(m *yaml.Node, key string) *yaml.Node {
	_, v := MapEntry(m, key)
	return v
}

// MapEntry returns the key and value nodes for key in mapping node m.
func MapEntry(m *yaml.Node, key string) (k, v *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

// Entries calls fn for each key/value pair of mapping node m.
func Entries(m *yaml.Node, fn func(k, v *yaml.Node)) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		fn(m.Content[i], m.Content[i+1])
	}
}

// Items returns the elements of sequence node s, or nil.
func Items(s *yaml.Node) []*yaml.Node {
	if s == nil || s.Kind != yaml.SequenceNode {
		return nil
	}
	return s.Content
}

// Start is the LSP position where node n begins.
func (d *Document) Start(n *yaml.Node) protocol.Position {
	return d.Position(n.Line-1, n.Column-1)
}

// Range is the LSP range covered by node n.
func (d *Document) Range(n *yaml.Node) protocol.Range {
	endLine, endCol := d.end(n)
	return protocol.Range{Start: d.Start(n), End: d.Position(endLine, endCol)}
}

// EntryRange covers a mapping entry from its key through its value.
func (d *Document) EntryRange(k, v *yaml.Node) protocol.Range {
	r := d.Range(k)
	if v != nil {
		if vr := d.Range(v); after(vr.End, r.End) {
			r.End = vr.End
		}
	}
	return r
}

// ItemRange covers a sequence item including its "- " marker.
func (d *Document) ItemRange(n *yaml.Node) protocol.Range {
	r := d.Range(n)
	line := d.Line(n.Line - 1)
	col := int(r.Start.Character)
	// Walk back over "- " to the dash when it is on the same line.
	prefix := line[:d.ByteOffset(r.Start)]
	if t := strings.TrimRight(prefix, " "); strings.HasSuffix(t, "-") {
		col = UTF16Col(line, utf8.RuneCountInString(t)-1)
	}
	r.Start.Character = uint32(col)
	return r
}

func after(a, b protocol.Position) bool {
	return a.Line > b.Line || (a.Line == b.Line && a.Character > b.Character)
}

// Contains reports whether pos is inside r (end inclusive, so a cursor
// placed right after a token still counts).
func Contains(r protocol.Range, pos protocol.Position) bool {
	return !after(r.Start, pos) && !after(pos, r.End)
}

// end returns the zero-based line and rune column just past node n.
func (d *Document) end(n *yaml.Node) (int, int) {
	switch n.Kind {
	case yaml.MappingNode, yaml.SequenceNode:
		if n.Style&yaml.FlowStyle != 0 {
			return d.flowEnd(n)
		}
		if len(n.Content) == 0 {
			return n.Line - 1, n.Column - 1
		}
		return d.end(n.Content[len(n.Content)-1])
	case yaml.AliasNode:
		return n.Line - 1, n.Column - 1 + 1 + utf8.RuneCountInString(n.Value)
	case yaml.ScalarNode:
		return d.scalarEnd(n)
	}
	return n.Line - 1, n.Column - 1
}

func (d *Document) scalarEnd(n *yaml.Node) (int, int) {
	line, col := n.Line-1, n.Column-1
	src := []rune(d.Line(line))

	switch {
	case n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0:
		// Block scalar: content is every following line indented deeper
		// than the line holding the indicator.
		indent := IndentOf(d.Line(line))
		last := line
		for i := line + 1; i < len(d.Lines); i++ {
			l := d.Lines[i]
			if strings.TrimSpace(l) == "" {
				continue
			}
			if IndentOf(l) <= indent {
				break
			}
			last = i
		}
		if last == line {
			return line, len(src)
		}
		return last, utf8.RuneCountInString(d.Line(last))

	case n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0:
		quote := '"'
		if n.Style&yaml.SingleQuotedStyle != 0 {
			quote = '\''
		}
		for l := line; l < len(d.Lines); l++ {
			rs := []rune(d.Lines[l])
			i := 0
			if l == line {
				i = col + 1
			}
			for ; i < len(rs); i++ {
				switch {
				case quote == '"' && rs[i] == '\\':
					i++
				case rs[i] == quote && quote == '\'' && i+1 < len(rs) && rs[i+1] == '\'':
					i++
				case rs[i] == quote:
					return l, i + 1
				}
			}
		}
		return line, len(src)

	default:
		// Plain scalar. Multi-line plain scalars fold newlines into spaces,
		// so only the first line's text can be matched reliably.
		v := n.Value
		if i := strings.IndexByte(v, ' '); strings.Contains(v, "\n") && i >= 0 {
			v = v[:i]
		}
		end := col + utf8.RuneCountInString(v)
		if end > len(src) {
			end = len(src)
		}
		// Keep plain scalars off trailing comments.
		return line, end
	}
}

func (d *Document) flowEnd(n *yaml.Node) (int, int) {
	open, closer := '[', ']'
	if n.Kind == yaml.MappingNode {
		open, closer = '{', '}'
	}
	depth := 0
	for l := n.Line - 1; l < len(d.Lines); l++ {
		rs := []rune(d.Lines[l])
		i := 0
		if l == n.Line-1 {
			i = n.Column - 1
		}
		for ; i < len(rs); i++ {
			switch rs[i] {
			case open:
				depth++
			case closer:
				depth--
				if depth == 0 {
					return l, i + 1
				}
			}
		}
	}
	return n.Line - 1, n.Column - 1
}

// PathElem is one step of a path through the YAML tree: a mapping key or
// a sequence index.
type PathElem struct {
	Key   string
	Index int // valid when IsIndex
	IsIdx bool
}

// Path is a sequence of keys and indices from the root.
type Path []PathElem

// Keys returns the path with indices replaced by "0", the form used by
// schema lookups.
func (p Path) Keys() []string {
	out := make([]string, len(p))
	for i, e := range p {
		if e.IsIdx {
			out[i] = "0"
		} else {
			out[i] = e.Key
		}
	}
	return out
}

// String renders the path as "pipeline[0].with.uri".
func (p Path) String() string {
	var b strings.Builder
	for i, e := range p {
		if e.IsIdx {
			b.WriteString("[")
			b.WriteString(itoa(e.Index))
			b.WriteString("]")
			continue
		}
		if i > 0 {
			b.WriteString(".")
		}
		b.WriteString(e.Key)
	}
	return b.String()
}

// Has reports whether key appears anywhere in the path.
func (p Path) Has(key string) bool {
	for _, e := range p {
		if !e.IsIdx && e.Key == key {
			return true
		}
	}
	return false
}

// Last returns the final key in the path, skipping indices.
func (p Path) Last() string {
	for i := len(p) - 1; i >= 0; i-- {
		if !p[i].IsIdx {
			return p[i].Key
		}
	}
	return ""
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}

// Location describes what the cursor is on.
type Location struct {
	// Path leads to Node. For a mapping key it ends with that key.
	Path Path
	// Node is the innermost node under the cursor.
	Node *yaml.Node
	// OnKey is true when Node is a mapping key; Value is then its value.
	OnKey bool
	Value *yaml.Node
	// Ancestors are the container nodes from the root down to Node's
	// parent, aligned with Path.
	Ancestors []*yaml.Node
}

// Locate finds the innermost node at pos. It returns nil if the document
// did not parse or pos is not on any node.
func (d *Document) Locate(pos protocol.Position) *Location {
	if d.Root == nil {
		return nil
	}
	loc := &Location{}
	if !d.locate(d.Root, pos, loc) {
		return nil
	}
	return loc
}

func (d *Document) locate(n *yaml.Node, pos protocol.Position, loc *Location) bool {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if Contains(d.Range(k), pos) {
				loc.Ancestors = append(loc.Ancestors, n)
				loc.Path = append(loc.Path, PathElem{Key: k.Value})
				loc.Node, loc.OnKey, loc.Value = k, true, v
				return true
			}
			if !Contains(d.EntryRange(k, v), pos) {
				continue
			}
			loc.Ancestors = append(loc.Ancestors, n)
			loc.Path = append(loc.Path, PathElem{Key: k.Value})
			if d.locate(v, pos, loc) {
				return true
			}
			loc.Node = v
			return true
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			if !Contains(d.ItemRange(item), pos) {
				continue
			}
			loc.Ancestors = append(loc.Ancestors, n)
			loc.Path = append(loc.Path, PathElem{Index: i, IsIdx: true})
			if d.locate(item, pos, loc) {
				return true
			}
			loc.Node = item
			return true
		}
	case yaml.ScalarNode, yaml.AliasNode:
		if Contains(d.Range(n), pos) {
			loc.Node = n
			return true
		}
	}
	return false
}

// NodeAtPath follows path from the root.
func (d *Document) NodeAtPath(path Path) *yaml.Node {
	n := d.Root
	for _, e := range path {
		if n == nil {
			return nil
		}
		if e.IsIdx {
			items := Items(n)
			if e.Index >= len(items) {
				return nil
			}
			n = items[e.Index]
		} else {
			n = MapValue(n, e.Key)
		}
	}
	return n
}

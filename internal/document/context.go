package document

import (
	"regexp"
	"strings"

	"go.lsp.dev/protocol"
)

// Context describes where the cursor is for completion. It is computed
// from indentation rather than the YAML tree because the document usually
// does not parse while a key is half typed.
type Context struct {
	// Path is the mapping the cursor is in. In a value position it ends
	// with the key whose value is being typed.
	Path Path
	// InValue is true after "key: ", with Key holding the key.
	InValue bool
	Key     string
	// Word is the partial identifier before the cursor.
	Word string
	// NewItem is true when the line starts a sequence item ("- ").
	NewItem bool
	// InComment is true when the cursor is after a '#'.
	InComment bool
	// Siblings are keys already present in the same mapping.
	Siblings map[string]bool
}

var (
	keyRe  = regexp.MustCompile(`^([^\s:#'"{}\[\],&*!|>%@` + "`" + `][^:#]*?):(?:\s|$)`)
	wordRe = regexp.MustCompile(`[\w./-]*$`)
)

// lineInfo is the structural reading of one line.
type lineInfo struct {
	blank   bool
	comment bool
	indent  int
	dashes  []int // columns of leading "- " markers
	keyCol  int   // column where the key (or scalar) starts
	key     string
}

func readLine(l string) lineInfo {
	t := strings.TrimSpace(l)
	li := lineInfo{indent: IndentOf(l)}
	if t == "" {
		li.blank = true
		return li
	}
	if strings.HasPrefix(t, "#") {
		li.comment = true
		return li
	}
	col := li.indent
	rest := l[col:]
	for rest == "-" || strings.HasPrefix(rest, "- ") {
		li.dashes = append(li.dashes, col)
		n := 1 + IndentOf(rest[1:])
		col += n
		rest = rest[n:]
	}
	li.keyCol = col
	if m := keyRe.FindStringSubmatch(rest); m != nil {
		li.key = strings.TrimSpace(m[1])
	}
	return li
}

// Context computes the completion context at pos.
func (d *Document) Context(pos protocol.Position) Context {
	line := d.Line(int(pos.Line))
	prefix := line[:d.ByteOffset(pos)]
	ctx := Context{Word: wordRe.FindString(prefix), Siblings: map[string]bool{}}

	if i := strings.Index(prefix, "#"); i >= 0 && (i == 0 || prefix[i-1] == ' ' || prefix[i-1] == '\t') {
		ctx.InComment = true
		return ctx
	}

	cur := readLine(prefix)
	targetCol := cur.keyCol
	if cur.blank {
		targetCol = len(prefix)
	}
	if len(cur.dashes) > 0 {
		ctx.NewItem = true
	}
	if cur.key != "" && strings.Contains(prefix[cur.keyCol:], ":") {
		ctx.InValue = true
		ctx.Key = cur.key
	}

	ctx.Path = d.ancestors(int(pos.Line), targetCol, cur.dashes)
	if ctx.InValue {
		ctx.Path = append(ctx.Path, PathElem{Key: ctx.Key})
	} else {
		ctx.Siblings = d.siblings(int(pos.Line), targetCol)
	}
	return ctx
}

// ancestors walks up from line to build the path of the mapping whose keys
// sit at column col. dashes are "- " markers already on the cursor line,
// each of which opens a new sequence item.
//
// Walking up, a line is an ancestor when it has structure left of col: a
// key there owns our value, and a dash there is the sequence item we are
// in. After an item, the owning key may sit at the dash's own column
// ("pipeline:\n- uses: ..."), which seqPending allows for.
func (d *Document) ancestors(line, col int, dashes []int) Path {
	var rev Path // innermost first
	seqPending := false

	for i := len(dashes) - 1; i >= 0; i-- {
		rev = append(rev, PathElem{IsIdx: true, Index: d.countItems(line, dashes[i])})
	}
	if len(dashes) > 0 {
		col, seqPending = dashes[0], true
	}

	for i := line - 1; i >= 0 && (col > 0 || seqPending); i-- {
		li := readLine(d.Lines[i])
		if li.blank || li.comment {
			continue
		}

		switch {
		case li.key != "" && li.keyCol < col:
			// Our mapping is this key's value.
			rev = append(rev, PathElem{Key: li.key})
			col, seqPending = li.keyCol, false
		case seqPending && li.key != "" && len(li.dashes) == 0 && li.keyCol == col:
			// Owner of a sequence written at the key's own indent.
			rev = append(rev, PathElem{Key: li.key})
			seqPending = false
			continue
		case len(li.dashes) > 0 && li.dashes[len(li.dashes)-1] < col && li.keyCol >= col:
			// A sibling key on the item's "- " line: we are in that item.
		default:
			continue
		}

		// Dashes left of the new col are enclosing sequence items.
		for j := len(li.dashes) - 1; j >= 0; j-- {
			if li.dashes[j] < col || (j == len(li.dashes)-1 && li.dashes[j] < li.keyCol && li.keyCol >= col) {
				rev = append(rev, PathElem{IsIdx: true, Index: d.countItems(i, li.dashes[j])})
				col, seqPending = li.dashes[j], true
			}
		}
	}

	out := make(Path, len(rev))
	for i, e := range rev {
		out[len(rev)-1-i] = e
	}
	return out
}

// countItems returns the index of the item starting on line within the
// sequence whose dashes sit at column dc.
func (d *Document) countItems(line, dc int) int {
	n := 0
	for i := line - 1; i >= 0; i-- {
		li := readLine(d.Lines[i])
		if li.blank || li.comment {
			continue
		}
		if slicesContains(li.dashes, dc) {
			n++
			continue
		}
		if li.indent <= dc {
			break
		}
	}
	return n
}

func slicesContains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// siblings collects keys at column col in the mapping around line. A
// "- " at col-2 starts a sequence item: the mapping begins on that line and
// ends before the next item's dash.
func (d *Document) siblings(line, col int) map[string]bool {
	keys := map[string]bool{}
	startsItem := func(li lineInfo) bool {
		return len(li.dashes) > 0 && li.keyCol == col
	}

	// When the cursor line opens the item, nothing above belongs to it.
	cur := readLine(d.Line(line))
	if cur.blank || cur.comment || !startsItem(cur) {
		for i := line - 1; i >= 0; i-- {
			li := readLine(d.Lines[i])
			if li.blank || li.comment {
				continue
			}
			if li.keyCol == col && li.key != "" {
				keys[li.key] = true
				if startsItem(li) {
					break
				}
				continue
			}
			if li.keyCol < col || li.indent < col {
				break
			}
		}
	}
	for i := line + 1; i < len(d.Lines); i++ {
		li := readLine(d.Lines[i])
		if li.blank || li.comment {
			continue
		}
		if startsItem(li) || li.keyCol < col || li.indent < col {
			break
		}
		if li.keyCol == col && li.key != "" {
			keys[li.key] = true
		}
	}
	return keys
}

// Siblings returns the keys at column col in the mapping around line.
func (d *Document) Siblings(line, col int) map[string]bool {
	return d.siblings(line, col)
}

package lsp

import (
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"
)

// invertTransform finds an input that a regexp replacement turns into out:
// for a match pattern of literals, character classes and capture groups,
// and a replace template of group references and literals, it reads each
// group's value out of out and rebuilds the input from the pattern. It
// returns false when the pattern is too complex or the result does not
// replace back to out.
func invertTransform(match, replace, out string) (string, bool) {
	re, err := regexp.Compile(match)
	if err != nil {
		return "", false
	}
	// A literal-only replacement: swap the text back.
	if !strings.Contains(replace, "$") {
		if regexp.QuoteMeta(match) == match && replace != "" {
			in := strings.ReplaceAll(out, replace, match)
			return in, re.ReplaceAllString(in, replace) == out
		}
		return "", false
	}

	// Read group values out of out using the replace template.
	var tmpl strings.Builder
	var order []int
	tmpl.WriteString("^")
	for i := 0; i < len(replace); {
		if replace[i] != '$' {
			j := strings.IndexByte(replace[i:], '$')
			if j < 0 {
				j = len(replace) - i
			}
			tmpl.WriteString(regexp.QuoteMeta(replace[i : i+j]))
			i += j
			continue
		}
		n, width := groupRef(replace[i:])
		if width == 0 {
			return "", false
		}
		order = append(order, n)
		tmpl.WriteString("(.+?)")
		i += width
	}
	tmpl.WriteString("$")
	vals := regexp.MustCompile(tmpl.String()).FindStringSubmatch(out)
	if vals == nil {
		return "", false
	}
	groups := map[int]string{}
	for i, n := range order {
		if prev, ok := groups[n]; ok && prev != vals[i+1] {
			return "", false
		}
		groups[n] = vals[i+1]
	}

	// Rebuild the input from the pattern.
	parsed, err := syntax.Parse(match, syntax.Perl)
	if err != nil {
		return "", false
	}
	var in strings.Builder
	if !buildInput(parsed.Simplify(), groups, &in) {
		return "", false
	}
	if re.ReplaceAllString(in.String(), replace) != out {
		return "", false
	}
	return in.String(), true
}

// groupRef parses $1, ${1} or ${name} at the start of s. Named groups are
// not supported.
func groupRef(s string) (int, int) {
	if len(s) > 2 && s[1] == '{' {
		end := strings.IndexByte(s, '}')
		if end < 0 {
			return 0, 0
		}
		n, err := strconv.Atoi(s[2:end])
		if err != nil {
			return 0, 0
		}
		return n, end + 1
	}
	j := 1
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == 1 {
		return 0, 0
	}
	n, _ := strconv.Atoi(s[1:j])
	return n, j
}

// buildInput writes a string matching re, using groups for capture contents.
func buildInput(re *syntax.Regexp, groups map[int]string, b *strings.Builder) bool {
	switch re.Op {
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			if !buildInput(sub, groups, b) {
				return false
			}
		}
		return true
	case syntax.OpCapture:
		v, ok := groups[re.Cap]
		if !ok {
			return false
		}
		b.WriteString(v)
		return true
	case syntax.OpLiteral:
		b.WriteString(string(re.Rune))
		return true
	case syntax.OpCharClass:
		if len(re.Rune) == 0 {
			return false
		}
		b.WriteRune(re.Rune[0])
		return true
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		b.WriteByte('.')
		return true
	case syntax.OpBeginText, syntax.OpEndText, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpEmptyMatch:
		return true
	case syntax.OpQuest, syntax.OpStar:
		return true // the empty choice
	case syntax.OpAlternate:
		return buildInput(re.Sub[0], groups, b)
	}
	return false
}

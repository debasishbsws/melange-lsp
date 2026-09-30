package lsp

import (
	"sort"
	"strings"
)

// suggest returns up to three candidates close to word, best first.
func suggest(word string, candidates []string) []string {
	if word == "" {
		return nil
	}
	type scored struct {
		s string
		d int
	}
	limit := len(word) / 3
	if limit < 2 {
		limit = 2
	}
	var out []scored
	lw := strings.ToLower(word)
	for _, c := range candidates {
		if c == word {
			continue
		}
		lc := strings.ToLower(c)
		d := distance(lw, lc)
		switch {
		case lc == lw:
			d = 0
		case d <= limit:
		case strings.HasPrefix(lc, lw) && len(lw) >= 3:
			d = limit + 1
		default:
			continue
		}
		out = append(out, scored{c, d})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].d != out[j].d {
			return out[i].d < out[j].d
		}
		return out[i].s < out[j].s
	})
	var res []string
	for i := 0; i < len(out) && i < 3; i++ {
		res = append(res, out[i].s)
	}
	return res
}

// distance is the optimal string alignment distance: Levenshtein plus
// transpositions of adjacent characters, the most common typo.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev2 := make([]int, len(rb)+1)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(rb)]
}

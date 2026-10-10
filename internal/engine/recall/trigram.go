package recall

import (
	"strings"
	"unicode"
)

// trigramSimilarity is pg_trgm's similarity(): the Jaccard index |A∩B|/|A∪B| of the two strings' trigram
// sets, where each lowercased word (a run of letters and digits) is padded with two leading spaces and one
// trailing space. It is 0 when either set is empty.
func trigramSimilarity(a, b string) float64 {
	ta, tb := trigrams(a), trigrams(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	common := 0
	for g := range ta {
		if _, ok := tb[g]; ok {
			common++
		}
	}
	return float64(common) / float64(len(ta)+len(tb)-common)
}

// trigrams returns the pg_trgm trigram set of s.
func trigrams(s string) map[string]struct{} {
	out := map[string]struct{}{}
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range words {
		rs := []rune("  " + w + " ")
		for i := 0; i+3 <= len(rs); i++ {
			out[string(rs[i:i+3])] = struct{}{}
		}
	}
	return out
}

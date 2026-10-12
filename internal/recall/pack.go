package recall

import (
	"memd/internal/domain"
	"sort"
	"unicode"
)

// tokenCounter counts tokens for packing, include budgets and the query limit: *ai.Tokenizer in production, a word
// counter in tests.
type tokenCounter interface {
	// Count returns the number of tokens in text.
	Count(text string) int
}

// pack walks cs in order and keeps each candidate whose unit text (counted with count) still fits in maxTokens
// (<= 0 = DefaultMaxTokens), skipping one that doesn't and trying the next, so an oversized candidate never evicts
// shorter ones behind it. When nothing fits, the top candidate is returned whole and over budget (Hindsight's
// floor: an empty answer would read as "nothing known"). It sets candidate.Tokens. Candidates for which skip (nil =
// none) reports true are left out without spending budget.
func pack(cs []candidate, maxTokens int, count tokenCounter, skip func(candidate) bool) []candidate {
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	var out []candidate
	used := 0
	first := -1
	for i, c := range cs {
		if skip != nil && skip(c) {
			continue
		}
		c.Tokens = count.Count(unitText(c))
		if first < 0 {
			first = i
		}
		if used+c.Tokens > maxTokens {
			continue
		}
		used += c.Tokens
		out = append(out, c)
	}
	if len(out) == 0 && first >= 0 {
		c := cs[first]
		c.Tokens = count.Count(unitText(c))
		return []candidate{c}
	}
	return out
}

// preferObservations follows Hindsight's prefer-observations dedup: every observation within the first window of
// ranked (window <= 0 = all of ranked; Recall passes 2 * the per-arm limit) supersedes the raw facts it cites
// (Unit.SourceIDs), which are dropped wherever they rank. The rest is packed into maxTokens (same rules as pack), so
// the freed budget backfills from lower-ranked candidates. An observation below the window never drops a fact.
func preferObservations(ranked []candidate, window, maxTokens int, count tokenCounter) []candidate {
	head := ranked
	if window > 0 && len(head) > window {
		head = head[:window]
	}
	superseded := map[domain.UnitID]bool{}
	for _, c := range head {
		if c.Unit != nil && c.Unit.Type == domain.FactTypeObservation {
			for _, src := range c.Unit.SourceIDs {
				superseded[src] = true
			}
		}
	}
	return pack(ranked, maxTokens, count, func(c candidate) bool {
		return c.Unit != nil && c.Unit.Type != domain.FactTypeObservation && superseded[c.ID]
	})
}

// unitText is the text a candidate spends budget on.
func unitText(c candidate) string {
	if c.Unit == nil {
		return ""
	}
	return c.Unit.Text
}

// truncateTokens returns the longest prefix of text that counts at most n tokens, cut at a word boundary when
// one fits, else mid-word. Text within n is returned unchanged.
func truncateTokens(text string, n int, count tokenCounter) string {
	if count.Count(text) <= n {
		return text
	}
	if n <= 0 {
		return ""
	}
	ends := wordEnds(text)
	k := sort.Search(len(ends), func(i int) bool { return count.Count(text[:ends[i]]) > n })
	if k > 0 {
		return text[:ends[k-1]]
	}
	runes := []rune(text)
	k = sort.Search(len(runes), func(i int) bool { return count.Count(string(runes[:i+1])) > n })
	return string(runes[:k])
}

// wordEnds returns the byte offsets where a word of text ends (a non-space rune followed by a space or the
// end of text).
func wordEnds(text string) []int {
	var out []int
	inWord := false
	for i, r := range text {
		space := unicode.IsSpace(r)
		if inWord && space {
			out = append(out, i)
		}
		inWord = !space
	}
	if inWord {
		out = append(out, len(text))
	}
	return out
}

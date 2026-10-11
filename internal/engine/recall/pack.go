package recall

import (
	"sort"
	"unicode"
)

// Packer fits ranked candidates into a token budget.
type Packer interface {
	// Pack walks cs in order and keeps each candidate whose unit text still fits in maxTokens (<= 0 =
	// DefaultMaxTokens), skipping one that doesn't and trying the next, so an oversized candidate never evicts
	// shorter ones behind it. When nothing fits, the top candidate is returned whole and over budget (Hindsight's
	// floor: an empty answer would read as "nothing known"). It sets Candidate.Tokens.
	Pack(cs []Candidate, maxTokens int) []Candidate
}

// TokenCounter counts tokens for packing and include budgets. It is declared here by its consumer; any
// tokenizer with this method satisfies it.
type TokenCounter interface {
	// Count returns the number of tokens in text.
	Count(text string) int
}

// packer is the Packer.
type packer struct{ count TokenCounter }

var _ Packer = (*packer)(nil)

// newPacker returns a Packer counting unit text with count.
func newPacker(count TokenCounter) Packer { return &packer{count: count} }

// Pack implements Packer.
func (p *packer) Pack(cs []Candidate, maxTokens int) []Candidate {
	out, _ := pack(cs, maxTokens, p.count, nil)
	return out
}

// pack is Pack with an optional skip filter (prefer_observations): candidates for which skip reports true are
// left out without spending budget. It also returns the tokens used.
func pack(cs []Candidate, maxTokens int, count TokenCounter, skip func(Candidate) bool) ([]Candidate, int) {
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	var out []Candidate
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
		return []Candidate{c}, c.Tokens
	}
	return out, used
}

// unitText is the text a candidate spends budget on.
func unitText(c Candidate) string {
	if c.Unit == nil {
		return ""
	}
	return c.Unit.Text
}

// truncateTokens returns the longest prefix of text that counts at most n tokens, cut at a word boundary when
// one fits, else mid-word. Text within n is returned unchanged.
func truncateTokens(text string, n int, count TokenCounter) string {
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

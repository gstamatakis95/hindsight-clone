package retain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"memd/internal/domain"
	"strings"
	"unicode/utf8"
)

// TokenCounter counts tokens for chunking and budgets. It is declared here by its consumer; any
// tokenizer with this method satisfies it.
type TokenCounter interface {
	// Count returns the number of tokens in text.
	Count(text string) int
}

// ApproxTokens is the default TokenCounter: about four characters per token, rounded up. It stands in until a
// real tokenizer is wired (decision 803).
type ApproxTokens struct{}

var _ TokenCounter = ApproxTokens{}

// Count returns ceil(runes / 4).
func (ApproxTokens) Count(text string) int { return (utf8.RuneCountInString(text) + 3) / 4 }

// ChunkOptions sizes chunks in tokens.
type ChunkOptions struct {
	// MaxTokens is the chunk budget; 0 = DefaultChunkMaxTokens.
	MaxTokens int
	// OverlapTokens repeats about this many trailing tokens of a plain-text chunk at the start of the next.
	OverlapTokens int
	// StructuredMaxTokens keeps a JSON conversation turn or JSONL line whole up to this size, even past
	// MaxTokens; 0 = MaxTokens.
	StructuredMaxTokens int
}

// separators are tried in order by the plain-text splitter (Hindsight's sentence-aware split).
var separators = []string{"\n\n", "\n", ". ", "! ", "? ", "; ", ", ", " ", ""}

// Chunk splits text into chunks of at most maxTokens tokens (see ChunkWith).
func Chunk(text string, maxTokens, overlap int, count TokenCounter) []domain.Chunk {
	return ChunkWith(text, ChunkOptions{MaxTokens: maxTokens, OverlapTokens: overlap}, count)
}

// ChunkWith splits text into indexed chunks, each with the SHA-256 of its text and its token count. Text
// within the budget is one chunk. A JSON array of objects (a chat) is cut between turns and JSONL between
// lines, so a turn or line is never split unless it alone exceeds StructuredMaxTokens; a single JSON object
// stays whole up to StructuredMaxTokens. Anything else is split at the most preferred separator present
// (paragraph, line, sentence, clause, word, character) and packed greedily. Blank text has no chunks.
// count nil = ApproxTokens.
func ChunkWith(text string, o ChunkOptions, count TokenCounter) []domain.Chunk {
	if count == nil {
		count = ApproxTokens{}
	}
	if o.MaxTokens <= 0 {
		o.MaxTokens = DefaultChunkMaxTokens
	}
	if o.StructuredMaxTokens <= 0 {
		o.StructuredMaxTokens = o.MaxTokens
	}
	pieces := chunkTexts(text, o, count)
	out := make([]domain.Chunk, 0, len(pieces))
	for i, p := range pieces {
		out = append(out, domain.Chunk{Index: i, Text: p, ContentHash: hashText(p), TokenCount: count.Count(p)})
	}
	return out
}

// chunkTexts picks the chunking strategy for text's shape.
func chunkTexts(text string, o ChunkOptions, count TokenCounter) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if count.Count(text) <= o.MaxTokens {
		return []string{text}
	}
	if turns, ok := conversationTurns(text); ok {
		return packStructured(turns, "[", ",", "]", o, count)
	}
	if isJSONObject(text) {
		if count.Count(text) <= o.StructuredMaxTokens {
			return []string{text}
		}
		return splitRecursive(text, o.MaxTokens, separators, count)
	}
	if lines, ok := jsonlLines(text); ok {
		return packStructured(lines, "", "\n", "", o, count)
	}
	return withOverlap(splitRecursive(text, o.MaxTokens, separators, count), o.OverlapTokens, count)
}

// conversationTurns returns the compacted objects of a JSON array whose elements are all objects.
func conversationTurns(text string) ([]string, bool) {
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil || len(raw) == 0 {
		return nil, false
	}
	turns := make([]string, 0, len(raw))
	for _, r := range raw {
		var buf bytes.Buffer
		if err := json.Compact(&buf, r); err != nil || buf.Len() == 0 || buf.Bytes()[0] != '{' {
			return nil, false
		}
		turns = append(turns, buf.String())
	}
	return turns, true
}

// isJSONObject reports whether text is one JSON object.
func isJSONObject(text string) bool {
	t := strings.TrimSpace(text)
	return strings.HasPrefix(t, "{") && json.Valid([]byte(t))
}

// jsonlLines returns the non-blank lines of text when there are at least two and each is a JSON object.
func jsonlLines(text string) ([]string, bool) {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSuffix(l, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		if !isJSONObject(l) {
			return nil, false
		}
		lines = append(lines, l)
	}
	return lines, len(lines) >= 2
}

// packStructured packs whole units (turns or lines) into chunks of at most o.MaxTokens, wrapping each chunk
// in open/close and joining units with sep. A unit over o.StructuredMaxTokens is split as text within
// min(StructuredMaxTokens, MaxTokens); a unit between the two budgets is kept whole as its own overflow.
func packStructured(units []string, open, sep, closing string, o ChunkOptions, count TokenCounter) []string {
	var out, cur []string
	size := 0
	flush := func() {
		if len(cur) > 0 {
			out = append(out, open+strings.Join(cur, sep)+closing)
			cur, size = nil, 0
		}
	}
	for _, u := range units {
		n := count.Count(u)
		if n > o.StructuredMaxTokens {
			flush()
			out = append(out, splitRecursive(u, min(o.StructuredMaxTokens, o.MaxTokens), separators, count)...)
			continue
		}
		if size+n > o.MaxTokens && len(cur) > 0 {
			flush()
		}
		cur = append(cur, u)
		size += n
	}
	flush()
	return out
}

// splitRecursive is Hindsight's sentence-aware split: cut text at the first separator present, pack the
// pieces greedily within maxTokens and recurse with the remaining separators into any piece still over
// budget. Chunks are whitespace-trimmed; empty ones are dropped.
func splitRecursive(text string, maxTokens int, seps []string, count TokenCounter) []string {
	sep, rest := pickSeparator(text, seps)
	var out, buf []string
	size := 0
	flush := func() {
		if packed := strings.TrimSpace(strings.Join(buf, "")); packed != "" {
			out = append(out, packed)
		}
		buf, size = nil, 0
	}
	for _, piece := range splitKeep(text, sep) {
		n := count.Count(piece)
		if n < maxTokens {
			if len(buf) > 0 && size+n > maxTokens {
				flush()
			}
			buf = append(buf, piece)
			size += n
			continue
		}
		flush()
		if len(rest) > 0 {
			out = append(out, splitRecursive(piece, maxTokens, rest, count)...)
		} else if p := strings.TrimSpace(piece); p != "" {
			out = append(out, p)
		}
	}
	flush()
	return out
}

// pickSeparator returns the first separator of seps present in text (or the last one, "") and the
// separators after it.
func pickSeparator(text string, seps []string) (string, []string) {
	for i, s := range seps {
		if s == "" || strings.Contains(text, s) {
			return s, seps[i+1:]
		}
	}
	return "", nil
}

// splitKeep cuts text before every occurrence of sep, keeping sep at the start of the following piece; ""
// splits into runes.
func splitKeep(text, sep string) []string {
	if sep == "" {
		out := make([]string, 0, len(text))
		for _, r := range text {
			out = append(out, string(r))
		}
		return out
	}
	var out []string
	prev, pos := 0, 0
	for {
		j := strings.Index(text[pos:], sep)
		if j < 0 {
			break
		}
		m := pos + j
		if m > prev {
			out = append(out, text[prev:m])
		}
		prev, pos = m, m+len(sep)
	}
	if prev < len(text) {
		out = append(out, text[prev:])
	}
	return out
}

// withOverlap prefixes every chunk after the first with about overlap trailing tokens of the chunk before.
func withOverlap(chunks []string, overlap int, count TokenCounter) []string {
	if overlap <= 0 || len(chunks) < 2 {
		return chunks
	}
	out := make([]string, len(chunks))
	out[0] = chunks[0]
	for i := 1; i < len(chunks); i++ {
		words := strings.Fields(chunks[i-1])
		start := len(words)
		for start > 0 && count.Count(strings.Join(words[start-1:], " ")) <= overlap {
			start--
		}
		if tail := strings.Join(words[start:], " "); tail != "" {
			out[i] = tail + " " + chunks[i]
		} else {
			out[i] = chunks[i]
		}
	}
	return out
}

// hashText is the hex SHA-256 of s.
func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

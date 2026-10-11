package memory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"memd/internal/domain"
	"strings"
	"unicode/utf8"
)

// tokenCounter counts tokens for chunking: ai.Tokenizer in production, approxTokens when none is wired.
type tokenCounter interface {
	// Count returns the number of tokens in text.
	Count(text string) int
}

// approxTokens is the default tokenCounter: about four characters per token, rounded up. It stands in when no
// tokenizer is wired.
type approxTokens struct{}

var _ tokenCounter = approxTokens{}

// Count returns ceil(runes / 4).
func (approxTokens) Count(text string) int { return (utf8.RuneCountInString(text) + 3) / 4 }

// chunkOptions sizes chunks in tokens.
type chunkOptions struct {
	// MaxTokens is the chunk budget; 0 = defaultChunkMaxTokens.
	MaxTokens int
	// OverlapTokens repeats about this many trailing tokens of a plain-text chunk at the start of the next.
	OverlapTokens int
	// StructuredMaxTokens keeps a JSON conversation turn or JSONL line whole up to this size, even past
	// MaxTokens; 0 = MaxTokens.
	StructuredMaxTokens int
}

// separators are tried in order by the plain-text splitter (Hindsight's sentence-aware split).
var separators = []string{"\n\n", "\n", ". ", "! ", "? ", "; ", ", ", " ", ""}

// chunkWith splits text into indexed chunks, each with the SHA-256 of its text and its token count. Text
// within the budget is one chunk. A JSON array of objects (a chat) is cut between turns and JSONL between
// lines, so a turn or line is never split unless it alone exceeds StructuredMaxTokens; a single JSON object
// stays whole up to StructuredMaxTokens. Anything else is split at the most preferred separator present
// (paragraph, line, sentence, clause, word, character) and packed greedily. Blank text has no chunks.
// count nil = approxTokens.
func chunkWith(text string, o chunkOptions, count tokenCounter) []domain.Chunk {
	if count == nil {
		count = approxTokens{}
	}
	if o.MaxTokens <= 0 {
		o.MaxTokens = defaultChunkMaxTokens
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
func chunkTexts(text string, o chunkOptions, count tokenCounter) []string {
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
	// Leave room for the overlap so a chunk plus the tail prepended to it stays within MaxTokens.
	budget := o.MaxTokens
	if o.OverlapTokens > 0 && o.OverlapTokens < o.MaxTokens {
		budget -= o.OverlapTokens
	}
	return withOverlap(splitRecursive(text, budget, separators, count), o.OverlapTokens, o.MaxTokens, count)
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

// packStructured packs whole units (turns or lines) into chunks of at most o.MaxTokens, counted on the whole
// chunk: open + units joined with sep + closing. A unit whose wrapped form is over o.StructuredMaxTokens is
// split as text within min(StructuredMaxTokens, MaxTokens); one between the two budgets is kept whole as its
// own overflow chunk.
func packStructured(units []string, open, sep, closing string, o chunkOptions, count tokenCounter) []string {
	var out, cur []string
	wrap := func(us []string) string { return open + strings.Join(us, sep) + closing }
	flush := func() {
		if len(cur) > 0 {
			out = append(out, wrap(cur))
			cur = nil
		}
	}
	for _, u := range units {
		if count.Count(open+u+closing) > o.StructuredMaxTokens {
			flush()
			out = append(out, splitRecursive(u, min(o.StructuredMaxTokens, o.MaxTokens), separators, count)...)
			continue
		}
		if len(cur) > 0 && count.Count(wrap(append(cur[:len(cur):len(cur)], u))) > o.MaxTokens {
			flush()
		}
		cur = append(cur, u)
	}
	flush()
	return out
}

// splitRecursive is Hindsight's sentence-aware split: cut text at the first separator present, pack the
// pieces greedily within maxTokens and recurse with the remaining separators into any piece still over
// budget. Chunks are whitespace-trimmed; empty ones are dropped. The budget is checked on the joined buffer:
// size sums the pieces' counts as an upper bound (a subadditive counter such as approxTokens never counts
// joined text higher than its parts) and is replaced by the exact count of the buffer whenever the sum
// passes maxTokens, so packing stays linear-ish even when splitting into single characters.
func splitRecursive(text string, maxTokens int, seps []string, count tokenCounter) []string {
	sep, rest := pickSeparator(text, seps)
	var out []string
	var buf strings.Builder
	size := 0
	flush := func() {
		if packed := strings.TrimSpace(buf.String()); packed != "" {
			out = append(out, packed)
		}
		buf.Reset()
		size = 0
	}
	for _, piece := range splitKeep(text, sep) {
		n := count.Count(piece)
		if n > maxTokens {
			flush()
			if len(rest) > 0 {
				out = append(out, splitRecursive(piece, maxTokens, rest, count)...)
			} else if p := strings.TrimSpace(piece); p != "" {
				out = append(out, p)
			}
			continue
		}
		if size+n > maxTokens && buf.Len() > 0 {
			if exact := count.Count(buf.String() + piece); exact <= maxTokens {
				size = exact - n // exact already includes piece; n is added back below
			} else {
				flush()
			}
		}
		buf.WriteString(piece)
		size += n
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

// withOverlap prefixes every chunk after the first with about overlap trailing tokens of the chunk before,
// never letting the prefixed chunk exceed maxTokens.
func withOverlap(chunks []string, overlap, maxTokens int, count tokenCounter) []string {
	if overlap <= 0 || len(chunks) < 2 {
		return chunks
	}
	out := make([]string, len(chunks))
	out[0] = chunks[0]
	for i := 1; i < len(chunks); i++ {
		words := strings.Fields(chunks[i-1])
		start := len(words)
		for start > 0 {
			tail := strings.Join(words[start-1:], " ")
			if count.Count(tail) > overlap || count.Count(tail+" "+chunks[i]) > maxTokens {
				break
			}
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

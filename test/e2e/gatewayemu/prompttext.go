package gatewayemu

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
)

// This file recovers the data memd rendered into its prompts (internal/prompts/templates), using the
// templates' structural markers: the extraction chunk after "Content:", "[id] text" fact lines, JSON tool
// outputs and fenced JSON blocks. Round-trip tests render the real templates and parse them back.

// Item is one quotable unit of a prompt: a fact, observation or memory with its id.
type Item struct {
	// ID is the unit id as the prompt showed it.
	ID string
	// Text is the unit text.
	Text string
}

// contentMarker precedes the chunk in extract_user; chunkStart opens the block of fixed header lines before
// it (the mission, which is free text, comes earlier).
const (
	contentMarker = "\nContent:\n"
	chunkStart    = "Extract facts from the following chunk."
)

// chunkText returns the chunk of an extraction user message: everything after the first "Content:" line that
// follows the header, so a chunk that itself contains such a line stays whole.
func chunkText(user string) string {
	header := strings.Index(user, chunkStart)
	if header < 0 {
		header = 0
	}
	if i := strings.Index(user[header:], contentMarker); i >= 0 {
		return strings.TrimSpace(user[header+i+len(contentMarker):])
	}
	return strings.TrimSpace(user)
}

// sentenceEnd matches the end of a sentence: terminal punctuation followed by whitespace.
var sentenceEnd = regexp.MustCompile(`([.!?])\s+`)

// Sentences splits text into sentences: on newlines and after ., ! or ? followed by whitespace. Blank pieces
// are dropped; the terminal punctuation stays with its sentence.
func Sentences(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		marked := sentenceEnd.ReplaceAllString(line, "$1\x00")
		for _, s := range strings.Split(marked, "\x00") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// entityNames returns the capitalised words of a sentence after the first word (which is capitalised anyway),
// deduplicated, as candidate entity names.
func entityNames(sentence string) []string {
	words := strings.FieldsFunc(sentence, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\''
	})
	seen := map[string]bool{}
	var out []string
	for i, w := range words {
		w = strings.TrimSuffix(w, "'s")
		r := []rune(w)
		if i == 0 || len(r) < 2 || !unicode.IsUpper(r[0]) || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// factLine matches a consolidate_user new-fact line: "[id] text" with an optional "(occurred_start=...)" tail.
var factLine = regexp.MustCompile(`^\[([^\]\s]+)\] (.*?)(?: \((?:occurred_start|occurred_end|mentioned_at)=[^)]*\))?$`)

// bulletLine matches a synthesize / delta source line: "- [type:id] text" with an optional date tail.
var bulletLine = regexp.MustCompile(`^- \[(?:[a-z_]+:)?([^\]\s:]+)\] (.*?)(?: \((?:occurred|mentioned_at): [^)]*\))?$`)

// lineItems returns the items of every line of text that re matches (group 1 the id, group 2 the text).
func lineItems(text string, re *regexp.Regexp) []Item {
	var out []Item
	for _, line := range strings.Split(text, "\n") {
		if m := re.FindStringSubmatch(strings.TrimRight(line, " ")); m != nil {
			out = append(out, Item{ID: m[1], Text: m[2]})
		}
	}
	return out
}

// newFacts returns the new facts of a consolidate_user message (the lines between "### New facts" and
// "### Existing observations").
func newFacts(user string) []Item {
	start := strings.Index(user, "### New facts")
	if start < 0 {
		return nil
	}
	section := user[start:]
	if end := strings.Index(section, "### Existing observations"); end >= 0 {
		section = section[:end]
	}
	return lineItems(section, factLine)
}

// sourceLines returns the "- [type:id] text" items of a synthesize or delta user message.
func sourceLines(user string) []Item { return lineItems(user, bulletLine) }

// sectionLine returns the first non-blank line after the markdown heading "## <heading>", or "".
func sectionLine(text, heading string) string {
	marker := "## " + heading + "\n"
	i := strings.Index(text, marker)
	if i < 0 {
		return ""
	}
	for _, line := range strings.Split(text[i+len(marker):], "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// between returns the text between the begin and end markers, trimmed, or "".
func between(text, begin, end string) string {
	_, after, ok := strings.Cut(text, begin)
	if !ok {
		return ""
	}
	inside, _, _ := strings.Cut(after, end)
	return strings.TrimSpace(inside)
}

// toolOutput is the part of a reflect tool's JSON output the emulator quotes: memories (recall),
// observations (search_observations) and source facts (expand).
type toolOutput struct {
	// Memories are recall's results.
	Memories []jsonItem `json:"memories"`
	// Observations are search_observations' results.
	Observations []jsonItem `json:"observations"`
}

// jsonItem is a unit in a tool output.
type jsonItem struct {
	// ID is the unit id.
	ID string `json:"id"`
	// Text is the unit text.
	Text string `json:"text"`
}

// toolItems returns the memories and observations of a tool output; anything else yields none.
func toolItems(output string) (memories, observations []Item) {
	var out toolOutput
	if json.Unmarshal([]byte(output), &out) != nil {
		return nil, nil
	}
	for _, m := range out.Memories {
		if m.Text != "" {
			memories = append(memories, Item(m))
		}
	}
	for _, o := range out.Observations {
		if o.Text != "" {
			observations = append(observations, Item(o))
		}
	}
	return memories, observations
}

// jsonFence matches a fenced ```json block.
var jsonFence = regexp.MustCompile("(?s)```json\n(.*?)\n```")

// fencedItems returns the memories and observations of every fenced JSON block of text (the retrieved data
// of reflect_final_user).
func fencedItems(text string) (memories, observations []Item) {
	for _, m := range jsonFence.FindAllStringSubmatch(text, -1) {
		mem, obs := toolItems(m[1])
		memories = append(memories, mem...)
		observations = append(observations, obs...)
	}
	return memories, observations
}

// answerDocument is the text structured_output_user asks to extract from (between the triple quotes).
func answerDocument(user string) string {
	return between(user, "\"\"\"\n", "\n\"\"\"")
}

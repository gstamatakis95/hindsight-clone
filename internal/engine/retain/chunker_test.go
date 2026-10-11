package retain

import (
	"encoding/json"
	"strings"
	"testing"
)

// approx is the default token counter.
var approx approxTokens

// chunkStrings returns the texts of chunkWith's chunks.
func chunkStrings(text string, o chunkOptions) []string {
	var out []string
	for _, c := range chunkWith(text, o, approxTokens{}) {
		out = append(out, c.Text)
	}
	return out
}

func TestChunkSmallTextIsOneChunk(t *testing.T) {
	got := chunkWith("Alice likes tea.", chunkOptions{MaxTokens: 100}, nil)
	if len(got) != 1 || got[0].Text != "Alice likes tea." || got[0].Index != 0 ||
		got[0].ContentHash != hashText("Alice likes tea.") ||
		got[0].TokenCount != 4 {
		t.Fatalf("chunks = %+v", got)
	}
	if got := chunkWith("  \n ", chunkOptions{}, nil); len(got) != 0 {
		t.Fatalf("blank text gave %d chunks", len(got))
	}
}

func TestChunkPlainTextSplitsAtParagraphsWithinBudget(t *testing.T) {
	got := chunkStrings(paragraphs(6, "one"), chunkOptions{MaxTokens: 12})
	if len(got) != 6 {
		t.Fatalf("%d chunks, want 6: %q", len(got), got)
	}
	for i, c := range got {
		if !strings.HasPrefix(c, "Paragraph "+string(rune('A'+i))) || approx.Count(c) > 12 {
			t.Errorf("chunk %d = %q", i, c)
		}
	}
	// A long sentence with no paragraph breaks falls back to word boundaries, every chunk within budget.
	long := strings.Repeat("word ", 200)
	for _, c := range chunkStrings(long, chunkOptions{MaxTokens: 10}) {
		if approx.Count(c) > 10 || strings.HasPrefix(c, " ") {
			t.Fatalf("chunk %q over budget or untrimmed", c)
		}
	}
}

func TestChunkKeepsChatTurnsWhole(t *testing.T) {
	turns := []map[string]string{}
	for i := 0; i < 6; i++ {
		turns = append(turns, map[string]string{"role": "user", "content": strings.Repeat("hello there. ", 4)})
	}
	raw, _ := json.Marshal(turns)
	got := chunkStrings(string(raw), chunkOptions{MaxTokens: 40})
	if len(got) < 2 {
		t.Fatalf("%d chunks, want several", len(got))
	}
	total := 0
	for _, c := range got {
		var part []map[string]string
		if err := json.Unmarshal([]byte(c), &part); err != nil {
			t.Fatalf("chunk is not a JSON array of whole turns: %q (%v)", c, err)
		}
		total += len(part)
	}
	if total != 6 {
		t.Errorf("%d turns across chunks, want 6", total)
	}
}

func TestChunkKeepsJSONLLinesWhole(t *testing.T) {
	var lines []string
	for i := 0; i < 8; i++ {
		lines = append(lines, `{"event":"login","user":"alice","note":"a fairly long note"}`)
	}
	got := chunkStrings(strings.Join(lines, "\n"), chunkOptions{MaxTokens: 40})
	if len(got) < 2 {
		t.Fatalf("%d chunks, want several", len(got))
	}
	n := 0
	for _, c := range got {
		for _, l := range strings.Split(c, "\n") {
			if l != lines[0] {
				t.Fatalf("line split: %q", l)
			}
			n++
		}
	}
	if n != 8 {
		t.Errorf("%d lines, want 8", n)
	}
}

func TestChunkStructuredLimit(t *testing.T) {
	big := `{"role":"user","content":"` + strings.Repeat("x ", 60) + `"}`
	small := `{"role":"user","content":"hi"}`
	text := "[" + small + "," + big + "," + small + "]"
	// Up to the structured limit an oversized turn stays whole as its own chunk.
	got := chunkStrings(text, chunkOptions{MaxTokens: 20, StructuredMaxTokens: 100})
	found := false
	for _, c := range got {
		if strings.Contains(c, big) {
			found = true
		}
	}
	if !found {
		t.Errorf("turn within the structured limit was split: %q", got)
	}
	// Past it, the turn is split as text within the budget.
	for _, c := range chunkStrings(text, chunkOptions{MaxTokens: 20, StructuredMaxTokens: 20}) {
		if approx.Count(c) > 20 {
			t.Errorf("chunk over budget: %q", c)
		}
	}
}

func TestChunkOverlap(t *testing.T) {
	got := chunkStrings(paragraphs(3, "one"), chunkOptions{MaxTokens: 16, OverlapTokens: 3})
	if len(got) != 3 || !strings.HasPrefix(got[1], "in depth. Paragraph B") {
		t.Fatalf("chunks = %q", got)
	}
	// With overlap every chunk, prefixed tail included, stays within MaxTokens.
	for _, o := range []chunkOptions{{MaxTokens: 12, OverlapTokens: 3}, {MaxTokens: 10, OverlapTokens: 4}} {
		for _, text := range []string{paragraphs(8, "overlap"), strings.Repeat("alpha beta gamma. ", 80)} {
			for _, c := range chunkStrings(text, o) {
				if approx.Count(c) > o.MaxTokens {
					t.Errorf("%+v: chunk %q has %d tokens", o, c, approx.Count(c))
				}
			}
		}
	}
}

// squash drops all whitespace from s.
func squash(s string) string { return strings.Join(strings.Fields(s), "") }

// chatText is a JSON chat of n turns with varied content.
func chatText(t *testing.T, n int) string {
	t.Helper()
	turns := make([]map[string]string, 0, n)
	for i := 0; i < n; i++ {
		turns = append(turns, map[string]string{
			"role": "user", "content": strings.Repeat("turn "+string(rune('a'+i%26))+" says hi. ", 1+i%3),
		})
	}
	raw, err := json.Marshal(turns)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestChunkDeterministicCompleteAndWithinBudget(t *testing.T) {
	const max = 24
	o := chunkOptions{MaxTokens: max}
	plain := paragraphs(10, "determinism") + "\n\n" + strings.Repeat("lorem ipsum dolor sit amet, ", 40) +
		strings.Repeat("x", 300)
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, `{"n":`+string(rune('0'+i%10))+`,"note":"`+strings.Repeat("ab ", 1+i%5)+`"}`)
	}
	jsonl := strings.Join(lines, "\n")
	chat := chatText(t, 20)
	for name, text := range map[string]string{"plain": plain, "jsonl": jsonl, "chat": chat} {
		first, second := chunkWith(text, o, nil), chunkWith(text, o, nil)
		if len(first) != len(second) {
			t.Fatalf("%s: %d then %d chunks", name, len(first), len(second))
		}
		for i := range first {
			if first[i] != second[i] {
				t.Errorf("%s: chunk %d differs between runs", name, i)
			}
			if approx.Count(first[i].Text) > max || first[i].TokenCount > max {
				t.Errorf("%s: chunk %q has %d tokens, budget %d", name, first[i].Text, approx.Count(first[i].Text), max)
			}
		}
	}
	// Plain text: nothing but whitespace is lost or duplicated.
	var joined strings.Builder
	for _, c := range chunkStrings(plain, o) {
		joined.WriteString(c)
	}
	if squash(joined.String()) != squash(plain) {
		t.Error("plain chunks do not cover the text exactly")
	}
	assertJSONLCovered(t, jsonl, chunkStrings(jsonl, o))
	assertChatCovered(t, chat, chunkStrings(chat, o))
}

// assertJSONLCovered checks every line of jsonl appears exactly once, in order, across chunks.
func assertJSONLCovered(t *testing.T, jsonl string, chunks []string) {
	t.Helper()
	var lines []string
	for _, c := range chunks {
		lines = append(lines, strings.Split(c, "\n")...)
	}
	if strings.Join(lines, "\n") != jsonl {
		t.Errorf("jsonl lines = %q", lines)
	}
}

// assertChatCovered checks every turn of chat appears exactly once, in order, across chunks.
func assertChatCovered(t *testing.T, chat string, chunks []string) {
	t.Helper()
	var turns, want []json.RawMessage
	for _, c := range chunks {
		var part []json.RawMessage
		if err := json.Unmarshal([]byte(c), &part); err != nil {
			t.Fatalf("chat chunk %q: %v", c, err)
		}
		turns = append(turns, part...)
	}
	if err := json.Unmarshal([]byte(chat), &want); err != nil {
		t.Fatal(err)
	}
	if len(turns) != len(want) {
		t.Fatalf("%d turns, want %d", len(turns), len(want))
	}
	for i := range want {
		if string(turns[i]) != string(want[i]) {
			t.Errorf("turn %d = %s, want %s", i, turns[i], want[i])
		}
	}
}

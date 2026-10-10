package retain

import (
	"encoding/json"
	"strings"
	"testing"
)

// approx is the default token counter.
var approx ApproxTokens

// chunkStrings returns the texts of ChunkWith's chunks.
func chunkStrings(text string, o ChunkOptions) []string {
	var out []string
	for _, c := range ChunkWith(text, o, ApproxTokens{}) {
		out = append(out, c.Text)
	}
	return out
}

func TestChunkSmallTextIsOneChunk(t *testing.T) {
	got := ChunkWith("Alice likes tea.", ChunkOptions{MaxTokens: 100}, nil)
	if len(got) != 1 || got[0].Text != "Alice likes tea." || got[0].Index != 0 ||
		got[0].ContentHash != hashText("Alice likes tea.") ||
		got[0].TokenCount != 4 {
		t.Fatalf("chunks = %+v", got)
	}
	if got := ChunkWith("  \n ", ChunkOptions{}, nil); len(got) != 0 {
		t.Fatalf("blank text gave %d chunks", len(got))
	}
}

func TestChunkPlainTextSplitsAtParagraphsWithinBudget(t *testing.T) {
	got := chunkStrings(paragraphs(6, "one"), ChunkOptions{MaxTokens: 12})
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
	for _, c := range chunkStrings(long, ChunkOptions{MaxTokens: 10}) {
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
	got := chunkStrings(string(raw), ChunkOptions{MaxTokens: 40})
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
	got := chunkStrings(strings.Join(lines, "\n"), ChunkOptions{MaxTokens: 40})
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
	got := chunkStrings(text, ChunkOptions{MaxTokens: 20, StructuredMaxTokens: 100})
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
	for _, c := range chunkStrings(text, ChunkOptions{MaxTokens: 20, StructuredMaxTokens: 20}) {
		if approx.Count(c) > 20 {
			t.Errorf("chunk over budget: %q", c)
		}
	}
}

func TestChunkOverlap(t *testing.T) {
	got := chunkStrings(paragraphs(3, "one"), ChunkOptions{MaxTokens: 12, OverlapTokens: 3})
	if len(got) != 3 || !strings.HasPrefix(got[1], "in depth. Paragraph B") {
		t.Fatalf("chunks = %q", got)
	}
}

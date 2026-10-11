package gatewayemu

import (
	"memd/internal/domain"
	"memd/internal/prompts"
	"reflect"
	"testing"
	"time"
)

// TestPromptTextRoundTrip renders the real templates and checks the emulator recovers exactly what was put
// in, so its parsing follows the templates rather than ad-hoc text.
func TestPromptTextRoundTrip(t *testing.T) {
	at := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	facts := []prompts.SourceFact{
		{ID: "f-1", Type: "world", Text: "Alice moved to Paris (finally).", MentionedAt: &at},
		{ID: "f-2", Type: "experience", Text: "Bob plays chess.", OccurredStart: &at, OccurredEnd: &at},
		{ID: "f-3", Text: "Ships on Fridays."},
	}
	want := []Item{{"f-1", facts[0].Text}, {"f-2", facts[1].Text}, {"f-3", facts[2].Text}}
	render := func(name string, data any) string {
		t.Helper()
		out, err := prompts.Render(name, data)
		if err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		return out
	}

	chunk := "Line one. Line two!\nContent:\nstill the chunk"
	user := render(prompts.ExtractUser, prompts.ExtractData{
		Mission: "m", Context: "c", MentionedAt: &at, Metadata: map[string]string{"k": "v"},
		Hints: []domain.EntityHint{{Text: "Alice"}}, AgentName: "Agent", Text: chunk,
	})
	if got := chunkText(user); got != chunk {
		t.Errorf("chunkText = %q, want %q", got, chunk)
	}
	plain := "Alice moved to Paris. She loves Go!"
	if got := chunkText(render(prompts.ExtractUser, prompts.ExtractData{Text: plain})); got != plain {
		t.Errorf("chunkText = %q, want %q", got, plain)
	}

	consolidate := render(prompts.ConsolidateUser, prompts.ConsolidateData{
		Facts:        facts,
		Capacity:     &prompts.ConsolidateCapacity{Max: 5, Remaining: 1},
		Observations: []prompts.ConsolidateObservation{{ID: "o-1", Text: "[x] not a new fact"}},
	})
	if got := newFacts(consolidate); !reflect.DeepEqual(got, want) {
		t.Errorf("newFacts = %v, want %v", got, want)
	}

	for _, name := range []string{
		prompts.SynthesizeUser, prompts.MentalModelDeltaUser, prompts.MentalModelStructuredDeltaUser,
	} {
		var data any = prompts.SynthesizeData{SourceQuery: "Topic?", Facts: facts}
		if name != prompts.SynthesizeUser {
			data = prompts.DeltaData{SourceQuery: "Topic?", CurrentMarkdown: "## A\n\nB", Facts: facts}
		}
		if got := sourceLines(render(name, data)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: sourceLines = %v, want %v", name, got, want)
		}
	}

	obs := render(prompts.SynthesizeUser, prompts.SynthesizeData{Observations: facts[:1]})
	if got := sourceLines(obs); !reflect.DeepEqual(got, want[:1]) {
		t.Errorf("observation lines = %v", got)
	}

	final := render(prompts.ReflectFinalUser, prompts.ReflectData{Query: "q", Retrieved: []prompts.ToolOutput{
		{Tool: "recall", Output: `{"memories":[{"id":"m-1","text":"Alice lives in Paris."}]}`},
		{Tool: "search_observations", Output: `{"observations":[{"id":"o-1","text":"Alice is in France."}]}`},
	}})
	mem, ob := fencedItems(final)
	if !reflect.DeepEqual(mem, []Item{{"m-1", "Alice lives in Paris."}}) ||
		!reflect.DeepEqual(ob, []Item{{"o-1", "Alice is in France."}}) {
		t.Errorf("fencedItems = %v, %v", mem, ob)
	}

	doc := "Answer line one.\nAnswer line two."
	so := render(prompts.StructuredOutputUser, prompts.StructuredOutputData{Document: doc, Schema: "{}"})
	if got := answerDocument(so); got != doc {
		t.Errorf("answerDocument = %q, want %q", got, doc)
	}
}

// TestSentences checks sentence splitting.
func TestSentences(t *testing.T) {
	got := Sentences("Alice moved to Paris. She loves Go!  Really?\nNew line without stop\n\n e.g.")
	want := []string{"Alice moved to Paris.", "She loves Go!", "Really?", "New line without stop", "e.g."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Sentences = %q, want %q", got, want)
	}
}

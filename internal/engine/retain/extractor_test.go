package retain

import (
	"errors"
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/prompts"
	"strings"
	"testing"
	"time"
)

// extractCfg binds EXTRACT and sets the mode.
func extractCfg(mode domain.ExtractionMode) domain.BankConfig {
	return domain.BankConfig{ExtractionMode: mode, Models: []domain.SlotBinding{
		{Slot: domain.ModelSlotExtract, Binding: domain.ModelBinding{Model: "m"}},
	}}
}

func TestExtractorChunksModeNoCall(t *testing.T) {
	llm := fake.NewLLM()
	facts, usage, err := NewExtractor(llm, ai.NewRouter()).Extract(t.Context(), extractInput{
		Text: "raw chunk", Config: extractCfg(domain.ExtractionModeChunks),
	})
	if err != nil || len(facts) != 1 || facts[0].Text != "raw chunk" || facts[0].Type != domain.FactTypeWorld ||
		len(usage) != 0 {
		t.Fatalf("facts %+v usage %v err %v", facts, usage, err)
	}
	if len(llm.Calls()) != 0 {
		t.Fatalf("chunks mode made %d calls", len(llm.Calls()))
	}
}

func TestExtractorComposesDimensions(t *testing.T) {
	llm := fake.NewLLM()
	f := fact("Emily married Sarah")
	f.When, f.Where, f.Who, f.Why = "June 2024", "Paris", "Emily, the user's roommate", "N/A"
	f.Entities = []jsonEntity{{Name: " Emily ", Kind: ptr("Person")}, {Name: ""}}
	f.Labels = []domain.Label{{Key: "topic", Value: "family"}}
	llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractVerbose, factsBody(t, f))
	at := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)
	facts, usage, err := NewExtractor(llm, ai.NewRouter()).Extract(t.Context(), extractInput{
		Text: "chunk", MentionedAt: &at, Context: "chat with user", Config: extractCfg(domain.ExtractionModeVerbose),
		Hints: []domain.EntityHint{{Text: "Sarah"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "Emily married Sarah | When: June 2024 | Involving: Emily, the user's roommate"
	if len(facts) != 1 || facts[0].Text != want {
		t.Fatalf("facts = %+v, want text %q", facts, want)
	}
	if len(facts[0].Entities) != 1 || facts[0].Entities[0] != (domain.Mention{Name: "Emily", Kind: "person"}) ||
		len(facts[0].Labels) != 1 {
		t.Errorf("entities %+v labels %+v", facts[0].Entities, facts[0].Labels)
	}
	if len(usage) != 1 || usage[0].Slot != domain.ModelSlotExtract {
		t.Errorf("usage = %+v", usage)
	}
	call := llm.Calls()[0].Structured
	if !strings.Contains(call.User, "chat with user") || !strings.Contains(call.User, "Sarah") ||
		!strings.Contains(call.User, "2024-07-01") || call.Binding.Model != "m" {
		t.Errorf("user prompt misses context, hints or date:\n%s", call.User)
	}
}

func TestExtractorFactTypeBySpeaker(t *testing.T) {
	tests := []struct {
		factType, kind string
		want           domain.FactType
	}{
		{"assistant", "conversation", domain.FactTypeExperience},
		{"world", "conversation", domain.FactTypeWorld},
	}
	for _, tc := range tests {
		llm := fake.NewLLM()
		f := fact("I fixed the bug")
		f.FactType, f.FactKind = tc.factType, tc.kind
		llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, f))
		facts, _, err := NewExtractor(llm, ai.NewRouter()).Extract(t.Context(), extractInput{
			Text: "x", Config: extractCfg(""),
		})
		if err != nil || len(facts) != 1 || facts[0].Type != tc.want {
			t.Errorf("fact_type %q: %+v %v, want %s", tc.factType, facts, err, tc.want)
		}
	}
	// The fallback for unexpected values (no schema enforcement) uses fact_kind, then world.
	for raw, want := range map[[2]string]domain.FactType{
		{"", "assistant"}: domain.FactTypeExperience, {"other", "event"}: domain.FactTypeWorld,
	} {
		if got := factType(rawFact{FactType: raw[0], FactKind: raw[1]}); got != want {
			t.Errorf("factType(%v) = %s, want %s", raw, got, want)
		}
	}
}

func TestExtractorEventDatesCausesAndDrops(t *testing.T) {
	llm := fake.NewLLM()
	cause := fact("It rained")
	cause.FactKind, cause.OccurredStart, cause.TimeExpression = "event", ptr("2024-05-01"), ptr("May 1")
	empty := fact("")
	effect := fact("The game was cancelled")
	effect.CausedBy = []int{0, 1, 2, 5}
	conv := fact("Alice talks a lot")
	conv.OccurredStart, conv.TimeExpression = ptr("2024-01-01"), ptr("last year")
	llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, cause, empty, effect, conv))
	facts, _, err := NewExtractor(llm, ai.NewRouter()).Extract(t.Context(), extractInput{
		Text: "x", Config: extractCfg(domain.ExtractionModeConcise),
	})
	if err != nil || len(facts) != 3 {
		t.Fatalf("facts = %+v, %v", facts, err)
	}
	day := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	if !sameTime(facts[0].OccurredStart, &day) || !sameTime(facts[0].OccurredEnd, &day) ||
		facts[0].TimeExpression != "May 1" {
		t.Errorf("event dates = %v %v %q", facts[0].OccurredStart, facts[0].OccurredEnd, facts[0].TimeExpression)
	}
	if len(facts[1].Causes) != 1 || facts[1].Causes[0] != 0 {
		t.Errorf("causes = %v, want [0] (the dropped fact and later ones removed)", facts[1].Causes)
	}
	if facts[2].OccurredStart != nil || facts[2].TimeExpression != "" {
		t.Errorf("conversation fact kept dates: %+v", facts[2])
	}
}

func TestExtractorVerbatimUsesChunkText(t *testing.T) {
	llm := fake.NewLLM()
	body := `{"facts":[{"when":"N/A","where":"N/A","who":"N/A","fact_kind":"conversation","fact_type":"world",` +
		`"occurred_start":null,"occurred_end":null,"time_expression":null,` +
		`"entities":[{"name":"Alice","kind":null}],"labels":[]}]}`
	llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractVerbatim, body)
	facts, _, err := NewExtractor(llm, ai.NewRouter()).Extract(t.Context(), extractInput{
		Text: "Alice said hi.", Config: extractCfg(domain.ExtractionModeVerbatim),
	})
	if err != nil || len(facts) != 1 || facts[0].Text != "Alice said hi." || len(facts[0].Entities) != 1 {
		t.Fatalf("facts = %+v, %v", facts, err)
	}
}

func TestExtractorCustomAndErrors(t *testing.T) {
	llm := fake.NewLLM()
	llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractCustom, factsBody(t, fact("x")))
	cfg := extractCfg(domain.ExtractionModeCustom)
	cfg.CustomExtractionPrompt = "Only extract recipes."
	ex := NewExtractor(llm, ai.NewRouter())
	if _, _, err := ex.Extract(t.Context(), extractInput{Text: "x", Config: cfg}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.Calls()[0].Structured.System, "Only extract recipes.") {
		t.Error("custom prompt not in the system message")
	}
	_, _, err := ex.Extract(t.Context(), extractInput{Config: extractCfg("bogus")})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("unknown mode: %v", err)
	}
	llm.Fail(domain.ModelSlotExtract, prompts.ExtractConcise, errExtract)
	if _, _, err := ex.Extract(t.Context(), extractInput{Config: extractCfg("")}); !errors.Is(err, errExtract) {
		t.Errorf("gateway failure: %v", err)
	}
}

func TestExtractorCustomBlankPromptUsesConcise(t *testing.T) {
	llm := fake.NewLLM()
	llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("x")))
	cfg := extractCfg(domain.ExtractionModeCustom)
	cfg.CustomExtractionPrompt = "  \n\t "
	facts, _, err := NewExtractor(llm, ai.NewRouter()).Extract(t.Context(), extractInput{Text: "x", Config: cfg})
	if err != nil || len(facts) != 1 {
		t.Fatalf("facts = %+v, %v", facts, err)
	}
	if calls := llm.Calls(); len(calls) != 1 || calls[0].Name != prompts.ExtractConcise {
		t.Errorf("calls = %+v, want one %s call", calls, prompts.ExtractConcise)
	}
}

func TestExtractorSpaceSeparatedDateAndDuplicateCauses(t *testing.T) {
	llm := fake.NewLLM()
	cause := fact("It rained")
	cause.FactKind, cause.OccurredStart = "event", ptr("2024-05-01 14:30:00")
	effect := fact("The game was cancelled")
	effect.CausedBy = []int{0, 0, 0}
	llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, cause, effect))
	facts, _, err := NewExtractor(llm, ai.NewRouter()).Extract(t.Context(), extractInput{
		Text: "x", Config: extractCfg(domain.ExtractionModeConcise),
	})
	if err != nil || len(facts) != 2 {
		t.Fatalf("facts = %+v, %v", facts, err)
	}
	at := time.Date(2024, 5, 1, 14, 30, 0, 0, time.UTC)
	if !sameTime(facts[0].OccurredStart, &at) {
		t.Errorf("occurred_start = %v, want %v", facts[0].OccurredStart, at)
	}
	if len(facts[1].Causes) != 1 || facts[1].Causes[0] != 0 {
		t.Errorf("causes = %v, want [0]", facts[1].Causes)
	}
}

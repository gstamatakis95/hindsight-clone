package reflect_test

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/recall"
	rf "memd/internal/engine/reflect"
	"memd/internal/fake"
	"memd/internal/port"
	"memd/internal/prompts"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestReflectRetrievalErrorIsUnavailable(t *testing.T) {
	cases := map[string]struct {
		cause, want error
	}{
		"database":      {errors.New("connection refused"), domain.ErrUnavailable},
		"reranker":      {fmt.Errorf("%w: rerank: 503", domain.ErrUnavailable), domain.ErrUnavailable},
		"embedder":      {fmt.Errorf("%w: embed: timeout", domain.ErrUnavailable), domain.ErrUnavailable},
		"bank vanished": {fmt.Errorf("%w: bank", domain.ErrNotFound), domain.ErrNotFound},
		"unbound slot": {
			fmt.Errorf("%w: no model bound to slot \"rerank\"", domain.ErrInvalidArgument), domain.ErrInvalidArgument,
		},
		"bank deleting": {domain.ErrBankDeleting, domain.ErrFailedPrecondition},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, domain.BankConfig{})
			h.rc.RecallFunc = func(context.Context, recall.RecallRequest) (recall.RecallResult, error) {
				return recall.RecallResult{}, tc.cause
			}
			h.turns(recallCall(), doneCall("never"))
			_, err := h.reflect(nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !errors.Is(tc.want, domain.ErrUnavailable) && errors.Is(err, domain.ErrUnavailable) {
				t.Errorf("a classified error must keep its class, not become UNAVAILABLE: %v", err)
			}
			if n := len(h.loopCalls()); n != 1 {
				t.Errorf("loop turns = %d, want 1 (no answer from missing evidence)", n)
			}
		})
	}
	t.Run("page reader", func(t *testing.T) {
		h := newHarness(t, domain.BankConfig{})
		h.pages.SearchPagesFunc = func(context.Context, port.BankRef, string, int) ([]domain.PageHit, error) {
			return nil, errors.New("pages down")
		}
		h.turns(calls(rf.ToolSearchMentalModels, `{"query":"alice"}`), doneCall("never"))
		if _, err := h.reflect(nil); !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("err = %v, want UNAVAILABLE", err)
		}
	})
}

func TestReflectEmptyRetrievalIsNotFailure(t *testing.T) {
	h := newHarness(t, domain.BankConfig{}) // recall finds nothing
	h.turns(recallCall(), doneCall("Nothing is recorded about Alice."))
	res, err := h.reflect(nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Nothing is recorded about Alice." || len(res.BasedOn.Memories) != 0 {
		t.Errorf("res = %+v", res)
	}
	if res.Trace.StopReason != rf.StopAnswered {
		t.Errorf("stop = %q", res.Trace.StopReason)
	}
}

func TestReflectEmptyAnswerFails(t *testing.T) {
	t.Run("blank done answer", func(t *testing.T) {
		h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"))
		h.turns(recallCall(), doneCall("   "))
		if _, err := h.reflect(nil); !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("err = %v, want UNAVAILABLE", err)
		}
	})
	t.Run("blank synthesis", func(t *testing.T) {
		h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"))
		h.turns(recallCall(), text("prose instead of done"))
		h.llm.Script(domain.ModelSlotReflect, prompts.ReflectDoneRequest, text("still prose"))
		h.llm.Script(domain.ModelSlotReflect, prompts.ReflectFinal, text(""))
		if _, err := h.reflect(nil); !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("err = %v, want UNAVAILABLE", err)
		}
	})
}

func TestReflectModelWithoutToolCallsFails(t *testing.T) {
	h := newHarness(t, domain.BankConfig{})
	h.turns(text("Alice likes tea."))
	_, err := h.reflect(nil)
	if !errors.Is(err, domain.ErrUnavailable) || !strings.Contains(err.Error(), "tool") {
		t.Fatalf("err = %v, want UNAVAILABLE about tool calls", err)
	}
	all := h.llm.Calls()
	if len(all) != 2 || all[0].Chat.ToolChoice != "required" || all[1].Chat.ToolChoice != "required" {
		t.Errorf("want one retry under tool_choice=required and no salvage of the prose; calls = %d", len(all))
	}

	t.Run("prose once, then a tool call", func(t *testing.T) {
		h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"))
		h.turns(text("Alice likes tea."), recallCall(), doneCall("Alice likes tea.", "u1"))
		res, err := h.reflect(nil)
		if err != nil || res.Text != "Alice likes tea." {
			t.Fatalf("res %q err %v", res.Text, err)
		}
		turns := h.loopCalls()
		var choices []string
		for _, c := range turns {
			choices = append(choices, c.Chat.ToolChoice)
		}
		if !slices.Equal(choices, []string{"required", "required", "auto"}) {
			t.Errorf("tool choices = %v, want required until the first tool call, then auto", choices)
		}
	})
}

// errOverflow is a gateway error for a prompt over the context window, classified as the gateway does it
// (domain.ErrContextOverflow, which wraps ErrUnavailable).
var errOverflow = fmt.Errorf("%w: gateway: status 400: This model's maximum context length is 8192 tokens",
	domain.ErrContextOverflow)

func TestReflectContextOverflowSynthesizes(t *testing.T) {
	h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes green tea"))
	h.turns(recallCall(), fake.Reply{Err: errOverflow})
	h.llm.Script(domain.ModelSlotReflect, prompts.ReflectFinal, text("Alice likes green tea."))
	res, err := h.reflect(nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Alice likes green tea." || res.Trace.StopReason != rf.StopContextFull {
		t.Fatalf("text %q stop %q", res.Text, res.Trace.StopReason)
	}
	final := h.llm.CallsFor(domain.ModelSlotReflect, prompts.ReflectFinal)
	if len(final) != 1 || len(final[0].Chat.Tools) != 0 {
		t.Fatalf("final calls = %d, want 1 tool-less call", len(final))
	}
	if !strings.Contains(final[0].Chat.Messages[1].Content, "Alice likes green tea") {
		t.Errorf("final prompt lacks the gathered evidence:\n%s", final[0].Chat.Messages[1].Content)
	}
	if len(res.BasedOn.Memories) != 1 || res.BasedOn.Memories[0].ID != "u1" {
		t.Errorf("based_on = %+v, want everything retrieved", res.BasedOn.Memories)
	}
}

func TestReflectSynthesisFallbacks(t *testing.T) {
	t.Run("synthesis overflow drops older results", func(t *testing.T) {
		h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes green tea"))
		h.turns(recallCall(), recallCall(), fake.Reply{Err: errOverflow})
		h.llm.Script(domain.ModelSlotReflect, prompts.ReflectFinal, fake.Reply{Err: errOverflow}, text("Tea."))
		res, err := h.reflect(nil)
		if err != nil || res.Text != "Tea." {
			t.Fatalf("res %q err %v", res.Text, err)
		}
		final := h.llm.CallsFor(domain.ModelSlotReflect, prompts.ReflectFinal)
		if len(final) != 2 || !strings.Contains(final[1].Chat.Messages[1].Content, "omitted") {
			t.Errorf("want a second, trimmed synthesis; got %d calls", len(final))
		}
	})
	t.Run("closing turn failure falls back to synthesis", func(t *testing.T) {
		h := newHarness(t, domain.BankConfig{Reflect: &domain.ReflectConfig{MaxIterations: ptr(2)}},
			unit("u1", "Alice likes tea"))
		h.turns(recallCall())
		h.llm.Script(domain.ModelSlotReflect, prompts.ReflectDoneRequest,
			fake.Reply{Err: fmt.Errorf("%w: gateway: status 400: tool_choice not supported", domain.ErrUnavailable)})
		h.llm.Script(domain.ModelSlotReflect, prompts.ReflectFinal, text("Tea."))
		res, err := h.reflect(nil)
		if err != nil || res.Text != "Tea." || res.Trace.StopReason != rf.StopMaxIterations {
			t.Fatalf("res %q stop %q err %v", res.Text, res.Trace.StopReason, err)
		}
	})
	t.Run("other chat errors fail", func(t *testing.T) {
		h := newHarness(t, domain.BankConfig{})
		h.turns(fake.Reply{Err: fmt.Errorf("%w: gateway: status 500", domain.ErrUnavailable)})
		if _, err := h.reflect(nil); !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestReflectMaxTotalTokensExhausted(t *testing.T) {
	cfg := domain.BankConfig{Reflect: &domain.ReflectConfig{MaxTotalTokens: ptr(int64(150))}}
	h := newHarness(t, cfg, unit("u1", "Alice likes tea"))
	h.turns(withUsage(recallCall(), 60, 20), withUsage(recallCall(), 60, 20))
	_, err := h.reflect(nil)
	if !errors.Is(err, domain.ErrResourceExhausted) {
		t.Fatalf("err = %v, want RESOURCE_EXHAUSTED", err)
	}
	if n := len(h.loopCalls()); n != 2 {
		t.Errorf("loop turns = %d, want 2", n)
	}

	t.Run("synthesis call", func(t *testing.T) {
		cfg := domain.BankConfig{Reflect: &domain.ReflectConfig{MaxTotalTokens: ptr(int64(100)), MaxIterations: ptr(2)}}
		h := newHarness(t, cfg, unit("u1", "Alice likes tea"))
		h.turns(withUsage(recallCall(), 40, 10), text("prose"))
		h.llm.Script(domain.ModelSlotReflect, prompts.ReflectDoneRequest, withUsage(text("prose"), 10, 10))
		h.llm.Script(domain.ModelSlotReflect, prompts.ReflectFinal, withUsage(text("Tea."), 30, 10))
		if _, err := h.reflect(nil); !errors.Is(err, domain.ErrResourceExhausted) {
			t.Fatalf("err = %v, want RESOURCE_EXHAUSTED from the synthesis call", err)
		}
	})
	t.Run("structured pass", func(t *testing.T) {
		cfg := domain.BankConfig{Reflect: &domain.ReflectConfig{MaxTotalTokens: ptr(int64(100))}}
		h := newHarness(t, cfg, unit("u1", "Alice likes tea"))
		h.turns(withUsage(recallCall(), 40, 10), withUsage(doneCall("tea", "u1"), 30, 10))
		h.llm.Script(domain.ModelSlotStructuredOutput, prompts.StructuredOutputSystem,
			fake.Reply{JSON: `{"drink":"tea"}`, Usage: port.Usage{InputTokens: 15, OutputTokens: 5}})
		_, err := h.reflect(func(r *rf.ReflectRequest) {
			r.ResponseSchemaJSON = `{"type":"object","properties":{"drink":{"type":"string"}}}`
		})
		if !errors.Is(err, domain.ErrResourceExhausted) {
			t.Fatalf("err = %v, want RESOURCE_EXHAUSTED from the structured pass", err)
		}
	})
	t.Run("request override", func(t *testing.T) {
		h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"))
		h.turns(withUsage(recallCall(), 60, 20), withUsage(doneCall("tea", "u1"), 10, 10))
		_, err := h.reflect(func(r *rf.ReflectRequest) {
			r.Overrides = &domain.BankConfig{Reflect: &domain.ReflectConfig{MaxTotalTokens: ptr(int64(50))}}
		})
		if !errors.Is(err, domain.ErrResourceExhausted) {
			t.Fatalf("err = %v, want RESOURCE_EXHAUSTED", err)
		}
	})
}

func TestReflectMalformedToolCallReturnsToModel(t *testing.T) {
	h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"))
	h.turns(
		calls(rf.ToolRecall, `{"query": `, "web_search", `{"q":"x"}`, rf.ToolRecall, `{"reason":"no query"}`),
		recallCall(),
		doneCall("Alice likes tea.", "u1"),
	)
	res, err := h.reflect(func(r *rf.ReflectRequest) { r.Include.ToolCalls = true })
	if err != nil {
		t.Fatal(err)
	}
	turn2 := h.loopCalls()[1]
	msgs := toolMessages(turn2)
	if len(msgs) != 3 {
		t.Fatalf("tool messages = %d, want 3", len(msgs))
	}
	for i, want := range []string{"not valid JSON", "Tool 'web_search' is not available", "requires a query"} {
		if !containsAll(msgs[i], `"error"`, want) {
			t.Errorf("tool message %d = %s, want an error about %q", i, msgs[i], want)
		}
	}
	errs := 0
	for _, c := range res.Trace.ToolCalls {
		if c.Error != "" {
			errs++
		}
	}
	if errs != 3 || res.Text != "Alice likes tea." {
		t.Errorf("trace errors = %d, text %q", errs, res.Text)
	}

	t.Run("over-long query", func(t *testing.T) {
		h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"))
		long := strings.Repeat("alice ", 400)
		h.turns(calls(rf.ToolRecall, `{"query":"`+long+`"}`), recallCall(), doneCall("tea", "u1"))
		if _, err := h.reflect(nil); err != nil {
			t.Fatal(err)
		}
		if msgs := toolMessages(h.loopCalls()[1]); len(msgs) != 1 || !strings.Contains(msgs[0], "too long") {
			t.Errorf("tool messages = %v", msgs)
		}
	})
}

func TestReflectDoneBeforeRetrievalRejected(t *testing.T) {
	h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"))
	h.turns(doneCall("guess"), recallCall(), doneCall("Alice likes tea.", "u1"))
	res, err := h.reflect(nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Alice likes tea." || res.Trace.Iterations != 3 {
		t.Fatalf("text %q iterations %d", res.Text, res.Trace.Iterations)
	}
	msgs := toolMessages(h.loopCalls()[1])
	if len(msgs) != 1 || !strings.Contains(msgs[0], "You must search for information first") {
		t.Errorf("tool messages = %v", msgs)
	}

	t.Run("require_evidence off", func(t *testing.T) {
		h := newHarness(t, domain.BankConfig{Reflect: &domain.ReflectConfig{RequireEvidence: ptr(false)}})
		h.turns(doneCall("guess"))
		if res, err := h.reflect(nil); err != nil || res.Text != "guess" {
			t.Fatalf("res %q err %v", res.Text, err)
		}
	})
}

func TestReflectCitationFiltering(t *testing.T) {
	obs := unit("o1", "Alice is a tea person")
	obs.Type = domain.FactTypeObservation
	h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"), unit("u2", "Bob likes coffee"), obs)
	h.pages.SearchPagesFunc = func(context.Context, port.BankRef, string, int) ([]domain.PageHit, error) {
		return []domain.PageHit{{MentalModelID: "mm-tea", Score: 0.9}}, nil
	}
	h.pages.ReadPagesFunc = func(context.Context, port.BankRef, []port.MentalModelID) ([]domain.MentalModel, error) {
		return []domain.MentalModel{{Spec: domain.MentalModelSpec{ID: "mm-tea", Name: "Tea"}, ContentMD: "Tea page"}}, nil
	}
	h.turns(
		calls(rf.ToolSearchMentalModels, `{"query":"tea"}`, rf.ToolSearchObservations, `{"query":"tea"}`),
		recallCall(),
		calls(rf.ToolDone, `{"answer":"Alice likes tea.","memory_ids":["u1","u9","u1"],`+
			`"observation_ids":["o1","o404"],"mental_model_ids":["mm-tea","mm-never"]}`),
	)
	res, err := h.reflect(nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []port.UnitID
	for _, m := range res.BasedOn.Memories {
		ids = append(ids, m.ID)
	}
	if !slices.Equal(ids, []port.UnitID{"u1", "o1"}) {
		t.Errorf("memories = %v, want [u1 o1] (u2 retrieved but not cited; u9, o404 never retrieved)", ids)
	}
	if !slices.Equal(res.BasedOn.MentalModelIDs, []port.MentalModelID{"mm-tea"}) {
		t.Errorf("mental models = %v, want [mm-tea]", res.BasedOn.MentalModelIDs)
	}

	t.Run("include.facts=false", func(t *testing.T) {
		h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"))
		h.turns(recallCall(), doneCall("tea", "u1"))
		res, err := h.reflect(func(r *rf.ReflectRequest) { r.Include.Facts = ptr(false) })
		if err != nil || len(res.BasedOn.Memories) != 0 {
			t.Fatalf("memories %v err %v", res.BasedOn.Memories, err)
		}
	})
}

func TestReflectIterationCapPerBudget(t *testing.T) {
	cases := []struct {
		name   string
		budget domain.Budget
		base   *int
		want   int
	}{
		{"unspecified is low", domain.BudgetUnspecified, nil, 5},
		{"low halves", domain.BudgetLow, nil, 5},
		{"mid", domain.BudgetMid, nil, 10},
		{"high doubles", domain.BudgetHigh, nil, 20},
		{"override at high", domain.BudgetHigh, ptr(3), 6},
		{"never below one", domain.BudgetLow, ptr(1), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"))
			h.turns(recallCall()) // never calls done
			h.llm.Script(domain.ModelSlotReflect, prompts.ReflectDoneRequest, doneCall("Alice likes tea.", "u1"))
			res, err := h.reflect(func(r *rf.ReflectRequest) {
				r.Budget = tc.budget
				if tc.base != nil {
					r.Overrides = &domain.BankConfig{Reflect: &domain.ReflectConfig{MaxIterations: tc.base}}
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if n := len(h.loopCalls()); n != tc.want {
				t.Errorf("loop turns = %d, want %d", n, tc.want)
			}
			closing := h.llm.CallsFor(domain.ModelSlotReflect, prompts.ReflectDoneRequest)
			if len(closing) != 1 || closing[0].Chat.ToolChoice != rf.ToolDone {
				t.Errorf("closing calls = %d, want 1 forcing done", len(closing))
			}
			if res.Trace.StopReason != rf.StopMaxIterations || res.Trace.Iterations != tc.want {
				t.Errorf("stop %q iterations %d", res.Trace.StopReason, res.Trace.Iterations)
			}
			if res.Text != "Alice likes tea." || len(res.BasedOn.Memories) != 1 {
				t.Errorf("text %q based_on %d", res.Text, len(res.BasedOn.Memories))
			}
		})
	}
}

func TestReflectExtraDirectivesAdded(t *testing.T) {
	h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"))
	global := h.addDirective(domain.Directive{Name: "Formal", Rules: []string{"Answer formally."}, Active: true})
	teamA := h.addDirective(domain.Directive{
		Name: "TeamA", Rules: []string{"Mention team A."}, Tags: []string{"team-a"},
		Active: true,
	})
	h.addDirective(domain.Directive{Name: "Off", Rules: []string{"Never applied."}})
	extra := domain.Directive{Name: "Extra", Rules: []string{"End with a summary line."}}

	run := func(mod func(*rf.ReflectRequest)) (rf.ReflectResult, fake.LLMCall) {
		h.llm.Reset()
		h.turns(recallCall(), doneCall("Alice likes tea.", "u1"))
		res, err := h.reflect(mod)
		if err != nil {
			t.Fatal(err)
		}
		return res, h.loopCalls()[0]
	}
	res, call := run(func(r *rf.ReflectRequest) {
		r.Tags = domain.TagFilter{Tags: []string{"team-b"}, Match: domain.TagsMatchAny}
		r.ExtraDirectives = []domain.Directive{extra}
	})
	sys := systemPrompt(call)
	if !containsAll(sys, "Answer formally.", "End with a summary line.") {
		t.Errorf("system prompt lacks the stored or extra directive:\n%s", sys)
	}
	if strings.Contains(sys, "Mention team A.") || strings.Contains(sys, "Never applied.") {
		t.Errorf("system prompt has a non-matching or inactive directive")
	}
	done, _ := toolSpec(call, rf.ToolDone)
	if !containsAll(string(done.Parameters), "directive_compliance", "Answer formally.", "End with a summary line.") {
		t.Errorf("done tool lacks the directive variant: %s", done.Parameters)
	}
	if !slices.Equal(res.BasedOn.DirectiveIDs, []port.DirectiveID{global}) {
		t.Errorf("directive ids = %v, want [%s]", res.BasedOn.DirectiveIDs, global)
	}

	res, call = run(func(r *rf.ReflectRequest) {
		r.Tags = domain.TagFilter{Tags: []string{"team-a"}}
	})
	if !containsAll(systemPrompt(call), "Answer formally.", "Mention team A.") || len(res.Trace.DirectiveIDs) != 2 {
		t.Errorf("tagged reflect should apply the global and the team-a directive: %v", res.Trace.DirectiveIDs)
	}
	res, _ = run(func(r *rf.ReflectRequest) {
		r.ApplyAllDirectives = true
		r.ExtraDirectives = []domain.Directive{{ID: "caller-id", Rules: []string{"Extra rule."}}}
	})
	if !slices.Equal(res.BasedOn.DirectiveIDs, []port.DirectiveID{global, teamA}) {
		t.Errorf("apply_all ids = %v (extras never appear as stored directive ids)", res.BasedOn.DirectiveIDs)
	}
	_, call = run(nil)
	if strings.Contains(systemPrompt(call), "Mention team A.") {
		t.Errorf("an untagged reflect must not see tag-scoped directives")
	}
}

func TestReflectStructuredOutput(t *testing.T) {
	schema := `{"type":"object","properties":{"drink":{"type":"string","description":"favourite drink"}},` +
		`"required":["drink"]}`
	h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"))
	h.turns(recallCall(), withUsage(doneCall("Alice likes tea.", "u1"), 10, 5))
	h.llm.Script(domain.ModelSlotStructuredOutput, prompts.StructuredOutputSystem,
		fake.Reply{JSON: `{"drink":"tea"}`, Usage: port.Usage{InputTokens: 7, OutputTokens: 3}})
	res, err := h.reflect(func(r *rf.ReflectRequest) { r.ResponseSchemaJSON = schema })
	if err != nil {
		t.Fatal(err)
	}
	if string(res.StructuredOutputJSON) != `{"drink":"tea"}` || res.StructuredOutputError != "" {
		t.Errorf("structured %s err %q", res.StructuredOutputJSON, res.StructuredOutputError)
	}
	sc := h.llm.CallsFor(domain.ModelSlotStructuredOutput, prompts.StructuredOutputSystem)
	want := "drink (string) (REQUIRED): favourite drink"
	if len(sc) != 1 || !containsAll(sc[0].Structured.User, "Alice likes tea.", want) {
		t.Errorf("structured prompt = %+v", sc)
	}
	if !hasUsage(res.Usage, domain.ModelSlotStructuredOutput, 7, 3) {
		t.Errorf("usage = %+v, want a STRUCTURED_OUTPUT entry", res.Usage)
	}

	t.Run("invalid schema is INVALID_ARGUMENT before any call", func(t *testing.T) {
		h := newHarness(t, domain.BankConfig{})
		_, err := h.reflect(func(r *rf.ReflectRequest) { r.ResponseSchemaJSON = `{"type":` })
		if !errors.Is(err, domain.ErrInvalidArgument) || len(h.llm.Calls()) != 0 {
			t.Fatalf("err = %v, calls %d", err, len(h.llm.Calls()))
		}
	})
}

func TestReflectStructuredOutputError(t *testing.T) {
	schema := `{"type":"object","properties":{"drink":{"type":"string"}},"required":["drink"]}`
	h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"))
	h.turns(recallCall(), doneCall("Alice likes tea.", "u1"))
	h.llm.Script(domain.ModelSlotStructuredOutput, prompts.StructuredOutputSystem, fake.Reply{JSON: `{"food":"cake"}`})
	res, err := h.reflect(func(r *rf.ReflectRequest) { r.ResponseSchemaJSON = schema })
	if err != nil {
		t.Fatalf("a failed projection must not fail reflect: %v", err)
	}
	if res.Text != "Alice likes tea." || res.StructuredOutputJSON != nil || res.StructuredOutputError == "" {
		t.Errorf("text %q structured %s error %q", res.Text, res.StructuredOutputJSON, res.StructuredOutputError)
	}
}

func TestPromptBuilderUsesEffectiveConfig(t *testing.T) {
	bankCfg := domain.BankConfig{ReflectMission: "Bank mission.", Disposition: &domain.Disposition{Skepticism: 1}}
	h := newHarness(t, bankCfg, unit("u1", "Alice likes tea"))
	h.turns(recallCall(), doneCall("tea", "u1"))
	_, err := h.reflect(func(r *rf.ReflectRequest) {
		r.Overrides = &domain.BankConfig{
			ReflectMission: "Request mission.", Disposition: &domain.Disposition{Skepticism: 5, Literalism: 3, Empathy: 3},
		}
		r.QueryTime = ptr(time.Date(2020, 5, 1, 12, 30, 0, 0, time.UTC))
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := systemPrompt(h.loopCalls()[0])
	if !containsAll(sys, "Request mission.", "highly skeptical", "Memory Bank: Bank One", "2020-05-01 12:30 UTC") ||
		strings.Contains(sys, "Bank mission.") {
		t.Errorf("system prompt does not reflect the effective config:\n%s", sys)
	}

	pb := rf.NewPromptBuilder(h.clock)
	out := pb.System(domain.BankConfig{ReflectMission: "Be brief.", Disposition: &domain.Disposition{Empathy: 5}},
		[]domain.Directive{{Name: "D", Rules: []string{"Cite dates."}}})
	if !containsAll(out, "Be brief.", "strongly consider the emotional state", "Cite dates.") {
		t.Errorf("PromptBuilder.System:\n%s", out)
	}
	off := pb.System(domain.BankConfig{Reflect: &domain.ReflectConfig{EnabledTools: []string{rf.ToolRecall}}}, nil)
	if strings.Contains(off, "search_mental_models") || !strings.Contains(off, "recall()") {
		t.Errorf("disabled tools must not be described:\n%s", off)
	}
}

func TestReflectTraceAndUsage(t *testing.T) {
	h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"))
	h.turns(withUsage(recallCall(), 100, 10), withUsage(doneCall("tea", "u1"), 120, 20))
	res, err := h.reflect(func(r *rf.ReflectRequest) {
		r.Include.ToolCalls = true
		r.Include.ToolOutputs = ptr(false)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasUsage(res.Usage, domain.ModelSlotReflect, 220, 30) || len(res.Usage) != 1 {
		t.Errorf("usage = %+v, want one REFLECT entry 220/30", res.Usage)
	}
	tc := res.Trace.ToolCalls
	if len(tc) != 1 || tc[0].Tool != rf.ToolRecall || tc[0].Iteration != 1 || tc[0].OutputJSON != "" ||
		tc[0].ArgumentsJSON == "" {
		t.Errorf("tool calls = %+v", tc)
	}
	if res.Trace.Iterations != 2 || res.Trace.StopReason != rf.StopAnswered {
		t.Errorf("trace = %+v", res.Trace)
	}

	h.llm.Reset()
	h.turns(recallCall(), doneCall("tea", "u1"))
	res, err = h.reflect(func(r *rf.ReflectRequest) { r.Include.ToolCalls = true })
	if err != nil || len(res.Trace.ToolCalls) != 1 || !strings.Contains(res.Trace.ToolCalls[0].OutputJSON, "u1") {
		t.Errorf("outputs default to on with tool_calls: %+v %v", res.Trace.ToolCalls, err)
	}
	h.llm.Reset()
	h.turns(recallCall(), doneCall("tea", "u1"))
	if res, _ = h.reflect(nil); len(res.Trace.ToolCalls) != 0 {
		t.Errorf("tool calls must be omitted unless asked: %+v", res.Trace.ToolCalls)
	}
}

func TestReflectAsDocument(t *testing.T) {
	h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"))
	h.turns(recallCall(), calls(rf.ToolDone, `{"document":{"sections":[{"heading":"Drinks","level":2,`+
		`"blocks":["Alice likes tea.","- green\n- black"]}]},"memory_ids":["u1"]}`))
	res, err := h.reflect(func(r *rf.ReflectRequest) {
		r.AsDocument = true
		r.Context = "You are writing a document called \"Drinks\"."
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "## Drinks\n\nAlice likes tea.\n\n- green\n- black" {
		t.Errorf("text = %q", res.Text)
	}
	call := h.loopCalls()[0]
	done, _ := toolSpec(call, rf.ToolDone)
	if !strings.Contains(string(done.Parameters), "sections") ||
		!strings.Contains(systemPrompt(call), "You are writing a document called") {
		t.Errorf("document mode not offered")
	}
}

func TestReflectRequestValidation(t *testing.T) {
	h := newHarness(t, domain.BankConfig{})
	if _, err := h.reflect(func(r *rf.ReflectRequest) { r.Query = " " }); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("empty query: %v", err)
	}
	_, err := h.reflect(func(r *rf.ReflectRequest) { r.Bank = port.BankRef{Tenant: "t1", Bank: "nope"} })
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown bank: %v", err)
	}
}

// hasUsage reports whether usage has an entry for slot with exactly these counts.
func hasUsage(usage []domain.Usage, slot domain.ModelSlot, in, out int64) bool {
	for _, u := range usage {
		if u.Slot == slot && u.InputTokens == in && u.OutputTokens == out {
			return true
		}
	}
	return false
}

func TestReflectHonoursTypes(t *testing.T) {
	obs := unit("o1", "Alice is a tea person")
	obs.Type = domain.FactTypeObservation
	cases := []struct {
		name      string
		types     []domain.FactType
		offered   []string
		withheld  []string
		recallFor []domain.FactType
	}{
		{
			"observations only",
			[]domain.FactType{domain.FactTypeObservation},
			[]string{rf.ToolSearchObservations},
			[]string{rf.ToolRecall},
			nil,
		},
		{
			"world only",
			[]domain.FactType{domain.FactTypeWorld},
			[]string{rf.ToolRecall},
			[]string{rf.ToolSearchObservations},
			[]domain.FactType{domain.FactTypeWorld},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, domain.BankConfig{}, unit("u1", "Alice likes tea"), obs)
			var asked [][]domain.FactType
			h.rc.RecallFunc = func(_ context.Context, req recall.RecallRequest) (recall.RecallResult, error) {
				asked = append(asked, req.Types)
				return recall.RecallResult{}, nil
			}
			h.build()
			h.turns(calls(tc.offered[0], `{"query":"alice"}`), calls(tc.withheld[0], `{"query":"alice"}`),
				doneCall("tea"))
			if _, err := h.reflect(func(r *rf.ReflectRequest) { r.Types = tc.types }); err != nil {
				t.Fatal(err)
			}
			first := h.loopCalls()[0]
			if !slices.Contains(tc.offered, rf.ToolRecall) && strings.Contains(systemPrompt(first), "recall()") {
				t.Errorf("the system prompt orders a withheld recall")
			}
			for _, name := range tc.offered {
				if _, ok := toolSpec(first, name); !ok {
					t.Errorf("%s not offered", name)
				}
			}
			for _, name := range tc.withheld {
				if _, ok := toolSpec(first, name); ok {
					t.Errorf("%s offered for types %v", name, tc.types)
				}
			}
			if msgs := toolMessages(h.loopCalls()[2]); len(msgs) != 2 || !strings.Contains(msgs[1], "is not available") {
				t.Errorf("a withheld tool must be rejected: %v", msgs)
			}
			if len(asked) != 1 || (tc.recallFor != nil && !slices.Equal(asked[0], tc.recallFor)) {
				t.Errorf("recall asked for %v", asked)
			}
		})
	}
}

func TestReflectClosingDoneKeepsEvidenceGuard(t *testing.T) {
	t.Run("forces one retrieval turn", func(t *testing.T) {
		h := newHarness(t, domain.BankConfig{Reflect: &domain.ReflectConfig{MaxIterations: ptr(4)}},
			unit("u1", "Alice likes tea"))
		h.turns(calls(rf.ToolRecall, `{"reason":"no query"}`), text("prose"), recallCall())
		h.llm.Script(domain.ModelSlotReflect, prompts.ReflectDoneRequest, doneCall("Alice likes tea.", "u1"))
		res, err := h.reflect(nil)
		if err != nil || res.Text != "Alice likes tea." {
			t.Fatalf("res %q err %v", res.Text, err)
		}
		turns := h.loopCalls()
		if len(turns) != 3 || turns[2].Chat.ToolChoice != rf.ToolRecall {
			t.Errorf("want a third turn forcing recall; got %d turns", len(turns))
		}
	})
	t.Run("no retrieval: synthesis, never done", func(t *testing.T) {
		h := newHarness(t, domain.BankConfig{Reflect: &domain.ReflectConfig{MaxIterations: ptr(4)}})
		h.turns(calls(rf.ToolRecall, `{"reason":"no query"}`), text("prose"), doneCall("made up"))
		h.llm.Script(domain.ModelSlotReflect, prompts.ReflectDoneRequest, doneCall("made up"))
		h.llm.Script(domain.ModelSlotReflect, prompts.ReflectFinal, text("No data was retrieved."))
		res, err := h.reflect(nil)
		if err != nil || res.Text != "No data was retrieved." {
			t.Fatalf("res %q err %v", res.Text, err)
		}
		if n := len(h.llm.CallsFor(domain.ModelSlotReflect, prompts.ReflectDoneRequest)); n != 0 {
			t.Errorf("closing done turns = %d, want 0 without evidence", n)
		}
	})
}

func TestReflectToolErrorsNameOfferedTools(t *testing.T) {
	cfg := domain.BankConfig{Reflect: &domain.ReflectConfig{EnabledTools: []string{rf.ToolRecall}}}
	h := newHarness(t, cfg, unit("u1", "Alice likes tea"))
	h.turns(calls(rf.ToolDone, `{"answer":"guess"}`, rf.ToolExpand, `{}`), calls(rf.ToolSearchObservations, `{}`),
		recallCall(), calls(rf.ToolDone, `{"memory_ids":["u1"]}`), doneCall("tea", "u1"))
	if _, err := h.reflect(nil); err != nil {
		t.Fatal(err)
	}
	turns := h.loopCalls()
	last := func(c fake.LLMCall) string { m := toolMessages(c); return m[len(m)-1] }
	guard, unknown, done := last(turns[1]), last(turns[2]), last(turns[4])
	if !containsAll(guard, "You must search for information first. Use recall()") ||
		strings.Contains(guard, "search_mental_models") || strings.Contains(guard, "search_observations") {
		t.Errorf("guard message = %s", guard)
	}
	if !containsAll(unknown, "Tool 'search_observations' is not available", "recall, done") ||
		strings.Contains(unknown, "search_mental_models") {
		t.Errorf("unknown-tool message = %s", unknown)
	}
	if !containsAll(done, "missing 'answer'", "Call done() again with an 'answer' string") {
		t.Errorf("done message = %s", done)
	}

	t.Run("search without read", func(t *testing.T) {
		cfg := domain.BankConfig{Reflect: &domain.ReflectConfig{EnabledTools: []string{rf.ToolSearchMentalModels}}}
		h := newHarness(t, cfg)
		h.turns(calls(rf.ToolReadMentalModels, `{"mental_model_ids":["x"]}`), doneCall("tea"))
		h.llm.Script(domain.ModelSlotReflect, prompts.ReflectDoneRequest, doneCall("tea"))
		h.llm.Script(domain.ModelSlotReflect, prompts.ReflectFinal, text("tea"))
		if _, err := h.reflect(nil); err != nil {
			t.Fatal(err)
		}
		msg := last(h.loopCalls()[1])
		if !containsAll(msg, "Tool 'read_mental_models' is not available", "search_mental_models, done") ||
			strings.Contains(strings.TrimPrefix(msg, `{"error":"Tool 'read_mental_models'`), "read_mental_models") {
			t.Errorf("unknown-tool message = %s", msg)
		}
		if strings.Contains(systemPrompt(h.loopCalls()[0]), "call read_mental_models") {
			t.Errorf("the system prompt names a withheld read_mental_models")
		}
	})
}

func TestReflectNoSearchToolFailsFast(t *testing.T) {
	cfg := domain.BankConfig{Reflect: &domain.ReflectConfig{EnabledTools: []string{rf.ToolExpand}}}
	h := newHarness(t, cfg)
	_, err := h.reflect(nil)
	if !errors.Is(err, domain.ErrFailedPrecondition) || len(h.llm.Calls()) != 0 {
		t.Fatalf("err = %v, calls %d; want FAILED_PRECONDITION before any model call", err, len(h.llm.Calls()))
	}

	t.Run("require_evidence off", func(t *testing.T) {
		cfg := domain.BankConfig{Reflect: &domain.ReflectConfig{
			EnabledTools: []string{rf.ToolExpand}, RequireEvidence: ptr(false),
		}}
		h := newHarness(t, cfg)
		h.turns(doneCall("Nothing to search."))
		if res, err := h.reflect(nil); err != nil || res.Text != "Nothing to search." {
			t.Fatalf("res %q err %v", res.Text, err)
		}
	})
}

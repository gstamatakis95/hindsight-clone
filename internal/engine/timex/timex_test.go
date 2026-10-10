package timex

import (
	"context"
	"encoding/json"
	"errors"
	"memd/internal/domain"
	"memd/internal/port"
	"reflect"
	"strings"
	"testing"
	"time"

	mockport "memd/internal/mocks/port"
)

// errUnexpected is what the mocks return when a test expects no model call.
var errUnexpected = errors.New("unexpected model call")

// harness is a normalizer over mocks; the clock has no NowFunc, so any use with a non-zero ref panics.
type harness struct {
	tn     TimeNormalizer
	llm    *mockport.LLM
	router *mockport.ModelRouter
}

// newHarness wires a normalizer whose model calls fail; tests that expect a call replace the funcs.
func newHarness() harness {
	llm := &mockport.LLM{
		StructuredFunc: func(_ context.Context, _ port.StructuredRequest, _ any) (port.Usage, error) {
			return port.Usage{}, errUnexpected
		},
	}
	router := &mockport.ModelRouter{
		ResolveFunc: func(_ domain.BankConfig, _ port.ModelSlot) (port.ModelBinding, error) {
			return port.ModelBinding{}, errUnexpected
		},
	}
	return harness{tn: NewNormalizer(llm, router, &mockport.Clock{}), llm: llm, router: router}
}

// scripted makes the LLM answer with raw JSON and the router return binding.
func (h harness) scripted(binding domain.ModelBinding, raw string) {
	h.router.ResolveFunc = func(_ domain.BankConfig, _ port.ModelSlot) (port.ModelBinding, error) {
		return binding, nil
	}
	h.llm.StructuredFunc = func(_ context.Context, _ port.StructuredRequest, out any) (port.Usage, error) {
		return port.Usage{}, json.Unmarshal([]byte(raw), out)
	}
}

// assertNoModelCalls fails when the router or the LLM was used.
func (h harness) assertNoModelCalls(t *testing.T) {
	t.Helper()
	if n := len(h.router.ResolveCalls()); n != 0 {
		t.Errorf("router.Resolve called %d times, want 0", n)
	}
	if n := len(h.llm.StructuredCalls()); n != 0 {
		t.Errorf("llm.Structured called %d times, want 0", n)
	}
}

// sameWindow compares windows by instant.
func sameWindow(a, b *domain.TimeWindow) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Start.Equal(b.Start) && a.End.Equal(b.End)
}

// TestQueryWindowCorpus resolves every corpus query by the rules alone, with no router or LLM call.
func TestQueryWindowCorpus(t *testing.T) {
	counts := map[string]int{}
	for _, row := range corpus() {
		counts[row.cat]++
		t.Run(row.cat+"/"+row.query, func(t *testing.T) {
			h := newHarness()
			ref := row.ref
			if ref.IsZero() {
				ref = testRef
			}
			got, err := h.tn.QueryWindow(context.Background(), domain.BankConfig{}, row.query, ref)
			if err != nil {
				t.Fatalf("QueryWindow(%q): %v", row.query, err)
			}
			if !sameWindow(got, row.want) {
				t.Errorf("QueryWindow(%q) = %v, want %v", row.query, got, row.want)
			}
			if got != nil && row.ref.IsZero() && got.Start.Location() != time.UTC && !got.Start.IsZero() {
				t.Errorf("window not in ref location: %v", got.Start.Location())
			}
			h.assertNoModelCalls(t)
		})
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	if total < 60 || counts["none"] < 15 {
		t.Fatalf("corpus too small: %v", counts)
	}
	t.Logf("corpus sizes: %v (total %d)", counts, total)
}

// TestZoneRowsKeepLocation checks non-UTC windows are expressed in the reference's location.
func TestZoneRowsKeepLocation(t *testing.T) {
	for _, row := range zoneRows {
		got, ok := parseRules(row.query, row.ref)
		if !ok || got.Start.Location() != row.ref.Location() || got.End.Location() != row.ref.Location() {
			t.Errorf("%q: got %v ok=%v, want bounds in %v", row.query, got, ok, row.ref.Location())
		}
	}
}

// TestNoCueMakesNoLLMCall checks a query without a temporal cue gets no window and no model call.
func TestNoCueMakesNoLLMCall(t *testing.T) {
	h := newHarness()
	h.scripted(domain.ModelBinding{Model: "tp"}, `{"start":"2026-01-01T00:00:00Z","end":null}`)
	for _, q := range []string{"what is my name", "may I ask about my cats", "spring water brands", ""} {
		got, err := h.tn.QueryWindow(context.Background(), domain.BankConfig{}, q, testRef)
		if err != nil || got != nil {
			t.Errorf("QueryWindow(%q) = %v, %v; want nil, nil", q, got, err)
		}
	}
	h.assertNoModelCalls(t)
}

// TestFallbackOnlyWhenCueAndNoRule checks a cue the rules cannot resolve makes exactly one TIME_PARSE call
// with the binding the router returned for the caller's cfg, and that the window is decoded from it.
func TestFallbackOnlyWhenCueAndNoRule(t *testing.T) {
	h := newHarness()
	binding := domain.ModelBinding{Model: "tp-override", ReasoningEffort: "low"}
	h.scripted(binding, `{"start":"2025-12-20T00:00:00Z","end":"2026-01-03T00:00:00Z"}`)
	cfg := domain.BankConfig{RetainMission: "request-level override"}

	got, err := h.tn.QueryWindow(context.Background(), cfg, "what did we do around the holidays?", testRef)
	if err != nil {
		t.Fatal(err)
	}
	if want := w(d(2025, 12, 20), d(2026, 1, 3)); !sameWindow(got, want) {
		t.Fatalf("window = %v, want %v", got, want)
	}
	rc := h.router.ResolveCalls()
	if len(rc) != 1 || rc[0].Slot != domain.ModelSlotTimeParse || !reflect.DeepEqual(rc[0].Cfg, cfg) {
		t.Fatalf("Resolve calls = %+v", rc)
	}
	sc := h.llm.StructuredCalls()
	if len(sc) != 1 {
		t.Fatalf("Structured calls = %d, want 1", len(sc))
	}
	req := sc[0].Req
	if req.Slot != domain.ModelSlotTimeParse || !reflect.DeepEqual(req.Binding, binding) {
		t.Errorf("request slot/binding = %v / %+v", req.Slot, req.Binding)
	}
	if req.Name != "time_parse_system" || len(req.Schema) == 0 || req.System == "" {
		t.Errorf("request name %q, schema %d bytes, system %d bytes", req.Name, len(req.Schema), len(req.System))
	}
	if !strings.Contains(req.User, "around the holidays") || !strings.Contains(req.User, "2026-10-10T15:04:05Z") ||
		!strings.Contains(req.User, "Saturday") {
		t.Errorf("user prompt lacks the query or reference:\n%s", req.User)
	}
}

// TestFallbackNullWindow checks both-null, inverted and unparseable answers mean no window.
func TestFallbackNullWindow(t *testing.T) {
	for _, raw := range []string{
		`{"start":null,"end":null}`,
		`{"start":"2026-02-01","end":"2026-01-01"}`,
		`{"start":"last tuesday-ish","end":null}`,
	} {
		h := newHarness()
		h.scripted(domain.ModelBinding{Model: "tp"}, raw)
		got, err := h.tn.QueryWindow(context.Background(), domain.BankConfig{}, "when I was in college", testRef)
		if err != nil || got != nil {
			t.Errorf("%s: got %v, %v; want nil, nil", raw, got, err)
		}
		if len(h.llm.StructuredCalls()) != 1 {
			t.Errorf("%s: want one call", raw)
		}
	}
}

// TestFallbackOpenAndDateOnlyBounds checks a one-sided answer and a date-only bound in ref's location.
func TestFallbackOpenAndDateOnlyBounds(t *testing.T) {
	h := newHarness()
	h.scripted(domain.ModelBinding{Model: "tp"}, `{"start":"2026-09-30","end":null}`)
	got, err := h.tn.QueryWindow(context.Background(), domain.BankConfig{}, "the other day", estRef)
	if err != nil {
		t.Fatal(err)
	}
	if want := w(dz(est, 2026, 9, 30), z); !sameWindow(got, want) {
		t.Fatalf("window = %v, want %v", got, want)
	}
}

// TestFallbackErrorsWrapped checks router and LLM failures surface wrapped.
func TestFallbackErrorsWrapped(t *testing.T) {
	h := newHarness()
	_, err := h.tn.QueryWindow(context.Background(), domain.BankConfig{}, "the other day", testRef)
	if !errors.Is(err, errUnexpected) || !strings.HasPrefix(err.Error(), "timex: ") {
		t.Fatalf("router error = %v", err)
	}
	h.router.ResolveFunc = func(_ domain.BankConfig, _ port.ModelSlot) (port.ModelBinding, error) {
		return port.ModelBinding{Model: "tp"}, nil
	}
	_, err = h.tn.QueryWindow(context.Background(), domain.BankConfig{}, "the other day", testRef)
	if !errors.Is(err, errUnexpected) || len(h.llm.StructuredCalls()) != 1 {
		t.Fatalf("llm error = %v", err)
	}
}

// TestZeroRefUsesClock checks a zero ref resolves against the injected clock.
func TestZeroRefUsesClock(t *testing.T) {
	clock := &mockport.Clock{NowFunc: func() time.Time { return testRef }}
	tn := NewNormalizer(&mockport.LLM{}, &mockport.ModelRouter{}, clock)
	got, err := tn.QueryWindow(context.Background(), domain.BankConfig{}, "yesterday", time.Time{})
	if err != nil || !sameWindow(got, w(d(2026, 10, 9), d(2026, 10, 10))) {
		t.Fatalf("got %v, %v", got, err)
	}
	if len(clock.NowCalls()) != 1 {
		t.Fatalf("clock calls = %d", len(clock.NowCalls()))
	}
}

// TestNormalizeByRules fills the occurrence from a rule-resolved expression without a model call.
func TestNormalizeByRules(t *testing.T) {
	h := newHarness()
	f := &domain.ExtractedFact{Text: "Went hiking", TimeExpression: "last spring"}
	if err := h.tn.Normalize(context.Background(), domain.BankConfig{}, f, testRef); err != nil {
		t.Fatal(err)
	}
	if f.OccurredStart == nil || f.OccurredEnd == nil ||
		!f.OccurredStart.Equal(d(2026, 3, 1)) || !f.OccurredEnd.Equal(d(2026, 6, 1)) {
		t.Fatalf("occurred = %v - %v", f.OccurredStart, f.OccurredEnd)
	}
	h.assertNoModelCalls(t)
}

// TestNormalizeOpenRange leaves the unbounded side nil.
func TestNormalizeOpenRange(t *testing.T) {
	h := newHarness()
	f := &domain.ExtractedFact{TimeExpression: "before 2020"}
	if err := h.tn.Normalize(context.Background(), domain.BankConfig{}, f, testRef); err != nil {
		t.Fatal(err)
	}
	if f.OccurredStart != nil || f.OccurredEnd == nil || !f.OccurredEnd.Equal(d(2020, 1, 1)) {
		t.Fatalf("occurred = %v - %v", f.OccurredStart, f.OccurredEnd)
	}
	h.assertNoModelCalls(t)
}

// TestNormalizeAlreadyAbsoluteUnchanged leaves a fact with absolute dates alone.
func TestNormalizeAlreadyAbsoluteUnchanged(t *testing.T) {
	h := newHarness()
	start := d(2020, 5, 1)
	f := &domain.ExtractedFact{TimeExpression: "yesterday", OccurredStart: &start}
	if err := h.tn.Normalize(context.Background(), domain.BankConfig{}, f, testRef); err != nil {
		t.Fatal(err)
	}
	if f.OccurredStart != &start || !f.OccurredStart.Equal(d(2020, 5, 1)) || f.OccurredEnd != nil {
		t.Fatalf("fact changed: %v - %v", f.OccurredStart, f.OccurredEnd)
	}
	h.assertNoModelCalls(t)
}

// TestNormalizeEmptyExpressionUnchanged leaves a fact without an expression (or a nil fact) alone.
func TestNormalizeEmptyExpressionUnchanged(t *testing.T) {
	h := newHarness()
	for _, f := range []*domain.ExtractedFact{{Text: "x"}, {TimeExpression: "   "}, nil} {
		if err := h.tn.Normalize(context.Background(), domain.BankConfig{}, f, testRef); err != nil {
			t.Fatal(err)
		}
		if f != nil && (f.OccurredStart != nil || f.OccurredEnd != nil) {
			t.Fatalf("fact changed: %+v", f)
		}
	}
	h.assertNoModelCalls(t)
}

// TestNormalizeFallback resolves an expression the rules cannot through one TIME_PARSE call.
func TestNormalizeFallback(t *testing.T) {
	h := newHarness()
	binding := domain.ModelBinding{Model: "tp-retain"}
	h.scripted(binding, `{"start":"2026-07-03T00:00:00Z","end":"2026-07-06T00:00:00Z"}`)
	cfg := domain.BankConfig{RetainMission: "m"}
	f := &domain.ExtractedFact{TimeExpression: "the long weekend in the mountains"}
	if err := h.tn.Normalize(context.Background(), cfg, f, testRef); err != nil {
		t.Fatal(err)
	}
	if f.OccurredStart == nil || !f.OccurredStart.Equal(d(2026, 7, 3)) || !f.OccurredEnd.Equal(d(2026, 7, 6)) {
		t.Fatalf("occurred = %v - %v", f.OccurredStart, f.OccurredEnd)
	}
	sc := h.llm.StructuredCalls()
	if len(sc) != 1 || sc[0].Req.Slot != domain.ModelSlotTimeParse || sc[0].Req.Binding.Model != "tp-retain" {
		t.Fatalf("Structured calls = %+v", sc)
	}
	if !strings.Contains(sc[0].Req.User, "Time expression from a stored fact") {
		t.Errorf("user prompt:\n%s", sc[0].Req.User)
	}
	if rc := h.router.ResolveCalls(); len(rc) != 1 || !reflect.DeepEqual(rc[0].Cfg, cfg) {
		t.Fatalf("Resolve calls = %+v", rc)
	}
}

// TestNormalizeFallbackNullLeavesFact keeps the fact unchanged when the fallback finds no window.
func TestNormalizeFallbackNullLeavesFact(t *testing.T) {
	h := newHarness()
	h.scripted(domain.ModelBinding{Model: "tp"}, `{"start":null,"end":null}`)
	f := &domain.ExtractedFact{TimeExpression: "once upon a time"}
	if err := h.tn.Normalize(context.Background(), domain.BankConfig{}, f, testRef); err != nil {
		t.Fatal(err)
	}
	if f.OccurredStart != nil || f.OccurredEnd != nil || len(h.llm.StructuredCalls()) != 1 {
		t.Fatalf("fact = %+v, calls = %d", f, len(h.llm.StructuredCalls()))
	}
}

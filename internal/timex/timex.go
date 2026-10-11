package timex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/prompts"
	"strings"
	"time"
)

// Prompt templates of the TIME_PARSE fallback; the system template also names the response schema and is
// the request's Name.
const (
	timeParsePrompt     = "time_parse_system"
	timeParseUserPrompt = "time_parse_user"
)

// Normalizer resolves relative time expressions against a reference time, for stored facts and for recall queries:
// the deterministic rules first, the TIME_PARSE model only for a cue they cannot resolve. Each method takes the
// caller's effective (already merged) BankConfig, consulted only for the TIME_PARSE binding, so per-request
// TIME_PARSE overrides reach the fallback call and an unbound slot never fails a call the rules can answer.
type Normalizer struct {
	llm    ai.LLM
	router *ai.Router
	clock  clock.Clock
}

// New returns a Normalizer that runs the deterministic rules and falls back to the TIME_PARSE slot (resolved
// through router per call, run through llm) only when they cannot resolve a cue. clock supplies the reference time
// when a caller passes a zero ref.
func New(llm ai.LLM, router *ai.Router, clock clock.Clock) *Normalizer {
	return &Normalizer{llm: llm, router: router, clock: clock}
}

// Normalize resolves f.TimeExpression against ref and fills f.OccurredStart and f.OccurredEnd in place; a fact
// with no expression, or one already carrying absolute dates, is left unchanged. A retain never fails on dates (as
// in Hindsight): any fallback failure (unbound slot, gateway unavailable, persistently invalid output, ...) is
// logged and leaves the fact's dates unset. Only the caller's own cancellation or deadline is returned.
func (n *Normalizer) Normalize(ctx context.Context, cfg domain.BankConfig, f *domain.ExtractedFact,
	ref time.Time,
) error {
	if f == nil || f.OccurredStart != nil || strings.TrimSpace(f.TimeExpression) == "" {
		return nil
	}
	ref = n.reference(ref)
	w, ok := parseRules(f.TimeExpression, ref)
	if !ok {
		var err error
		if w, err = n.fallback(ctx, cfg, f.TimeExpression, "expression", ref); err != nil {
			if callerDone(ctx, err) {
				return err
			}
			slog.Default().WarnContext(ctx, "timex: time_parse failed; fact keeps no dates",
				"expression", f.TimeExpression, "error", err)
			return nil
		}
	}
	if w == nil {
		return nil
	}
	f.OccurredStart = bound(w.Start)
	f.OccurredEnd = bound(w.End)
	return nil
}

// QueryWindow is the query side: "last spring" -> window. The model runs only when a temporal cue is detected and
// the rules can't resolve it; no cue means no window (nil) and no call. Recall never fails on time analysis: a
// fallback failure is logged and means no time constraint (nil, nil); only the caller's cancellation or deadline
// is returned.
func (n *Normalizer) QueryWindow(ctx context.Context, cfg domain.BankConfig, query string,
	ref time.Time,
) (*domain.TimeWindow, error) {
	if !hasTemporalCue(query) {
		return nil, nil
	}
	ref = n.reference(ref)
	if w, ok := parseRules(query, ref); ok {
		return w, nil
	}
	w, err := n.fallback(ctx, cfg, query, "query", ref)
	if err == nil {
		return w, nil
	}
	if callerDone(ctx, err) {
		return nil, err
	}
	slog.Default().WarnContext(ctx, "timex: time_parse failed; query gets no time constraint", "error", err)
	return nil, nil
}

// callerDone reports whether a fallback error is the caller's own cancellation or deadline, the only
// error timex passes on. A gateway's per-call timeout (ErrUnavailable wrapping DeadlineExceeded under a
// live ctx) is not.
func callerDone(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errors.Is(err, context.Canceled)
}

// reference is ref, or the clock's now when ref is zero.
func (n *Normalizer) reference(ref time.Time) time.Time {
	if ref.IsZero() {
		return n.clock.Now()
	}
	return ref
}

// bound is a window bound as an optional time; a zero (unbounded) bound is nil.
func bound(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// promptData is the data of the time_parse templates.
type promptData struct {
	// Reference is ref as RFC 3339 with its offset.
	Reference string
	// Weekday is ref's weekday name.
	Weekday string
	// Timezone is ref's location name.
	Timezone string
	// Kind is "query" (a recall query) or "expression" (an extracted time expression).
	Kind string
	// Text is the query or expression to resolve.
	Text string
}

// timeParseResult is the TIME_PARSE response: RFC 3339 or YYYY-MM-DD bounds, null when unbounded.
type timeParseResult struct {
	Start *string `json:"start"`
	End   *string `json:"end"`
}

// fallback asks the TIME_PARSE slot for a window. Both bounds null, an unparseable bound or an empty or
// inverted window mean no constraint (nil window, no error); router and LLM errors are returned wrapped.
func (n *Normalizer) fallback(ctx context.Context, cfg domain.BankConfig, text, kind string,
	ref time.Time,
) (*domain.TimeWindow, error) {
	binding, err := n.router.Resolve(cfg, domain.ModelSlotTimeParse)
	if err != nil {
		return nil, fmt.Errorf("timex: resolve time_parse binding: %w", err)
	}
	req, err := buildRequest(binding, text, kind, ref)
	if err != nil {
		return nil, err
	}
	var out timeParseResult
	if _, err := n.llm.Structured(ctx, req, &out); err != nil {
		return nil, fmt.Errorf("timex: time_parse call: %w", err)
	}
	return decodeWindow(out, ref), nil
}

// buildRequest renders the time_parse prompts into a structured request.
func buildRequest(binding domain.ModelBinding, text, kind string, ref time.Time) (ai.StructuredRequest, error) {
	data := promptData{
		Reference: ref.Format(time.RFC3339),
		Weekday:   ref.Weekday().String(),
		Timezone:  ref.Location().String(),
		Kind:      kind,
		Text:      text,
	}
	system, err := prompts.Render(timeParsePrompt, data)
	if err != nil {
		return ai.StructuredRequest{}, fmt.Errorf("timex: render system prompt: %w", err)
	}
	user, err := prompts.Render(timeParseUserPrompt, data)
	if err != nil {
		return ai.StructuredRequest{}, fmt.Errorf("timex: render user prompt: %w", err)
	}
	schema, err := prompts.Schema(timeParsePrompt)
	if err != nil {
		return ai.StructuredRequest{}, fmt.Errorf("timex: time_parse schema: %w", err)
	}
	return ai.StructuredRequest{
		Slot:    domain.ModelSlotTimeParse,
		Binding: binding,
		Name:    timeParsePrompt,
		System:  system,
		User:    user,
		Schema:  schema,
	}, nil
}

// decodeWindow turns the model's bounds into a window, or nil when they carry no usable constraint (a
// bound outside years 1-9999 included).
func decodeWindow(out timeParseResult, ref time.Time) *domain.TimeWindow {
	start, okStart := parseBound(out.Start, ref.Location())
	end, okEnd := parseBound(out.End, ref.Location())
	if !okStart || !okEnd || (start.IsZero() && end.IsZero()) {
		return nil
	}
	if !start.IsZero() && !end.IsZero() && !start.Before(end) {
		return nil
	}
	w := domain.TimeWindow{Start: start, End: end}
	if !inYearRange(w) {
		return nil
	}
	return &w
}

// boundLayouts are the accepted bound formats; layouts without an offset are read in the ref's location.
var boundLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"}

// parseBound parses one bound; nil or empty is unbounded (zero, ok), an unknown format is not ok.
func parseBound(s *string, loc *time.Location) (time.Time, bool) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return time.Time{}, true
	}
	v := strings.TrimSpace(*s)
	for _, layout := range boundLayouts {
		if t, err := time.ParseInLocation(layout, v, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

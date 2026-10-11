package retain

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"strings"
	"time"
)

// ExtractedFact is one fact produced by extraction, before normalization, embedding and storage. It is
// an alias of domain.ExtractedFact, which lives in domain so timex can normalize it without importing
// retain.
type ExtractedFact = domain.ExtractedFact

// Mention is one entity mention (name + kind) of an extracted fact; an alias of domain.Mention.
type Mention = domain.Mention

// Extractor turns one chunk into facts.
type Extractor interface {
	// Extract makes one LLM call per chunk (EXTRACT slot); mission + speaker context injected. The
	// strategy for in.Config's extraction mode builds the prompt; chunks mode returns the chunk as a
	// single fact with no call. It also returns the call's token usage (empty with no call).
	Extract(ctx context.Context, in extractInput) ([]ExtractedFact, []domain.Usage, error)
}

// extractInput is one chunk to extract facts from, with the item context the prompt needs.
type extractInput struct {
	// Bank owns the document.
	Bank port.BankRef
	// DocumentID is the source document.
	DocumentID port.DocumentID
	// ChunkIndex is the chunk's position in the document, from 0.
	ChunkIndex int
	// Text is the chunk text.
	Text string
	// Context is the retain item's free-form context (speaker, conversation, source).
	Context string
	// MentionedAt is the item timestamp, the reference time for relative dates; nil when timeless.
	MentionedAt *time.Time
	// Metadata is the retain item's string metadata.
	Metadata map[string]string
	// Hints are caller-supplied entities, shown to the extractor and merged with extracted mentions.
	Hints []domain.EntityHint
	// Config is the effective config (bank < request < item): mission, extraction mode, custom prompt,
	// entity labels and the EXTRACT binding.
	Config domain.BankConfig
}

// extractor is the Extractor over the gateway.
type extractor struct {
	llm    port.LLM
	router port.ModelRouter
	modes  map[domain.ExtractionMode]ExtractionStrategy
}

var _ Extractor = (*extractor)(nil)

// NewExtractor returns the Extractor. modes are the strategies it can run; none = all five built-in ones.
func NewExtractor(llm port.LLM, router port.ModelRouter, modes ...ExtractionStrategy) Extractor {
	if len(modes) == 0 {
		modes = []ExtractionStrategy{concise(), verbose(), verbatim(), chunks(), custom()}
	}
	m := make(map[domain.ExtractionMode]ExtractionStrategy, len(modes))
	for _, s := range modes {
		m[s.Mode()] = s
	}
	return &extractor{llm: llm, router: router, modes: m}
}

// Extract implements Extractor. An unset mode is concise; an unknown one is domain.ErrInvalidArgument.
func (e *extractor) Extract(ctx context.Context, in extractInput) ([]ExtractedFact, []domain.Usage, error) {
	mode := in.Config.ExtractionMode
	if mode == domain.ExtractionModeUnspecified {
		mode = domain.ExtractionModeConcise
	}
	s, ok := e.modes[mode]
	if !ok {
		return nil, nil, fmt.Errorf("%w: unknown extraction mode %q", domain.ErrInvalidArgument, mode)
	}
	if !s.NeedsLLM() {
		return []ExtractedFact{{Text: in.Text, Type: domain.FactTypeWorld}}, nil, nil
	}
	p, err := s.Prompt(in.Config, in)
	if err != nil {
		return nil, nil, fmt.Errorf("retain: %s prompt: %w", mode, err)
	}
	binding, err := e.router.Resolve(in.Config, domain.ModelSlotExtract)
	if err != nil {
		return nil, nil, err
	}
	var resp extractResponse
	usage, err := e.llm.Structured(ctx, port.StructuredRequest{
		Slot: domain.ModelSlotExtract, Binding: binding, Name: p.Name, System: p.System, User: p.User, Schema: p.Schema,
	}, &resp)
	if err != nil {
		return nil, nil, err
	}
	pc := parseConfig{
		verbatim: s.KeepsChunkText(), chunk: in.Text, causal: tuningOf(in.Config).causal,
		freeForm: boolOr(in.Config.EntitiesAllowFreeForm, true),
	}
	return parseFacts(resp.Facts, pc), []domain.Usage{usage}, nil
}

// extractResponse is the EXTRACT response shape shared by every mode's schema.
type extractResponse struct {
	Facts []rawFact `json:"facts"`
}

// rawFact is one fact as the model returned it; dimension fields are absent in some modes.
type rawFact struct {
	What           *string        `json:"what"`
	When           *string        `json:"when"`
	Where          *string        `json:"where"`
	Who            *string        `json:"who"`
	Why            *string        `json:"why"`
	FactKind       string         `json:"fact_kind"`
	FactType       string         `json:"fact_type"`
	OccurredStart  *string        `json:"occurred_start"`
	OccurredEnd    *string        `json:"occurred_end"`
	TimeExpression *string        `json:"time_expression"`
	Entities       []rawEntity    `json:"entities"`
	Labels         []domain.Label `json:"labels"`
	CausedBy       []int          `json:"caused_by"`
}

// rawEntity is one extracted mention.
type rawEntity struct {
	Name string  `json:"name"`
	Kind *string `json:"kind"`
}

// parseConfig steers parseFacts.
type parseConfig struct {
	verbatim bool
	chunk    string
	causal   bool
	freeForm bool
}

// maxCauses is the most causal parents one fact keeps (Hindsight: max 2).
const maxCauses = 2

// parseFacts turns the model's facts into ExtractedFacts. Verbatim collapses to one fact holding the chunk
// text (metadata from the first entry, mentions and labels from all). Otherwise each fact's text is
// Hindsight's "what | When: when | Involving: who | why"; a fact with no what is dropped, and caused_by
// indexes are remapped past dropped facts (only earlier facts, at most two; none when causal links are off).
func parseFacts(raw []rawFact, pc parseConfig) []ExtractedFact {
	if pc.verbatim {
		return verbatimFact(raw, pc)
	}
	out := make([]ExtractedFact, 0, len(raw))
	index := make(map[int]int, len(raw)) // raw index -> output index
	for i, r := range raw {
		text := composeText(r)
		if text == "" {
			continue
		}
		f := baseFact(r, pc)
		f.Text = text
		if pc.causal {
			f.Causes = remapCauses(r.CausedBy, i, index)
		}
		index[i] = len(out)
		out = append(out, f)
	}
	return out
}

// verbatimFact is the one fact of a verbatim chunk.
func verbatimFact(raw []rawFact, pc parseConfig) []ExtractedFact {
	f := ExtractedFact{Text: pc.chunk, Type: domain.FactTypeWorld}
	if len(raw) > 0 {
		f = baseFact(raw[0], pc)
		f.Text = pc.chunk
		for _, r := range raw[1:] {
			more := baseFact(r, pc)
			f.Entities = append(f.Entities, more.Entities...)
			f.Labels = append(f.Labels, more.Labels...)
		}
	}
	return []ExtractedFact{f}
}

// baseFact maps the type, dates, mentions and labels of one raw fact.
func baseFact(r rawFact, pc parseConfig) ExtractedFact {
	f := ExtractedFact{Type: factType(r), Labels: r.Labels}
	if r.FactKind == "event" {
		f.OccurredStart = parseTime(dimension(r.OccurredStart))
		f.OccurredEnd = parseTime(dimension(r.OccurredEnd))
		if f.OccurredEnd == nil && f.OccurredStart != nil {
			end := *f.OccurredStart
			f.OccurredEnd = &end
		}
		f.TimeExpression = dimension(r.TimeExpression)
	}
	if pc.freeForm {
		for _, e := range r.Entities {
			if name := strings.TrimSpace(e.Name); name != "" {
				f.Entities = append(f.Entities, Mention{Name: name, Kind: strings.ToLower(dimension(e.Kind))})
			}
		}
	}
	return f
}

// factType maps the model's fact_type: "assistant" is the agent's own experience, "world" is world; any
// other value falls back to fact_kind, then world (Hindsight; decision 404).
func factType(r rawFact) domain.FactType {
	switch r.FactType {
	case "assistant":
		return domain.FactTypeExperience
	case "world":
		return domain.FactTypeWorld
	}
	if r.FactKind == "assistant" {
		return domain.FactTypeExperience
	}
	return domain.FactTypeWorld
}

// composeText is Hindsight's combined fact text from the what / when / who / why dimensions ("where" is
// left out, as in Hindsight).
func composeText(r rawFact) string {
	what := dimension(r.What)
	if what == "" {
		return ""
	}
	parts := []string{what}
	if when := dimension(r.When); when != "" {
		parts = append(parts, "When: "+when)
	}
	if who := dimension(r.Who); who != "" {
		parts = append(parts, "Involving: "+who)
	}
	if why := dimension(r.Why); why != "" {
		parts = append(parts, why)
	}
	return strings.Join(parts, " | ")
}

// dimension returns the trimmed value, treating nil, empty and "N/A" alike as empty.
func dimension(s *string) string {
	if s == nil {
		return ""
	}
	v := strings.TrimSpace(*s)
	if strings.EqualFold(v, "n/a") {
		return ""
	}
	return v
}

// remapCauses keeps the caused_by indexes that point at earlier, kept facts, mapped to output indexes.
func remapCauses(causedBy []int, self int, index map[int]int) []int {
	var out []int
	seen := map[int]bool{}
	for _, c := range causedBy {
		if c < 0 || c >= self {
			continue
		}
		if o, ok := index[c]; ok && !seen[o] && len(out) < maxCauses {
			seen[o] = true
			out = append(out, o)
		}
	}
	return out
}

// timeLayouts are the date formats accepted from the model, most specific first.
var timeLayouts = []string{
	time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02",
}

// parseTime parses an ISO 8601 date or timestamp (UTC when no zone is given); anything else is nil.
func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}

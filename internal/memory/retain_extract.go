package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/prompts"
	"strings"
	"time"
)

// extractor turns one chunk into facts: llmExtractor in production; tests wrap it to hook a run between calls.
type extractor interface {
	// Extract makes one LLM call per chunk (EXTRACT slot); mission + speaker context injected. The
	// strategy for in.Config's extraction mode builds the prompt; chunks mode returns the chunk as a
	// single fact with no call. It also returns the call's token usage (empty with no call).
	Extract(ctx context.Context, in extractInput) ([]domain.ExtractedFact, []domain.Usage, error)
}

// extractInput is one chunk to extract facts from, with the item context the prompt needs.
type extractInput struct {
	// Bank owns the document.
	Bank domain.BankRef
	// DocumentID is the source document.
	DocumentID domain.DocumentID
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

// llmExtractor is the extractor over ai.LLM.
type llmExtractor struct {
	llm    ai.LLM
	router *ai.Router
	modes  map[domain.ExtractionMode]extractionStrategy
}

var _ extractor = (*llmExtractor)(nil)

// newExtractor returns the extractor. modes are the strategies it can run; none = all five built-in ones.
func newExtractor(llm ai.LLM, router *ai.Router, modes ...extractionStrategy) extractor {
	if len(modes) == 0 {
		modes = []extractionStrategy{conciseMode(), verboseMode(), verbatimMode(), chunksMode(), customMode()}
	}
	m := make(map[domain.ExtractionMode]extractionStrategy, len(modes))
	for _, s := range modes {
		m[s.Mode()] = s
	}
	return &llmExtractor{llm: llm, router: router, modes: m}
}

// Extract implements extractor. An unset mode is concise; an unknown one is domain.ErrInvalidArgument.
func (e *llmExtractor) Extract(ctx context.Context, in extractInput) ([]domain.ExtractedFact, []domain.Usage, error) {
	mode := in.Config.GetRetain().ExtractionMode
	if mode == domain.ExtractionModeUnspecified {
		mode = domain.ExtractionModeConcise
	}
	s, ok := e.modes[mode]
	if !ok {
		return nil, nil, fmt.Errorf("%w: unknown extraction mode %q", domain.ErrInvalidArgument, mode)
	}
	if !s.NeedsLLM() {
		return []domain.ExtractedFact{{Text: in.Text, Type: domain.FactTypeWorld}}, nil, nil
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
	usage, err := e.llm.Structured(ctx, ai.StructuredRequest{
		Slot: domain.ModelSlotExtract, Binding: binding, Name: p.Name, System: p.System, User: p.User, Schema: p.Schema,
	}, &resp)
	if err != nil {
		return nil, nil, err
	}
	pc := parseConfig{
		verbatim: s.KeepsChunkText(), chunk: in.Text, causal: tuningOf(in.Config).causal,
		freeForm: domain.ValueOr(in.Config.GetRetain().EntitiesAllowFreeForm, true),
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
func parseFacts(raw []rawFact, pc parseConfig) []domain.ExtractedFact {
	if pc.verbatim {
		return verbatimFact(raw, pc)
	}
	out := make([]domain.ExtractedFact, 0, len(raw))
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
func verbatimFact(raw []rawFact, pc parseConfig) []domain.ExtractedFact {
	f := domain.ExtractedFact{Text: pc.chunk, Type: domain.FactTypeWorld}
	if len(raw) > 0 {
		f = baseFact(raw[0], pc)
		f.Text = pc.chunk
		for _, r := range raw[1:] {
			more := baseFact(r, pc)
			f.Entities = append(f.Entities, more.Entities...)
			f.Labels = append(f.Labels, more.Labels...)
		}
	}
	return []domain.ExtractedFact{f}
}

// baseFact maps the type, dates, mentions and labels of one raw fact.
func baseFact(r rawFact, pc parseConfig) domain.ExtractedFact {
	f := domain.ExtractedFact{Type: factType(r), Labels: r.Labels}
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
				f.Entities = append(f.Entities, domain.Mention{Name: name, Kind: strings.ToLower(dimension(e.Kind))})
			}
		}
	}
	return f
}

// factType maps the model's fact_type: "assistant" is the agent's own experience, "world" is world; any
// other value falls back to fact_kind, then world (as in Hindsight).
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

// extractionStrategy produces facts for one mode; the extractor picks one per document.
type extractionStrategy interface {
	// Mode is the extraction mode the strategy implements: concise | verbose | verbatim | chunks | custom.
	Mode() domain.ExtractionMode
	// NeedsLLM is false for chunks mode, which stores chunks as units with no call.
	NeedsLLM() bool
	// KeepsChunkText is true for verbatim mode: the chunk text is the fact text and the call only adds
	// metadata (dates, entities, labels).
	KeepsChunkText() bool
	// Prompt renders the system and user messages and the JSON Schema of the EXTRACT call for one chunk.
	Prompt(cfg domain.BankConfig, in extractInput) (extractPrompt, error)
}

// extractPrompt is one rendered EXTRACT call.
type extractPrompt struct {
	// Name is the system template name, also the request Name (tracing, scripted fakes).
	Name string
	// System is the system message.
	System string
	// User is the user message.
	User string
	// Schema is the response JSON Schema.
	Schema json.RawMessage
}

// strategy is the one extractionStrategy implementation; the modes differ only in template and flags.
type strategy struct {
	mode     domain.ExtractionMode
	tmpl     string
	llm      bool
	verbatim bool
}

var _ extractionStrategy = (*strategy)(nil)

// conciseMode extracts compact facts (the default mode).
func conciseMode() extractionStrategy {
	return &strategy{mode: domain.ExtractionModeConcise, tmpl: prompts.ExtractConcise, llm: true}
}

// verboseMode extracts detailed facts with every dimension.
func verboseMode() extractionStrategy {
	return &strategy{mode: domain.ExtractionModeVerbose, tmpl: prompts.ExtractVerbose, llm: true}
}

// verbatimMode keeps each chunk's text as one fact and extracts only its metadata.
func verbatimMode() extractionStrategy {
	return &strategy{mode: domain.ExtractionModeVerbatim, tmpl: prompts.ExtractVerbatim, llm: true, verbatim: true}
}

// chunksMode stores each chunk as one fact with no LLM call.
func chunksMode() extractionStrategy {
	return &strategy{mode: domain.ExtractionModeChunks, verbatim: true}
}

// customMode extracts with retain.custom_extraction_prompt as the guidelines; a blank prompt falls back to the
// concise template.
func customMode() extractionStrategy {
	return &strategy{mode: domain.ExtractionModeCustom, tmpl: prompts.ExtractCustom, llm: true}
}

// Mode implements extractionStrategy.
func (s *strategy) Mode() domain.ExtractionMode { return s.mode }

// NeedsLLM implements extractionStrategy.
func (s *strategy) NeedsLLM() bool { return s.llm }

// KeepsChunkText implements extractionStrategy.
func (s *strategy) KeepsChunkText() bool { return s.verbatim }

// Prompt implements extractionStrategy. Chunks mode has no prompt (domain.ErrInvalidArgument); custom mode
// with a blank CustomExtractionPrompt renders the concise template.
func (s *strategy) Prompt(cfg domain.BankConfig, in extractInput) (extractPrompt, error) {
	if !s.llm {
		return extractPrompt{}, fmt.Errorf("%w: extraction mode %s makes no call", domain.ErrInvalidArgument, s.mode)
	}
	tmpl := s.tmpl
	if s.mode == domain.ExtractionModeCustom && strings.TrimSpace(cfg.GetRetain().CustomExtractionPrompt) == "" {
		tmpl = prompts.ExtractConcise
	}
	data := prompts.ExtractData{
		CustomPrompt:     cfg.GetRetain().CustomExtractionPrompt,
		CausalLinks:      tuningOf(cfg).causal,
		FreeFormEntities: domain.ValueOr(cfg.GetRetain().EntitiesAllowFreeForm, true),
		Labels:           cfg.GetRetain().EntityLabels,
		Mission:          cfg.GetRetain().Mission,
		MentionedAt:      in.MentionedAt,
		Context:          in.Context,
		Metadata:         in.Metadata,
		Hints:            in.Hints,
		Text:             in.Text,
	}
	system, err := prompts.Render(tmpl, data)
	if err != nil {
		return extractPrompt{}, fmt.Errorf("retain: render %s: %w", tmpl, err)
	}
	user, err := prompts.Render(prompts.ExtractUser, data)
	if err != nil {
		return extractPrompt{}, fmt.Errorf("retain: render %s: %w", prompts.ExtractUser, err)
	}
	schema, err := prompts.ExtractSchema(tmpl, data.OptionalDimensions)
	if err != nil {
		return extractPrompt{}, fmt.Errorf("retain: schema %s: %w", tmpl, err)
	}
	return extractPrompt{Name: tmpl, System: system, User: user, Schema: schema}, nil
}

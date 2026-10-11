package retain

import (
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/prompts"
	"strings"
)

// ExtractionStrategy produces facts for one mode; Extractor picks one per document.
type ExtractionStrategy interface {
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

// strategy is the one ExtractionStrategy implementation; the modes differ only in template and flags.
type strategy struct {
	mode     domain.ExtractionMode
	tmpl     string
	llm      bool
	verbatim bool
}

var _ ExtractionStrategy = (*strategy)(nil)

// conciseMode extracts compact facts (the default mode).
func conciseMode() ExtractionStrategy {
	return &strategy{mode: domain.ExtractionModeConcise, tmpl: prompts.ExtractConcise, llm: true}
}

// verboseMode extracts detailed facts with every dimension.
func verboseMode() ExtractionStrategy {
	return &strategy{mode: domain.ExtractionModeVerbose, tmpl: prompts.ExtractVerbose, llm: true}
}

// verbatimMode keeps each chunk's text as one fact and extracts only its metadata.
func verbatimMode() ExtractionStrategy {
	return &strategy{mode: domain.ExtractionModeVerbatim, tmpl: prompts.ExtractVerbatim, llm: true, verbatim: true}
}

// chunksMode stores each chunk as one fact with no LLM call.
func chunksMode() ExtractionStrategy {
	return &strategy{mode: domain.ExtractionModeChunks, verbatim: true}
}

// customMode extracts with BankConfig.CustomExtractionPrompt as the guidelines; a blank prompt falls back to the
// concise template.
func customMode() ExtractionStrategy {
	return &strategy{mode: domain.ExtractionModeCustom, tmpl: prompts.ExtractCustom, llm: true}
}

// Mode implements ExtractionStrategy.
func (s *strategy) Mode() domain.ExtractionMode { return s.mode }

// NeedsLLM implements ExtractionStrategy.
func (s *strategy) NeedsLLM() bool { return s.llm }

// KeepsChunkText implements ExtractionStrategy.
func (s *strategy) KeepsChunkText() bool { return s.verbatim }

// Prompt implements ExtractionStrategy. Chunks mode has no prompt (domain.ErrInvalidArgument); custom mode
// with a blank CustomExtractionPrompt renders the concise template.
func (s *strategy) Prompt(cfg domain.BankConfig, in extractInput) (extractPrompt, error) {
	if !s.llm {
		return extractPrompt{}, fmt.Errorf("%w: extraction mode %s makes no call", domain.ErrInvalidArgument, s.mode)
	}
	tmpl := s.tmpl
	if s.mode == domain.ExtractionModeCustom && strings.TrimSpace(cfg.CustomExtractionPrompt) == "" {
		tmpl = prompts.ExtractConcise
	}
	data := prompts.ExtractData{
		CustomPrompt:     cfg.CustomExtractionPrompt,
		CausalLinks:      tuningOf(cfg).causal,
		FreeFormEntities: boolOr(cfg.EntitiesAllowFreeForm, true),
		Labels:           cfg.EntityLabels,
		Mission:          cfg.RetainMission,
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

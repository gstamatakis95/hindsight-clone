package retain

import (
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/prompts"
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
	Prompt(cfg domain.BankConfig, in ExtractInput) (ExtractPrompt, error)
}

// ExtractPrompt is one rendered EXTRACT call.
type ExtractPrompt struct {
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

// Concise extracts compact facts (the default mode).
func Concise() ExtractionStrategy {
	return &strategy{mode: domain.ExtractionModeConcise, tmpl: prompts.ExtractConcise, llm: true}
}

// Verbose extracts detailed facts with every dimension.
func Verbose() ExtractionStrategy {
	return &strategy{mode: domain.ExtractionModeVerbose, tmpl: prompts.ExtractVerbose, llm: true}
}

// Verbatim keeps each chunk's text as one fact and extracts only its metadata.
func Verbatim() ExtractionStrategy {
	return &strategy{mode: domain.ExtractionModeVerbatim, tmpl: prompts.ExtractVerbatim, llm: true, verbatim: true}
}

// Chunks stores each chunk as one fact with no LLM call.
func Chunks() ExtractionStrategy {
	return &strategy{mode: domain.ExtractionModeChunks, verbatim: true}
}

// Custom extracts with BankConfig.CustomExtractionPrompt as the guidelines.
func Custom() ExtractionStrategy {
	return &strategy{mode: domain.ExtractionModeCustom, tmpl: prompts.ExtractCustom, llm: true}
}

// Mode implements ExtractionStrategy.
func (s *strategy) Mode() domain.ExtractionMode { return s.mode }

// NeedsLLM implements ExtractionStrategy.
func (s *strategy) NeedsLLM() bool { return s.llm }

// KeepsChunkText implements ExtractionStrategy.
func (s *strategy) KeepsChunkText() bool { return s.verbatim }

// Prompt implements ExtractionStrategy. Chunks mode has no prompt (domain.ErrInvalidArgument).
func (s *strategy) Prompt(cfg domain.BankConfig, in ExtractInput) (ExtractPrompt, error) {
	if !s.llm {
		return ExtractPrompt{}, fmt.Errorf("%w: extraction mode %s makes no call", domain.ErrInvalidArgument, s.mode)
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
	system, err := prompts.Render(s.tmpl, data)
	if err != nil {
		return ExtractPrompt{}, fmt.Errorf("retain: render %s: %w", s.tmpl, err)
	}
	user, err := prompts.Render(prompts.ExtractUser, data)
	if err != nil {
		return ExtractPrompt{}, fmt.Errorf("retain: render %s: %w", prompts.ExtractUser, err)
	}
	schema, err := prompts.ExtractSchema(s.tmpl, data.OptionalDimensions)
	if err != nil {
		return ExtractPrompt{}, fmt.Errorf("retain: schema %s: %w", s.tmpl, err)
	}
	return ExtractPrompt{Name: s.tmpl, System: system, User: user, Schema: schema}, nil
}

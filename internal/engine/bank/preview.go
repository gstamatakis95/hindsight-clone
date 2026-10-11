package bank

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/prompts"
	"strings"
)

// PromptPreviewer renders the exact messages an operation would send, with no LLM call.
type PromptPreviewer interface {
	// Preview renders op's prompt for bank b with overrides o merged on top of the effective config.
	Preview(
		ctx context.Context, b domain.BankRef, op domain.PromptOperation, o domain.BankConfig,
	) (domain.PromptPreview, error)
}

// Placeholders stand in for the runtime content a real call carries (decision 1306).
const (
	phChunk       = "{chunk text}"
	phContext     = "{item context}"
	phFactID      = "{fact id}"
	phFact        = "{fact text}"
	phObsID       = "{observation id}"
	phObservation = "{observation text}"
	phQuery       = "{query}"
	phModelName   = "{mental model name}"
	phSourceQuery = "{mental model source query}"
	phHeading     = "{section heading}"
	phBlock       = "{block markdown}"
	phNow         = "{now}"
	// zeroNow is how the templates render the zero time; Preview replaces it with phNow.
	zeroNow = "0001-01-01 00:00"
)

// previewer implements PromptPreviewer with internal/prompts.
type previewer struct {
	cfg  config.Resolver
	dirs DirectiveSelector
}

var _ PromptPreviewer = (*previewer)(nil)

// NewPromptPreviewer returns the PromptPreviewer; cfg resolves the effective config and dirs picks the
// directives reflect and synthesize would see (global ones: a preview has no request tags).
func NewPromptPreviewer(cfg config.Resolver, dirs DirectiveSelector) PromptPreviewer {
	return &previewer{cfg: cfg, dirs: dirs}
}

// Preview resolves the effective config with o on top and renders op's messages from the built-in
// templates, with placeholders for request-time content.
func (p *previewer) Preview(
	ctx context.Context, b domain.BankRef, op domain.PromptOperation, o domain.BankConfig,
) (domain.PromptPreview, error) {
	cfg, err := p.cfg.Effective(ctx, b, o)
	if err != nil {
		return domain.PromptPreview{}, err //nolint:wrapcheck // config.Resolver errors are already domain error classes
	}
	switch op {
	case domain.PromptOperationRetain:
		return previewRetain(cfg)
	case domain.PromptOperationConsolidation:
		return previewConsolidation(cfg)
	case domain.PromptOperationMentalModelDelta:
		return previewDelta(cfg)
	case domain.PromptOperationReflect, domain.PromptOperationSynthesize:
		dirs, err := p.dirs.Select(ctx, b, nil, false)
		if err != nil {
			return domain.PromptPreview{}, fmt.Errorf("preview directives: %w", err)
		}
		if op == domain.PromptOperationReflect {
			return previewReflect(cfg, dirs)
		}
		return previewSynthesize(cfg, dirs)
	default:
		return domain.PromptPreview{}, fmt.Errorf("%w: unknown prompt operation %q", domain.ErrInvalidArgument, op)
	}
}

// message renders template name with data into one builtin block followed by the config blocks feeding it.
func message(
	role domain.PromptRole, name string, data any, cfgBlocks ...domain.PromptBlock,
) (domain.PromptMessage, error) {
	text, err := prompts.Render(name, data)
	if err != nil {
		return domain.PromptMessage{}, fmt.Errorf("render %s: %w", name, err)
	}
	text = strings.ReplaceAll(text, zeroNow, phNow)
	blocks := append([]domain.PromptBlock{{
		Text: text, Source: domain.PromptSourceBuiltin, Section: name, Active: true,
	}}, cfgBlocks...)
	return domain.PromptMessage{Role: role, Blocks: blocks}, nil
}

// cfgBlock is a config-sourced block: active when the field is set.
func cfgBlock(field, section, text string) domain.PromptBlock {
	return domain.PromptBlock{
		Text: text, Source: domain.PromptSourceConfig, Field: field, Section: section, Active: text != "",
	}
}

// build renders a system and a user message and attaches the response schema of schemaName ("" = none).
func build(sys, user string, data any, schema json.RawMessage, sysBlocks ...domain.PromptBlock) (
	domain.PromptPreview, error,
) {
	s, err := message(domain.PromptRoleSystem, sys, data, sysBlocks...)
	if err != nil {
		return domain.PromptPreview{}, err
	}
	u, err := message(domain.PromptRoleUser, user, data)
	if err != nil {
		return domain.PromptPreview{}, err
	}
	return domain.PromptPreview{Messages: []domain.PromptMessage{s, u}, ResponseSchemaJSON: string(schema)}, nil
}

// extractTemplate maps an extraction mode to its system template; chunks mode has none.
func extractTemplate(mode domain.ExtractionMode) (string, bool) {
	switch mode {
	case domain.ExtractionModeVerbose:
		return prompts.ExtractVerbose, true
	case domain.ExtractionModeVerbatim:
		return prompts.ExtractVerbatim, true
	case domain.ExtractionModeCustom:
		return prompts.ExtractCustom, true
	case domain.ExtractionModeChunks:
		return "", false
	default:
		return prompts.ExtractConcise, true
	}
}

// previewRetain renders the extraction call of the effective extraction mode.
func previewRetain(cfg domain.BankConfig) (domain.PromptPreview, error) {
	name, ok := extractTemplate(cfg.GetRetain().ExtractionMode)
	if !ok {
		return domain.PromptPreview{SkippedReason: "extraction_mode chunks stores each chunk as a unit with no LLM call"},
			nil
	}
	data := prompts.ExtractData{
		CustomPrompt:     cfg.GetRetain().CustomExtractionPrompt,
		CausalLinks:      cfg.Retain == nil || deref(cfg.Retain.ExtractCausalLinks, true),
		FreeFormEntities: deref(cfg.GetRetain().EntitiesAllowFreeForm, true),
		Labels:           cfg.GetRetain().EntityLabels,
		Mission:          cfg.GetRetain().Mission,
		Context:          phContext,
		Text:             phChunk,
	}
	schema, err := prompts.ExtractSchema(name, false)
	if err != nil {
		return domain.PromptPreview{}, fmt.Errorf("extraction schema: %w", err)
	}
	custom := ""
	if name == prompts.ExtractCustom {
		custom = cfg.GetRetain().CustomExtractionPrompt
	}
	return build(name, prompts.ExtractUser, data, schema,
		cfgBlock("retain.mission", "mission", cfg.GetRetain().Mission),
		cfgBlock("retain.custom_extraction_prompt", "custom_prompt", custom),
		cfgBlock("retain.entity_labels", "labels", jsonText(cfg.GetRetain().EntityLabels)))
}

// previewConsolidation renders one CONSOLIDATE call over a placeholder fact and observation.
func previewConsolidation(cfg domain.BankConfig) (domain.PromptPreview, error) {
	data := prompts.ConsolidateData{
		Mission:      cfg.GetObservations().Mission,
		Facts:        []prompts.SourceFact{{ID: phFactID, Text: phFact}},
		Observations: []prompts.ConsolidateObservation{{ID: phObsID, Text: phObservation, ProofCount: 1}},
	}
	if n := cfg.GetObservations().MaxObservationsPerScope; n != nil {
		data.Capacity = &prompts.ConsolidateCapacity{Max: *n, Remaining: *n}
	}
	schema, err := prompts.Schema(prompts.ConsolidateSystem)
	if err != nil {
		return domain.PromptPreview{}, fmt.Errorf("consolidation schema: %w", err)
	}
	return build(prompts.ConsolidateSystem, prompts.ConsolidateUser, data, schema,
		cfgBlock("observations.mission", "mission", cfg.GetObservations().Mission))
}

// previewReflect renders the reflect loop's system prompt and the question turn.
func previewReflect(cfg domain.BankConfig, dirs []domain.Directive) (domain.PromptPreview, error) {
	data := prompts.ReflectData{
		Mission: cfg.GetReflect().Mission, Disposition: cfg.GetReflect().Disposition, Directives: dirs,
		SearchMentalModels: true, ReadMentalModels: true, Observations: true, Recall: true, Expand: true,
		Budget: string(domain.BudgetLow), Query: phQuery,
	}
	s, err := message(domain.PromptRoleSystem, prompts.ReflectSystem, data,
		cfgBlock("reflect.mission", "mission", cfg.GetReflect().Mission),
		cfgBlock("reflect.disposition", "disposition", jsonText(cfg.GetReflect().Disposition)),
		cfgBlock("", "directives", directiveText(dirs)))
	if err != nil {
		return domain.PromptPreview{}, err
	}
	u := domain.PromptMessage{Role: domain.PromptRoleUser, Blocks: []domain.PromptBlock{{
		Text: phQuery, Source: domain.PromptSourceBuiltin, Section: "query", Active: true,
	}}}
	return domain.PromptPreview{Messages: []domain.PromptMessage{s, u}}, nil
}

// previewSynthesize renders the single-shot synthesize call.
func previewSynthesize(cfg domain.BankConfig, dirs []domain.Directive) (domain.PromptPreview, error) {
	data := prompts.SynthesizeData{
		Name: phModelName, SourceQuery: phSourceQuery, Mission: cfg.GetReflect().Mission, Directives: dirs,
		Observations: []prompts.SourceFact{{ID: phObsID, Text: phObservation}},
		Facts:        []prompts.SourceFact{{ID: phFactID, Text: phFact}},
		MaxTokens:    maxContentTokens(cfg),
	}
	return build(prompts.SynthesizeSystem, prompts.SynthesizeUser, data, nil,
		cfgBlock("reflect.mission", "mission", cfg.GetReflect().Mission),
		cfgBlock("", "directives", directiveText(dirs)))
}

// previewDelta renders the structured delta call over a one-section placeholder document.
func previewDelta(cfg domain.BankConfig) (domain.PromptPreview, error) {
	data := prompts.DeltaData{
		Name: phModelName, SourceQuery: phSourceQuery,
		Document: prompts.DeltaDocument{Sections: []prompts.DeltaSection{{
			ID: "s1", Heading: phHeading, Level: 2, Blocks: []prompts.DeltaBlock{{ID: "b1", Text: phBlock}},
		}}},
		Observations:   []prompts.SourceFact{{ID: phObsID, Text: phObservation}},
		Facts:          []prompts.SourceFact{{ID: phFactID, Text: phFact}},
		DocumentBudget: maxContentTokens(cfg),
	}
	schema, err := prompts.Schema(prompts.MentalModelStructuredDeltaSystem)
	if err != nil {
		return domain.PromptPreview{}, fmt.Errorf("delta schema: %w", err)
	}
	return build(prompts.MentalModelStructuredDeltaSystem, prompts.MentalModelStructuredDeltaUser, data, schema)
}

// maxContentTokens is MentalModelConfig.MaxContentTokens, 0 when unset.
func maxContentTokens(cfg domain.BankConfig) int {
	if cfg.MentalModels == nil {
		return 0
	}
	return deref(cfg.MentalModels.MaxContentTokens, 0)
}

// directiveText lists directives as "name: rule; rule" lines; empty when none.
func directiveText(dirs []domain.Directive) string {
	lines := make([]string, 0, len(dirs))
	for _, d := range dirs {
		lines = append(lines, d.Name+": "+strings.Join(d.Rules, "; "))
	}
	return strings.Join(lines, "\n")
}

// jsonText is v as compact JSON, empty for nil, empty lists and the zero value's null.
func jsonText(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	switch s := string(raw); s {
	case "null", "[]", "{}":
		return ""
	default:
		return s
	}
}

// deref returns *p, or def when p is nil.
func deref[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

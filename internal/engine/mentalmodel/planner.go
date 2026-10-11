package mentalmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/prompts"
	"slices"
	"strings"
)

// DirectiveSelector is the part of bank.DirectiveSelector the single-shot synthesizer consumes to put the
// bank's directives into SYNTHESIZE; bank.DirectiveSelector satisfies it (same signature).
type DirectiveSelector interface {
	// Select returns bank b's active directives that are global or match tags; all = every active
	// directive regardless of tags.
	Select(ctx context.Context, b domain.BankRef, tags []string, all bool) ([]domain.Directive, error)
}

// ModelDeps are what the synthesizers need to make their own LLM calls.
type ModelDeps struct {
	// LLM makes the SYNTHESIZE and MENTAL_MODEL_DELTA calls.
	LLM ai.LLM
	// Router resolves each slot's binding from the merged config.
	Router *ai.Router
	// Clock dates the prompts.
	Clock clock.Clock
	// Directives selects the directives for SYNTHESIZE; nil = none.
	Directives DirectiveSelector
}

// deltaPlanner makes the MENTAL_MODEL_DELTA call: edit ops over the current document's sections and
// blocks, applied at once, with one retry that shows the model the rejected ops when none applied or when
// any op named a section or block id the document does not have.
type deltaPlanner struct{ d ModelDeps }

// deltaResponse is the MENTAL_MODEL_DELTA response schema's shape.
type deltaResponse struct {
	// Operations are the proposed ops.
	Operations []domain.DeltaOp `json:"operations"`
}

// plan asks for ops integrating facts (observations listed first) into m's document and applies them.
// facts must be non-empty; the caller returns ErrNoNewFacts before calling it otherwise.
func (p deltaPlanner) plan(
	ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, facts []domain.Unit,
) (deltaDraft, error) {
	binding, err := p.d.Router.Resolve(cfg, domain.ModelSlotMentalModelDelta)
	if err != nil {
		return deltaDraft{}, fmt.Errorf("mentalmodel: delta model: %w", err)
	}
	doc := splitMarkdown(m.ContentMD)
	obs, raw := promptFacts(facts)
	data := prompts.DeltaData{
		Name: m.Spec.Name, SourceQuery: m.Spec.SourceQuery, Document: doc.prompt(),
		Observations: obs, Facts: raw, DocumentTokens: approxTokens(m.ContentMD),
		DocumentBudget: maxContentTokens(m, cfg), MaxOutputTokens: derefInt(binding.MaxOutputTokens),
	}
	draft := deltaDraft{ContentMD: m.ContentMD, SourceIDs: unitIDs(facts), FactsRetrieved: len(facts)}
	ops, usage, err := p.call(ctx, binding, data)
	draft.Usage = append(draft.Usage, usage)
	if err != nil {
		return draft, err
	}
	draft.ContentMD, draft.Ops = applyEdits(m.ContentMD, ops)
	retry := retryOps(draft.Ops)
	if len(retry) == 0 {
		return draft, nil
	}
	first := draft
	data.Rejected = rejectedOps(retry)
	ops, usage, err = p.call(ctx, binding, data)
	draft.Usage = append(draft.Usage, usage)
	if err != nil {
		return draft, err
	}
	draft.ContentMD, draft.Ops = applyEdits(m.ContentMD, ops)
	switch {
	case appliedCount(draft.Ops) > 0:
		return draft, nil
	case appliedCount(first.Ops) > 0:
		// The retry was empty or fully rejected after a partial first attempt: keep attempt 1's good ops.
		first.Usage = draft.Usage
		return first, nil
	}
	draft.ContentMD = m.ContentMD
	return draft, fmt.Errorf("%w: %d ops after one retry", ErrAllOpsRejected, len(draft.Ops))
}

// retryOps returns the skipped ops that earn the one retry: every op when none applied, else only the ops
// skipped for an id the document does not have (Hindsight's _is_bad_reference). The retry's reply replaces
// the first one wholesale.
func retryOps(ops []domain.DeltaOp) []domain.DeltaOp {
	all := appliedCount(ops) == 0
	var out []domain.DeltaOp
	for _, op := range ops {
		if !op.Applied && (all || isBadReference(op.SkipReason)) {
			out = append(out, op)
		}
	}
	return out
}

// isBadReference reports whether a skip reason is an unknown or wrong-section section or block id.
func isBadReference(reason string) bool {
	return strings.HasPrefix(reason, "unknown section_id") || strings.HasPrefix(reason, "unknown block_id") ||
		strings.Contains(reason, " belongs to section ")
}

// call renders the delta prompts with data and makes one structured MENTAL_MODEL_DELTA call.
func (p deltaPlanner) call(
	ctx context.Context, binding domain.ModelBinding, data prompts.DeltaData,
) ([]domain.DeltaOp, domain.Usage, error) {
	system, err := prompts.Render(prompts.MentalModelStructuredDeltaSystem, data)
	if err != nil {
		return nil, domain.Usage{}, fmt.Errorf("mentalmodel: render delta system: %w", err)
	}
	user, err := prompts.Render(prompts.MentalModelStructuredDeltaUser, data)
	if err != nil {
		return nil, domain.Usage{}, fmt.Errorf("mentalmodel: render delta user: %w", err)
	}
	schema, err := prompts.Schema(prompts.MentalModelStructuredDeltaSystem)
	if err != nil {
		return nil, domain.Usage{}, fmt.Errorf("mentalmodel: delta schema: %w", err)
	}
	var resp deltaResponse
	usage, err := p.d.LLM.Structured(ctx, ai.StructuredRequest{
		Slot: domain.ModelSlotMentalModelDelta, Binding: binding, Name: prompts.MentalModelStructuredDeltaSystem,
		System: system, User: user, Schema: schema,
	}, &resp)
	if err != nil {
		return nil, usage, fmt.Errorf("mentalmodel: delta call: %w", err)
	}
	for i := range resp.Operations {
		resp.Operations[i].Applied, resp.Operations[i].SkipReason = false, ""
	}
	return resp.Operations, usage, nil
}

// appliedCount counts the ops that were applied.
func appliedCount(ops []domain.DeltaOp) int {
	n := 0
	for _, op := range ops {
		if op.Applied {
			n++
		}
	}
	return n
}

// rejectedOps lists the skipped ops as the retry prompt shows them: the op as sent, and why.
func rejectedOps(ops []domain.DeltaOp) []prompts.RejectedOp {
	var out []prompts.RejectedOp
	for _, op := range ops {
		if op.Applied {
			continue
		}
		sent := op
		sent.SkipReason = ""
		raw, err := json.Marshal(struct {
			Kind         domain.DeltaOpKind `json:"kind"`
			SectionID    string             `json:"section_id"`
			BlockID      string             `json:"block_id"`
			AfterBlockID string             `json:"after_block_id"`
			Heading      string             `json:"heading"`
			BlocksMD     []string           `json:"blocks_md"`
		}{sent.Kind, sent.SectionID, sent.BlockID, sent.AfterBlockID, sent.Heading, sent.BlocksMD})
		if err != nil {
			raw = []byte(string(op.Kind))
		}
		out = append(out, prompts.RejectedOp{Op: string(raw), Reason: op.SkipReason})
	}
	return out
}

// promptFacts splits units into observations and raw facts, as the prompts show them.
func promptFacts(units []domain.Unit) (observations, facts []prompts.SourceFact) {
	for _, u := range units {
		f := prompts.SourceFact{
			ID: string(u.ID), Text: u.Text, Context: u.Context,
			OccurredStart: u.OccurredStart, OccurredEnd: u.OccurredEnd, MentionedAt: u.MentionedAt,
		}
		if u.Type == domain.FactTypeObservation {
			observations = append(observations, f)
			continue
		}
		f.Type = string(u.Type)
		facts = append(facts, f)
	}
	return observations, facts
}

// unitIDs returns the ids of units, deduplicated, in first-seen order.
func unitIDs(units []domain.Unit) []domain.UnitID {
	out := make([]domain.UnitID, 0, len(units))
	for _, u := range units {
		if !slices.Contains(out, u.ID) {
			out = append(out, u.ID)
		}
	}
	return out
}

// approxTokens estimates a text's token count (about four bytes per token).
func approxTokens(s string) int { return (len(s) + 3) / 4 }

// maxContentTokens is the page's length budget: the model's max_tokens, else the bank's
// max_content_tokens, else 0 (none).
func maxContentTokens(m domain.MentalModel, cfg domain.BankConfig) int {
	if m.Spec.MaxTokens != nil && *m.Spec.MaxTokens > 0 {
		return *m.Spec.MaxTokens
	}
	if cfg.MentalModels != nil && cfg.MentalModels.MaxContentTokens != nil {
		return max(*cfg.MentalModels.MaxContentTokens, 0)
	}
	return 0
}

// derefInt returns *p, or 0 when p is nil.
func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

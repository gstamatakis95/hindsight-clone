package observation

import (
	"context"
	"fmt"
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/prompts"
	"memd/internal/store"
	"strings"
)

// Dedup defaults.
const (
	// defaultDedupThreshold is dedup_threshold when unset (the spec's 0.97).
	defaultDedupThreshold = 0.97
	// dedupTopK is how many nearest observations the probe reads; only the best one is checked.
	dedupTopK = 5
)

// dedupVerdict is the outcome of one dedup check.
type dedupVerdict struct {
	// TwinID is the near-duplicate observation; empty when none is at or above the threshold.
	TwinID domain.UnitID
	// Merge reports that DEDUP_CHECK judged both to state the same fact.
	Merge bool
	// Text is the merged observation text (Merge only).
	Text string
	// Usage is the DEDUP_CHECK call's usage; nil when no call was made.
	Usage *domain.Usage
}

// deduper reconciles a new observation with a near-duplicate already in its scope, over the vector search of
// store.Reader.
type deduper struct {
	llm    ai.LLM
	router *ai.Router
	read   store.Reader
}

// newDeduper returns the deduper. It probes with store.Reader.SearchVector (exact cosine over the scope's
// observations) instead of recall: the threshold is a cosine similarity, which fused recall scores
// are not (decision 1001).
func newDeduper(llm ai.LLM, router *ai.Router, r store.Reader) *deduper {
	return &deduper{llm: llm, router: router, read: r}
}

// dedupResponse is the DEDUP_CHECK response (prompts dedup_system.schema.json).
type dedupResponse struct {
	Action string `json:"action"`
	Text   string `json:"text"`
	Reason string `json:"reason"`
}

// Check finds the observation of scope s nearest to emb with cosine >= cfg's dedup_threshold (>= 1 disables
// dedup) and asks DEDUP_CHECK whether text repeats it. It runs outside any transaction; the caller folds a
// merge into its apply transaction (the twin is updated with the merged text and gains the new observation's
// evidence, and the new observation is not created).
func (d *deduper) Check(
	ctx context.Context, s domain.ScopeKey, cfg domain.BankConfig, text string, emb []float32,
) (dedupVerdict, error) {
	threshold := defaultDedupThreshold
	if cfg.GetObservations().DedupThreshold != nil {
		threshold = *cfg.GetObservations().DedupThreshold
	}
	if threshold >= 1 || len(emb) == 0 {
		return dedupVerdict{}, nil
	}
	twin, ok, err := d.nearest(ctx, s, emb, threshold)
	if err != nil || !ok {
		return dedupVerdict{}, err
	}
	binding, err := d.router.Resolve(cfg, domain.ModelSlotDedupCheck)
	if err != nil {
		return dedupVerdict{}, fmt.Errorf("consolidate: dedup binding: %w", err)
	}
	req, err := dedupRequest(binding, text, twin.Text)
	if err != nil {
		return dedupVerdict{}, err
	}
	var out dedupResponse
	usage, err := d.llm.Structured(ctx, req, &out)
	v := dedupVerdict{TwinID: twin.ID, Usage: &usage}
	if err != nil {
		return v, fmt.Errorf("consolidate: dedup check: %w", err)
	}
	if out.Action == "merge" {
		v.Merge = true
		v.Text = strings.TrimSpace(out.Text)
		if v.Text == "" {
			v.Text = twin.Text
		}
	}
	return v, nil
}

// nearest returns the scope's observation most similar to emb at or above threshold.
func (d *deduper) nearest(
	ctx context.Context, s domain.ScopeKey, emb []float32, threshold float64,
) (domain.Unit, bool, error) {
	hits, err := d.read.SearchVector(ctx, store.VectorQuery{
		Bank:          s.BankRef,
		Embedding:     emb,
		Types:         []domain.FactType{domain.FactTypeObservation},
		Tags:          scopePredicate(s),
		MinSimilarity: threshold,
		Limit:         dedupTopK,
	})
	if err != nil {
		return domain.Unit{}, false, fmt.Errorf("consolidate: dedup probe: %w", err)
	}
	if len(hits) == 0 || hits[0].Score < threshold {
		return domain.Unit{}, false, nil
	}
	us, err := d.read.GetUnits(ctx, s.BankRef, []domain.UnitID{hits[0].ID})
	if err != nil {
		return domain.Unit{}, false, fmt.Errorf("consolidate: dedup twin: %w", err)
	}
	if len(us) == 0 {
		return domain.Unit{}, false, nil
	}
	return us[0], true, nil
}

// dedupRequest renders the DEDUP_CHECK call.
func dedupRequest(binding domain.ModelBinding, text, existing string) (ai.StructuredRequest, error) {
	data := prompts.DedupData{New: text, Existing: existing}
	system, err := prompts.Render(prompts.DedupSystem, data)
	if err != nil {
		return ai.StructuredRequest{}, fmt.Errorf("consolidate: render dedup system: %w", err)
	}
	user, err := prompts.Render(prompts.DedupUser, data)
	if err != nil {
		return ai.StructuredRequest{}, fmt.Errorf("consolidate: render dedup user: %w", err)
	}
	schema, err := prompts.Schema(prompts.DedupSystem)
	if err != nil {
		return ai.StructuredRequest{}, fmt.Errorf("consolidate: dedup schema: %w", err)
	}
	return ai.StructuredRequest{
		Slot: domain.ModelSlotDedupCheck, Binding: binding, Name: prompts.DedupSystem,
		System: system, User: user, Schema: schema,
	}, nil
}

// scopePredicate selects exactly the observations of scope s (its exact tag set; the shared scope is
// the untagged one).
func scopePredicate(s domain.ScopeKey) *domain.TagPredicate {
	return &domain.TagPredicate{Op: domain.PredicateTags, Tags: s.Tags, Match: domain.TagsMatchExact}
}

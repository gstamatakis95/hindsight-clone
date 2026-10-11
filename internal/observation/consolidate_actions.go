package observation

import (
	"context"
	"fmt"
	"memd/internal/domain"
)

// actionKind is what a consolidation decision does to a scope's observations.
type actionKind string

// Action kinds.
const (
	// actionCreate adds a new observation.
	actionCreate actionKind = "create"
	// actionUpdate rewrites an existing observation and bumps its version.
	actionUpdate actionKind = "update"
)

// consolidationAction is one decision of the CONSOLIDATE call for one scope.
type consolidationAction struct {
	// Kind is Create | Update | None.
	Kind actionKind
	// ObservationID is the observation to rewrite, for Update.
	ObservationID domain.UnitID
	// Text is the new observation text (Create and Update).
	Text string
	// Evidence is each source unit + exact quote the observation rests on.
	Evidence []domain.Evidence
	// Embedding is Text's embedding, computed before the apply transaction.
	Embedding []float32
}

// embed fills the embedding of every action that has none, in one EmbedDocuments call.
func (c *consolidator) embed(ctx context.Context, actions []consolidationAction) error {
	var idx []int
	var texts []string
	for i, a := range actions {
		if len(a.Embedding) == 0 {
			idx = append(idx, i)
			texts = append(texts, a.Text)
		}
	}
	if len(texts) == 0 {
		return nil
	}
	vecs, err := c.d.Embedder.EmbedDocuments(ctx, texts)
	if err != nil {
		return fmt.Errorf("consolidate: embed observations: %w", err)
	}
	if len(vecs) != len(texts) {
		return fmt.Errorf("%w: consolidate: embedder returned %d vectors for %d texts",
			domain.ErrUnavailable, len(vecs), len(texts))
	}
	for k, i := range idx {
		actions[i].Embedding = vecs[k]
	}
	return nil
}

// dedup checks each create against the scope's existing observations. A merge verdict turns the create
// into an update of the twin with the merged text (re-embedded later), so the twin gains the evidence
// and no near-duplicate is written; keep leaves the create. A failed check keeps the create (nothing is
// lost) unless the run is stopping.
func (c *consolidator) dedup(
	ctx context.Context, s domain.ScopeKey, cfg domain.BankConfig, actions []consolidationAction,
) ([]domain.Usage, error) {
	var usage []domain.Usage
	for i, a := range actions {
		if a.Kind != actionCreate {
			continue
		}
		v, err := c.deduper.Check(ctx, s, cfg, a.Text, a.Embedding)
		if v.Usage != nil {
			usage = append(usage, *v.Usage)
		}
		if err != nil {
			if errStops(ctx, err) {
				return usage, fmt.Errorf("consolidate: dedup: %w", err)
			}
			continue
		}
		if v.Merge && v.TwinID != "" {
			actions[i] = consolidationAction{Kind: actionUpdate, ObservationID: v.TwinID, Text: v.Text, Evidence: a.Evidence}
		}
	}
	return usage, nil
}

// firstUpdates keeps the first update (or dedup merge) of each observation and drops the later ones, so
// one batch writes each observation once and no evidence is attached to a text that doesn't state it.
// dropped reports a drop: the batch's facts are then left unstamped and re-decided by the next run.
func firstUpdates(actions []consolidationAction) (out []consolidationAction, dropped bool) {
	seen := map[domain.UnitID]bool{}
	for _, a := range actions {
		if a.Kind == actionUpdate {
			if seen[a.ObservationID] {
				dropped = true
				continue
			}
			seen[a.ObservationID] = true
		}
		out = append(out, a)
	}
	return out, dropped
}

package consolidate

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
)

// ActionKind is what a consolidation decision does to a scope's observations.
type ActionKind string

// Action kinds.
const (
	// ActionUnspecified is the zero value; never applied.
	ActionUnspecified ActionKind = ""
	// ActionCreate adds a new observation.
	ActionCreate ActionKind = "create"
	// ActionUpdate rewrites an existing observation and bumps its version.
	ActionUpdate ActionKind = "update"
	// ActionNone records that the facts add nothing; the units are still stamped. A batch's facts that no
	// action cites are decided as none implicitly, so decide never emits it.
	ActionNone ActionKind = "none"
)

// ConsolidationAction is one decision of the CONSOLIDATE call for one scope.
type ConsolidationAction struct {
	// Kind is Create | Update | None.
	Kind ActionKind
	// ObservationID is the observation to rewrite, for Update.
	ObservationID port.UnitID
	// Text is the new observation text (Create and Update).
	Text string
	// Evidence is each source unit + exact quote the observation rests on.
	Evidence []domain.Evidence
	// Embedding is Text's embedding, computed before the apply transaction.
	Embedding []float32
}

// embed fills the embedding of every action that has none, in one EmbedDocuments call.
func (c *consolidator) embed(ctx context.Context, actions []ConsolidationAction) error {
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
	ctx context.Context, s port.ScopeKey, cfg domain.BankConfig, actions []ConsolidationAction,
) ([]port.Usage, error) {
	var usage []port.Usage
	for i, a := range actions {
		if a.Kind != ActionCreate {
			continue
		}
		v, err := c.d.Deduper.Check(ctx, s, cfg, a.Text, a.Embedding)
		if v.Usage != nil {
			usage = append(usage, *v.Usage)
		}
		if err != nil {
			if errStops(ctx, err) {
				return usage, err
			}
			continue
		}
		if v.Merge && v.TwinID != "" {
			actions[i] = ConsolidationAction{Kind: ActionUpdate, ObservationID: v.TwinID, Text: v.Text, Evidence: a.Evidence}
		}
	}
	return usage, nil
}

// mergeUpdates collapses updates of the same observation into one (the last text, the union of the
// evidence), so one batch writes each observation once. Creates keep their order.
func mergeUpdates(actions []ConsolidationAction) []ConsolidationAction {
	out := make([]ConsolidationAction, 0, len(actions))
	at := map[port.UnitID]int{}
	for _, a := range actions {
		if a.Kind != ActionUpdate {
			out = append(out, a)
			continue
		}
		i, ok := at[a.ObservationID]
		if !ok {
			at[a.ObservationID] = len(out)
			out = append(out, a)
			continue
		}
		prev := out[i]
		prev.Evidence = unionEvidence(prev.Evidence, a.Evidence)
		if prev.Text != a.Text {
			prev.Text, prev.Embedding = a.Text, a.Embedding
		}
		out[i] = prev
	}
	return out
}

// unionEvidence appends the units of b not already in a.
func unionEvidence(a, b []domain.Evidence) []domain.Evidence {
	out := append([]domain.Evidence(nil), a...)
	for _, e := range b {
		dup := false
		for _, x := range out {
			if x.Unit == e.Unit {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, e)
		}
	}
	return out
}

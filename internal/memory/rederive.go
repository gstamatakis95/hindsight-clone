package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/graph"
	"memd/internal/jobs"
	"memd/internal/linking"
	"memd/internal/store"
)

// rederiverDeps are the rederiver's dependencies.
type rederiverDeps struct {
	// Store opens the job's short write transaction.
	Store store.DB
	// Embedder re-embeds the unit text, outside any transaction.
	Embedder ai.Embedder
	// Linker rebuilds the unit's temporal and semantic links.
	Linker *linking.Linker
}

// rederiver is the rederive_memory job: read and embed outside any transaction, then one short write.
type rederiver struct {
	d rederiverDeps
}

// newRederiver returns the rederiver.
func newRederiver(d rederiverDeps) *rederiver {
	return &rederiver{d: d}
}

// Rederive re-embeds the job's unit, rebuilds its temporal and semantic links and enqueues consolidation.
// Entity changes are not its job: the curator resolves an edit's names and a restore's snapshot names in the
// RPC's own transaction, so a rederive keeps the unit's current attachments and an older rederive retried
// after a newer edit can never re-apply stale names. A unit that is gone (archived or
// replaced) or whose text changed after the embedding was computed is left alone: the job succeeds and the
// newer edit's rederive does the work. Running it twice is harmless.
func (r *rederiver) Rederive(ctx context.Context, j store.Job) error {
	var p jobs.RederivePayload
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return fmt.Errorf("%w: rederive_memory payload %s: %w", domain.ErrInvalidArgument, j.Payload, err)
	}
	if p.UnitID == "" {
		return fmt.Errorf("%w: rederive_memory payload %s has no unit_id", domain.ErrInvalidArgument, j.Payload)
	}
	us, err := r.d.Store.Read().GetUnits(ctx, j.Bank, []domain.UnitID{p.UnitID})
	if err != nil {
		return err
	}
	if len(us) == 0 {
		return nil // archived or replaced since: nothing to rederive
	}
	u := us[0]
	vecs, err := r.d.Embedder.EmbedDocuments(ctx, []string{u.Text})
	if err != nil {
		return err
	}
	if len(vecs) != 1 {
		return fmt.Errorf("%w: embedder returned %d vectors for 1 text", domain.ErrUnavailable, len(vecs))
	}
	err = r.d.Store.InTx(ctx, func(tx store.Tx) error {
		return r.write(ctx, tx, j.Bank, u.ID, u.Text, vecs[0])
	})
	if err != nil && ctx.Err() != nil {
		return fmt.Errorf("%w: rederive of unit %s: %w", domain.ErrCancelled, u.ID, err)
	}
	return err
}

// write is the job's transaction. It re-reads the unit and does nothing when the unit is gone or its text
// changed after embedding (the newer edit's rederive re-embeds and relinks). Writing the embedding clears
// the unit's consolidation stamps (UnitRepo.Update), so consolidation sees the unit again only once it has
// an embedding. Entity attachments stay as the RPC committed them.
func (r *rederiver) write(
	ctx context.Context, tx store.Tx, b domain.BankRef, id domain.UnitID, text string, vec []float32,
) error {
	seq, err := tx.Seq(ctx, b)
	if err != nil {
		return err
	}
	us, err := tx.GetUnits(ctx, b, []domain.UnitID{id})
	if err != nil {
		return err
	}
	if len(us) == 0 || us[0].Text != text {
		return nil
	}
	u := us[0]
	u.Embedding, u.Seq = vec, seq
	if err := tx.Units().Update(ctx, u); err != nil {
		return err
	}
	rebuilt := []graph.Kind{graph.KindTemporal, graph.KindSemantic}
	if _, err := tx.Graph().DeleteUnitLinks(ctx, id, rebuilt); err != nil {
		return err
	}
	if err := r.d.Linker.Link(ctx, tx, b, []domain.Unit{u}, nil); err != nil {
		return fmt.Errorf("relink unit %s: %w", id, err)
	}
	if _, _, err := jobs.EnqueueGraph(ctx, tx, b); err != nil {
		return err
	}
	_, _, err = jobs.EnqueueConsolidate(ctx, tx, b)
	return err
}

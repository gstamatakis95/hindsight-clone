package curate

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/retain"
	"memd/internal/port"
	"time"
)

// defaultEntityMatchThreshold is the resolver threshold when the effective config sets none.
const defaultEntityMatchThreshold = 0.6

// rederiver implements Rederiver: read and embed outside any transaction, then one short write.
type rederiver struct {
	d RederiverDeps
}

var _ Rederiver = (*rederiver)(nil)

// NewRederiver returns the rederive_memory job.
func NewRederiver(d RederiverDeps) Rederiver {
	return &rederiver{d: d}
}

// Rederive re-embeds, re-resolves and re-links the job's unit and enqueues consolidation.
func (r *rederiver) Rederive(ctx context.Context, j port.Job) error {
	var p RederivePayload
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return fmt.Errorf("%w: rederive_memory payload %s: %w", domain.ErrInvalidArgument, j.Payload, err)
	}
	if p.UnitID == "" {
		return fmt.Errorf("%w: rederive_memory payload %s has no unit_id", domain.ErrInvalidArgument, j.Payload)
	}
	us, err := r.d.Store.Read().GetUnits(ctx, j.Bank, []port.UnitID{p.UnitID})
	if err != nil {
		return err
	}
	if len(us) == 0 {
		return nil // archived or replaced since: nothing to rederive
	}
	u := us[0]
	threshold, err := r.threshold(ctx, j)
	if err != nil {
		return err
	}
	vecs, err := r.d.Embedder.EmbedDocuments(ctx, []string{u.Text})
	if err != nil {
		return err
	}
	if len(vecs) != 1 {
		return fmt.Errorf("%w: embedder returned %d vectors for 1 text", domain.ErrUnavailable, len(vecs))
	}
	in := retain.ResolveInput{
		Mentions:  mentions(p.Entities, u.Entities),
		Literal:   p.ResolveEntities != nil && !*p.ResolveEntities,
		At:        anchor(u),
		Threshold: threshold,
	}
	return r.d.Store.InTx(ctx, func(tx port.Tx) error {
		return r.write(ctx, tx, j.Bank, u.ID, u.Text, vecs[0], in)
	})
}

// threshold resolves the entity merge threshold with the job's overrides.
func (r *rederiver) threshold(ctx context.Context, j port.Job) (float64, error) {
	var ov []domain.BankConfig
	if j.Overrides != nil {
		ov = append(ov, *j.Overrides)
	}
	cfg, err := r.d.Config.Effective(ctx, j.Bank, ov...)
	if err != nil {
		return 0, err
	}
	if cfg.Retain != nil && cfg.Retain.EntityMatchThreshold != nil {
		return *cfg.Retain.EntityMatchThreshold, nil
	}
	return defaultEntityMatchThreshold, nil
}

// write is the job's transaction. It re-reads the unit and does nothing when the unit is gone or its text
// changed after embedding (a newer rederive, serialized behind this one, does the work).
func (r *rederiver) write(
	ctx context.Context, tx port.Tx, b port.BankRef, id port.UnitID, text string, vec []float32,
	in retain.ResolveInput,
) error {
	seq, err := tx.Seq(ctx, b)
	if err != nil {
		return err
	}
	us, err := tx.GetUnits(ctx, b, []port.UnitID{id})
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
	if err := r.reresolve(ctx, tx, b, id, in); err != nil {
		return err
	}
	rebuilt := []domain.LinkKind{domain.LinkTemporal, domain.LinkSemantic}
	if _, err := tx.Links().DeleteForUnit(ctx, id, rebuilt); err != nil {
		return err
	}
	if err := r.d.Linker.Link(ctx, tx, b, []domain.Unit{u}, nil); err != nil {
		return fmt.Errorf("relink unit %s: %w", id, err)
	}
	_, err = enqueueConsolidate(ctx, tx, b)
	return err
}

// reresolve replaces the unit's entity attachments with the resolution of in's mentions.
func (r *rederiver) reresolve(
	ctx context.Context, tx port.Tx, b port.BankRef, id port.UnitID, in retain.ResolveInput,
) error {
	if err := tx.Entities().Detach(ctx, id); err != nil {
		return err
	}
	if len(in.Mentions) == 0 {
		return nil
	}
	ents, err := r.d.Resolver.Resolve(ctx, tx, b, in)
	if err != nil {
		return fmt.Errorf("resolve entities of unit %s: %w", id, err)
	}
	return tx.Entities().Attach(ctx, id, ents)
}

// mentions turns the payload's names (nil = the unit's current names) into deduplicated mentions.
func mentions(payload, current []string) []domain.Mention {
	names := current
	if payload != nil {
		names = payload
	}
	seen := map[string]bool{}
	var out []domain.Mention
	for _, n := range names {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, domain.Mention{Name: n})
	}
	return out
}

// anchor is the unit's time for temporal proximity: occurrence start, else mention time; zero = unknown.
func anchor(u domain.Unit) time.Time {
	switch {
	case u.OccurredStart != nil:
		return *u.OccurredStart
	case u.MentionedAt != nil:
		return *u.MentionedAt
	default:
		return time.Time{}
	}
}

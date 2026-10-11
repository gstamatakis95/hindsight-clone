package curate

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/linking"
	"memd/internal/store"
	"time"
)

// entityChange is the entity rewrite an Update commits in its own transaction: the names to resolve and the
// resolver input (At is filled in the transaction from the unit). nil = leave the attachments alone.
type entityChange struct {
	names []string
	in    linking.ResolveInput
}

// planEntities decides, before the transaction, whether the Update rewrites the unit's entities: an edit's
// explicit names, or a restore's archive-snapshot names (graph maintenance may have pruned those entities
// while the unit was archived, so Restore alone would drop them). It reads the threshold here so the
// transaction makes no config read.
func (c *curator) planEntities(ctx context.Context, req UpdateMemoryRequest, snapshot []string) (*entityChange, error) {
	names := req.Entities
	literal := req.ResolveEntities != nil && !*req.ResolveEntities
	if names == nil {
		if snapshot == nil {
			return nil, nil
		}
		names, literal = snapshot, false
	}
	if c.d.Resolver == nil {
		return nil, fmt.Errorf("%w: no entity resolver wired for entity changes", domain.ErrFailedPrecondition)
	}
	threshold, err := c.threshold(ctx, req)
	if err != nil {
		return nil, err
	}
	return &entityChange{names: names, in: linking.ResolveInput{
		Mentions: mentions(names), Literal: literal, Threshold: threshold,
	}}, nil
}

// threshold resolves the entity merge threshold with the request overrides.
func (c *curator) threshold(ctx context.Context, req UpdateMemoryRequest) (float64, error) {
	if c.d.Config == nil {
		return linking.DefaultEntityMatchThreshold, nil
	}
	var ov []domain.BankConfig
	if req.Overrides != nil {
		ov = append(ov, *req.Overrides)
	}
	cfg, err := c.d.Config.Effective(ctx, req.Bank, ov...)
	if err != nil {
		return 0, fmt.Errorf("curate: %w", err)
	}
	if cfg.Retain != nil && cfg.Retain.EntityMatchThreshold != nil {
		return *cfg.Retain.EntityMatchThreshold, nil
	}
	return linking.DefaultEntityMatchThreshold, nil
}

// commitEntities applies change to u in tx when the request names entities or a restore brought the unit
// back (its snapshot names), and returns u with the names now attached.
func (c *curator) commitEntities(
	ctx context.Context, tx store.Tx, u domain.Unit, req UpdateMemoryRequest, restored bool, change *entityChange,
) (domain.Unit, error) {
	if change == nil || (req.Entities == nil && !restored) {
		return u, nil
	}
	names, err := c.applyEntities(ctx, tx, u, change)
	if err != nil {
		return domain.Unit{}, err
	}
	u.Entities = names
	return u, nil
}

// applyEntities replaces u's entity attachments with the resolution of ch in tx (Detach queues the old
// entities for maintenance; Resolve makes no model call) and returns the names now attached.
func (c *curator) applyEntities(ctx context.Context, tx store.Tx, u domain.Unit, ch *entityChange) ([]string, error) {
	if err := tx.Entities().Detach(ctx, u.ID); err != nil {
		return nil, err
	}
	if len(ch.in.Mentions) == 0 {
		return []string{}, nil
	}
	in := ch.in
	in.At = anchor(u)
	ents, err := c.d.Resolver.Resolve(ctx, tx, u.Bank, in)
	if err != nil {
		return nil, fmt.Errorf("resolve entities of unit %s: %w", u.ID, err)
	}
	if err := tx.Entities().Attach(ctx, u.ID, ents); err != nil {
		return nil, err
	}
	return ch.names, nil
}

// mentions turns names into deduplicated mentions.
func mentions(names []string) []domain.Mention {
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

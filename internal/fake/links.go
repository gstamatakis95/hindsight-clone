package fake

import (
	"cmp"
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
)

// linkRepo is the LinkRepo of one transaction.
type linkRepo struct{ t *tx }

var _ port.LinkRepo = linkRepo{}

// Insert stores links between live units of one bank; an existing (from, to, kind) edge keeps its place
// and takes the new weight. A link whose units are unknown, archived or of another bank is
// domain.ErrNotFound. An empty Link.Bank is taken from the units.
func (r linkRepo) Insert(_ context.Context, ls []domain.Link) error {
	st := r.t.st
	for _, l := range ls {
		from, okF := st.units[l.From]
		to, okT := st.units[l.To]
		if l.Bank.IsZero() && okF {
			l.Bank = from.u.Bank
		}
		if !okF || !okT || from.u.Bank != l.Bank || to.u.Bank != l.Bank {
			return notFound("link %s -> %s: unit", l.From, l.To)
		}
		k := linkKey{l.From, l.To, l.Kind}
		row, ok := st.links[k]
		if !ok {
			row = linkRow{order: st.next()}
			l.CreatedAt = r.t.now
		} else {
			l.CreatedAt = row.l.CreatedAt
		}
		row.l = l
		st.links[k] = row
	}
	return nil
}

// DeleteForUnit removes the links of the given kinds (empty = all) touching unit in either direction and
// returns how many it removed.
func (r linkRepo) DeleteForUnit(_ context.Context, unit port.UnitID, kinds []domain.LinkKind) (int, error) {
	n := 0
	for k := range r.t.st.links {
		if (k.from == unit || k.to == unit) && (len(kinds) == 0 || slices.Contains(kinds, k.kind)) {
			delete(r.t.st.links, k)
			n++
		}
	}
	return n, nil
}

// Count returns how many links of kind touch unit in either direction.
func (r linkRepo) Count(_ context.Context, unit port.UnitID, kind domain.LinkKind) (int, error) {
	n := 0
	for k := range r.t.st.links {
		if k.kind == kind && (k.from == unit || k.to == unit) {
			n++
		}
	}
	return n, nil
}

// Dirty returns up to limit live units queued in graph_dirty for b, oldest first.
func (r linkRepo) Dirty(_ context.Context, b port.BankRef, limit int) ([]port.UnitID, error) {
	type queued struct {
		id    port.UnitID
		order int64
	}
	var qs []queued
	for k, order := range r.t.st.dirty {
		if u, ok := r.t.st.units[k.unit]; ok && k.bank == b && u.u.Bank == b {
			qs = append(qs, queued{k.unit, order})
		}
	}
	slices.SortFunc(qs, func(x, y queued) int { return cmp.Compare(x.order, y.order) })
	qs = capped(qs, limit)
	out := make([]port.UnitID, len(qs))
	for i, q := range qs {
		out[i] = q.id
	}
	return out, nil
}

// ClearDirty removes ids from b's graph_dirty.
func (r linkRepo) ClearDirty(_ context.Context, b port.BankRef, ids []port.UnitID) error {
	for _, id := range ids {
		delete(r.t.st.dirty, bankUnitKey{b, id})
	}
	return nil
}

// evidenceRepo is the EvidenceRepo of one transaction.
type evidenceRepo struct{ t *tx }

var _ port.EvidenceRepo = evidenceRepo{}

// Add records evidence for a live observation; each cited unit must be a live unit of the same bank
// (domain.ErrNotFound otherwise). Re-adding an (observation, unit) pair is a no-op.
func (r evidenceRepo) Add(_ context.Context, obs port.UnitID, ev []domain.Evidence) error {
	st := r.t.st
	o, ok := st.units[obs]
	if !ok {
		return notFound("observation %s", obs)
	}
	for _, e := range ev {
		u, ok := st.units[e.Unit]
		if !ok || u.u.Bank != o.u.Bank {
			return notFound("evidence unit %s", e.Unit)
		}
		k := evidenceKey{obs, e.Unit}
		if _, dup := st.evidence[k]; !dup {
			st.evidence[k] = e.Quote
		}
	}
	return nil
}

// Cites reports whether an observation of scope s (same bank, same tag set) cites unit.
func (r evidenceRepo) Cites(_ context.Context, s port.ScopeKey, unit port.UnitID) (bool, error) {
	st := r.t.st
	for k := range st.evidence {
		if k.unit != unit {
			continue
		}
		o, ok := st.units[k.obs]
		if ok && o.u.Bank == s.BankRef && o.u.Type == domain.FactTypeObservation && domain.SameTagSet(o.u.Tags, s.Tags) {
			return true, nil
		}
	}
	return false, nil
}

// DeleteCiting deletes the observations of b citing any of ids and clears consolidated_at on their other
// live sources. It returns both sets, sorted.
func (r evidenceRepo) DeleteCiting(_ context.Context, b port.BankRef, ids []port.UnitID) (deleted, reset []port.UnitID, err error) {
	st := r.t.st
	doomed := map[port.UnitID]bool{}
	for k := range st.evidence {
		if o, ok := st.units[k.obs]; ok && o.u.Bank == b && slices.Contains(ids, k.unit) {
			doomed[k.obs] = true
		}
	}
	resetSet := map[port.UnitID]bool{}
	for k := range st.evidence {
		if doomed[k.obs] && !slices.Contains(ids, k.unit) && !doomed[k.unit] {
			resetSet[k.unit] = true
		}
	}
	deleted, reset = slices.Sorted(slices.Values(mapKeys(doomed))), slices.Sorted(slices.Values(mapKeys(resetSet)))
	st.removeUnits(b, deleted)
	for _, id := range reset {
		if row, ok := st.units[id]; ok {
			row.u.ConsolidatedAt, row.u.UpdatedAt = nil, r.t.now
			st.units[id] = row
		}
	}
	return deleted, reset, nil
}

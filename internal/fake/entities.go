package fake

import (
	"cmp"
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"strings"
	"unicode"
)

// candidateMinSimilarity is the trigram similarity a candidate needs, pg_trgm's default for the % operator.
const candidateMinSimilarity = 0.3

// entityRepo is the EntityRepo of one transaction.
type entityRepo struct{ t *tx }

var _ port.EntityRepo = entityRepo{}

// Candidates returns up to limit entities of b whose canonical names have a pg_trgm similarity of at least
// 0.3 to name, most similar first (ties by name).
func (r entityRepo) Candidates(
	_ context.Context, b port.BankRef, name string, limit int,
) ([]domain.EntityCandidate, error) {
	var out []domain.EntityCandidate
	for _, row := range r.t.st.entities {
		if row.e.Bank != b {
			continue
		}
		if sim := trigramSimilarity(name, row.e.CanonicalName); sim >= candidateMinSimilarity {
			out = append(out, domain.EntityCandidate{Entity: entityOut(row.e), NameSimilarity: sim})
		}
	}
	slices.SortFunc(out, func(x, y domain.EntityCandidate) int {
		return cmp.Or(cmp.Compare(y.NameSimilarity, x.NameSimilarity),
			cmp.Compare(x.Entity.CanonicalName, y.Entity.CanonicalName), cmp.Compare(x.Entity.ID, y.Entity.ID))
	})
	return capped(out, limit), nil
}

// Cooccurrence returns how many of others share at least one unit with entity.
func (r entityRepo) Cooccurrence(
	_ context.Context, b port.BankRef, entity port.EntityID, others []port.EntityID,
) (int, error) {
	if !r.t.st.entityInBank(entity, b) {
		return 0, nil
	}
	n := 0
	for _, o := range slices.Compact(slices.Sorted(slices.Values(others))) {
		if o != entity && r.t.st.cooc[newPair(entity, o)] > 0 {
			n++
		}
	}
	return n, nil
}

// Upsert inserts e (a new id when e.ID is empty) or replaces the existing entity's fields; a label entity
// with the same (kind, label value) resolves to the existing row. The bank must exist; an id owned by
// another bank is domain.ErrNotFound.
func (r entityRepo) Upsert(_ context.Context, e domain.Entity) (port.EntityID, error) {
	st := r.t.st
	if _, ok := st.banks[e.Bank]; !ok {
		return "", notFound("bank %s/%s", e.Bank.Tenant, e.Bank.Bank)
	}
	if e.ID == "" && e.IsLabel() {
		for id, row := range st.entities {
			if row.e.Bank == e.Bank && row.e.Kind == e.Kind && row.e.LabelValue == e.LabelValue {
				return id, nil
			}
		}
	}
	if e.ID == "" {
		e.ID = port.EntityID(newUUID())
	}
	row, ok := st.entities[e.ID]
	if ok && row.e.Bank != e.Bank {
		return "", notFound("entity %s", e.ID)
	}
	created, order := r.t.now, st.next()
	if ok {
		created, order = row.e.CreatedAt, row.order
	}
	e.FirstSeen, e.LastSeen = ptrCopy(e.FirstSeen), ptrCopy(e.LastSeen)
	e.CreatedAt, e.UpdatedAt = created, r.t.now
	st.entities[e.ID] = entityRow{e: e, order: order}
	return e.ID, nil
}

// Attach links a live unit to entities of its bank and bumps their co-occurrence counts and mention
// counts; re-attaching is a no-op. Unknown units or entities are domain.ErrNotFound.
func (r entityRepo) Attach(_ context.Context, unit port.UnitID, ents []port.EntityID) error {
	st := r.t.st
	u, ok := st.units[unit]
	if !ok {
		return notFound("unit %s", unit)
	}
	for _, e := range ents {
		if !st.entityInBank(e, u.u.Bank) {
			return notFound("entity %s", e)
		}
	}
	r.attach(unit, ents)
	return nil
}

// attach adds the unit_entities rows of unit that are missing and counts the new co-occurrences.
func (r entityRepo) attach(unit port.UnitID, ents []port.EntityID) {
	st := r.t.st
	var have []port.EntityID
	for k := range st.unitEntities {
		if k.unit == unit {
			have = append(have, k.entity)
		}
	}
	for _, e := range ents {
		if slices.Contains(have, e) {
			continue
		}
		for _, o := range have {
			st.cooc[newPair(e, o)]++
		}
		have = append(have, e)
		st.unitEntities[unitEntityKey{unit, e}] = struct{}{}
		row := st.entities[e]
		row.e.MentionCount++
		st.entities[e] = row
	}
}

// PendingMaintenance returns up to limit entity ids queued in entity_maintenance for b, oldest first.
func (r entityRepo) PendingMaintenance(_ context.Context, b port.BankRef, limit int) ([]port.EntityID, error) {
	type queued struct {
		id    port.EntityID
		order int64
	}
	var qs []queued
	for k, order := range r.t.st.entMaint {
		if k.bank == b {
			qs = append(qs, queued{k.entity, order})
		}
	}
	slices.SortFunc(qs, func(x, y queued) int { return cmp.Compare(x.order, y.order) })
	qs = capped(qs, limit)
	out := make([]port.EntityID, len(qs))
	for i, q := range qs {
		out[i] = q.id
	}
	return out, nil
}

// Detach removes every unit_entities row of unit and queues the detached entities in entity_maintenance.
// Co-occurrence counts are not decremented.
func (r entityRepo) Detach(_ context.Context, unit port.UnitID) error {
	st := r.t.st
	for k := range st.unitEntities {
		if k.unit != unit {
			continue
		}
		st.queueEntity(st.entities[k.entity].e.Bank, k.entity)
		delete(st.unitEntities, k)
	}
	return nil
}

// PruneOrphans deletes those of ids (in b) that no live unit references, with their co-occurrences,
// removes all of ids from entity_maintenance and returns how many entities it deleted.
func (r entityRepo) PruneOrphans(_ context.Context, b port.BankRef, ids []port.EntityID) (int, error) {
	st := r.t.st
	used := map[port.EntityID]bool{}
	for k := range st.unitEntities {
		used[k.entity] = true
	}
	n := 0
	for _, id := range ids {
		delete(st.entMaint, bankEntityKey{b, id})
		if used[id] || !st.entityInBank(id, b) {
			continue
		}
		delete(st.entities, id)
		for p := range st.cooc {
			if p.a == id || p.b == id {
				delete(st.cooc, p)
			}
		}
		n++
	}
	return n, nil
}

// entityOut returns a caller-owned copy of an entity.
func entityOut(e domain.Entity) domain.Entity {
	e.FirstSeen, e.LastSeen = ptrCopy(e.FirstSeen), ptrCopy(e.LastSeen)
	return e
}

// trigramSimilarity is pg_trgm's similarity(): the Jaccard index of the two strings' trigram sets, where
// each lowercased alphanumeric word is padded with two leading spaces and one trailing space.
func trigramSimilarity(a, b string) float64 {
	ta, tb := trigrams(a), trigrams(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	common := 0
	for g := range ta {
		if tb[g] {
			common++
		}
	}
	return float64(common) / float64(len(ta)+len(tb)-common)
}

// trigrams returns the pg_trgm trigram set of s.
func trigrams(s string) map[string]bool {
	out := map[string]bool{}
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range words {
		rs := []rune("  " + w + " ")
		for i := 0; i+3 <= len(rs); i++ {
			out[string(rs[i:i+3])] = true
		}
	}
	return out
}

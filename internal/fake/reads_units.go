package fake

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// getChunks returns b's chunks with the given ids in the order of ids; unknown, other-bank and repeated
// ids are skipped.
func getChunks(ctx context.Context, st *state, b port.BankRef, ids []port.ChunkID) ([]domain.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxErr(err)
	}
	byID := map[port.ChunkID]domain.Chunk{}
	for k, c := range st.chunks {
		if k.bank == b {
			byID[c.ID] = c
		}
	}
	out := []domain.Chunk{}
	seen := map[port.ChunkID]bool{}
	for _, id := range ids {
		if c, ok := byID[id]; ok && !seen[id] {
			seen[id] = true
			out = append(out, c)
		}
	}
	return out, nil
}

// entityStates returns the entities attached to any of the given live units of b, ordered by entity id.
// Each one's ObservationIDs are b's live observations attached to it or citing a live fact attached to it.
func entityStates(ctx context.Context, st *state, b port.BankRef, units []port.UnitID) ([]domain.EntityState, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxErr(err)
	}
	ents := map[port.EntityID]bool{}
	for k := range st.unitEntities {
		if row, ok := st.units[k.unit]; ok && row.u.Bank == b && slices.Contains(units, k.unit) {
			ents[k.entity] = true
		}
	}
	out := []domain.EntityState{}
	for _, e := range slices.Sorted(maps.Keys(ents)) {
		out = append(out, domain.EntityState{
			EntityID: e, CanonicalName: st.entities[e].e.CanonicalName, ObservationIDs: st.entityObservations(b, e),
		})
	}
	return out, nil
}

// entityObservations returns the sorted live observations of b attached to entity e or citing a live fact
// attached to it.
func (s *state) entityObservations(b port.BankRef, e port.EntityID) []port.UnitID {
	attached := map[port.UnitID]bool{}
	for k := range s.unitEntities {
		if k.entity == e {
			attached[k.unit] = true
		}
	}
	obs := map[port.UnitID]bool{}
	for id := range attached {
		if row, ok := s.units[id]; ok && row.u.Bank == b && row.u.Type == domain.FactTypeObservation {
			obs[id] = true
		}
	}
	for k := range s.evidence {
		if !attached[k.unit] {
			continue
		}
		f, okF := s.units[k.unit]
		o, okO := s.units[k.obs]
		if okF && okO && isFactType(f.u.Type) && o.u.Bank == b && o.u.Type == domain.FactTypeObservation {
			obs[k.obs] = true
		}
	}
	out := slices.Sorted(maps.Keys(obs))
	if out == nil {
		out = []port.UnitID{}
	}
	return out
}

// getUnit returns b's live unit id (State valid, Stale computed for observations) or else its archived unit
// (State invalidated); anything else, or an unknown or tombstoned bank, is domain.ErrNotFound.
func getUnit(ctx context.Context, st *state, b port.BankRef, id port.UnitID) (domain.Unit, error) {
	if err := liveBank(ctx, st, b); err != nil {
		return domain.Unit{}, err
	}
	if row, ok := st.units[id]; ok && row.u.Bank == b {
		return st.liveOut(row), nil
	}
	if a, ok := st.archive[id]; ok && a.u.Bank == b {
		return archivedOut(a), nil
	}
	return domain.Unit{}, notFound("unit %s", id)
}

// liveOut returns a caller-owned copy of a live unit with Entities, SourceIDs, State and Stale filled.
func (s *state) liveOut(row unitRow) domain.Unit {
	u := unitsOut(s, []unitRow{row})[0]
	u.State = domain.MemoryStateValid
	u.Stale = u.Type == domain.FactTypeObservation && s.countUnconsolidated(u.Bank, u.Tags) > 0
	return u
}

// archivedOut returns the archive's view of a unit: the columns memory_units_archive has, the snapshot
// entity names, State invalidated and UpdatedAt the archive time. SourceIDs, ProofCount, Version,
// Embedding and the consolidation stamps are not archived, so they are empty.
func archivedOut(a archiveRow) domain.Unit {
	u := cloneUnit(a.u)
	u.Entities = slices.Clone(a.names)
	u.SourceIDs, u.Embedding, u.ProofCount, u.Version = nil, nil, 0, 0
	u.ConsolidatedAt, u.ConsolidationFailedAt = nil, nil
	u.State, u.Stale, u.UpdatedAt = domain.MemoryStateInvalidated, false, a.at
	return u
}

// listUnits returns up to q.Limit (> 0) units of q.Bank matching q, ordered by (created_at, id)
// descending and strictly after q.After, each filled like getUnit.
func listUnits(ctx context.Context, st *state, q port.UnitQuery) ([]domain.Unit, error) {
	if err := checkUnitQuery(q); err != nil {
		return nil, err
	}
	if err := liveBank(ctx, st, q.Bank); err != nil {
		return nil, err
	}
	var out []domain.Unit
	if q.State != domain.MemoryStateInvalidated {
		for _, row := range st.units {
			if row.u.Bank == q.Bank && st.unitMatches(row.u, row.u.UpdatedAt, q) {
				out = append(out, st.liveOut(row))
			}
		}
	}
	if q.State != domain.MemoryStateValid && q.EntityID == "" { // archived units never match an entity
		for _, a := range st.archive {
			if a.u.Bank == q.Bank && st.unitMatches(a.u, a.at, q) {
				out = append(out, archivedOut(a))
			}
		}
	}
	slices.SortFunc(out, func(x, y domain.Unit) int {
		return cmp.Or(y.CreatedAt.Compare(x.CreatedAt), cmp.Compare(y.ID, x.ID))
	})
	if out == nil {
		out = []domain.Unit{}
	}
	return capped(out, q.Limit), nil
}

// checkUnitQuery rejects a non-positive limit, an unknown State or TimeField and a cursor whose id is not a
// UUID with domain.ErrInvalidArgument (decisions 1500 and 1530).
func checkUnitQuery(q port.UnitQuery) error {
	if err := checkLimit("ListUnits", q.Limit); err != nil {
		return err
	}
	switch q.State {
	case domain.MemoryStateUnspecified, domain.MemoryStateValid, domain.MemoryStateInvalidated:
	default:
		return fmt.Errorf("%w: ListUnits state %q", domain.ErrInvalidArgument, q.State)
	}
	switch q.TimeField {
	case domain.TimeFieldUnspecified, domain.TimeFieldCreatedAt, domain.TimeFieldUpdatedAt,
		domain.TimeFieldMentionedAt, domain.TimeFieldOccurredStart, domain.TimeFieldOccurredEnd:
	default:
		return fmt.Errorf("%w: ListUnits time field %q", domain.ErrInvalidArgument, q.TimeField)
	}
	if q.After != nil {
		if _, err := uuid.Parse(string(q.After.ID)); err != nil {
			return fmt.Errorf("%w: ListUnits cursor id %q", domain.ErrInvalidArgument, q.After.ID)
		}
	}
	return nil
}

// unitMatches applies q's filters (other than bank and state) to u, whose updated_at is updated.
func (s *state) unitMatches(u domain.Unit, updated time.Time, q port.UnitQuery) bool {
	return (len(q.Types) == 0 || slices.Contains(q.Types, u.Type)) &&
		(q.Q == "" || containsFold(u.Text, q.Q)) &&
		(q.DocumentID == "" || u.DocumentID == q.DocumentID) &&
		(q.EntityID == "" || s.attached(u.ID, q.EntityID)) &&
		inTimeRange(unitTime(u, updated, q.TimeField), q.Start, q.End) &&
		tagsAndCursor(u, q)
}

// tagsAndCursor applies q's tag filter (only when q.Tags is non-empty) and keyset cursor to u.
func tagsAndCursor(u domain.Unit, q port.UnitQuery) bool {
	return (len(q.Tags) == 0 || domain.MatchTags(u.Tags, q.Tags, q.TagsMatch)) &&
		(q.After == nil || before(u.CreatedAt, string(u.ID), q.After.CreatedAt, string(q.After.ID)))
}

// containsFold reports whether sub is a case-insensitive substring of s (ILIKE with every character
// literal).
func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// attached reports whether a unit_entities row links unit and entity.
func (s *state) attached(unit port.UnitID, entity port.EntityID) bool {
	_, ok := s.unitEntities[unitEntityKey{unit, entity}]
	return ok
}

// unitTime returns the unit's timestamp named by f (nil when that column is null).
func unitTime(u domain.Unit, updated time.Time, f domain.TimeField) *time.Time {
	switch f {
	case domain.TimeFieldUpdatedAt:
		return &updated
	case domain.TimeFieldMentionedAt:
		return u.MentionedAt
	case domain.TimeFieldOccurredStart:
		return u.OccurredStart
	case domain.TimeFieldOccurredEnd:
		return u.OccurredEnd
	default:
		return &u.CreatedAt
	}
}

// inTimeRange reports whether v is in [start, end); a nil bound does not filter and a nil v never matches
// a set bound.
func inTimeRange(v, start, end *time.Time) bool {
	if start == nil && end == nil {
		return true
	}
	return v != nil && (start == nil || !v.Before(*start)) && (end == nil || v.Before(*end))
}

// before reports whether (at, id) sorts strictly before (cursorAt, cursorID), comparing ids in byte order:
// the keyset condition of a newest-first listing.
func before(at time.Time, id string, cursorAt time.Time, cursorID string) bool {
	return cmp.Or(at.Compare(cursorAt), cmp.Compare(id, cursorID)) < 0
}

// observationHistory returns b's observation_versions rows of id, ordered by version and then time, both
// descending. No rows, or an unknown or tombstoned bank, is domain.ErrNotFound.
func observationHistory(
	ctx context.Context, st *state, b port.BankRef, id port.UnitID,
) ([]domain.ObservationVersion, error) {
	if err := liveBank(ctx, st, b); err != nil {
		return nil, err
	}
	var out []domain.ObservationVersion
	for _, row := range st.obsVersions {
		if row.bank == b && row.obs == id {
			v := row.v
			v.SourceIDs = slices.Clone(v.SourceIDs)
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil, notFound("observation history of %s", id)
	}
	slices.SortStableFunc(out, func(x, y domain.ObservationVersion) int {
		return cmp.Or(cmp.Compare(y.Version, x.Version), y.At.Compare(x.At))
	})
	return out, nil
}

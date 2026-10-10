package fake

import (
	"cmp"
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"time"
)

// unitRepo is the UnitRepo of one transaction.
type unitRepo struct{ t *tx }

var _ port.UnitRepo = unitRepo{}

// Insert stores new units exactly as given (a zero CreatedAt becomes the transaction time). The bank and
// a non-empty document must exist (domain.ErrNotFound otherwise, like the foreign keys); a live id that is
// already taken is domain.ErrAlreadyExists.
func (r unitRepo) Insert(_ context.Context, us []domain.Unit) error {
	for _, u := range us {
		if err := r.insert(u); err != nil {
			return err
		}
	}
	return nil
}

// insert stores one new unit.
func (r unitRepo) insert(u domain.Unit) error {
	st := r.t.st
	if u.ID == "" {
		u.ID = port.UnitID(newUUID())
	}
	if _, ok := st.units[u.ID]; ok {
		return fmt.Errorf("%w: unit %s", domain.ErrAlreadyExists, u.ID)
	}
	if _, ok := st.banks[u.Bank]; !ok {
		return notFound("bank %s/%s of unit %s", u.Bank.Tenant, u.Bank.Bank, u.ID)
	}
	if u.DocumentID != "" {
		if _, ok := st.documents[docKey{u.Bank, u.DocumentID}]; !ok {
			return notFound("document %s of unit %s", u.DocumentID, u.ID)
		}
	}
	if err := validUnit(u); err != nil {
		return err
	}
	u = cloneUnit(u)
	if u.CreatedAt.IsZero() {
		u.CreatedAt = r.t.now
	}
	u.UpdatedAt, u.State, u.Entities, u.SourceIDs = r.t.now, domain.MemoryStateValid, nil, nil
	st.units[u.ID] = unitRow{u: u, order: st.next()}
	return nil
}

// UpsertObservation inserts the observation or replaces the text, proof count, embedding, version and seq
// of the existing one with the same id.
func (r unitRepo) UpsertObservation(_ context.Context, o domain.Unit) error {
	if o.Type != domain.FactTypeObservation {
		return fmt.Errorf("%w: UpsertObservation of a %q unit", domain.ErrInvalidArgument, o.Type)
	}
	row, ok := r.t.st.units[o.ID]
	if !ok {
		return r.insert(o)
	}
	if row.u.Bank != o.Bank {
		return notFound("observation %s in bank %s/%s", o.ID, o.Bank.Tenant, o.Bank.Bank)
	}
	if row.u.Type != domain.FactTypeObservation {
		return fmt.Errorf("%w: unit %s is a fact, not an observation", domain.ErrInvalidArgument, o.ID)
	}
	if err := checkDims(o); err != nil {
		return err
	}
	row.u.Text, row.u.ProofCount, row.u.Version, row.u.Seq = o.Text, o.ProofCount, o.Version, o.Seq
	row.u.Embedding = slices.Clone(o.Embedding)
	row.u.UpdatedAt = r.t.now
	r.t.st.units[o.ID] = row
	return nil
}

// RecordObservationVersion appends one observation_versions row.
func (r unitRepo) RecordObservationVersion(
	_ context.Context, b port.BankRef, obs port.UnitID, v domain.ObservationVersion,
) error {
	if !r.t.st.unitInBank(obs, b) {
		return notFound("observation %s", obs)
	}
	v.SourceIDs = slices.Clone(v.SourceIDs)
	if v.At.IsZero() {
		v.At = r.t.now
	}
	r.t.st.obsVersions = append(r.t.st.obsVersions, obsVersionRow{bank: b, obs: obs, v: v})
	return nil
}

// Update replaces a live fact's curated fields and embedding and clears its consolidation stamps.
func (r unitRepo) Update(_ context.Context, u domain.Unit) error {
	row, ok := r.t.st.units[u.ID]
	if !ok || row.u.Bank != u.Bank {
		return notFound("unit %s", u.ID)
	}
	if row.u.Type == domain.FactTypeObservation || !isFactType(u.Type) {
		return fmt.Errorf("%w: Update edits facts only (unit %s is %q, new type %q)", domain.ErrInvalidArgument,
			u.ID, row.u.Type, u.Type)
	}
	if err := checkDims(u); err != nil {
		return err
	}
	c := cloneUnit(u)
	row.u.Text, row.u.Context, row.u.Type, row.u.Timeless = c.Text, c.Context, c.Type, c.Timeless
	row.u.OccurredStart, row.u.OccurredEnd = c.OccurredStart, c.OccurredEnd
	row.u.EditedAt, row.u.Seq, row.u.Embedding = c.EditedAt, c.Seq, c.Embedding
	row.u.ConsolidatedAt, row.u.ConsolidationFailedAt = nil, nil
	row.u.UpdatedAt = r.t.now
	r.t.st.units[u.ID] = row
	return nil
}

// SetDocumentTags rewrites the tags and seq of the document's live units, clears their consolidation
// stamps and returns their ids.
func (r unitRepo) SetDocumentTags(
	_ context.Context, b port.BankRef, doc port.DocumentID, tags []string, seq int64,
) ([]port.UnitID, error) {
	if err := checkTags(tags); err != nil {
		return nil, err
	}
	var ids []port.UnitID
	for id, row := range r.t.st.units {
		if row.u.Bank != b || row.u.DocumentID != doc {
			continue
		}
		row.u.Tags, row.u.Seq = slices.Clone(tags), seq
		row.u.ConsolidatedAt, row.u.ConsolidationFailedAt = nil, nil
		row.u.UpdatedAt = r.t.now
		r.t.st.units[id] = row
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

// AppendAudit writes one memory_audit row.
func (r unitRepo) AppendAudit(_ context.Context, a domain.AuditEntry) error {
	a.Before, a.After = slices.Clone(a.Before), slices.Clone(a.After)
	if a.At.IsZero() {
		a.At = r.t.now
	}
	r.t.st.audit = append(r.t.st.audit, a)
	return nil
}

// Unconsolidated returns the bank's oldest unstamped facts (created_at, then id), optionally only those
// whose tags contain every tag of one of scopes.
func (r unitRepo) Unconsolidated(
	_ context.Context, b port.BankRef, scopes []port.ScopeKey, limit int,
) ([]domain.Unit, error) {
	var out []unitRow
	for _, row := range r.t.st.units {
		u := row.u
		if u.Bank != b || u.Type == domain.FactTypeObservation || u.ConsolidatedAt != nil ||
			u.ConsolidationFailedAt != nil || !inAnyScope(u.Tags, scopes) {
			continue
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(x, y unitRow) int {
		return cmp.Or(x.u.CreatedAt.Compare(y.u.CreatedAt), cmp.Compare(x.u.ID, y.u.ID))
	})
	return unitsOut(r.t.st, capped(out, limit)), nil
}

// inAnyScope reports whether tags contain every tag of at least one scope; no scopes = no filter.
func inAnyScope(tags []string, scopes []port.ScopeKey) bool {
	if len(scopes) == 0 {
		return true
	}
	have := domain.NormalizeTags(tags)
	for _, s := range scopes {
		if containsAll(have, domain.NormalizeTags(s.Tags)) {
			return true
		}
	}
	return false
}

// containsAll reports whether every element of want is in have.
func containsAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

// MarkConsolidated stamps consolidated_at on the given live units.
func (r unitRepo) MarkConsolidated(_ context.Context, ids []port.UnitID, at time.Time) error {
	r.stamp(ids, func(u *domain.Unit) { u.ConsolidatedAt = ptrCopy(&at) })
	return nil
}

// MarkConsolidationFailed stamps consolidation_failed_at on the given live units.
func (r unitRepo) MarkConsolidationFailed(_ context.Context, ids []port.UnitID, at time.Time) error {
	r.stamp(ids, func(u *domain.Unit) { u.ConsolidationFailedAt = ptrCopy(&at) })
	return nil
}

// stamp applies set to every live unit in ids.
func (r unitRepo) stamp(ids []port.UnitID, set func(*domain.Unit)) {
	for _, id := range ids {
		if row, ok := r.t.st.units[id]; ok {
			set(&row.u)
			row.u.UpdatedAt = r.t.now
			r.t.st.units[id] = row
		}
	}
}

// DeleteByDocument removes the document's live units with their links, entity rows and evidence, queues
// their surviving link neighbours in graph_dirty and their entities in entity_maintenance, and returns the
// removed ids.
func (r unitRepo) DeleteByDocument(_ context.Context, b port.BankRef, doc port.DocumentID) ([]port.UnitID, error) {
	var ids []port.UnitID
	for id, row := range r.t.st.units {
		if row.u.Bank == b && row.u.DocumentID == doc {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	r.t.st.removeUnits(b, ids)
	return ids, nil
}

// Archive moves a live unit to the archive with snapshots of its entities and causal links, then removes
// it like DeleteByDocument does.
func (r unitRepo) Archive(_ context.Context, b port.BankRef, id port.UnitID, reason string) error {
	st := r.t.st
	row, ok := st.units[id]
	if !ok || row.u.Bank != b {
		return notFound("unit %s", id)
	}
	a := archiveRow{u: row.u, reason: reason, at: r.t.now}
	a.u.State, a.u.Embedding = domain.MemoryStateInvalidated, nil
	for k := range st.unitEntities {
		if k.unit == id {
			a.entities = append(a.entities, k.entity)
		}
	}
	slices.Sort(a.entities)
	for k, l := range st.links {
		if k.kind == domain.LinkCausal && (k.from == id || k.to == id) {
			a.links = append(a.links, l.l)
		}
	}
	st.archive[id] = a
	st.removeUnits(b, []port.UnitID{id})
	return nil
}

// Restore moves an archived unit back under the transaction's change seq (the caller must have called
// Tx.Seq for the bank; domain.ErrFailedPrecondition otherwise) and
// reattaches the snapshot entities and causal links whose partners still exist. The embedding stays
// empty and the consolidation stamps unset.
func (r unitRepo) Restore(_ context.Context, b port.BankRef, id port.UnitID) error {
	st := r.t.st
	a, ok := st.archive[id]
	if !ok || a.u.Bank != b {
		return notFound("archived unit %s", id)
	}
	seq, ok := r.t.seqs[b]
	if !ok {
		return precondition("Restore of unit %s: call Tx.Seq for the bank first", id)
	}
	u := a.u
	u.State, u.Seq, u.UpdatedAt = domain.MemoryStateValid, seq, r.t.now
	u.ConsolidatedAt, u.ConsolidationFailedAt = nil, nil
	delete(st.archive, id)
	st.units[id] = unitRow{u: u, order: st.next()}
	ents := entityRepo(r)
	var keep []port.EntityID
	for _, e := range a.entities {
		if _, ok := st.entities[e]; ok {
			keep = append(keep, e)
		}
	}
	ents.attach(id, keep)
	for _, l := range a.links {
		if _, ok := st.units[l.From]; !ok {
			continue
		}
		if _, ok := st.units[l.To]; ok {
			st.links[linkKey{l.From, l.To, l.Kind}] = linkRow{l: l, order: st.next()}
		}
	}
	return nil
}

// removeUnits deletes the given live units of b and everything that references them (links, entity rows,
// evidence, graph_dirty rows), queueing their surviving link neighbours in graph_dirty and their entities
// in entity_maintenance.
func (s *state) removeUnits(b port.BankRef, ids []port.UnitID) {
	gone := map[port.UnitID]bool{}
	for _, id := range ids {
		gone[id] = true
	}
	for k := range s.links {
		if !gone[k.from] && !gone[k.to] {
			continue
		}
		for _, n := range []port.UnitID{k.from, k.to} {
			if !gone[n] {
				s.markDirty(b, n)
			}
		}
		delete(s.links, k)
	}
	for k := range s.unitEntities {
		if gone[k.unit] {
			s.queueEntity(b, k.entity)
			delete(s.unitEntities, k)
		}
	}
	for k := range s.evidence {
		if gone[k.obs] || gone[k.unit] {
			delete(s.evidence, k)
		}
	}
	for id := range gone {
		delete(s.units, id)
		delete(s.dirty, bankUnitKey{b, id})
	}
}

// markDirty queues a unit in graph_dirty (keeping its first queue position).
func (s *state) markDirty(b port.BankRef, id port.UnitID) {
	k := bankUnitKey{b, id}
	if _, ok := s.dirty[k]; !ok {
		s.dirty[k] = s.next()
	}
}

// queueEntity queues an entity in entity_maintenance (keeping its first queue position).
func (s *state) queueEntity(b port.BankRef, id port.EntityID) {
	k := bankEntityKey{b, id}
	if _, ok := s.entMaint[k]; !ok {
		s.entMaint[k] = s.next()
	}
}

// getUnits returns the live units of b with the given ids, in the order of ids, with Entities and
// SourceIDs filled.
func getUnits(ctx context.Context, st *state, b port.BankRef, ids []port.UnitID) ([]domain.Unit, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxErr(err)
	}
	var rows []unitRow
	for _, id := range ids {
		if row, ok := st.units[id]; ok && row.u.Bank == b {
			rows = append(rows, row)
		}
	}
	return unitsOut(st, rows), nil
}

// unitsOut returns caller-owned copies of rows with the reader-filled fields set.
func unitsOut(st *state, rows []unitRow) []domain.Unit {
	if len(rows) == 0 {
		return nil
	}
	out := make([]domain.Unit, len(rows))
	for i, row := range rows {
		u := cloneUnit(row.u)
		u.Entities, u.SourceIDs = st.entityNames(u.ID), st.sources(u.ID)
		out[i] = u
	}
	return out
}

// entityNames returns the sorted canonical names of the unit's entities.
func (s *state) entityNames(id port.UnitID) []string {
	var names []string
	for k := range s.unitEntities {
		if k.unit == id {
			names = append(names, s.entities[k.entity].e.CanonicalName)
		}
	}
	slices.Sort(names)
	return names
}

// sources returns the sorted ids of the facts an observation cites.
func (s *state) sources(obs port.UnitID) []port.UnitID {
	var ids []port.UnitID
	for k := range s.evidence {
		if k.obs == obs {
			ids = append(ids, k.unit)
		}
	}
	slices.Sort(ids)
	return ids
}

// checkDims rejects an embedding that is set but not domain.EmbeddingDim long (the vector(768) column).
func checkDims(u domain.Unit) error {
	if len(u.Embedding) != 0 && len(u.Embedding) != domain.EmbeddingDim {
		return fmt.Errorf("%w: unit %s embedding has %d dims, want %d", domain.ErrInvalidArgument, u.ID,
			len(u.Embedding), domain.EmbeddingDim)
	}
	return nil
}

// validUnit checks a unit about to be stored: a known fact type, normalized tags, a 768-dim embedding.
func validUnit(u domain.Unit) error {
	if !isFactType(u.Type) && u.Type != domain.FactTypeObservation {
		return fmt.Errorf("%w: unit %s has fact type %q", domain.ErrInvalidArgument, u.ID, u.Type)
	}
	if err := checkTags(u.Tags); err != nil {
		return err
	}
	return checkDims(u)
}

// isFactType reports whether t is world or experience.
func isFactType(t domain.FactType) bool {
	return t == domain.FactTypeWorld || t == domain.FactTypeExperience
}

// checkTags rejects tags that are not already in domain.NormalizeTags form (the domain types say stored
// tags are normalized; writers normalize).
func checkTags(tags []string) error {
	if !slices.Equal(tags, domain.NormalizeTags(tags)) {
		return fmt.Errorf("%w: tags %q are not normalized (domain.NormalizeTags)", domain.ErrInvalidArgument, tags)
	}
	return nil
}

// capped returns the first limit elements of xs; limit <= 0 means no cap.
func capped[T any](xs []T, limit int) []T {
	if limit > 0 && len(xs) > limit {
		return xs[:limit]
	}
	return xs
}

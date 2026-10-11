package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"slices"
	"time"
)

// curator commits edits (UpdateMemory), invalidations (InvalidateMemory) and restores (RestoreMemory), each
// in one short transaction.
type curator struct {
	d curatorDeps
}

// newCurator returns the curator.
func newCurator(d curatorDeps) *curator {
	return &curator{d: d}
}

// Update validates req and commits it in one transaction: an invalidate, a restore or an edit. It rejects
// observations, writes an audit row and returns the rederive operation, read back in the same transaction (nil
// for an invalidate, and for a restore of a live unit).
func (c *curator) Update(ctx context.Context, req curation) (domain.Unit, *domain.Operation, error) {
	if err := validateCuration(req); err != nil {
		return domain.Unit{}, nil, err
	}
	names, err := c.snapshotNames(ctx, req)
	if err != nil {
		return domain.Unit{}, nil, err
	}
	change, err := c.planEntities(ctx, req, names)
	if err != nil {
		return domain.Unit{}, nil, err
	}
	var (
		out domain.Unit
		op  *domain.Operation
	)
	err = c.d.Store.InTx(ctx, func(tx store.Tx) error {
		out, op = domain.Unit{}, nil
		var err error
		if req.act == actionInvalidate {
			out, err = c.invalidate(ctx, tx, req)
			return err
		}
		var id domain.OperationID
		if out, id, err = c.restoreOrEdit(ctx, tx, req, change); err != nil || id == "" {
			return err
		}
		got, err := tx.Queue().Get(ctx, req.Bank, id)
		if err != nil {
			return err
		}
		op = &got
		return nil
	})
	if err != nil {
		return domain.Unit{}, nil, fmt.Errorf("update memory %s: %w", req.ID, err)
	}
	return out, op, nil
}

// validateCuration rejects malformed requests before any transaction: a missing id and, for an edit, an empty
// or unknown field list, an empty text, or a fact type other than world or experience.
func validateCuration(req curation) error {
	if req.ID == "" {
		return domain.InvalidField("memory_id", "memory id is required")
	}
	if req.act != actionEdit {
		return nil
	}
	if !req.hasEdits() {
		return domain.InvalidField("update_mask", "nothing to update")
	}
	for _, f := range req.Fields {
		if !slices.Contains(editFields, f) {
			return domain.InvalidField("update_mask", "%q cannot be updated", f)
		}
	}
	switch {
	case req.edits(EditFactType) && req.Edit.FactType != domain.FactTypeWorld &&
		req.Edit.FactType != domain.FactTypeExperience:
		return domain.InvalidField("memory.fact_type", "fact type can only change between world and experience")
	case req.edits(EditText) && req.Edit.Text == "":
		return domain.InvalidField("memory.text", "text can't be empty")
	}
	return nil
}

// liveFact reads a live fact in tx; an absent unit is domain.ErrNotFound, an observation is rejected.
func liveFact(ctx context.Context, tx store.Tx, b domain.BankRef, id domain.UnitID) (domain.Unit, error) {
	us, err := tx.GetUnits(ctx, b, []domain.UnitID{id})
	if err != nil {
		return domain.Unit{}, err
	}
	if len(us) == 0 {
		return domain.Unit{}, fmt.Errorf("%w: memory %s", domain.ErrNotFound, id)
	}
	if us[0].Type == domain.FactTypeObservation {
		return domain.Unit{}, fmt.Errorf("%w: memory %s", domain.ErrMemoryIsObservation, id)
	}
	return us[0], nil
}

// invalidate archives a fact and does every in-tx effect of the Curation table: citing observations
// deleted (before the archive, which drops the evidence rows) and their co-sources reset, audit row,
// event, consolidate, graph_maintenance and refreshes of the mental models whose source_ids cite the unit or
// one of the deleted observations.
func (c *curator) invalidate(ctx context.Context, tx store.Tx, req curation) (domain.Unit, error) {
	seq, err := tx.Seq(ctx, req.Bank)
	if err != nil {
		return domain.Unit{}, err
	}
	u, err := liveFact(ctx, tx, req.Bank, req.ID)
	if err != nil {
		return domain.Unit{}, err
	}
	deleted, _, err := tx.Evidence().DeleteCiting(ctx, req.Bank, []domain.UnitID{u.ID})
	if err != nil {
		return domain.Unit{}, err
	}
	if err := tx.Units().Archive(ctx, req.Bank, u.ID, req.Reason); err != nil {
		return domain.Unit{}, err
	}
	if err := c.audit(ctx, tx, u, domain.CurationInvalidate, req.Reason, snapshotOf(u), nil, ""); err != nil {
		return domain.Unit{}, err
	}
	if _, _, err := jobs.EnqueueConsolidate(ctx, tx, req.Bank); err != nil {
		return domain.Unit{}, err
	}
	if _, _, err := jobs.EnqueueGraph(ctx, tx, req.Bank); err != nil {
		return domain.Unit{}, err
	}
	if err := c.refreshCiting(ctx, tx, req.Bank, append([]domain.UnitID{u.ID}, deleted...)); err != nil {
		return domain.Unit{}, err
	}
	if err := c.event(ctx, tx, req.Bank, seq, u.ID, ""); err != nil {
		return domain.Unit{}, err
	}
	u.State, u.Embedding = domain.MemoryStateInvalidated, nil
	return u, nil
}

// refreshCiting queues, in tx, a refresh of every mental model whose source_ids cite any of ids: the curated
// fact and the observations deleted because they cited it. The models are read in tx, after the deletes.
func (c *curator) refreshCiting(ctx context.Context, tx store.Tx, b domain.BankRef, ids []domain.UnitID) error {
	if len(ids) == 0 {
		return nil
	}
	models, err := tx.ModelsCiting(ctx, b, ids)
	if err != nil {
		return fmt.Errorf("find models citing %v: %w", ids, err)
	}
	for _, m := range models {
		if _, err := jobs.QueueModelRefresh(ctx, tx, b, m, c.d.Clock.Now()); err != nil {
			return fmt.Errorf("queue refresh of model %s: %w", m, err)
		}
	}
	return nil
}

// restoreOrEdit restores an archived unit (a restore) or applies the edits (an edit), commits entity changes
// (an edit's names, or a restore's snapshot names) and enqueues the rederive, which only re-embeds and
// relinks. A restore of a live unit writes nothing.
func (c *curator) restoreOrEdit(
	ctx context.Context, tx store.Tx, req curation, change *entityChange,
) (domain.Unit, domain.OperationID, error) {
	seq, err := tx.Seq(ctx, req.Bank)
	if err != nil {
		return domain.Unit{}, "", err
	}
	restored := false
	if req.act == actionRestore {
		if restored, err = c.restoreIfArchived(ctx, tx, req.Bank, req.ID); err != nil {
			return domain.Unit{}, "", err
		}
	}
	u, err := liveFact(ctx, tx, req.Bank, req.ID)
	if err != nil {
		return domain.Unit{}, "", err
	}
	if !restored && !req.hasEdits() {
		return u, "", nil
	}
	before := snapshotOf(u)
	if req.hasEdits() {
		if u, err = c.applyEdit(ctx, tx, u, req, seq); err != nil {
			return domain.Unit{}, "", err
		}
	}
	if u, err = c.commitEntities(ctx, tx, u, req, restored, change); err != nil {
		return domain.Unit{}, "", err
	}
	op, _, err := jobs.EnqueueRederive(ctx, tx, u, req.Overrides)
	if err != nil {
		return domain.Unit{}, "", err
	}
	if err := c.auditRestoreEdit(ctx, tx, u, req, restored, before, op); err != nil {
		return domain.Unit{}, "", err
	}
	if err := c.event(ctx, tx, req.Bank, seq, u.ID, op); err != nil {
		return domain.Unit{}, "", err
	}
	return u, op, nil
}

// snapshotNames reads the entity names of the archived unit a restore brings back, before the restore. Graph
// maintenance may have pruned those entities while the unit was archived (Restore then skips them), so the
// restore's own transaction re-resolves the names and re-creates them. It returns nil when no restore is
// asked for or the unit is not archived. For an archived unit, GetUnit returns the
// names of its archive snapshot, even when the entity rows were pruned since.
func (c *curator) snapshotNames(ctx context.Context, req curation) ([]string, error) {
	if req.act != actionRestore {
		return nil, nil
	}
	u, err := c.d.Store.Read().GetUnit(ctx, req.Bank, req.ID)

	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read archived unit %s: %w", req.ID, err)
	}
	if u.State != domain.MemoryStateInvalidated || len(u.Entities) == 0 {
		return nil, nil
	}
	return slices.Clone(u.Entities), nil
}

// restoreIfArchived moves an archived unit back; a unit that is not archived reports false (the caller
// then finds it live or not at all). The restored unit is stamped consolidated so consolidation never
// decides on it while its embedding is empty; the rederive's write clears the stamp.
func (c *curator) restoreIfArchived(
	ctx context.Context, tx store.Tx, b domain.BankRef, id domain.UnitID,
) (bool, error) {
	err := tx.Units().Restore(ctx, b, id)
	switch {
	case err == nil:
		return true, tx.Units().MarkConsolidated(ctx, []domain.UnitID{id}, c.d.Clock.Now())
	case errors.Is(err, domain.ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}

// applyEdit writes the new curated values (embedding cleared until rederive), keeps the unit stamped
// consolidated until the rederive writes its new embedding, deletes the observations citing
// the unit, resetting their co-sources, queues graph maintenance for the removed observations and refreshes
// of the mental models citing them.
func (c *curator) applyEdit(
	ctx context.Context, tx store.Tx, u domain.Unit, req curation, seq int64,
) (domain.Unit, error) {
	u = editedValues(u, req)
	now := c.d.Clock.Now()
	u.EditedAt, u.Embedding, u.Seq = &now, nil, seq
	if err := tx.Units().Update(ctx, u); err != nil {
		return domain.Unit{}, err
	}
	if err := tx.Units().MarkConsolidated(ctx, []domain.UnitID{u.ID}, now); err != nil {
		return domain.Unit{}, err
	}
	deleted, _, err := tx.Evidence().DeleteCiting(ctx, u.Bank, []domain.UnitID{u.ID})
	if err != nil {
		return domain.Unit{}, err
	}
	if _, _, err := jobs.EnqueueGraph(ctx, tx, u.Bank); err != nil {
		return domain.Unit{}, err
	}
	if err := c.refreshCiting(ctx, tx, u.Bank, deleted); err != nil {
		return domain.Unit{}, err
	}
	if req.edits(EditEntities) {
		u.Entities = append([]string{}, req.Edit.Entities...)
	}
	u.ConsolidatedAt, u.ConsolidationFailedAt = &now, nil
	return u, nil
}

// editedValues returns u with the edited fields of req applied. Setting an occurrence date clears Timeless;
// clearing one leaves it.
func editedValues(u domain.Unit, req curation) domain.Unit {
	e := req.Edit
	if req.edits(EditText) {
		u.Text = e.Text
	}
	if req.edits(EditContext) {
		u.Context = e.Context
	}
	if req.edits(EditOccurredStart) {
		u.OccurredStart = e.OccurredStart
		u.Timeless = u.Timeless && e.OccurredStart == nil
	}
	if req.edits(EditOccurredEnd) {
		u.OccurredEnd = e.OccurredEnd
		u.Timeless = u.Timeless && e.OccurredEnd == nil
	}
	if req.edits(EditFactType) {
		u.Type = e.FactType
	}
	return u
}

// auditRestoreEdit writes the restore or edit audit row of one Update.
func (c *curator) auditRestoreEdit(
	ctx context.Context, tx store.Tx, u domain.Unit, req curation, restored bool, before *curated,
	op domain.OperationID,
) error {
	if restored {
		if err := c.audit(ctx, tx, u, domain.CurationRestore, req.Reason, nil, nil, op); err != nil {
			return err
		}
	}
	if !req.hasEdits() {
		return nil
	}
	return c.audit(ctx, tx, u, domain.CurationEdit, req.Reason, before, snapshotOf(u), op)
}

// curated is the audit snapshot of a unit's curated fields.
type curated struct {
	// Text is the unit text.
	Text string `json:"text"`
	// Context is the unit context.
	Context string `json:"context,omitempty"`
	// OccurredStart is the occurrence start.
	OccurredStart *time.Time `json:"occurred_start,omitempty"`
	// OccurredEnd is the occurrence end.
	OccurredEnd *time.Time `json:"occurred_end,omitempty"`
	// FactType is the fact type.
	FactType domain.FactType `json:"fact_type"`
	// Entities are the entity names.
	Entities []string `json:"entities,omitempty"`
}

// snapshotOf returns u's curated fields.
func snapshotOf(u domain.Unit) *curated {
	return &curated{
		Text: u.Text, Context: u.Context, OccurredStart: u.OccurredStart, OccurredEnd: u.OccurredEnd,
		FactType: u.Type, Entities: u.Entities,
	}
}

// audit appends one memory_audit row with the given snapshots (nil = none).
func (c *curator) audit(
	ctx context.Context, tx store.Tx, u domain.Unit, action domain.CurationAction, reason string,
	before, after *curated, op domain.OperationID,
) error {
	e := domain.AuditEntry{
		Bank: u.Bank, UnitID: u.ID, Action: action, Reason: reason, OperationID: op, At: c.d.Clock.Now(),
	}
	var err error
	if e.Before, err = encodeSnapshot(before); err != nil {
		return err
	}
	if e.After, err = encodeSnapshot(after); err != nil {
		return err
	}
	return tx.Units().AppendAudit(ctx, e)
}

// encodeSnapshot marshals a snapshot; nil is nil.
func encodeSnapshot(s *curated) (json.RawMessage, error) {
	if s == nil {
		return nil, nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encode audit snapshot: %w", err)
	}
	return raw, nil
}

// event appends the memory_curated lifecycle event under the transaction's seq.
func (c *curator) event(
	ctx context.Context, tx store.Tx, b domain.BankRef, seq int64, id domain.UnitID, op domain.OperationID,
) error {
	return tx.Events().Append(ctx, domain.LifecycleEvent{
		Seq: seq, Type: domain.EventTypeMemoryCurated, Bank: b, CreatedAt: c.d.Clock.Now(), OperationID: op,
		SubjectID: string(id),
	})
}

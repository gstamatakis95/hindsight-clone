package curate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"time"
)

// curator implements Curator over the Store, committing every curation in one short transaction.
type curator struct {
	d CuratorDeps
}

var _ Curator = (*curator)(nil)

// NewCurator returns the Curator. A nil Citing or Refresh skips the invalidate's model refreshes (the
// models still go stale through the change sequence); WP-14 wires both.
func NewCurator(d CuratorDeps) Curator {
	return &curator{d: d}
}

// Update validates req and commits it in one transaction: invalidate, or restore and/or edit.
func (c *curator) Update(ctx context.Context, req UpdateMemoryRequest) (domain.Unit, port.OperationID, error) {
	if err := validateUpdate(req); err != nil {
		return domain.Unit{}, "", err
	}
	names, err := c.snapshotNames(ctx, req)
	if err != nil {
		return domain.Unit{}, "", err
	}
	var (
		out domain.Unit
		op  port.OperationID
	)
	err = c.d.Store.InTx(ctx, func(tx port.Tx) error {
		out, op = domain.Unit{}, ""
		var err error
		if req.State == domain.MemoryStateInvalidated {
			out, err = c.invalidate(ctx, tx, req)
			return err
		}
		out, op, err = c.restoreOrEdit(ctx, tx, req, names)
		return err
	})
	if err != nil {
		return domain.Unit{}, "", fmt.Errorf("update memory %s: %w", req.ID, err)
	}
	return out, op, nil
}

// hasEdits reports whether req changes any curated field.
func hasEdits(req UpdateMemoryRequest) bool {
	return req.Text != nil || req.Context != nil || req.OccurredStart != nil || req.OccurredEnd != nil ||
		req.ClearOccurred || req.FactType != domain.FactTypeUnspecified || req.Entities != nil
}

// validateUpdate rejects malformed requests before any transaction.
func validateUpdate(req UpdateMemoryRequest) error {
	switch req.State {
	case domain.MemoryStateUnspecified, domain.MemoryStateValid, domain.MemoryStateInvalidated:
	default:
		return fmt.Errorf("%w: unknown memory state %q", domain.ErrInvalidArgument, req.State)
	}
	if req.ID == "" {
		return fmt.Errorf("%w: memory id is required", domain.ErrInvalidArgument)
	}
	return validateFields(req)
}

// validateFields checks the state/edit combination and the edited values.
func validateFields(req UpdateMemoryRequest) error {
	switch {
	case req.State == domain.MemoryStateInvalidated && hasEdits(req):
		return fmt.Errorf("%w: invalidate can't be combined with edits", domain.ErrInvalidArgument)
	case req.State == domain.MemoryStateUnspecified && !hasEdits(req):
		return fmt.Errorf("%w: nothing to update", domain.ErrInvalidArgument)
	case req.FactType != domain.FactTypeUnspecified && req.FactType != domain.FactTypeWorld &&
		req.FactType != domain.FactTypeExperience:
		return fmt.Errorf("%w: fact type can only change between world and experience", domain.ErrInvalidArgument)
	case req.Text != nil && *req.Text == "":
		return fmt.Errorf("%w: text can't be empty", domain.ErrInvalidArgument)
	case req.ClearOccurred && (req.OccurredStart != nil || req.OccurredEnd != nil):
		return fmt.Errorf("%w: clear_occurred can't be combined with occurred dates", domain.ErrInvalidArgument)
	}
	return nil
}

// liveFact reads a live fact in tx; an absent unit is domain.ErrNotFound, an observation is rejected.
func liveFact(ctx context.Context, tx port.Tx, b port.BankRef, id port.UnitID) (domain.Unit, error) {
	us, err := tx.GetUnits(ctx, b, []port.UnitID{id})
	if err != nil {
		return domain.Unit{}, err
	}
	if len(us) == 0 {
		return domain.Unit{}, fmt.Errorf("%w: memory %s", domain.ErrNotFound, id)
	}
	if us[0].Type == domain.FactTypeObservation {
		return domain.Unit{}, fmt.Errorf("%w: observations are not curated directly; they re-derive from "+
			"their sources", domain.ErrInvalidArgument)
	}
	return us[0], nil
}

// invalidate archives a fact and does every in-tx effect of the Curation table: citing observations
// deleted (before the archive, which drops the evidence rows) and their co-sources reset, audit row,
// event, consolidate, graph_maintenance and refreshes of the models citing the unit or a deleted
// observation.
func (c *curator) invalidate(ctx context.Context, tx port.Tx, req UpdateMemoryRequest) (domain.Unit, error) {
	seq, err := tx.Seq(ctx, req.Bank)
	if err != nil {
		return domain.Unit{}, err
	}
	u, err := liveFact(ctx, tx, req.Bank, req.ID)
	if err != nil {
		return domain.Unit{}, err
	}
	deleted, _, err := tx.Evidence().DeleteCiting(ctx, req.Bank, []port.UnitID{u.ID})
	if err != nil {
		return domain.Unit{}, err
	}
	if err := tx.Units().Archive(ctx, req.Bank, u.ID, req.Reason); err != nil {
		return domain.Unit{}, err
	}
	if err := c.audit(ctx, tx, u, domain.CurationInvalidate, req.Reason, snapshotOf(u), nil, ""); err != nil {
		return domain.Unit{}, err
	}
	if _, err := enqueueConsolidate(ctx, tx, req.Bank); err != nil {
		return domain.Unit{}, err
	}
	if err := enqueueGraph(ctx, tx, req.Bank); err != nil {
		return domain.Unit{}, err
	}
	if err := c.refreshCiting(ctx, tx, req.Bank, append([]port.UnitID{u.ID}, deleted...)); err != nil {
		return domain.Unit{}, err
	}
	if err := c.event(ctx, tx, req.Bank, seq, u.ID, ""); err != nil {
		return domain.Unit{}, err
	}
	u.State, u.Embedding = domain.MemoryStateInvalidated, nil
	return u, nil
}

// refreshCiting queues a refresh of every model whose source_ids include any of ids.
func (c *curator) refreshCiting(ctx context.Context, tx port.Tx, b port.BankRef, ids []port.UnitID) error {
	if c.d.Citing == nil || c.d.Refresh == nil {
		return nil
	}
	models, err := c.d.Citing.ModelsCiting(ctx, b, ids)
	if err != nil {
		return fmt.Errorf("find models citing %v: %w", ids, err)
	}
	seen := map[port.MentalModelID]bool{}
	for _, m := range models {
		if seen[m] {
			continue
		}
		seen[m] = true
		if _, err := c.d.Refresh.QueueRefresh(ctx, tx, b, m); err != nil {
			return fmt.Errorf("queue refresh of model %s: %w", m, err)
		}
	}
	return nil
}

// restoreOrEdit restores an archived unit when asked (State valid), applies any edits and enqueues the
// rederive. State valid on a live unit is no state change; with no edits either, nothing is written. names
// are the archived unit's snapshot entity names; a restore without new entities re-resolves them.
func (c *curator) restoreOrEdit(
	ctx context.Context, tx port.Tx, req UpdateMemoryRequest, names []string,
) (domain.Unit, port.OperationID, error) {
	seq, err := tx.Seq(ctx, req.Bank)
	if err != nil {
		return domain.Unit{}, "", err
	}
	restored := false
	if req.State == domain.MemoryStateValid {
		if restored, err = restoreIfArchived(ctx, tx, req.Bank, req.ID); err != nil {
			return domain.Unit{}, "", err
		}
	}
	u, err := liveFact(ctx, tx, req.Bank, req.ID)
	if err != nil {
		return domain.Unit{}, "", err
	}
	if !restored && !hasEdits(req) {
		return u, "", nil
	}
	before := snapshotOf(u)
	if hasEdits(req) {
		if u, err = c.applyEdit(ctx, tx, u, req, seq); err != nil {
			return domain.Unit{}, "", err
		}
	}
	entities := req.Entities
	if entities == nil && restored {
		entities = names
	}
	op, err := enqueueRederive(ctx, tx, u, req, entities)
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
// rederive re-resolves the names and re-creates them. It returns nil when no restore is asked for, new
// entities are given, no reader is wired or the unit is not archived.
func (c *curator) snapshotNames(ctx context.Context, req UpdateMemoryRequest) ([]string, error) {
	if req.State != domain.MemoryStateValid || req.Entities != nil || c.d.Archived == nil {
		return nil, nil
	}
	u, err := c.d.Archived.GetUnit(ctx, req.Bank, req.ID)
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
// then finds it live or not at all).
func restoreIfArchived(ctx context.Context, tx port.Tx, b port.BankRef, id port.UnitID) (bool, error) {
	err := tx.Units().Restore(ctx, b, id)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, domain.ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}

// applyEdit writes the new curated values (embedding cleared until rederive) and deletes the observations
// citing the unit, resetting their co-sources.
func (c *curator) applyEdit(
	ctx context.Context, tx port.Tx, u domain.Unit, req UpdateMemoryRequest, seq int64,
) (domain.Unit, error) {
	if req.Text != nil {
		u.Text = *req.Text
	}
	if req.Context != nil {
		u.Context = *req.Context
	}
	if req.ClearOccurred {
		u.OccurredStart, u.OccurredEnd = nil, nil
	}
	if req.OccurredStart != nil {
		u.OccurredStart = req.OccurredStart
	}
	if req.OccurredEnd != nil {
		u.OccurredEnd = req.OccurredEnd
	}
	if req.FactType != domain.FactTypeUnspecified {
		u.Type = req.FactType
	}
	now := c.d.Clock.Now()
	u.EditedAt, u.Embedding, u.Seq = &now, nil, seq
	if err := tx.Units().Update(ctx, u); err != nil {
		return domain.Unit{}, err
	}
	if _, _, err := tx.Evidence().DeleteCiting(ctx, u.Bank, []port.UnitID{u.ID}); err != nil {
		return domain.Unit{}, err
	}
	if req.Entities != nil {
		u.Entities = slices.Clone(req.Entities)
	}
	u.ConsolidatedAt, u.ConsolidationFailedAt = nil, nil
	return u, nil
}

// auditRestoreEdit writes the restore and/or edit audit rows of one Update.
func (c *curator) auditRestoreEdit(
	ctx context.Context, tx port.Tx, u domain.Unit, req UpdateMemoryRequest, restored bool, before *curated,
	op port.OperationID,
) error {
	if restored {
		if err := c.audit(ctx, tx, u, domain.CurationRestore, req.Reason, nil, nil, op); err != nil {
			return err
		}
	}
	if !hasEdits(req) {
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
	ctx context.Context, tx port.Tx, u domain.Unit, action domain.CurationAction, reason string,
	before, after *curated, op port.OperationID,
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
	ctx context.Context, tx port.Tx, b port.BankRef, seq int64, id port.UnitID, op port.OperationID,
) error {
	return tx.Events().Append(ctx, domain.LifecycleEvent{
		Seq: seq, Type: domain.EventTypeMemoryCurated, Bank: b, At: c.d.Clock.Now(), OperationID: op,
		SubjectID: string(id),
	})
}

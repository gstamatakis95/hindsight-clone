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

// curator commits edits, invalidations and restores (UpdateMemory), each in one short transaction.
type curator struct {
	d curatorDeps
}

// newCurator returns the curator.
func newCurator(d curatorDeps) *curator {
	return &curator{d: d}
}

// Update validates req and commits it in one transaction: invalidate, or restore and/or edit. It rejects
// observations, writes an audit row and returns the rederive operation, read back in the same transaction (nil
// for an invalidate).
func (c *curator) Update(ctx context.Context, req UpdateRequest) (domain.Unit, *domain.Operation, error) {
	if err := validateUpdate(req); err != nil {
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
	models, err := c.citingModels(ctx, req)
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
		if req.State == domain.MemoryStateInvalidated {
			out, err = c.invalidate(ctx, tx, req, models)
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

// hasEdits reports whether req changes any curated field.
func hasEdits(req UpdateRequest) bool {
	return req.Text != nil || req.Context != nil || req.OccurredStart != nil || req.OccurredEnd != nil ||
		req.ClearOccurred || req.FactType != domain.FactTypeUnspecified || req.Entities != nil
}

// validateUpdate rejects malformed requests before any transaction.
func validateUpdate(req UpdateRequest) error {
	switch req.State {
	case domain.MemoryStateUnspecified, domain.MemoryStateValid, domain.MemoryStateInvalidated:
	default:
		return domain.InvalidField("state", "unknown memory state %q", req.State)
	}
	if req.ID == "" {
		return domain.InvalidField("memory_id", "memory id is required")
	}
	return validateFields(req)
}

// validateFields checks the state/edit combination and the edited values.
func validateFields(req UpdateRequest) error {
	switch {
	case req.State == domain.MemoryStateInvalidated && hasEdits(req):
		return domain.InvalidField("state", "invalidate can't be combined with edits")
	case req.State == domain.MemoryStateUnspecified && !hasEdits(req):
		return fmt.Errorf("%w: nothing to update", domain.ErrInvalidArgument)
	case req.FactType != domain.FactTypeUnspecified && req.FactType != domain.FactTypeWorld &&
		req.FactType != domain.FactTypeExperience:
		return domain.InvalidField("fact_type", "fact type can only change between world and experience")
	case req.Text != nil && *req.Text == "":
		return domain.InvalidField("text", "text can't be empty")
	case req.ClearOccurred && (req.OccurredStart != nil || req.OccurredEnd != nil):
		return domain.InvalidField("clear_occurred", "clear_occurred can't be combined with occurred dates")
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
// event, consolidate, graph_maintenance and refreshes of the mental models whose source_ids cite the unit
// (read before the transaction).
func (c *curator) invalidate(
	ctx context.Context, tx store.Tx, req UpdateRequest, models []domain.MentalModelID,
) (domain.Unit, error) {
	seq, err := tx.Seq(ctx, req.Bank)
	if err != nil {
		return domain.Unit{}, err
	}
	u, err := liveFact(ctx, tx, req.Bank, req.ID)
	if err != nil {
		return domain.Unit{}, err
	}
	if _, _, err := tx.Evidence().DeleteCiting(ctx, req.Bank, []domain.UnitID{u.ID}); err != nil {
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
	if err := c.queueRefreshes(ctx, tx, req.Bank, models); err != nil {
		return domain.Unit{}, err
	}
	if err := c.event(ctx, tx, req.Bank, seq, u.ID, ""); err != nil {
		return domain.Unit{}, err
	}
	u.State, u.Embedding = domain.MemoryStateInvalidated, nil
	return u, nil
}

// citingModels reads, before the invalidate's transaction (so no pool read runs while the transaction holds
// a connection), the mental models whose source_ids cite the unit. Other requests read nothing.
func (c *curator) citingModels(ctx context.Context, req UpdateRequest) ([]domain.MentalModelID, error) {
	if req.State != domain.MemoryStateInvalidated {
		return nil, nil
	}
	models, err := c.d.Store.Read().ModelsCiting(ctx, req.Bank, []domain.UnitID{req.ID})
	if err != nil {
		return nil, fmt.Errorf("find models citing %s: %w", req.ID, err)
	}
	return models, nil
}

// queueRefreshes queues one refresh per distinct model in tx.
func (c *curator) queueRefreshes(
	ctx context.Context, tx store.Tx, b domain.BankRef, models []domain.MentalModelID,
) error {
	seen := map[domain.MentalModelID]bool{}
	for _, m := range models {
		if seen[m] {
			continue
		}
		seen[m] = true
		if _, err := jobs.QueueModelRefresh(ctx, tx, b, m, c.d.Clock.Now()); err != nil {
			return fmt.Errorf("queue refresh of model %s: %w", m, err)
		}
	}
	return nil
}

// restoreOrEdit restores an archived unit when asked (State valid), applies any edits, commits entity
// changes (an edit's names, or a restore's snapshot names) and enqueues the rederive, which only re-embeds
// and relinks. State valid on a live unit is no state change; with no edits either, nothing is written.
func (c *curator) restoreOrEdit(
	ctx context.Context, tx store.Tx, req UpdateRequest, change *entityChange,
) (domain.Unit, domain.OperationID, error) {
	seq, err := tx.Seq(ctx, req.Bank)
	if err != nil {
		return domain.Unit{}, "", err
	}
	restored := false
	if req.State == domain.MemoryStateValid {
		if restored, err = c.restoreIfArchived(ctx, tx, req.Bank, req.ID); err != nil {
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
// asked for, new entities are given or the unit is not archived. For an archived unit, GetUnit returns the
// names of its archive snapshot, even when the entity rows were pruned since.
func (c *curator) snapshotNames(ctx context.Context, req UpdateRequest) ([]string, error) {
	if req.State != domain.MemoryStateValid || req.Entities != nil {
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
// decides on it while its embedding is empty; the rederive's write clears the stamp (decision 1305).
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
// consolidated until the rederive writes its new embedding (decision 1305), deletes the observations citing
// the unit, resetting their co-sources, and queues graph maintenance for the removed observations.
func (c *curator) applyEdit(
	ctx context.Context, tx store.Tx, u domain.Unit, req UpdateRequest, seq int64,
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
		u.OccurredStart, u.Timeless = req.OccurredStart, false
	}
	if req.OccurredEnd != nil {
		u.OccurredEnd, u.Timeless = req.OccurredEnd, false
	}
	if req.FactType != domain.FactTypeUnspecified {
		u.Type = req.FactType
	}
	now := c.d.Clock.Now()
	u.EditedAt, u.Embedding, u.Seq = &now, nil, seq
	if err := tx.Units().Update(ctx, u); err != nil {
		return domain.Unit{}, err
	}
	if err := tx.Units().MarkConsolidated(ctx, []domain.UnitID{u.ID}, now); err != nil {
		return domain.Unit{}, err
	}
	if _, _, err := tx.Evidence().DeleteCiting(ctx, u.Bank, []domain.UnitID{u.ID}); err != nil {
		return domain.Unit{}, err
	}
	if _, _, err := jobs.EnqueueGraph(ctx, tx, u.Bank); err != nil {
		return domain.Unit{}, err
	}
	if req.Entities != nil {
		u.Entities = slices.Clone(req.Entities)
	}
	u.ConsolidatedAt, u.ConsolidationFailedAt = &now, nil
	return u, nil
}

// auditRestoreEdit writes the restore and/or edit audit rows of one Update.
func (c *curator) auditRestoreEdit(
	ctx context.Context, tx store.Tx, u domain.Unit, req UpdateRequest, restored bool, before *curated,
	op domain.OperationID,
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
		Seq: seq, Type: domain.EventTypeMemoryCurated, Bank: b, At: c.d.Clock.Now(), OperationID: op,
		SubjectID: string(id),
	})
}

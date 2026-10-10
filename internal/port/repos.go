package port

import (
	"context"
	"memd/internal/domain"
	"time"
)

// UnitRepo stores facts and observations. Live units only; invalidated ones sit in the archive.
type UnitRepo interface {
	// Insert stores new units (facts or observations) exactly as given, including their Seq stamp.
	// Callers set every field they care about; a zero CreatedAt means the store's current time.
	Insert(ctx context.Context, us []domain.Unit) error
	// UpsertObservation inserts an observation or replaces the text, proof count, embedding, version and
	// seq of the existing one with the same id.
	UpsertObservation(ctx context.Context, o domain.Unit) error
	// Unconsolidated returns the bank's oldest units with consolidated_at and consolidation_failed_at
	// unset, optionally only those whose tags contain one of scopes. Facts only: observations are never
	// consolidated. Oldest = created_at, then id.
	Unconsolidated(ctx context.Context, b BankRef, scopes []ScopeKey, limit int) ([]domain.Unit, error)
	// MarkConsolidated stamps units once every scope they belong to has been decided.
	MarkConsolidated(ctx context.Context, ids []UnitID, at time.Time) error
	// MarkConsolidationFailed parks a unit that fails even in a batch of one, so it can't block the bank.
	MarkConsolidationFailed(ctx context.Context, ids []UnitID, at time.Time) error
	// DeleteByDocument removes a document's units and records their link neighbours in graph_dirty.
	DeleteByDocument(ctx context.Context, b BankRef, doc DocumentID) ([]UnitID, error)
	// Archive moves a unit to memory_units_archive with snapshots of its entities and causal links.
	// Nothing references the archive by foreign key; the unit's links and entity rows are deleted.
	Archive(ctx context.Context, b BankRef, id UnitID, reason string) error
	// Restore moves an archived unit back; the embedding is recomputed by rederive.
	Restore(ctx context.Context, b BankRef, id UnitID) error
}

// DocumentRepo tracks documents and whether their last retain finished.
type DocumentRepo interface {
	// Get returns the document; an unknown id returns domain.ErrNotFound.
	Get(ctx context.Context, b BankRef, id DocumentID) (domain.Document, error)
	// Upsert inserts or replaces the document's tags, overrides and blob key, and clears extracted_at.
	// The hashes of the last completed retain are kept until Complete.
	Upsert(ctx context.Context, d domain.Document) error
	// Complete stamps content_hash, overrides_hash and extracted_at after the last chunk commits.
	Complete(ctx context.Context, b BankRef, id DocumentID, contentHash, overridesHash string) error
	// SetTags replaces the document's tag set.
	SetTags(ctx context.Context, b BankRef, id DocumentID, tags []string) error
}

// ChunkRepo stores chunks with their own content hash, so an interrupted retain resumes.
type ChunkRepo interface {
	// Committed returns the document's committed chunks as index -> content hash.
	Committed(ctx context.Context, b BankRef, doc DocumentID) (map[int]string, error)
	// Insert stores chunks; a chunk at an already committed (document, index) replaces it.
	Insert(ctx context.Context, cs []domain.Chunk) error
}

// EntityRepo resolves and stores entities.
type EntityRepo interface {
	// Candidates returns up to limit entities whose names are trigram-similar to name, most similar first.
	Candidates(ctx context.Context, b BankRef, name string, limit int) ([]domain.EntityCandidate, error)
	// Cooccurrence returns how many of others co-occur with entity (share a unit with it).
	Cooccurrence(ctx context.Context, b BankRef, entity EntityID, others []EntityID) (int, error)
	// Upsert inserts e (a new id when e.ID is empty) or updates the existing entity; it returns the id.
	// A label entity with the same (kind, label value) resolves to the existing row.
	Upsert(ctx context.Context, e domain.Entity) (EntityID, error)
	// Attach links a unit to entities and bumps their co-occurrence counts; re-attaching is a no-op.
	Attach(ctx context.Context, unit UnitID, ents []EntityID) error
}

// LinkRepo stores temporal, semantic and causal edges.
type LinkRepo interface {
	// Insert stores links; an existing (from, to, kind) edge is kept with the new weight.
	Insert(ctx context.Context, ls []domain.Link) error
}

// EvidenceRepo links observations to the facts they rest on.
type EvidenceRepo interface {
	// Add records evidence for an observation; unique (observation, unit), so re-adding is a no-op.
	Add(ctx context.Context, obs UnitID, ev []domain.Evidence) error
	// Cites reports whether an observation in scope s already cites unit; a replayed run skips it.
	Cites(ctx context.Context, s ScopeKey, unit UnitID) (bool, error)
	// DeleteCiting deletes observations citing any of ids and resets consolidated_at on their
	// other sources. It returns both sets so callers can queue consolidation and refreshes.
	DeleteCiting(ctx context.Context, b BankRef, ids []UnitID) (deleted, reset []UnitID, err error)
}

// EventRepo appends lifecycle events in the caller's tx; ListEvents reads them by seq.
type EventRepo interface {
	// Append stores e; e.Seq must be the tx's change seq (from Tx.Seq).
	Append(ctx context.Context, e domain.LifecycleEvent) error
	// List returns up to limit events of the bank with seq > afterSeq, in seq order.
	List(ctx context.Context, b BankRef, afterSeq int64, limit int) ([]domain.LifecycleEvent, error)
}

// BankRepo stores banks and tenant configuration. A write to an unknown bank creates it with tenant
// defaults (Ensure); every read of an unknown bank is domain.ErrNotFound.
type BankRepo interface {
	// Get returns the bank, including a tombstoned one (DeletingAt set); unknown = domain.ErrNotFound.
	Get(ctx context.Context, b BankRef) (domain.Bank, error)
	// Ensure returns the bank, creating it with empty overrides when it does not exist. A tombstoned
	// bank returns domain.ErrBankDeleting.
	Ensure(ctx context.Context, b BankRef) (domain.Bank, error)
	// Create inserts a new bank with its name and config; an existing id is domain.ErrAlreadyExists.
	Create(ctx context.Context, bank domain.Bank) error
	// SetConfig replaces the bank's own overrides.
	SetConfig(ctx context.Context, b BankRef, cfg domain.BankConfig) error
	// MarkDeleting tombstones the bank by setting deleting_at; marking it again keeps the first time.
	MarkDeleting(ctx context.Context, b BankRef, at time.Time) error
	// Delete removes the bank row (the last step of delete_bank); unknown = domain.ErrNotFound.
	Delete(ctx context.Context, b BankRef) error
	// TenantConfig returns the tenant-level config; a tenant with none set returns the zero config.
	TenantConfig(ctx context.Context, tenant string) (domain.BankConfig, error)
	// SetTenantConfig replaces the tenant-level config.
	SetTenantConfig(ctx context.Context, tenant string, cfg domain.BankConfig) error
}

// MentalModelRepo stores mental models and their history.
type MentalModelRepo interface {
	// Get returns the model; unknown = domain.ErrNotFound.
	Get(ctx context.Context, b BankRef, id MentalModelID) (domain.MentalModel, error)
	// Create inserts a new model; an existing id is domain.ErrAlreadyExists.
	Create(ctx context.Context, m domain.MentalModel) error
	// Update replaces the model's spec, content and refresh state; unknown = domain.ErrNotFound.
	Update(ctx context.Context, m domain.MentalModel) error
	// Delete removes the model and its history; unknown = domain.ErrNotFound.
	Delete(ctx context.Context, b BankRef, id MentalModelID) error
	// AppendHistory adds an entry, then keeps only the newest keep entries of that kind (keep <= 0 = all).
	AppendHistory(ctx context.Context, b BankRef, id MentalModelID, e domain.MentalModelHistoryEntry, keep int) error
	// History returns up to limit entries, newest first; kind unspecified = every kind.
	History(
		ctx context.Context, b BankRef, id MentalModelID, kind domain.HistoryKind, limit int,
	) ([]domain.MentalModelHistoryEntry, error)
}

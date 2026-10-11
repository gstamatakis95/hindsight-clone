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
	// seq of the existing one with the same id. Its scope is its Tags. History rows are written separately
	// with RecordObservationVersion.
	UpsertObservation(ctx context.Context, o domain.Unit) error
	// RecordObservationVersion appends one observation_versions row (written on every create and update).
	RecordObservationVersion(ctx context.Context, b BankRef, obs UnitID, v domain.ObservationVersion) error
	// Update replaces a live fact's curated fields (text, context, occurred dates, timeless, fact type,
	// edited_at, seq) and its embedding: a nil Embedding clears it, so the unit leaves semantic search until
	// rederive sets it again. It also clears consolidated_at and consolidation_failed_at so the unit is
	// consolidated again (its old observations are removed separately with EvidenceRepo.DeleteCiting).
	// Unknown or archived ids return domain.ErrNotFound.
	Update(ctx context.Context, u domain.Unit) error
	// SetDocumentTags rewrites the tags (and seq) of every live unit of a document, clears their
	// consolidated_at and consolidation_failed_at (callers delete the old observations with
	// EvidenceRepo.DeleteCiting), and returns their ids.
	SetDocumentTags(ctx context.Context, b BankRef, doc DocumentID, tags []string, seq int64) ([]UnitID, error)
	// IDsByDocument returns the ids of doc's live units, sorted; an unknown document has none. It writes
	// nothing, so callers can run EvidenceRepo.DeleteCiting before DeleteByDocument.
	IDsByDocument(ctx context.Context, b BankRef, doc DocumentID) ([]UnitID, error)
	// AppendAudit writes one memory_audit row.
	AppendAudit(ctx context.Context, a domain.AuditEntry) error
	// Unconsolidated returns the bank's oldest units with consolidated_at and consolidation_failed_at
	// unset, optionally only those whose tags contain one of scopes. Facts only: observations are never
	// consolidated (the partial index excludes fact_type 'observation'). Oldest = created_at, then id.
	Unconsolidated(ctx context.Context, b BankRef, scopes []ScopeKey, limit int) ([]domain.Unit, error)
	// MarkConsolidated stamps units once every scope they belong to has been decided.
	MarkConsolidated(ctx context.Context, ids []UnitID, at time.Time) error
	// MarkConsolidationFailed parks a unit that fails even in a batch of one, so it can't block the bank.
	MarkConsolidationFailed(ctx context.Context, ids []UnitID, at time.Time) error
	// DeleteByDocument removes a document's units, records their link neighbours in graph_dirty and their
	// entities in entity_maintenance, and returns the removed ids.
	DeleteByDocument(ctx context.Context, b BankRef, doc DocumentID) ([]UnitID, error)
	// Archive moves a unit to memory_units_archive with snapshots of its entities and causal links.
	// Nothing references the archive by foreign key; the unit's links and entity rows are deleted, its
	// link neighbours go to graph_dirty and its entities to entity_maintenance.
	Archive(ctx context.Context, b BankRef, id UnitID, reason string) error
	// Restore moves an archived unit back and reattaches its entities and causal links from the snapshot;
	// snapshot entities and link partners that no longer exist are skipped. The restored unit carries the
	// transaction's change seq (the caller calls Tx.Seq first). The embedding stays empty until rederive
	// recomputes it. An id that is not archived returns domain.ErrNotFound.
	Restore(ctx context.Context, b BankRef, id UnitID) error
}

// DocumentRepo tracks documents and whether their last retain finished.
type DocumentRepo interface {
	// Get returns the document; an unknown id returns domain.ErrNotFound.
	Get(ctx context.Context, b BankRef, id DocumentID) (domain.Document, error)
	// Upsert inserts or replaces the document's tags, overrides, observation scopes and blob key, and clears
	// extracted_at.
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
	// PendingMaintenance returns up to limit entity ids queued in entity_maintenance for b.
	PendingMaintenance(ctx context.Context, b BankRef, limit int) ([]EntityID, error)
	// Detach removes every unit_entities row of unit (rederive re-resolves them) and queues the detached
	// entities in entity_maintenance. Co-occurrence counts are not decremented.
	Detach(ctx context.Context, unit UnitID) error
	// PruneOrphans deletes those of ids that no live unit references (with their co-occurrences), removes
	// all of ids from entity_maintenance and returns how many entities it deleted.
	PruneOrphans(ctx context.Context, b BankRef, ids []EntityID) (int, error)
}

// LinkRepo stores temporal, semantic and causal edges.
type LinkRepo interface {
	// Insert stores links; an existing (from, to, kind) edge is kept with the new weight.
	Insert(ctx context.Context, ls []domain.Link) error
	// DeleteForUnit removes the links of the given kinds (empty = all) touching unit in either direction,
	// so rederive can rebuild them, and returns how many it removed.
	DeleteForUnit(ctx context.Context, unit UnitID, kinds []domain.LinkKind) (int, error)
	// Count returns how many links of kind touch unit (either direction), for the per-unit caps.
	Count(ctx context.Context, unit UnitID, kind domain.LinkKind) (int, error)
	// Dirty returns up to limit live units queued in graph_dirty for b, oldest first.
	Dirty(ctx context.Context, b BankRef, limit int) ([]UnitID, error)
	// ClearDirty removes ids from graph_dirty.
	ClearDirty(ctx context.Context, b BankRef, ids []UnitID) error
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
	// CitingScopes returns the distinct scopes of b's live observations that cite any of ids, ordered by
	// ScopeKey.String().
	CitingScopes(ctx context.Context, b BankRef, ids []UnitID) ([]ScopeKey, error)
	// DeleteScopes deletes every live observation of b whose scope is one of scopes (none when scopes is
	// empty), resets their remaining live sources exactly as DeleteCiting does, and returns the deleted
	// observations and the reset sources, both sorted. observation_scopes rows are kept, so a cleared scope
	// still lists with 0 observations. The caller calls Tx.Seq.
	DeleteScopes(ctx context.Context, b BankRef, scopes []ScopeKey) (deleted, reset []UnitID, err error)
	// DeleteAllObservations is DeleteScopes over every scope of b (ClearObservations with no scopes).
	DeleteAllObservations(ctx context.Context, b BankRef) (deleted, reset []UnitID, err error)
}

// EventRepo appends lifecycle events in the caller's tx; ListEvents reads them by seq.
type EventRepo interface {
	// Append stores e; e.Seq must be the tx's change seq (from Tx.Seq).
	Append(ctx context.Context, e domain.LifecycleEvent) error
	// List returns events of the bank with seq > afterSeq in seq order, keeping only types (empty = all).
	// The limit is soft: a page never splits a seq group (one transaction's events). lastSeq is the highest
	// seq scanned, filtered-out events included, and never below afterSeq; the next poll passes it as
	// afterSeq, so a type filter over non-matching events still advances.
	List(
		ctx context.Context, b BankRef, afterSeq int64, limit int, types []domain.EventType,
	) (events []domain.LifecycleEvent, lastSeq int64, err error)
	// Prune deletes events whose At is before before and returns how many.
	Prune(ctx context.Context, before time.Time) (int, error)
}

// DirectiveRepo stores a bank's directives.
type DirectiveRepo interface {
	// Upsert creates the directive (a new id when d.ID is empty) or replaces it; it returns the id.
	Upsert(ctx context.Context, d domain.Directive) (DirectiveID, error)
	// List returns the bank's directives (only active ones when activeOnly), oldest first. Tag selection
	// is the DirectiveSelector's job.
	List(ctx context.Context, b BankRef, activeOnly bool) ([]domain.Directive, error)
	// Delete removes the directive; unknown = domain.ErrNotFound.
	Delete(ctx context.Context, b BankRef, id DirectiveID) error
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
	// DeleteBatch deletes up to limit rows of the tombstoned bank's content and returns how many it
	// deleted; 0 means nothing is left but the bank row, its events and its operations. It covers every
	// bank-keyed table: memory_units, memory_units_archive, documents, chunks, entities, unit_entities,
	// entity_cooccurrences, memory_links, parked_links, graph_dirty, entity_maintenance,
	// observation_evidence, observation_scopes, observation_versions, memory_audit, mental_models,
	// mental_model_history, mental_model_folders and directives. A bank whose deleting_at is not set
	// returns domain.ErrFailedPrecondition and deletes nothing.
	DeleteBatch(ctx context.Context, b BankRef, limit int) (int, error)
	// Delete removes the tombstoned bank's row together with its remaining lifecycle events and operations
	// (including the running delete_bank job's own row, so its Complete then reports domain.ErrNotFound,
	// which the runner treats as done). A write afterwards creates a fresh, empty bank. A bank whose
	// deleting_at is not set returns domain.ErrFailedPrecondition; unknown = domain.ErrNotFound.
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

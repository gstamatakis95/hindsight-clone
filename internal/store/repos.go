package store

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
	RecordObservationVersion(ctx context.Context, b domain.BankRef, obs domain.UnitID, v domain.ObservationVersion) error
	// Update replaces a live fact's curated fields (text, context, occurred dates, timeless, fact type,
	// edited_at, seq) and its embedding: a nil Embedding clears it, so the unit leaves semantic search until
	// rederive sets it again. It also clears consolidated_at and consolidation_failed_at so the unit is
	// consolidated again (its old observations are removed separately with EvidenceRepo.DeleteCiting).
	// Unknown or archived ids return domain.ErrNotFound.
	Update(ctx context.Context, u domain.Unit) error
	// SetDocumentTags replaces the document-derived tags of every unit of a document, live and archived:
	// each unit's tags become normalize((tags − oldTags) ∪ newTags), so a unit's own extra tags (the
	// write_to_tags label tags) survive. On the live units it also sets seq and clears consolidated_at
	// and consolidation_failed_at (callers delete the old observations with EvidenceRepo.DeleteCiting).
	// It returns the live units' ids. Both tag lists must be normalized (domain.NormalizeTags), else
	// domain.ErrInvalidArgument. Passing the same list twice leaves the tags as they are, apart from
	// adding any of them a unit lacks, and only resets the stamps.
	SetDocumentTags(
		ctx context.Context, b domain.BankRef, doc domain.DocumentID, oldTags, newTags []string, seq int64,
	) ([]domain.UnitID, error)
	// IDsByDocument returns the ids of doc's live units, sorted; an unknown document has none. It writes
	// nothing, so callers can run EvidenceRepo.DeleteCiting before DeleteByDocument.
	IDsByDocument(ctx context.Context, b domain.BankRef, doc domain.DocumentID) ([]domain.UnitID, error)
	// AppendAudit writes one memory_audit row.
	AppendAudit(ctx context.Context, a domain.AuditEntry) error
	// Unconsolidated returns the bank's oldest units with consolidated_at and consolidation_failed_at
	// unset, optionally only those whose tags contain one of scopes. Facts only: observations are never
	// consolidated (the partial index excludes fact_type 'observation'). Oldest = created_at, then id.
	Unconsolidated(ctx context.Context, b domain.BankRef, scopes []domain.ScopeKey, limit int) ([]domain.Unit, error)
	// MarkConsolidated stamps units once every scope they belong to has been decided.
	MarkConsolidated(ctx context.Context, ids []domain.UnitID, at time.Time) error
	// MarkConsolidationFailed parks a unit that fails even in a batch of one, so it can't block the bank.
	MarkConsolidationFailed(ctx context.Context, ids []domain.UnitID, at time.Time) error
	// DeleteByDocument removes a document's units, records their link neighbours in graph_dirty and their
	// entities in entity_maintenance, and returns the removed ids.
	DeleteByDocument(ctx context.Context, b domain.BankRef, doc domain.DocumentID) ([]domain.UnitID, error)
	// Archive moves a unit to memory_units_archive with snapshots of its entities and causal links.
	// Nothing references the archive by foreign key; the unit's links and entity rows are deleted, its
	// link neighbours go to graph_dirty and its entities to entity_maintenance.
	Archive(ctx context.Context, b domain.BankRef, id domain.UnitID, reason string) error
	// Restore moves an archived unit back and reattaches its entities and causal links from the snapshot;
	// snapshot entities and link partners that no longer exist are skipped. The restored unit carries the
	// transaction's change seq (the caller calls Tx.Seq first). The embedding stays empty until rederive
	// recomputes it. An id that is not archived returns domain.ErrNotFound.
	Restore(ctx context.Context, b domain.BankRef, id domain.UnitID) error
	// DirtyLinks returns up to limit live units queued in graph_dirty for b (units whose links need a top-up after
	// a neighbour was removed or archived), oldest first.
	DirtyLinks(ctx context.Context, b domain.BankRef, limit int) ([]domain.UnitID, error)
	// ClearDirtyLinks removes ids from b's graph_dirty.
	ClearDirtyLinks(ctx context.Context, b domain.BankRef, ids []domain.UnitID) error
}

// DocumentRepo tracks documents and whether their last retain finished.
type DocumentRepo interface {
	// Get returns the document; an unknown id returns domain.ErrNotFound.
	Get(ctx context.Context, b domain.BankRef, id domain.DocumentID) (domain.Document, error)
	// Upsert inserts or replaces the document's tags, overrides, observation scopes and blob key, and clears
	// extracted_at. A non-nil Revision replaces the stored revision; nil keeps it.
	// The hashes of the last completed retain are kept until Complete.
	Upsert(ctx context.Context, d domain.Document) error
	// Complete stamps content_hash, overrides_hash and extracted_at after the last chunk commits.
	Complete(ctx context.Context, b domain.BankRef, id domain.DocumentID, contentHash, overridesHash string) error
	// SetTags replaces the document's tag set.
	SetTags(ctx context.Context, b domain.BankRef, id domain.DocumentID, tags []string) error
}

// ChunkRepo stores chunks with their own content hash, so an interrupted retain resumes.
type ChunkRepo interface {
	// Committed returns the document's committed chunks as index -> content hash.
	Committed(ctx context.Context, b domain.BankRef, doc domain.DocumentID) (map[int]string, error)
	// Insert stores chunks; a chunk at an already committed (document, index) replaces it.
	Insert(ctx context.Context, cs []domain.Chunk) error
}

// EntityRepo resolves and stores entities.
type EntityRepo interface {
	// Candidates returns up to limit entities whose names are trigram-similar to name, most similar first.
	Candidates(ctx context.Context, b domain.BankRef, name string, limit int) ([]domain.EntityCandidate, error)
	// Cooccurrence returns how many of others co-occur with entity (share a unit with it).
	Cooccurrence(ctx context.Context, b domain.BankRef, entity domain.EntityID, others []domain.EntityID) (int, error)
	// Upsert inserts e (a new id when e.ID is empty) or updates the existing entity; it returns the id.
	// A label entity with the same (kind, label value) resolves to the existing row.
	Upsert(ctx context.Context, e domain.Entity) (domain.EntityID, error)
	// Attach links a unit to entities and bumps their co-occurrence counts; re-attaching is a no-op.
	Attach(ctx context.Context, unit domain.UnitID, ents []domain.EntityID) error
	// PendingMaintenance returns up to limit entity ids queued in entity_maintenance for b.
	PendingMaintenance(ctx context.Context, b domain.BankRef, limit int) ([]domain.EntityID, error)
	// Detach removes every unit_entities row of unit (rederive re-resolves them) and queues the detached
	// entities in entity_maintenance. Co-occurrence counts are not decremented.
	Detach(ctx context.Context, unit domain.UnitID) error
	// PruneOrphans deletes those of ids that no live unit references (with their co-occurrences), removes
	// all of ids from entity_maintenance and returns how many entities it deleted.
	PruneOrphans(ctx context.Context, b domain.BankRef, ids []domain.EntityID) (int, error)
}

// EvidenceRepo links observations to the facts they rest on.
type EvidenceRepo interface {
	// Add records evidence for an observation; unique (observation, unit), so re-adding is a no-op.
	Add(ctx context.Context, obs domain.UnitID, ev []domain.Evidence) error
	// Cites reports whether an observation in scope s already cites unit; a replayed run skips it.
	Cites(ctx context.Context, s domain.ScopeKey, unit domain.UnitID) (bool, error)
	// DeleteCiting deletes observations citing any of ids and resets consolidated_at on their
	// other sources. It returns both sets so callers can queue consolidation and refreshes.
	DeleteCiting(ctx context.Context, b domain.BankRef, ids []domain.UnitID) (deleted, reset []domain.UnitID, err error)
	// CitingScopes returns the distinct scopes of b's live observations that cite any of ids, ordered by
	// ScopeKey.String().
	CitingScopes(ctx context.Context, b domain.BankRef, ids []domain.UnitID) ([]domain.ScopeKey, error)
	// DeleteScopes deletes every live observation of b whose scope is one of scopes (none when scopes is
	// empty), resets their remaining live sources exactly as DeleteCiting does, and returns the deleted
	// observations and the reset sources, both sorted. observation_scopes rows are kept, so a cleared scope
	// still lists with 0 observations. The caller calls Tx.Seq.
	DeleteScopes(
		ctx context.Context, b domain.BankRef, scopes []domain.ScopeKey,
	) (deleted, reset []domain.UnitID, err error)
	// DeleteAllObservations is DeleteScopes over every scope of b (ClearObservations with no scopes).
	DeleteAllObservations(ctx context.Context, b domain.BankRef) (deleted, reset []domain.UnitID, err error)
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
		ctx context.Context, b domain.BankRef, afterSeq int64, limit int, types []domain.EventType,
	) (events []domain.LifecycleEvent, lastSeq int64, err error)
	// Prune deletes events whose At is before before and returns how many.
	Prune(ctx context.Context, before time.Time) (int, error)
}

// DirectiveRepo stores a bank's directives.
type DirectiveRepo interface {
	// Create inserts the directive (a new id when d.ID is empty) and returns its id. A taken id or name in the
	// bank is domain.ErrAlreadyExists; an unknown bank is domain.ErrNotFound.
	Create(ctx context.Context, d domain.Directive) (domain.DirectiveID, error)
	// Get returns one directive, locked until the transaction ends; unknown = domain.ErrNotFound.
	Get(ctx context.Context, b domain.BankRef, id domain.DirectiveID) (domain.Directive, error)
	// Update replaces the name, rules, tags and active flag of directive d.ID. Unknown = domain.ErrNotFound; a
	// name taken by another directive of the bank is domain.ErrAlreadyExists.
	Update(ctx context.Context, d domain.Directive) error
	// List returns the bank's directives (only active ones when activeOnly), oldest first. Tag selection
	// is domain.SelectDirectives.
	List(ctx context.Context, b domain.BankRef, activeOnly bool) ([]domain.Directive, error)
	// Page returns one keyset page of the bank's directives for the ListDirectives RPC, oldest first
	// (created_at, then id), filtered by q.
	Page(ctx context.Context, q DirectiveQuery) ([]domain.Directive, error)
	// Delete removes the directive; unknown = domain.ErrNotFound.
	Delete(ctx context.Context, b domain.BankRef, id domain.DirectiveID) error
}

// OrgRepo stores orgs and their config level. Orgs are never created on first use: a bank can only be created
// in an org that exists and is not being deleted.
type OrgRepo interface {
	// Get returns the org, including a tombstoned one (DeletingAt set); unknown = domain.ErrOrgNotFound.
	Get(ctx context.Context, o domain.OrgRef) (domain.Org, error)
	// Create inserts a new org with its name and config; an existing id is domain.ErrAlreadyExists, a tombstoned
	// one domain.ErrOrgDeleting.
	Create(ctx context.Context, org domain.Org) error
	// SetConfig replaces the org's own config level. Unknown = domain.ErrOrgNotFound; tombstoned =
	// domain.ErrOrgDeleting. It never blocks bank creation in the org.
	SetConfig(ctx context.Context, o domain.OrgRef, cfg domain.BankConfig) error
	// LockConfig locks the org's config for the rest of the transaction, so a read-modify-write (LockConfig, Get,
	// SetConfig) serializes with any other that locks it too (Postgres: pg_advisory_xact_lock). Plain reads never
	// wait on it.
	LockConfig(ctx context.Context, o domain.OrgRef) error
	// LockForDelete locks the org row exclusively until the transaction ends (Postgres: FOR UPDATE) and returns
	// the org, tombstoned or not; unknown = domain.ErrOrgNotFound. The lock waits for bank creations in flight
	// (they share-lock the row) and makes new ones wait, so it is the first statement of an org delete.
	LockForDelete(ctx context.Context, o domain.OrgRef) (domain.Org, error)
	// MarkDeleting tombstones the org (marking it again keeps the first time) and then, in a later statement,
	// every live bank of it, and returns how many banks it tombstoned. Call LockForDelete first in the same
	// transaction: the bank statement then sees every bank committed while that lock waited.
	MarkDeleting(ctx context.Context, o domain.OrgRef, at time.Time) (int, error)
	// BanksAwaitingDelete returns up to limit tombstoned banks of o that have no pending or processing
	// delete_bank operation, in id order.
	BanksAwaitingDelete(ctx context.Context, o domain.OrgRef, limit int) ([]domain.BankRef, error)
	// CountBanks returns how many bank rows of o are left, tombstoned or not.
	CountBanks(ctx context.Context, o domain.OrgRef) (int, error)
	// DeleteBatch deletes up to limit rows of the tombstoned org's content by org, whatever their bank (rows whose
	// bank row is already gone included), and returns how many it deleted; 0 means none is left. It covers the
	// tables BankRepo.DeleteBatch covers, in the same order; operations and lifecycle events go with Delete. An
	// org that is not tombstoned is domain.ErrFailedPrecondition, an unknown one domain.ErrOrgNotFound.
	DeleteBatch(ctx context.Context, o domain.OrgRef, limit int) (int, error)
	// CountRows counts o's rows in every table that has an org_id column, as the database schema lists them at
	// call time (orgs itself excluded), so a table added later is checked without a code change. It returns how
	// many tables it counted and the total of rows found.
	CountRows(ctx context.Context, o domain.OrgRef) (tables int, rows int64, err error)
	// Delete removes the tombstoned org's operations (the running delete_org's own row included, so its Complete
	// then reports domain.ErrNotFound, which the runner treats as done) and lifecycle events, then the org row
	// itself. A bank row still in the org makes it domain.ErrFailedPrecondition; an org that is not tombstoned is
	// domain.ErrFailedPrecondition and an unknown one domain.ErrOrgNotFound.
	Delete(ctx context.Context, o domain.OrgRef) error
}

// ReceiptRepo stores deletion receipts. A receipt outlives its org: no delete path touches it, and only Prune
// removes it.
type ReceiptRepo interface {
	// Create inserts r (a new id when r.ID is empty) and returns its id.
	Create(ctx context.Context, r domain.DeletionReceipt) (string, error)
	// Get returns one receipt of tenant; unknown = domain.ErrNotFound.
	Get(ctx context.Context, tenant, id string) (domain.DeletionReceipt, error)
	// Latest returns the most recently requested receipt of org o (looked up by domain.OrgIDHash); none =
	// domain.ErrNotFound.
	Latest(ctx context.Context, o domain.OrgRef) (domain.DeletionReceipt, error)
	// Update replaces r's status, completed_at, verification and server version; unknown = domain.ErrNotFound.
	Update(ctx context.Context, r domain.DeletionReceipt) error
	// Prune deletes completed receipts whose completed_at is before before and returns how many.
	Prune(ctx context.Context, before time.Time) (int, error)
}

// BankRepo stores banks and tenant configuration. A write to an unknown bank creates it with empty overrides
// (Ensure), but only in an existing org; every read of an unknown bank is domain.ErrNotFound. Every method
// rejects a BankRef without a tenant, org or bank id as domain.ErrInvalidArgument (a programming error).
type BankRepo interface {
	// Get returns the bank, including a tombstoned one (DeletingAt set); unknown = domain.ErrNotFound.
	Get(ctx context.Context, b domain.BankRef) (domain.Bank, error)
	// Ensure returns the bank, creating it with empty overrides when it does not exist. It first share-locks the
	// org row, so it waits for an org delete in progress: an unknown org is domain.ErrOrgNotFound and a
	// tombstoned one domain.ErrOrgDeleting. A tombstoned bank returns domain.ErrBankDeleting.
	Ensure(ctx context.Context, b domain.BankRef) (domain.Bank, error)
	// Create inserts a new bank with its name and config; an existing id is domain.ErrAlreadyExists. The org
	// checks are Ensure's.
	Create(ctx context.Context, bank domain.Bank) error
	// SetConfig replaces the bank's own overrides, creating the bank when it does not exist (the org checks are
	// Ensure's); a tombstoned bank is domain.ErrBankDeleting.
	SetConfig(ctx context.Context, b domain.BankRef, cfg domain.BankConfig) error
	// MarkDeleting tombstones the bank by setting deleting_at; marking it again keeps the first time.
	MarkDeleting(ctx context.Context, b domain.BankRef, at time.Time) error
	// DeleteBatch deletes up to limit rows of the tombstoned bank's content and returns how many it
	// deleted; 0 means nothing is left but the bank row, its events and its operations. It covers every
	// bank-keyed table: memory_units, memory_units_archive, documents, chunks, entities, unit_entities,
	// entity_cooccurrences, memory_links, graph_dirty, entity_maintenance, observation_evidence,
	// observation_scopes, observation_versions, memory_audit, mental_models, mental_model_history and
	// directives. A bank whose deleting_at is not set returns domain.ErrFailedPrecondition and deletes
	// nothing.
	DeleteBatch(ctx context.Context, b domain.BankRef, limit int) (int, error)
	// Delete removes the tombstoned bank's row together with its remaining lifecycle events and operations
	// (including the running delete_bank job's own row, so its Complete then reports domain.ErrNotFound,
	// which the runner treats as done). A write afterwards creates a fresh, empty bank. A bank whose
	// deleting_at is not set returns domain.ErrFailedPrecondition; unknown = domain.ErrNotFound.
	Delete(ctx context.Context, b domain.BankRef) error
	// TenantConfig returns the tenant-level config; a tenant with none set returns the zero config.
	TenantConfig(ctx context.Context, tenant string) (domain.BankConfig, error)
	// SetTenantConfig replaces the tenant-level config.
	SetTenantConfig(ctx context.Context, tenant string, cfg domain.BankConfig) error
	// LockTenantConfig locks the tenant's config for the rest of the transaction, whether or not one is
	// set, so a read-modify-write (LockTenantConfig, TenantConfig, SetTenantConfig) serializes with any
	// other that locks it too (Postgres: pg_advisory_xact_lock). Plain TenantConfig reads never wait on it.
	LockTenantConfig(ctx context.Context, tenant string) error
}

// MentalModelRepo stores mental models and their history.
type MentalModelRepo interface {
	// Get returns the model; unknown = domain.ErrNotFound.
	Get(ctx context.Context, b domain.BankRef, id domain.MentalModelID) (domain.MentalModel, error)
	// Create inserts a new model; an existing id is domain.ErrAlreadyExists.
	Create(ctx context.Context, m domain.MentalModel) error
	// Update replaces the model's spec, content and refresh state; unknown = domain.ErrNotFound.
	Update(ctx context.Context, m domain.MentalModel) error
	// Delete removes the model and its history; unknown = domain.ErrNotFound.
	Delete(ctx context.Context, b domain.BankRef, id domain.MentalModelID) error
	// AppendHistory adds an entry, then keeps only the newest keep entries of that kind (keep <= 0 = all).
	AppendHistory(
		ctx context.Context, b domain.BankRef, id domain.MentalModelID, e domain.MentalModelHistoryEntry, keep int,
	) error
	// History returns up to limit entries, newest first; kind unspecified = every kind.
	History(
		ctx context.Context, b domain.BankRef, id domain.MentalModelID, kind domain.HistoryKind, limit int,
	) ([]domain.MentalModelHistoryEntry, error)
}

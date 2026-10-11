package store

import (
	"context"
	"memd/internal/domain"
	"memd/internal/graph"
	"time"
)

// DB opens transactions. No LLM, embedding or rerank call ever runs inside one.
type DB interface {
	// InTx runs fn in one transaction and commits when fn returns nil; any error (or panic) rolls back.
	// Adapter obligation: the transaction runs at READ COMMITTED.
	// fn must not call a model: read, call the gateway, then write in a short InTx.
	InTx(ctx context.Context, fn func(Tx) error) error
	// Read returns the read-only view used by the stateless API path. The Reader is long-lived and safe
	// for concurrent use; each method runs as its own statement against the latest committed data (no
	// snapshot spans two calls, so recall arms may see a write that lands between them). It never blocks
	// on an open InTx.
	Read() Reader
}

// Tx is one Postgres transaction; a state change and the jobs it causes commit together.
type Tx interface {
	Reader
	// Seq locks the bank row and returns this transaction's change sequence (one bump per tx).
	// Units, observations and events written in the tx carry it, so within a bank seq order is
	// commit order. Every tx that changes memory content calls it. Calling it again in the same tx
	// for the same bank returns the same value. An unknown bank returns domain.ErrNotFound and a
	// tombstoned one domain.ErrBankDeleting.
	Seq(ctx context.Context, b domain.BankRef) (int64, error)
	// Banks returns the bank repository bound to this transaction.
	Banks() BankRepo
	// Documents returns the document repository bound to this transaction.
	Documents() DocumentRepo
	// Chunks returns the chunk repository bound to this transaction.
	Chunks() ChunkRepo
	// Units returns the memory-unit repository bound to this transaction.
	Units() UnitRepo
	// Entities returns the entity repository bound to this transaction.
	Entities() EntityRepo
	// Graph returns the memory graph bound to this transaction: its link writes commit with the transaction and its
	// reads see them.
	Graph() graph.Tx
	// Evidence returns the observation-evidence repository bound to this transaction.
	Evidence() EvidenceRepo
	// MentalModels returns the mental-model repository bound to this transaction.
	MentalModels() MentalModelRepo
	// Directives returns the directive repository bound to this transaction.
	Directives() DirectiveRepo
	// Queue returns the operations table (producer side and operation control); job rows are visible to
	// workers only after commit.
	Queue() Queue
	// Events returns the lifecycle-event repository bound to this transaction.
	Events() EventRepo
}

// Reader holds every query the API path needs, split by consumer: [Search] for recall and the engines that
// retrieve, [Catalog] for the listing and admin reads, and [Freshness] for change detection. Depend on the narrowest
// one.
type Reader interface {
	Search
	Catalog
	Freshness
}

// Search serves retrieval: three of the four recall arms (the graph arm reads a graph.Reader), hydration and the
// reads around them. Searches return only live units of the query's bank, best first.
type Search interface {
	// SearchVector serves the vector arm: HNSW cosine search with iterative scan.
	SearchVector(ctx context.Context, q VectorQuery) ([]Hit, error)
	// SearchText serves the text arm: pg_search BM25 over unit text.
	SearchText(ctx context.Context, q TextQuery) ([]Hit, error)
	// SearchTemporal serves the temporal arm: units whose occurrence overlaps the window, spread over buckets.
	SearchTemporal(ctx context.Context, q TemporalQuery) ([]Hit, error)
	// GetUnits returns the live units with the given ids, in the order of ids; unknown or archived
	// ids are skipped, not an error.
	GetUnits(ctx context.Context, b domain.BankRef, ids []domain.UnitID) ([]domain.Unit, error)
	// GetChunks returns b's chunks with the given ids, in the order of ids; unknown ids and chunks of
	// another bank are skipped, not an error.
	GetChunks(ctx context.Context, b domain.BankRef, ids []domain.ChunkID) ([]domain.Chunk, error)
	// EntityStates returns every entity of b attached to any of units, ordered by entity id. Each one's
	// ObservationIDs are b's live observations attached to it or citing a live fact attached to it, sorted.
	// Unknown units are skipped.
	EntityStates(ctx context.Context, b domain.BankRef, units []domain.UnitID) ([]domain.EntityState, error)
	// StaleScopes returns, in input order, those of scopes (all of bank b) holding a live fact with
	// consolidated_at and consolidation_failed_at unset whose tags contain every tag of the scope (the
	// Unconsolidated predicate; the shared scope matches any such fact). It backs Memory.stale.
	StaleScopes(ctx context.Context, b domain.BankRef, scopes []domain.ScopeKey) ([]domain.ScopeKey, error)
	// CountObservations returns how many live observations scope s holds (exactly its tag set, matched by
	// scope_key); 0 for an unknown scope or bank.
	CountObservations(ctx context.Context, s domain.ScopeKey) (int, error)
}

// Catalog serves the listing and admin reads: a tenant's banks, and one bank's contents.
type Catalog interface {
	// ListBanks returns up to q.Limit of q.Tenant's banks whose id starts with q.IDPrefix, ordered by id in byte
	// order and strictly after q.After; tombstoned banks are left out.
	ListBanks(ctx context.Context, q BankQuery) ([]domain.Bank, error)
	// GetUnit returns one unit of b, live (State valid) or archived (State invalidated), with Entities,
	// SourceIDs and, for observations, Stale filled; an archived unit's Entities are its snapshot names.
	// Unknown unit, or unknown or tombstoned bank: domain.ErrNotFound.
	GetUnit(ctx context.Context, b domain.BankRef, id domain.UnitID) (domain.Unit, error)
	// ListUnits returns up to q.Limit units matching q, newest first (created_at, then id, both descending),
	// strictly after q.After, filled like GetUnit. Unknown or tombstoned bank: domain.ErrNotFound.
	ListUnits(ctx context.Context, q UnitQuery) ([]domain.Unit, error)
	// ObservationHistory returns up to limit observation_versions rows of observation id, newest first, with a
	// version below before (0 = from the newest). Unknown or tombstoned bank, or no rows when before is 0:
	// domain.ErrNotFound; no rows below a non-zero before is an empty result.
	ObservationHistory(
		ctx context.Context, b domain.BankRef, id domain.UnitID, before int64, limit int,
	) ([]domain.ObservationVersion, error)
	// ListDocuments returns up to q.Limit documents matching q, newest first (created_at, then id, both
	// descending), strictly after q.After, with MemoryCount and MemoriesByFactType filled. Unknown or tombstoned
	// bank: domain.ErrNotFound.
	ListDocuments(ctx context.Context, q DocumentQuery) ([]domain.Document, error)
	// BankStats returns b's counts from indexed COUNT queries. Unknown or tombstoned bank: domain.ErrNotFound.
	BankStats(ctx context.Context, b domain.BankRef) (domain.BankStats, error)
	// TagCounts returns up to limit (<= 0 = all) tags of b's source with how many items carry each, ordered
	// by tag in byte order, keeping tags that start with prefix and sort strictly after after ("" = from
	// the start). Source unspecified = memories (live units). Unknown or tombstoned bank: domain.ErrNotFound.
	TagCounts(
		ctx context.Context, b domain.BankRef, source domain.TagSource, prefix, after string, limit int,
	) ([]domain.TagCount, error)
	// ListObservationScopes returns up to limit (> 0) of b's observation scopes (its observation_scopes rows: every
	// scope that has held an observation) ordered by ScopeKey.String() in byte order, starting strictly
	// after after ("" = from the start, shared scope included). The shared scope's key is "", so to continue
	// after a page that ends at it, pass "0" (decision 1560). Observations counts the scope's live
	// observations and UnconsolidatedMemories the facts StaleScopes would match. Unknown or tombstoned bank:
	// domain.ErrNotFound.
	ListObservationScopes(ctx context.Context, b domain.BankRef, after string, limit int) ([]domain.ScopeInfo, error)
	// ListMentalModels returns one page of q.Bank's models matching q, ordered by id and with content, and
	// the next page token ("" = last page). Unknown or tombstoned bank: domain.ErrNotFound.
	ListMentalModels(ctx context.Context, q ModelQuery) ([]domain.MentalModel, string, error)
}

// Freshness tells whether derived content (observations, mental models) is behind the memories it was built from.
type Freshness interface {
	// BankSeq returns the bank's latest committed change_seq (0 for a new bank). Unknown bank:
	// domain.ErrNotFound.
	BankSeq(ctx context.Context, b domain.BankRef) (int64, error)
	// NewestSeqInScope returns the highest seq of a live unit (fact or observation) matching q, or 0 when
	// none matches.
	NewestSeqInScope(ctx context.Context, q ScopeQuery) (int64, error)
	// ModelsCiting returns the ids of b's mental models whose source_ids include any of ids, ordered by id.
	ModelsCiting(ctx context.Context, b domain.BankRef, ids []domain.UnitID) ([]domain.MentalModelID, error)
}

// Maintenance holds the cross-bank scans the maintenance tick runs, plus the job queue read of the optional
// metrics sampler (QueueStats). Each runs as its own statement (or short transaction) outside any InTx and skips
// tombstoned banks unless it says otherwise.
type Maintenance interface {
	// UnconsolidatedBanks returns every bank that is not tombstoned and has at least one live fact with an
	// embedding, consolidated_at and consolidation_failed_at unset, created before before, with the created_at of
	// its oldest such fact.
	UnconsolidatedBanks(ctx context.Context, before time.Time) ([]UnconsolidatedBank, error)
	// StalledBankDeletes returns every tombstoned bank with no pending or processing delete_bank operation: its
	// delete stopped (ReclaimStale or a shutdown release failed it at the attempt cap, or it failed for good).
	StalledBankDeletes(ctx context.Context) ([]domain.BankRef, error)
	// BanksWithGraphWork returns every bank that is not tombstoned and has graph_dirty or entity_maintenance
	// rows.
	BanksWithGraphWork(ctx context.Context) ([]domain.BankRef, error)
	// StrandedFacts returns up to limit live facts (Bank, ID and DocumentID set) on banks that are not
	// tombstoned whose rederive_memory ended FAILED or CANCELLED: an edit or restore clears the embedding and
	// keeps the consolidation stamp until rederive writes the new embedding, so a rederive that fails for good
	// leaves the fact out of semantic search and consolidation. Units with a rederive that is pending,
	// processing or ended less than retryAfter ago are skipped.
	StrandedFacts(ctx context.Context, retryAfter time.Duration, limit int) ([]domain.Unit, error)
	// SettleRetainBatches ends every PENDING retain_batch parent whose children are all terminal (FAILED if
	// one failed, else CANCELLED if one was cancelled, else COMPLETED) and returns how many it settled. A
	// parent is never claimed or run, so this is what moves it out of PENDING. It is idempotent under
	// concurrent callers.
	SettleRetainBatches(ctx context.Context) (int, error)
	// CronModels returns every model with refresh_cron set on a bank that is not tombstoned.
	CronModels(ctx context.Context) ([]CronModel, error)
	// FailedRefreshes returns, per existing model on a bank that is not tombstoned, its latest terminal
	// refresh_mental_model operation when that operation is FAILED.
	FailedRefreshes(ctx context.Context) ([]FailedRefresh, error)
	// QueueStats returns the job queue's depth: one entry per kind and status with at least one pending or
	// processing operation (retain_batch parents excluded), each with the age of the kind's oldest due pending
	// job. It is a single read for metrics, cheap enough to run every few seconds.
	QueueStats(ctx context.Context) ([]QueueStat, error)
}

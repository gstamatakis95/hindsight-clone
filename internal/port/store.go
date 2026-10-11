package port

import (
	"context"
	"memd/internal/domain"
)

// ---------- persistence ----------

// Store opens transactions. No LLM, embedding or rerank call ever runs inside one.
type Store interface {
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
	Seq(ctx context.Context, b BankRef) (int64, error)
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
	// Links returns the link repository bound to this transaction.
	Links() LinkRepo
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

// Reader holds every query the API path needs; Postgres serves all four recall arms.
// Searches return only live units of the query's bank, best first.
type Reader interface {
	// SearchVector is the semantic arm: HNSW cosine search with iterative scan.
	SearchVector(ctx context.Context, q VectorQuery) ([]Hit, error)
	// SearchText is the keyword arm: pg_search BM25 over unit text.
	SearchText(ctx context.Context, q TextQuery) ([]Hit, error)
	// SearchTemporal is the temporal arm: units whose occurrence overlaps the window, spread over buckets.
	SearchTemporal(ctx context.Context, q TemporalQuery) ([]Hit, error)
	// ExpandGraph is the graph arm: a recursive CTE over memory_links from the seed units.
	ExpandGraph(ctx context.Context, q GraphQuery) ([]Hit, error)
	// GetUnits returns the live units with the given ids, in the order of ids; unknown or archived
	// ids are skipped, not an error.
	GetUnits(ctx context.Context, b BankRef, ids []UnitID) ([]domain.Unit, error)
	// BankSeq returns the bank's latest committed change_seq (0 for a new bank). Unknown bank:
	// domain.ErrNotFound.
	BankSeq(ctx context.Context, b BankRef) (int64, error)
	// GetChunks returns b's chunks with the given ids, in the order of ids; unknown ids and chunks of
	// another bank are skipped, not an error.
	GetChunks(ctx context.Context, b BankRef, ids []ChunkID) ([]domain.Chunk, error)
	// EntityStates returns every entity of b attached to any of units, ordered by entity id. Each one's
	// ObservationIDs are b's live observations attached to it or citing a live fact attached to it, sorted.
	// Unknown units are skipped.
	EntityStates(ctx context.Context, b BankRef, units []UnitID) ([]domain.EntityState, error)
	// StaleScopes returns, in input order, those of scopes (all of bank b) holding a live fact with
	// consolidated_at and consolidation_failed_at unset whose tags contain every tag of the scope (the
	// Unconsolidated predicate; the shared scope matches any such fact). It backs Memory.stale.
	StaleScopes(ctx context.Context, b BankRef, scopes []ScopeKey) ([]ScopeKey, error)
	// TagCounts returns up to limit (<= 0 = all) tags of b's source with how many items carry each, ordered
	// by tag in byte order, keeping tags that start with prefix and sort strictly after after ("" = from
	// the start). Source unspecified = memories (live units). Unknown or tombstoned bank: domain.ErrNotFound.
	TagCounts(
		ctx context.Context, b BankRef, source domain.TagSource, prefix, after string, limit int,
	) ([]domain.TagCount, error)
	// CountObservations returns how many live observations scope s holds (exactly its tag set, matched by
	// scope_key); 0 for an unknown scope or bank.
	CountObservations(ctx context.Context, s ScopeKey) (int, error)
	// ListScopes returns up to limit (> 0) of b's observation scopes (its observation_scopes rows: every
	// scope that has held an observation) ordered by ScopeKey.String() in byte order, starting strictly
	// after after ("" = from the start, shared scope included). The shared scope's key is "", so to continue
	// after a page that ends at it, pass "0" (decision 1560). Observations counts the scope's live
	// observations and UnconsolidatedUnits the facts StaleScopes would match. Unknown or tombstoned bank:
	// domain.ErrNotFound.
	ListScopes(ctx context.Context, b BankRef, after string, limit int) ([]domain.ScopeInfo, error)
	// ListMentalModels returns one page of q.Bank's models matching q, ordered by id and with content, and
	// the next page token ("" = last page). Unknown or tombstoned bank: domain.ErrNotFound.
	ListMentalModels(ctx context.Context, q ModelQuery) ([]domain.MentalModel, string, error)
	// NewestSeqInScope returns the highest seq of a live unit (fact or observation) matching q, or 0 when
	// none matches.
	NewestSeqInScope(ctx context.Context, q ScopeQuery) (int64, error)
	// CronModels returns every model with refresh_cron set on a bank that is not tombstoned (cross-bank;
	// maintenance only).
	CronModels(ctx context.Context) ([]CronModel, error)
	// FailedRefreshes returns, per existing model on a bank that is not tombstoned, its latest terminal
	// refresh_mental_model operation when that operation is FAILED (cross-bank; maintenance only).
	FailedRefreshes(ctx context.Context) ([]FailedRefresh, error)
	// GetUnit returns one unit of b, live (State valid) or archived (State invalidated), with Entities,
	// SourceIDs and, for observations, Stale filled; an archived unit's Entities are its snapshot names.
	// Unknown unit, or unknown or tombstoned bank: domain.ErrNotFound.
	GetUnit(ctx context.Context, b BankRef, id UnitID) (domain.Unit, error)
	// ListUnits returns up to q.Limit units matching q, newest first (created_at, then id, both descending),
	// strictly after q.After, filled like GetUnit. Unknown or tombstoned bank: domain.ErrNotFound.
	ListUnits(ctx context.Context, q UnitQuery) ([]domain.Unit, error)
	// ObservationHistory returns the observation_versions rows of observation id, newest first. Unknown id,
	// or unknown or tombstoned bank: domain.ErrNotFound.
	ObservationHistory(ctx context.Context, b BankRef, id UnitID) ([]domain.ObservationVersion, error)
	// BankStats returns b's counts from indexed COUNT queries. Unknown or tombstoned bank: domain.ErrNotFound.
	BankStats(ctx context.Context, b BankRef) (domain.BankStats, error)
	// ListDocuments returns up to q.Limit documents matching q, newest first (created_at, then id, both
	// descending), strictly after q.After, with UnitCount and UnitsByFactType filled. Unknown or tombstoned
	// bank: domain.ErrNotFound.
	ListDocuments(ctx context.Context, q DocumentQuery) ([]domain.Document, error)
	// ModelsCiting returns the ids of b's mental models whose source_ids include any of ids, ordered by id.
	ModelsCiting(ctx context.Context, b BankRef, ids []UnitID) ([]MentalModelID, error)
}

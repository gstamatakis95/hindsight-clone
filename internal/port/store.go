package port

import (
	"context"
	"memd/internal/domain"
)

// ---------- persistence ----------

// Store opens transactions. No LLM, embedding or rerank call ever runs inside one.
type Store interface {
	// InTx runs fn in one transaction and commits when fn returns nil; any error (or panic) rolls back.
	// fn must not call a model: read, call the gateway, then write in a short InTx.
	InTx(ctx context.Context, fn func(Tx) error) error
	// Read returns the read-only view used by the stateless API path; each call is its own snapshot.
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
	// Queue returns the job producer; job rows are visible to workers only after commit.
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
}

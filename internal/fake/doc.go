// Package fake provides in-memory implementations of every port (Store with its Tx, Reader and repos,
// Queue and Claimer, Blob, LLM, Embedder, Reranker, Clock) for engine unit tests. Each fake passes the
// same internal/porttest contract suite as its production adapter, so fakes can't drift from real
// behaviour. Model fakes are scripted per prompt name and slot and record their calls.
//
// # Quick start
//
//	clock := fake.NewClock(time.Time{})          // frozen at DefaultClockStart; Advance / Set it
//	store := fake.NewStore(clock)                // port.Store and port.Claimer over an empty database
//	llm := fake.NewLLM()                         // port.LLM: script replies per (slot, prompt name)
//	llm.RespondJSON(domain.ModelSlotExtract, "extract_facts", `{"facts":[]}`)
//	emb := fake.NewEmbedder(fake.EmbedSimilar)   // port.Embedder: 768-dim, deterministic
//	rr := fake.NewReranker()                     // port.Reranker: token overlap unless scripted
//	blob := fake.NewBlob()                       // port.Blob
//
// See the Example functions for complete, runnable setups.
//
// # Store
//
// Write transactions are serialized on one store-wide lock and work on a private copy that commit
// publishes atomically; an error or panic discards every write (units, events, job rows, the Seq bump).
// Read() and Claimer.Alive read the latest committed snapshot without the lock and never block. Every
// other Claimer method is its own short transaction. Consequently an InTx callback must not open another
// InTx or call Claim, Heartbeat, Progress, Complete, Fail or ReclaimStale: it would wait for its own lock
// until ctx ends. (Engines must not do that anyway: no transaction spans another call.) A Tx (or a repo
// taken from it) used after its InTx returned panics, like a closed Postgres transaction errors.
//
// Ids are not checked to be UUIDs (Postgres's uuid columns would reject "u1"); tests may use readable ids.
//
// All times the store writes or compares (created_at, run_at, heartbeats, finished_at, event times,
// document extracted_at) come from the Clock passed to NewStore, read once per transaction like
// Postgres's now(). Advance a *Clock to make parked jobs due or heartbeats stale. Ties in "oldest first"
// orders are broken by insertion order, so a frozen clock still gives deterministic results.
//
// The store is a little stricter than strictly necessary, to catch engine bugs early:
//   - Foreign keys hold: units, documents, entities, mental models and directives need an existing bank;
//     units with a DocumentID and chunks need the document; links, evidence and entity attachments need
//     live units (and entities) of one bank. Violations are domain.ErrNotFound.
//   - EventRepo.Append rejects an event whose Seq is not the Seq this transaction took for its bank
//     (domain.ErrInvalidArgument): call Tx.Seq first and stamp the event with it.
//   - Embeddings must be domain.EmbeddingDim long (domain.ErrInvalidArgument), like the vector(768) column.
//   - Every by-id write that names a bank returns domain.ErrNotFound for a row of another bank or tenant.
//
// Search arms approximate Postgres closely enough for ranking tests:
//   - SearchVector: exact cosine similarity.
//   - SearchText: Okapi BM25 (k1 1.2, b 0.75) over lowercase letter/digit tokens, no stemming; only units
//     matching a query term are hits.
//   - SearchTemporal: units whose occurrence (occurred_start..occurred_end, else mentioned_at) overlaps the
//     half-open window, scored 1 / (1 + days from the window centre); Buckets spreads hits round-robin.
//   - ExpandGraph: outgoing links only; score is the best product of edge weights over paths.
//   - EntityRepo.Candidates: pg_trgm similarity with pg_trgm's default 0.3 threshold.
//
// A Limit of 0 (or less) on a search, Unconsolidated, Dirty, PendingMaintenance, History or Events.List
// means no cap.
//
// # Model fakes
//
// LLM replies are FIFO per (slot, prompt name) and the last one repeats; name "" is a per-slot fallback.
// Unscripted calls fail with ErrNotScripted. Timeout, Fail and InvalidJSON script failures; Calls and
// CallsFor return what was sent. Embedder in EmbedHash mode gives unrelated vectors per text; EmbedSimilar
// makes texts that share words close (cosine) and texts that share none orthogonal. No nomic prefixes are
// applied, so EmbedQuery(x) equals EmbedDocuments([x])[0] and Vector(x). Reranker scores are scripted per
// (query, doc) or per doc, else token overlap, with the adapter's sigmoid rule for out-of-range scores.
package fake

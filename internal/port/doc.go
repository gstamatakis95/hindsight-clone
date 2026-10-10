// Package port declares the interfaces engines use to reach the outside world: persistence (Store,
// Tx, Reader and the repos), the Postgres job queue (Queue, Claimer), blob storage, the AI gateway
// (LLM, Embedder, Reranker), model routing, config resolution and the clock.
//
// Each port has one production adapter (internal/adapter/...), one in-memory fake (internal/fake) and
// one shared contract suite (internal/porttest) both must pass. Besides interfaces, this package holds
// only the id, query, hit and request types the interfaces reference; identity types are aliases of
// their internal/domain definitions so domain values carry them without importing port.
package port

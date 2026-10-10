// Package consolidate turns facts into observations: durable, deduplicated statements per observation
// scope, each citing the facts it rests on with exact quotes.
//
// The consolidate job (Consolidator) takes a bounded run of the bank's oldest unconsolidated units and
// decides each in every scope it belongs to (ScopeExpander fans a unit's tags out by scope mode). Per
// scope, LLM batches are decided with no transaction open: related observations come from the scope's
// semantic and keyword arms, the CONSOLIDATE call proposes create / update actions under a per-call
// schema, new texts are embedded and each create is checked for a near-duplicate (Deduper: cosine >=
// dedup_threshold, then DEDUP_CHECK). One short transaction per batch then calls tx.Seq, writes the
// observations, their evidence and observation_versions rows, and stamps the units whose last scope it
// was. A failing call is halved; a unit that fails alone is parked. A final transaction enqueues the
// continuation while units remain and the refreshes of stale models in the changed scopes.
//
// StrategyMatcher picks the per-scope ConsolidationStrategy (it also serves config.NewResolver), and
// Invalidator clears derived observations so their sources are re-consolidated after edits,
// invalidations, retags and reprocessing. Reads the frozen ports lack are consumer-declared in extras.go.
package consolidate

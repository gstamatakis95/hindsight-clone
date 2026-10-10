// Package consolidate turns facts into observations: durable, deduplicated statements per observation
// scope, each citing the facts it rests on with exact quotes.
//
// The consolidate job (Consolidator) takes a bounded run of the bank's oldest unconsolidated units and
// decides each in every scope it belongs to (ScopeExpander fans a unit's tags out by scope mode). The
// CONSOLIDATE model call proposes create / update / none actions outside any transaction; one short
// transaction then applies them, stamps finished units and calls tx.Seq. Deduper merges near-duplicate
// observations (cosine >= dedup_threshold, then a DEDUP_CHECK call). StrategyMatcher picks the
// per-scope ConsolidationStrategy overrides, and Invalidator clears derived observations so their
// sources are re-consolidated after edits, invalidations, retags and reprocessing.
//
// consolidate may import recall (built first, never imports consolidate) and declares the small
// Recaller it consumes.
package consolidate

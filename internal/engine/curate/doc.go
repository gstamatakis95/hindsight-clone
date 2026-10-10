// Package curate lists, inspects and curates individual memory units without hard deletes.
//
// MemoryLister reads live and archived units with filters and keyset pagination, plus an observation's
// version history. Curator commits edits, invalidations and restores in the RPC's own transaction:
// everything that would leave recall inconsistent (archive moves, deleting citing observations and
// resetting their co-sources, graph_dirty, audit rows, events, tx.Seq) happens there, and only
// re-embedding and re-linking wait for the rederive_memory job, which Rederiver runs. Observations are
// never curated directly; they re-derive from their sources.
package curate

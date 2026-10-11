// Package graph is the boundary to the memory graph: the weighted temporal, semantic and causal links between
// memory units. It declares the link types ([Link], [Kind]), the expansion the recall graph arm runs ([Query],
// [Hit]) and three interfaces: [Reader] (Expand and the per-unit Degree), [Writer] (AddLinks, DeleteUnitLinks) and
// [Tx], both bound to one store transaction.
//
// # Where it sits
//
// graph imports only domain, so internal/store can hand out the transaction-bound graph (store.Tx.Graph). Today
// Postgres is the only adapter: internal/store/postgres stores the links in memory_links, in the same transaction
// as the units they join, so atomicity, the tx.Seq stamping of the units, archive and restore, and bank delete are
// unchanged. postgres.DB.Graph returns the pool-backed Reader that cmd/memd wires into recall.
//
//   - Writes (retain's and rederive's linker, graph_maintenance's top-up) go through store.Tx.Graph inside the
//     caller's transaction.
//   - The recall graph arm reads through a Reader. It treats Query.Types, Query.Tags and Query.Seq as hints and
//     re-checks every hit against the store (store.Search.GetUnits plus the same filters), keeping the hits'
//     order and scores. Postgres applies the filters natively before the limit, so the re-check removes nothing.
//   - Entities (entity resolution, co-occurrence, unit_entities) are not part of this boundary yet; they stay in
//     store.EntityRepo. Moving them is a future extension of this package.
//
// # Contract
//
// The interface comments are the specification. Every adapter runs the contract suite in internal/graph/graphtest
// from its own tests; the Postgres adapter runs it against pgtest.
//
// # Adding a graph-database adapter
//
// A pure graph database can serve Expand later without a redesign. Do not build one before it is needed; the
// recipe is:
//
//  1. Outbox: make the Postgres Tx.Graph also append every AddLinks and DeleteUnitLinks to an outbox table in the
//     same transaction. Some memory_links changes bypass Tx.Graph and must reach the outbox too: archiving a unit
//     deletes its links (a cascade, keeping a causal snapshot), restoring it re-inserts the causal links whose
//     partner is live, removing a document's units cascades, and a bank delete removes the bank's links.
//  2. Sync job: a job kind that drains the outbox in order into the graph database, idempotently (a job can run
//     twice after a reclaim), and records how far it got.
//  3. Reader: an internal/graph/<db> package whose Reader serves Expand (and Degree) from the graph database. It
//     may ignore the filter hints; archived and removed units are then filtered out by recall's re-check, which is
//     what keeps them out while the sync lags.
//  4. Wiring: cmd/memd passes that Reader to recall instead of postgres.DB.Graph(). Writes keep going through
//     store.Tx.Graph, so the store stays the source of truth.
//  5. Run graphtest.Run against the adapter (after a sync), and document its settings in the README.
package graph

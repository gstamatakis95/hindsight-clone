// Package graphtest is the contract suite every graph adapter runs from its own tests: [Run] takes a constructor
// that returns a fresh, empty [Fixture] (a store.DB to seed units and write links through Tx.Graph, and the
// graph.Reader under test). The Postgres adapter runs it in internal/store/postgres against pgtest; an adapter that
// syncs from an outbox syncs before it answers. Filter hints, archived units and restore stay Postgres store
// tests: the recall graph arm re-checks those against the store.
package graphtest

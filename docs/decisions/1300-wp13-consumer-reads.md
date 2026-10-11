# 1300 — Reads the ports lack: consumer interfaces mirroring CCR WP-13-1

## Context
ListMemories, GetMemory (archived), observation history, stats, tags, list documents, list scopes,
template export and the invalidate refresh lookup all need reads `port.Reader` does not have. Frozen ports
change only through a CCR, and engine work must not block on it.

**Update (WP-16):** CCR WP-13-1 landed (WP-15). The query and cursor types are now port aliases, and
`ModelSpecReader` is gone: Export uses `ListMentalModels`. The remaining consumer interfaces are satisfied by
`port.Reader`. See decision 1603.

## Decision
- Each missing read is declared as a small interface in the consuming engine package (`curate.UnitSource`,
  `curate.CitingModels`, `curate.ScopeSource`, `bank.StatsSource`, `bank.ModelSpecReader`,
  `docs.DocumentSource`). Every method is exactly the `port.Reader` method proposed in CCR WP-13-1, so
  once the CCR is applied `store.Read()` satisfies them unchanged. Query/cursor types (`UnitQuery`,
  `DocumentQuery`, ...) are declared here for now and become aliases of the port types.
- The engines keep the RPC logic (validation, page-size defaults and caps, opaque keyset tokens, limit+1
  next-page detection); the sources only filter and order.
- The tombstone rule ("every read of a deleting bank returns NOT_FOUND") is part of each proposed read's
  contract. Reads WP-13 does through existing ports (directives, export, GetDocument) check
  `Banks().Get(...).Deleting()` in a short read transaction.
- `DirectiveSelector` and `DirectiveAdmin` need no CCR: `Tx.Directives()` exists, so they take
  `port.Store` and read in a short `InTx` (the spec's `NewDirectiveSelector(port.Reader)` becomes
  `NewDirectiveSelector(port.Store)`).
- `worker.ConsolidationCandidates` gets no wrapper: CCR WP-13-1 proposes `Reader.BanksToConsolidate` with
  its exact signature, so WP-14 passes `store.Read()`.
- Tests stub the consumer interfaces in-package; everything that runs on existing ports is tested against
  `internal/fake`.

## Alternatives
- Implementing listings over existing ports (e.g. scanning units through searches): rejected, wrong and
  unbounded.
- Waiting for the CCR: rejected by the run rules.

## Spec lines involved
SPEC "Curation" (ListMemories reads both tables, observation_versions), "Behavioural rules" (unknown or
deleting bank reads NOT_FOUND; indexed COUNTs; no total), gRPC surface (ListMemories, ListTags,
ListDocuments, ListScopes, BankTemplate, Memory.stale), decision 502.

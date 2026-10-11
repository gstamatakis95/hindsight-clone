# 1603 — Curate, docs and bank engines on the batch ports

## Context
WP-13 declared the reads it needed as consumer interfaces whose methods matched CCR WP-13-1 (decision 1300).
WP-15 applied that CCR: `ModelSpecReader` merged into `ListMentalModels`, the query and cursor types moved to
port, and ClearObservations must report what was actually deleted.

## Decision
- **Aliases.** `curate.UnitQuery`, `curate.UnitCursor`, `docs.DocumentQuery` and `docs.DocumentCursor`
  are aliases of the port types. Server code that uses the engine names compiles unchanged.
- **`ModelSpecReader` is deleted.**
  - `NewTemplateCodec(s, refresh)` now takes two arguments.
  - Export lists the specs with `s.Read().ListMentalModels(ModelQuery{Bank: b})`, which orders them by id
    and returns all of them, and keeps `.Spec`.
- **Small consumer interfaces stay.** These are `UnitSource`, `UnitGetter`, `CitingModels`, `ScopeSource`,
  `StatsSource`, `DocumentSource` and `BlobLister`.
  - Their methods are now port methods, and `port.Reader` (or `port.Blob`) satisfies them.
  - Their docs follow the port: `ScopeSource` lists the observation_scopes population, and
    `CitingModels` returns empty for an unknown bank.
- **ClearObservations.**
  - `ObservationClearer.ClearScopes` returns the deleted ids, and `Deleted = len(deleted)`.
  - The pre-clear `countObservations` pager is removed. It was a second ListScopes pager with the same
    shared-scope bug.
- **ListScopes RPC pager.**
  - After a page that ends at the shared scope (key `""`), the next-page token carries `"0"`, which sorts
    before every tagged key (decision 1560).
  - A page size of 1 therefore terminates. `TestListScopesPageSizeOneTerminates` and
    `TestObservationAdminListScopes` (sizes 1, 2 and 10) cover it.
- **Reprocess** is the retain engine's `Reprocessor` (decision 1600). It satisfies `docs.Reprocessor`, uses
  `IDsByDocument`, and carries `Document.ObservationScopes`.

## Alternatives
- Collapsing every consumer interface into `port.Reader`: this widens the engine dependencies for no gain,
  and the card asks to keep consumer interfaces small.

## Spec lines involved
SPEC 147 and 380 (BankTemplate), 155 and 670 (ListDocuments), 163 and 693-697 (ListScopes,
ClearObservations), 1964 (invalidate refreshes); CCR batch §4 WP-13; decision 1560.

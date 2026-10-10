# 141 — How BankRepo.DeleteBatch counts and orders, and how Delete locks

Status: accepted (WP-01)

## Context

`DeleteBatch(b, limit)` deletes "up to limit rows" across every bank-keyed table and returns how many; 0
means only the bank row, its events and its operations are left. Several tables cascade into others
(documents -> chunks and memory_units; memory_units -> unit_entities, memory_links,
observation_evidence; entities -> unit_entities, entity_cooccurrences; mental_models ->
mental_model_history), so a naive order would delete rows the count never sees. `Delete` must not let a
concurrent Enqueue commit an operation for a bank that is gone.

## Decision

- DeleteBatch checks the tombstone first (unknown = `ErrNotFound`, live = `ErrFailedPrecondition`) and
  refuses `limit <= 0` with `ErrInvalidArgument` (returning 0 would falsely mean "done").
- It deletes table by table in a fixed order, children first: unit_entities, entity_cooccurrences,
  memory_links, observation_evidence, parked_links, graph_dirty, entity_maintenance, observation_versions,
  memory_audit, memory_units_archive, memory_units, chunks, documents, entities, observation_scopes,
  mental_model_history, mental_models, mental_model_folders, directives. Each table gets the remaining
  budget as its LIMIT and the loop stops when the budget is spent. A table is only reached once every
  table before it is empty for the bank, so cascades only ever fire into empty tables: the count is
  exact, never above `limit`, and 0 means every table was found empty.
- Postgres has no `DELETE ... LIMIT`; each step is `DELETE ... WHERE ctid = ANY(ARRAY(SELECT ctid ...
  LIMIT n))`. The four join tables without a bank index are found through their indexed parent key
  (`unit_id`, `entity_a`, `from_unit_id`, `observation_id` joined to the bank's memory_units / entities)
  instead of scanning the whole table by its denormalised bank columns.
- Delete deletes the bank row first (`... AND deleting_at IS NOT NULL`), so an Enqueue holding
  `FOR KEY SHARE` on it makes Delete wait and its operation row is then deleted too, while an Enqueue that
  comes after finds no bank. Then it deletes the bank's lifecycle_events and operations (the running
  delete_bank row included). If no row was deleted it reads the bank to tell unknown (`ErrNotFound`) from
  live (`ErrFailedPrecondition`).

## Alternatives

- Delete only parents and let cascades remove the rest: one batch could remove an unbounded number of
  rows, and the count would not say how much was deleted.
- Dynamic SQL over a table-name list: shorter, but trips gosec (G201/G202) and loses sqlc's checks.

## Spec lines involved

- SPEC.md 1980 (DeleteBank: batches, then the bank row); decision 004 (foreign keys, delete_bank's final
  transaction, Enqueue takes FOR KEY SHARE on the bank row); decision 012 R9 and H3.

# 130 — Archive and restore of units

## Context

`UnitRepo.Archive` moves a unit to `memory_units_archive` "with snapshots of its entities and causal links";
`Restore` reattaches them, skipping vanished entities and partners. The schema also has `parked_links`, whose
use no port names.

## Decision

- Archive takes the bank's change seq first (bank row before unit row), locks the live unit, copies it into the
  archive stamped with that seq (no embedding, per decision 004) with `entities_snapshot` = `[{id, name}]` and
  `links_snapshot` = its causal links `[{from, to, kind, weight, created_at}]`, then removes the live row the
  same way as `DeleteByDocument`: link neighbours to `graph_dirty`, entities to `entity_maintenance`, links,
  entity rows and evidence by cascade. Callers run `EvidenceRepo.DeleteCiting` first in the same tx.
- Restore takes the seq, deletes the archive row and inserts the unit back with that seq, no embedding and no
  consolidation stamps (rederive and consolidation redo them). It reattaches snapshot entities that still exist
  in the bank and causal links whose partner is live. Causal links whose partner is itself archived are
  appended to the partner's snapshot, so restoring the partner later brings them back. A vanished chunk is
  dropped; a vanished document is `ErrFailedPrecondition`; an id not archived in the bank is `ErrNotFound`.
- Temporal and semantic links are not snapshotted: rederive rebuilds them (SPEC "Curation").
- `parked_links` is not used by v1; `DeleteBatch` still clears it so a bank delete leaves nothing behind.

## Alternatives

- Parking links in `parked_links` rows instead of JSON: equivalent, but two places to restore from; the JSON
  snapshot is what the port names.

## Spec lines involved

SPEC "Curation" (~1957-1971); `internal/port/repos.go` (Archive, Restore); decision 004.

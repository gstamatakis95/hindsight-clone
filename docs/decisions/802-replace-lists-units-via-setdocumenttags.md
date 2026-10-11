# 802 — Replacement lists the document's units with SetDocumentTags

Status: superseded (WP-16): `replace` now lists the units with `UnitRepo.IDsByDocument` (CCR batch #20), which writes nothing. The rest of the order below is unchanged.

## Context
When retain replaces a document, it must delete the observations citing the old units and reset their other
sources. That is `EvidenceRepo.DeleteCiting(ids)`, and it must run before `UnitRepo.DeleteByDocument`:
evidence rows cascade on unit delete, and the adapter documents "callers run DeleteCiting first". No port
method lists a document's unit ids without removing them.

## Decision
The first batch transaction does this, in order:
1. `Seq`.
2. `Units().SetDocumentTags(doc, newTags, seq)`. It returns exactly the document's live unit ids. Its side
   effects are harmless: the live rows it touches are deleted a moment later, and the document's archived
   units take the document's new tags, as a retag would do.
3. `Evidence().DeleteCiting(ids)`.
4. `Units().DeleteByDocument(doc)`.
5. Enqueue `graph_maintenance` (serial key `graph`, DedupePendingOrRunning).

The flow was also run once against the Postgres adapter (testcontainers, not committed). That run confirmed
the observation is deleted and the co-source's `consolidated_at` is reset.

## Alternatives
- A rolled-back probe transaction calling `DeleteByDocument` to learn the ids: costs an extra transaction
  and has a race.
- A new `UnitRepo.IDsByDocument`: this is a frozen contract, so it goes through CCR WP-08-1.

## Spec lines involved
SPEC "Behavioural rules" (the first batch replaces old units, deleting their observations and resetting
co-sources); `UnitRepo` / `EvidenceRepo` docs; `internal/adapter/postgres/units_remove.go`.

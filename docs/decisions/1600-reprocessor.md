# 1600 — The retain engine's Reprocessor

Supersedes the "Item observation scopes" bullet of decision 808. `DocumentScopesWriter` is gone, and the
Ingestor sets `Document.ObservationScopes` on `Upsert`. The no-op hash rules of 808 are unchanged.

## Context
Decision 1303 delegates ReprocessDocument to the retain engine through `docs.Reprocessor`, but no
implementation existed. The WP-16 card requires that every re-upsert path carry `Document.ObservationScopes`,
because `DocumentRepo.Upsert` writes the column and would otherwise overwrite it with NULL. A
`retain_document` payload also needs the item's context, metadata, mention time and timeless flag, and the
document row stores none of them.

## Decision
- `retain.NewReprocessor(port.Store)` makes no model call. It works in one transaction:
  1. `Seq`. This fails with NotFound on an unknown bank and with BankDeleting on a tombstoned one.
  2. `Documents().Get`. An unknown document is NotFound.
  3. Re-`Upsert` the row with its tags, blob key and `ObservationScopes`. The overrides are the new ones
     when given, else the stored ones (SPEC 904).
  4. Enqueue `retain_document` on `doc:{id}` with `ForceReextract` and `Job.Overrides` set to those
     overrides.
- The payload's scopes are the document's. That keeps the Retainer's no-op hash identical to the
  Ingestor's (decision 808).
- **Content hash:** it is the last segment of the stored blob key (`domain.DocumentBlobKey`), validated by
  rebuilding the key. `Document.ContentHash` can't be used: it belongs to the last *completed* retain and is
  empty for a document that never completed. The Retainer re-hashes the blob anyway and fails loudly on a
  mismatch.
- **Context, metadata, mention time and timeless flag:** these are read from one unit of the document.
  Every unit of one retain shares them.
  - The unit is the newest one that no curator edited (`EditedAt == nil`), live or archived. It comes from
    `ListUnits` (`DocumentID`, newest first, up to 100). A curated context or timeless flag must not spread
    to every re-extracted unit, because reprocess resets curation (SPEC 1970).
  - If every listed unit was edited, the newest unit is used.
  - A document with no units at all (zero facts) reprocesses with none of these values, and its mention
    time becomes "now".
  - Entity hints and `resolve_entities` are not stored anywhere, so a reprocess runs without them.
- Not done: tagging the replaced units' audit and archive rows with the reprocess operation id (SPEC
  1970). No port method exists for it. This is a known gap, carried from WP-08.

## Alternatives
- Storing the full item payload on the document: needs a migration (a CCR). Deferred; the unit-derived
  constants cover what the spec lists for retain items except the hints.
- Rejecting documents that never completed: needless, since the blob key always pins the content.

## Spec lines involved
SPEC 158 (ReprocessDocument resets curation), 900 and 904 (stored overrides replayed or replaced), 1919
(retain_document is enqueued by ReprocessDocument), 1970 (reprocess replaces units); CCR batch #24 / ruling 1a = A.

# 1900 — A retag replaces only the document-derived tags and rewrites the units in UpdateDocument's tx

Status: accepted (WP-19, CCR WP-19-1)

## Context
Retain writes each unit with `normalize(document tags ∪ label tags)`. The label tags are the `key:value` tags
of `entity_labels` with `write_to_tags`. Final review B (H1) found that a retag overwrote the units' tags with
the new document tags, so the label tags were lost.

To keep them, the store has to know which of a unit's tags came from the document. That means the document's
tags *before* the retag. `SetDocumentTags` now takes `oldTags` and `newTags` and computes
`(tags − oldTags) ∪ newTags` per unit (CCR WP-19-1). The open question was where the old tags come from.

- **In `Apply`:** impossible. `Retag` commits the new tags on the document row when the operation is queued,
  so `Apply` only ever reads the new set.
- **In the job payload:** exact on the happy path, but wrong after a FAILED or CANCELLED retag.
  - At that point the units may still carry the failed job's old tags. A cancel can also land after `Apply`
    committed, in which case they carry its new ones.
  - `domain.Operation` does not expose the payload, so the next `Retag` can't recover that set.
  - The next job would then subtract the wrong set and leave stale document tags on the units for good. That
    leaks the memories into a tag scope the user removed.

## Decision
- On a real change, `Retag` does three things in one transaction, under `tx.Seq`:
  1. It re-reads the document. Its tags are exactly the document-derived tags the units carry.
  2. It stores the new tags on the document.
  3. It calls `SetDocumentTags(old, new, seq)`.

  The document row and its units change atomically, so the invariant "the units' document-derived tags are
  the document's tags" always holds. A retain batch adopts the document's tags under the same lock
  (decision 1700), so a running retain stays consistent too.
- `retag_document` (`Apply`) still clears the observations that cite the document's units, resets their
  co-sources and enqueues consolidate (plus graph_maintenance when it deleted observations). It calls
  `SetDocumentTags(cur, cur, seq)`, which leaves the tags as they are, resets the consolidation stamps again
  (a consolidation may have run since `Retag`) and returns the ids for `DeleteCiting`. A second run is
  harmless.
- A set-equal update is still a no-op: it reads only, writes nothing and doesn't bump the seq. After a FAILED
  or CANCELLED retag it still re-enqueues the job, which now only redoes the observation clearing, because
  the units already carry the tags.
- Observable change: the units' tags change when `UpdateDocument` returns, not when the operation completes.
  Until the job runs, the old observations (derived under the old tags) are still there. That was already the
  case before, when the units also still had the old tags.

- Known edge case: a tag that is both a document tag and a label tag can't be told apart. If a retag removes
  it from the document, it also leaves the units.
- This supersedes one bullet of decision 1700. A running retain's earlier batches no longer carry stale tags
  until `retag_document`, because `Retag` rewrites them at once. The retain test
  `runRetaggedBetweenBatches` now expects 0 stale units, where it used to expect 1.
- It also changes the reason behind decision 1307's "set-equal update after a failed retag re-enqueues". The
  behaviour is unchanged, but the units no longer still carry the old tags after a failure. What is left
  undone is the observation clearing and the stamp reset.

## Alternatives
- **Payload with old tags:** rejected for the failure case above.
- **Recognize label tags by shape (`key:value`) or from the bank's `entity_labels` config:** document tags can
  contain `:`, and the config can change after retain. Fragile.
- **Store the document-derived tags per unit:** this needs a schema change and is out of proportion for an
  early iteration.

## Spec lines involved
- SPEC "Jobs", the `retag_document` row ("Rewrite tags on the document's units, clear their observations,
  enqueue consolidate"): the rewrite now happens in UpdateDocument's transaction, and the job re-asserts it
  idempotently.
- SPEC "Behavioural rules", UpdateDocument: a set-equal update is a no-op; a change re-consolidates under the
  new tags.

Tests:
- `porttest` `UnitsSetDocumentTagsKeepsExtraTags` (fake and Postgres);
- `TestRetagKeepsLabelTags`;
- `TestRetagSetEqualIsNoop`.

## Addendum (orchestrator, closing verification)

- A label tag that is the same string as a document tag is removed when a retag drops that document tag. This
  is accepted: the two can't be told apart without a separate column.
- Accepted gap: Apply runs `SetDocumentTags(cur, cur)`, so it only adds the current document tags. If a
  content-changing retain left units carrying an older tag set and then failed or is still pending, a later
  retag leaves `old ∪ new` on those units until a successful retain or reprocess replaces them. It needs a
  failed or pending retain to trigger, it follows the approved CCR formula, and no facts are lost.

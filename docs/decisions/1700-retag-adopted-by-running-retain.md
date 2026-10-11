# 1700 — A running retain adopts a retag instead of being superseded

Status: accepted (WP-17)

## Context
Decision 807 made a retain run stop as superseded whenever the document's blob key, overrides or *tags* were
no longer the run's. UpdateDocument (`docs.Retagger.Retag`) changes only the tags: it stores them on the
document row and enqueues `retag_document` on the document's `doc:` serial key.

A retag that lands while a `retain_document` job (a new async retain or a ReprocessDocument) is running has
two possible effects:
- **between batches:** the next batch transaction sees different tags, so the run stops. Only the chunks it
  already committed have units, and `extracted_at` stays unset.
- **after the enqueue but before the first batch:** the pre-check stops the run before it does anything. A
  reprocess never runs.

The `retag_document` job runs next and retags only the units that exist. The facts of the remaining chunks
are lost, with no error anywhere.

## Decision
- Ownership means the blob key and the stored overrides. A difference in either still supersedes (807
  unchanged).
- When only the tags differ, the run sets `run.tags` to the document's current tags and keeps going. This
  happens in the check before any model call, and in `checkOwner` inside every write transaction right after
  `Tx.Seq`. That transaction's units are stamped with the tags read under the lock, so a retag can't slip in
  between the read and the write.
- The units an earlier batch wrote with the old tags are fixed by the `retag_document` job. It is queued on
  the same `doc:` key, so the claimer runs it after the retain job finishes. An inline (sync) retain is not on
  the serial key, but the retag's `Apply` and every retain batch both take the bank's Seq lock. Either the
  batch commits first and `Apply` retags its units, or `Apply` commits first and the batch reads the new tags.
- `RetainResult.Scopes` is computed from the tags the run ended with.
- An older run of the same content and overrides that resumes after a newer retain changed only the tags now
  completes under the newer tags instead of stopping. The end state is the same as the newer retain's.
  `TestRetainOlderRunCannotOverwriteNewerTags` now asserts that end state (no unit keeps the older tags)
  instead of `Superseded`.

## Alternatives
- **Have the retag cancel or re-enqueue the running retain:** more moving parts, and it still re-runs model
  calls for nothing.
- **Have the retain re-stamp every earlier unit's tags itself:** duplicates `retag_document`, which also has
  to delete the observations citing those units and reset their co-sources.

## Not done
ReprocessDocument still enqueues with `DedupeNone`. Joining a pending reprocess job could lose a change of
overrides: the document row would take the new overrides while the joined job carries the old ones, so the job
would supersede itself on the overrides check. It is left as is.

## Spec lines involved
SPEC "Behavioural rules" (retain idempotency, UpdateDocument retags), "Jobs" (`retain_document` and
`retag_document` share the `doc:{id}` serial key). Tests:
- `TestReprocessRetaggedBetweenBatchesKeepsAllFacts`;
- `TestRetainRetaggedBetweenBatchesKeepsAllFacts`;
- `TestReprocessRetaggedBeforeFirstBatchCompletes`;
- `TestRetainOverridesChangeMidRunSupersedes`.

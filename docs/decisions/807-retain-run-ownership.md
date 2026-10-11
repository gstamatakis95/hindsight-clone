# 807 — Retain run ownership: superseded runs stop without writing

Status: accepted (WP-08 review round 1, H1 + H2)

## Context
Two retains of one document can overlap in three ways:
- a sync retain runs while an async job of the same document is between attempts or running (no serial key
  covers inline runs);
- a failed job is re-run by RetryOperation after a newer retain completed;
- a reclaimed job resumes after a newer retain started.

Salting chunk hashes per run (decision 801) is not enough on its own. Two failure modes remain:
- An older run could resume and trust chunk rows a newer run had since deleted the units of. Rows at indexes
  past the newer content's chunk count survive as leftovers too.
- An older run could finish and stamp the document complete over the newer content.

## Decision
- **Before any model call.** The Retainer re-reads the document. If its `blob_key` or its stored overrides
  are no longer this run's, a newer retain owns the document: the run ends as superseded (see the last two
  bullets).
- **Tags alone are not ownership (WP-17, decision 1700).** If the blob key and overrides still match and
  only the document's tags differ, the run still owns the document. The tags changed because UpdateDocument
  retagged it, or because a newer retain of the same content and overrides did. The run adopts the
  document's current tags and stamps every unit it writes from then on with them. This happens both in the
  check before the model calls and in every transaction check (under the Seq lock, see below).
  The retag_document job queued on the same `doc:` key runs after this job and retags the units the run had
  already written. Before WP-17 a tag difference superseded the run, which lost the facts of every chunk not
  yet committed.
- **Trusting committed chunks.** Committed chunk hashes are trusted only when two things hold:
  - index 0 carries this run's salted hash;
  - the document is still in flight (`extracted_at` unset).

  Otherwise the run starts over with a full replacement. Rows at indexes past the content's chunk count are
  ignored.
- **Inside every write transaction.** Each batch transaction and the completing transaction check, right
  after `Tx.Seq` (which row-locks the bank, so the check cannot race another retain's write):
  - that the document still has this run's blob key and overrides (then it re-reads the tags and adopts
    them, see above);
  - that every chunk this run committed still carries its salted hash.

  A newer run's first batch always rewrites chunk 0, so it is caught. If either check fails, the transaction
  aborts and writes nothing.
- **A superseded async run** returns `RetainResult.Superseded` with no error, so the job completes and is not
  retried. It never stamps the document complete. Its earlier units were already replaced by the newer
  run's first batch, or will be.
- **A superseded sync run** returns `retain.ErrSuperseded`. It wraps `domain.ErrConflict`, so the client
  sees ABORTED and may retry.

The latest retain to take over the document wins. That matches upsert semantics.

## Alternatives
- Serialising sync retains through the queue: it changes the spec's sync path.
- Advisory locks held across the run: they would hold a lock across model calls.

## Spec lines involved
SPEC "Behavioural rules" (retain idempotency, resume), "Jobs" (`retain_document` serial key), "Operations"
(RetryOperation). Tests:
- `TestRetainSupersededAfterInterveningRetain`;
- `TestRetainRetryOperationAfterNewerRetain`;
- `TestRetainOverlappingRetainsOfOneDocument`;
- `TestRetainOverridesChangeMidRunSupersedes` and `TestRetainOlderRunCannotOverwriteNewerTags` (WP-17).

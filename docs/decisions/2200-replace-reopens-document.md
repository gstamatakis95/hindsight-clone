# 2200 — The first replace reopens the document

Status: accepted (WP-22 fix-up, review finding: partial retain left the document stamped complete)

## Context
Only `Documents().Upsert` clears `documents.extracted_at`, and only `Complete` sets it. The no-op check, both in
the Ingestor and in `ProcessDocument`, treats a document whose `extracted_at` is set and whose hashes match
as unchanged.

Two runs of the same document, content and overrides can overlap (decision 807). A sync retain runs inline
with no serial key. Before this fix, the following sequence lost data:
1. Run A completes and sets `extracted_at`.
2. Run B's first batch calls `replace()` and deletes A's units. B still owns the document because its blob key
   and overrides match.
3. B fails before `Complete`.

The document then held only B's first batch but was still stamped complete. B's job retry, the client's retry
and every later same-content retain returned Unchanged, so the missing facts were never extracted.

## Decision
`replace()` first reopens the document. It runs in the first batch's transaction, or in the completing
transaction when no batch committed anything. If `extracted_at` is set, it re-upserts the document as read in
that same transaction. The bank is Seq-locked there, so tags, overrides, scopes and blob key are written back
unchanged and only `extracted_at` is cleared. No port or schema changes.

This restores the "Trusting committed chunks" invariant of decision 807: a document whose units are being
replaced is in flight.
- A failed async job resumes from its own committed chunks on retry (`trustedCommitted`).
- A failed sync run's client retry is not unchanged, so it re-extracts the whole document.
- A run that completes stamps the document as before.

Forced runs (ReprocessDocument, force_reextract) already clear `extracted_at` through their upsert. For them
the reopen is a no-op. Tags adopted mid-run (decision 1700) are kept, because the re-upsert writes the
document's current tags.

## Residual behaviour
A run that fails after its first batch leaves the document in flight with partial units until a retry, or a
later retain, completes it. For a sync run that means until the client retries. This is the same as any
failed retain of changed content: the error reaches the caller, and no later non-forced retain treats the
document as complete.

## Alternatives
- Ending a run as unchanged when `checkOwner` sees `extracted_at` set: this would save the repeated work.
  It still needs the reopen for runs that already replaced, and it changes the outcome of overlapping runs
  that decision 807 lets proceed.

## Tests
- `TestRetainPartialReplaceAfterOtherRunCompletedIsNotUnchanged`
- `TestRetainSyncVictimPartialReplaceIsNotUnchanged`

# 801 — Chunk hashes are salted with the retain run

Status: accepted (WP-08)

## Context
Resume rule: "an interrupted retain resumes, skipping chunks already committed with a matching hash".
`ChunkRepo.Committed` returns index -> hash for every chunk row of the document. In Postgres that includes
leftovers of the previous completed retain, and `UnitRepo.DeleteByDocument` does not delete chunks.

Suppose a re-retain of a changed document compares plain content hashes:
1. Its first batch deletes all the old units.
2. It crashes.
3. On resume it skips every chunk whose text and index match a leftover chunk of the old retain.

The facts of those chunks are then lost. `force_reextract` on unchanged content loses them the same way.

## Decision
- The stored `chunks.content_hash` is `sha256(runKey + "\x00" + sha256(text))`, where `runKey` is:
  - the job id for a `retain_document` job, which is stable across reclaims and RetryOperation;
  - a fresh uuid for an inline (sync) run.
- A chunk is skipped only when `Committed[index]` equals this run's salted hash.
- If any chunk of the run is already committed, an earlier attempt of this run has already replaced the old
  units, so no batch replaces them again. Otherwise the first batch transaction (or the final one, for
  content with no chunks) does the replacement.
- An inline run never resumes. A failed sync Retain is retried by the client as a new run, which replaces
  whatever the failed run wrote, so nothing is duplicated.

## Alternatives
- Deleting the old chunk rows in the replacement tx: there is no port method for it (CCR).
- Deterministic chunk ids per run: `Committed` does not return ids.

## Spec lines involved
SPEC "Behavioural rules" (retain idempotency); `ChunkRepo` doc in `internal/port/repos.go`; decision 140.
Tests: `TestRetainChangedDocumentCrashResumeKeepsAllFacts`, `TestRetainForceReextractCrashResume`.

## Addendum (WP-25): sync retains longer than the deadline
An inline run never resumes, so a sync Retain that fails part-way (an RPC deadline, an LLM outage) can leave
the document partly replaced, and the client's retry is a new run that re-extracts every chunk. A document
whose extraction takes longer than the call's deadline (`MEMD_RPC_DEADLINE`, 120s by default, when the
client sets none) can therefore never complete synchronously.

This is accepted, and the salting is unchanged: run ownership (decision 807) and the reopen-on-replace rule
(decision 2200) rely on it. Long documents should use `run_async` (the `retain_document` job resumes from
its committed chunks) or a longer client deadline. The README says so under "Sync vs async retain".

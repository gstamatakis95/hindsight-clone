# 2003 — Manual consolidate retries, handler progress, and the retain bank check before the blob Put

## Context
Final review B's LOW bundle:
- A failing manual consolidate (with scopes or overrides) skipped its retry whenever an automatic
  consolidate was due, so its overrides were never applied.
- The refresh, rederive and retag handlers wrote no progress (SPEC "Operations": handlers write `progress`
  at each checkpoint).
- Retain put the blob before the transaction that rejects a deleting bank, which leaves orphan blobs under
  a deleted bank's prefix.

## Decision
- **Retry skip, automatic jobs only.** A consolidate is automatic when its job has no overrides and its
  payload decodes (as `consolidate.Payload`) to no scopes. An undecodable payload counts as manual, so its
  retry is never dropped. A manual job's continuations carry the same scopes and overrides, so they count as
  manual too. Only an automatic consolidate skips its retry when another automatic one is due.
- **Progress through one start checkpoint.** `Handlers.Checkpoint` is wired to `worker.NewCheckpoint`. The
  refresh, rederive and retag handlers report `Progress{Stage: <kind>, Total: 1}` before calling their
  engine.
  - This is their progress write. It is also their cancel check: a cancelled job, or one whose bank is
    being deleted, stops there with domain.ErrCancelled before any model call. delete_bank's `blockIfBusy`
    therefore no longer waits for a refresh that a delete has already cancelled.
  - There is no checkpoint after the engine returns. A late cancel there would turn committed work into a
    failure, and Complete settles a cancel-requested row as CANCELLED anyway.
- **Bank check before the blob Put.** The ingestor's pre-write read transaction (`markUnchanged`) reads the
  bank first. A tombstoned bank is domain.ErrBankDeleting (FAILED_PRECONDITION) before `storeBlobs`. An
  unknown bank is fine, because the write transaction creates it.

## Known gap
Progress between the turns of a long agentic refresh (and inside rederive or retag) needs a checkpoint
parameter on the mental-model, curate and docs engines. Those are outside WP-20's paths. Until then, a
running refresh stops on cancel or bank delete only through the heartbeat (Alive false) or a failed
`tx.Seq`, as before.

A retain can still race DeleteBank between this check and the Put. The window is now one read transaction
wide. An orphan blob it leaves behind is removed by the delete's final DeletePrefix only if that runs after
the Put.

## Spec lines involved
SPEC "Jobs" (retries: skip only when another consolidation is pending), "Operations" (progress at
checkpoints; Cancel), "Behavioural rules" (DeleteBank); decision 500.

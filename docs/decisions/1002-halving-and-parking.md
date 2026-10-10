# 1002 — Halving and parking never park during a gateway outage

## Context
The spec says "A failing LLM batch is halved and retried; a unit that fails alone is parked with
consolidation_failed_at." The gateway reports two kinds of failure with the same class,
`domain.ErrUnavailable`, so they can't be told apart (decisions 202, 301):
- invalid output after its retries;
- a gateway that is down.

No port un-parks a unit except an edit or a retag. If an outage parked units, their observations would
be lost.

## Decision
- **What halves.** A failed CONSOLIDATE call halves the batch and decides each half. Only CONSOLIDATE
  call errors do this. Read, write, embedding and routing errors, and cancellation, stop the run, and the
  job retries.
- **A unit that fails alone is deferred, not parked.** The next CONSOLIDATE call that succeeds parks
  every deferred unit. A success proves the gateway works, so the failure is the unit's.
- **The run fails if no later call succeeds.** If a top-level batch ends with deferred units, the run
  fails with `ErrUnavailable` and parks nothing; committed work stays. Consolidate retries indefinitely
  with backoff. On the retry the poison unit is the oldest unconsolidated unit, so it leads a batch, fails
  alone, and is parked when a sibling succeeds.
- **Known cost.** A poison unit that is the bank's only unconsolidated unit keeps the job retrying, with
  capped backoff, until another unit arrives. It is not parked. That is the price of never losing data to
  an outage.
- **Outage cost.** An outage costs at most one halving tree, 2n-1 calls for a batch of n, before the run
  fails.

## Alternatives
- **Park whenever a unit fails alone (literal reading).** Rejected: an outage would park every unit of
  the run.
- **Park once any call of the run has succeeded.** Rejected: an outage that starts mid-run would still
  park everything after it.
- **Park after N job attempts.** Rejected: during a long outage it parks units one attempt at a time.

## Spec lines involved
Engines (Consolidator doc), Jobs (Retries: consolidation retries indefinitely), card WP-10 ("halve a
failing batch; park a unit that fails alone").

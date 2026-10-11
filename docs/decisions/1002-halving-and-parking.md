# 1002 — Halving and parking: probe, retry alone, then park

## Context
The spec says "A failing LLM batch is halved and retried; a unit that fails alone is parked with
consolidation_failed_at." The gateway reports two kinds of failure with the same class,
`domain.ErrUnavailable`, so they can't be told apart (decisions 202, 301):
- invalid output after its retries;
- a gateway or model that is down.

No port un-parks a unit except an edit or a retag. If an outage parked units, their observations would
be lost. On the other hand, a poison unit must not block its scope or the bank (review round 1, H1).

## Decision
- **What halves.** A failed CONSOLIDATE call halves the batch and decides each half. Only CONSOLIDATE
  call errors do this. Read, write, embedding and routing errors, and cancellation, stop the run, and the
  job retries.
- **A unit that fails alone is deferred.** It is left out of the rest of its top-level batch. When that
  batch is done, `settle` handles every deferred unit.
- **settle uses the failing scope's binding (round 2, H2).** It resolves the CONSOLIDATE binding through
  the scope's config (strategy < request), so a healthy default model never vouches for a broken
  strategy model. Deferred units never cross top-level batches, so they all share that binding.
  1. **Health probe.** One CONSOLIDATE call with no facts and no observations; its per-call schema (an
     empty `source_fact_ids` enum) allows no action. If the probe fails, the run fails with
     `ErrUnavailable` and nothing is parked; committed work stays, and the job retries with backoff.
  2. **Retry alone (round 2, H3).** After a successful probe, each deferred unit is retried alone once
     with the same binding. A success applies normally. Only a unit that fails again is parked. So a
     gateway that recovers right after a dead batch parks nothing.
- **Circuit breaker (round 2, M5).** If two top-level batches in a row fail on every call, with no
  successful CONSOLIDATE call between them, the run fails with `ErrUnavailable` instead of probing and
  parking again. Probes don't count as successes; retries that succeed do, and reset the count.
- **Park re-checks the units (round 2, M4).** The park transaction takes `tx.Seq` first, then re-reads
  the units. One edited, retagged or removed since the plan is not parked; it is left for the next run.
- **Outcomes.**
  - A poison unit alone in its scope is parked on the first run, and the other scopes consolidate.
  - A poison unit that is the bank's only unconsolidated unit is parked, so the job terminates.
  - An outage parks nothing, whether it starts at the first call or mid-run, and whether it hits the
    default model or one strategy's model.
  - A gateway that recovers right after a dead batch parks nothing either.

## Alternatives
- **Park whenever a unit fails alone (literal reading).** Rejected: an outage would park every unit of
  the run.
- **Park on the next success anywhere (round 1).** Rejected in review: another binding's success, or a
  recovery right after a dead batch, would park healthy units.
- **Park after N job attempts.** Rejected: during a long outage it parks units one attempt at a time.

## Spec lines involved
Engines (Consolidator doc), Jobs (Retries: consolidation retries indefinitely), Model routing (strategy
bindings), card WP-10 ("halve a failing batch; park a unit that fails alone").

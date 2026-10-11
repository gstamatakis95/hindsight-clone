# 1801 — Re-check the scopes this run wrote before stamping a unit

## Context
Final review B, H2. A unit is stamped `consolidated_at` in the transaction that writes its last scope.
Before stamping, `stampable` re-checked only two things: that the unit is live (same seq and tags) and
that the scopes the plan skipped (already citing it) still cite it. It did not re-check the earlier
scopes that this run wrote.

Several things can delete an observation the run wrote in an earlier scope, between that scope and the
last one:
- a ClearObservations of that scope;
- an invalidate of a co-source (DeleteCiting);
- a retag of another document whose unit is a co-source (ClearDocumentUnits).

These reset `consolidated_at` on the unit but don't change its seq, so `liveFacts` doesn't see it. The
unit was then stamped with no observation in that scope. The scope was never re-decided, and the derived
knowledge was lost silently.

## Decision
- `applyOutput.cited` returns the facts that the actions which actually landed cite. Skipped actions
  are not counted. After each apply, `run.recordCited` adds the batch's scope to `r.cited[unit]`, with no
  duplicates and only for planned units. `r.cited` now means "scopes that cite the unit": either skipped
  at plan time or written by this run.
- `stampable`, in apply (at a unit's last scope) and in `finish`, already re-checks
  `EvidenceRepo.Cites` for every scope in `r.cited`. Both run after `tx.Seq`, which locks the bank row,
  so no clear can commit between the check and the stamp.
- A unit that fails the check is left unstamped. `finish` then sees unconsolidated units, enqueues the
  continuation and reports `More=true`. The continuation's plan skips the scopes that still cite the unit
  and re-decides only the cleared one.

## Alternatives
- **Bump the seq of reset co-sources in DeleteCiting / DeleteScopes.** Rejected: that is a change to a
  frozen adapter contract, and it would make an edit-style "changed" fact out of a unit whose content
  did not change.
- **Stamp each unit per scope.** Rejected: there is no per-scope stamp column (frozen schema).

## Spec lines involved
Jobs table (consolidate: every scope of each unit, stamped in its last scope's transaction),
Behavioural rules (ClearObservations, Invalidate, UpdateDocument reset sources for re-consolidation).

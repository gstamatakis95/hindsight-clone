# 503 — PruneOperations takes a Clock

## Context
SPEC "Function signatures" lists `PruneOperations(ctx, s port.Store, olderThan time.Duration)`. To turn
`olderThan` into a cut-off it would have to call `time.Now`, but AGENTS.md says logic never calls
`time.Now` and always uses an injected Clock. `internal/worker` is not a frozen path.

## Decision
The signature is `PruneOperations(ctx, s port.Store, c port.Clock, olderThan time.Duration) (int, error)`.
It deletes terminal operations that finished before `c.Now() − olderThan`, through `Queue.Prune` in one
short transaction. The maintenance tick prunes lifecycle events the same way, through `EventRepo.Prune`.

## Alternatives
- Keep the spec signature and call `time.Now`. Rejected: it breaks the clock rule and makes the prune
  untestable.
- Take a `before time.Time`. Rejected: the duration form is closer to the spec.

## Spec lines involved
SPEC "Function signatures" (worker: `PruneOperations`), "Behavioural rules" (terminal operations are
pruned after a configurable retention period).

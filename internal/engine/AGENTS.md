# AGENTS.md — internal/engine

Engines hold memd's business logic: retain, recall, consolidate, reflect, mentalmodel, bank, curate,
docs, maintenance, timex and routing. These rules add to the root AGENTS.md.

## Boundaries
- Depend on ports only: stdlib, `memd/internal/domain`, `memd/internal/port` and other engine packages.
- Never import `internal/adapter`, `internal/server`, `internal/worker`, `memd/gen`, pgx, `net/http`
  or the AWS SDK. depguard fails the build if you do.
- Engines never import each other in a cycle. When you need another engine, declare the small
  interface you consume in your own package (consumer-declared), e.g. mentalmodel declares the
  `Reflector` it calls and docs declares the `Invalidator` subset it needs. Build order: recall
  first; consolidate, reflect and mentalmodel may import recall; mentalmodel may import reflect;
  nothing imports mentalmodel.
- A consumer-declared interface must use the producer's exact parameter and result types (named
  types from another package are not interchangeable), so the producer's interface is assignable to it.

## Time
- Inject `port.Clock`; never call `time.Now` (or `time.Since`, `time.After` for logic). Recency,
  debounce, interval floors and budgets must be deterministic in tests.

## Models
- Every LLM call goes through `port.ModelRouter.Resolve(cfg, slot)` with its model slot, then
  `port.LLM`. Never hard-code a model name or pick a binding yourself.
- Never call the LLM, embedder or reranker inside `Store.InTx`: read, call the gateway, then write in
  a short transaction.
- Prompts live in `internal/prompts/templates`; render them by name. No prompt text in engine code.

## Writes
- A transaction that changes memory content calls `tx.Seq(ctx, bank)` and stamps every unit,
  observation and event it writes with that seq.
- Jobs a change causes are enqueued through `tx.Queue()` in the same transaction as the change.

## Shape
- Each package owns small interfaces. Constructors take interfaces and return the package's
  interface, never a concrete type.
- Put `var _ Iface = (*impl)(nil)` next to each implementation.
- Every exported identifier, interface method and struct field has a doc comment; every package has
  a `doc.go`. Lines are at most 120 columns; McCabe complexity at most 15.

## Tests
- Test through the package interfaces against real Postgres (`pgtest.NewStore(t)`, one database per
  test) with the LLM, embedder, reranker, blob and clock fakes in `internal/fake` and an injected Clock.
  Postgres judges due-ness and heartbeats by its own clock: move run_at or heartbeat_at with SQL rather
  than advancing a fake clock. Mocks come from mockery (`internal/mocks`); never hand-write one.
- Each `_test.go` sits next to the file it tests; run with `-race`.

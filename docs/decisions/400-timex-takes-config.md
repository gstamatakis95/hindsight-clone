# 400 — TimeNormalizer takes the effective BankConfig

## Context
SPEC "Model routing" makes TIME_PARSE overridable per request: on RetainRequest, RecallRequest and (via
UpdateMemory's rederive) the EXTRACT / TIME_PARSE models. `port.ModelRouter.Resolve(cfg, slot)` needs the
merged config to pick that binding. But the WP-00 `timex.TimeNormalizer` methods,
`Normalize(ctx, f, ref)` and `QueryWindow(ctx, query, ref)`, take no config, and
`NewNormalizer(llm, router, clock)` is a long-lived constructor. With that shape the binding could only be
fixed at construction, so a per-request TIME_PARSE override would be silently ignored, which the spec
forbids ("never silently ignored").

## Decision
`internal/engine/timex` is owned by WP-04 and is not a frozen path. Both methods gain a
`cfg domain.BankConfig` parameter (the caller's already-merged config):

```go
Normalize(ctx context.Context, cfg domain.BankConfig, f *domain.ExtractedFact, ref time.Time) error
QueryWindow(ctx context.Context, cfg domain.BankConfig, query string, ref time.Time) (*domain.TimeWindow, error)
```

The binding is resolved lazily, only after a cue is found and no rule matched. So a query with no cue
never touches the router or the LLM, and an unbound TIME_PARSE slot can't fail a query that the rules
can answer. The mock in `internal/mocks/engine/timex` was regenerated.

WP-08 (retain) and WP-09 (recall) call it with the config they already resolved.

## Alternatives
- Carry the config on the context. Rejected: hidden coupling, and easy to forget.
- A CCR on the frozen ports. Not needed: the interface lives in an engine package.
- Bind TIME_PARSE at construction from server defaults. Rejected: it breaks per-request overrides.

## Spec lines involved
Model routing (TIME_PARSE row; "only when a cue is found and rules fail"), Per-request overrides
(Retain, Recall, UpdateMemory rows), Engines (TimeNormalizer), Function signatures (timex).

# 302 — Gateway concurrency limits and usage recording

## Context
The spec says: "The gateway client holds a process-wide limit plus an optional per-slot limit (as Hindsight
does), so adding worker replicas can't push the gateway into 429s." It also says: "Every LLM call records
slot, model and tokens on the operation." Embedding and rerank calls go to the same gateway, but
port.Embedder returns no usage.

## Decision
- **What is limited.** Every HTTP call the client makes (chat, embeddings, rerank) takes a permit from the
  process-wide semaphore, `Config.MaxConcurrent` (default 32). `Config.SlotConcurrency` can also cap a slot.
  Embeddings run under `ModelSlotEmbed` and rerank under `ModelSlotRerank`, so they can be capped too.
- **Order and scope.**
  - The slot permit is taken first, then the global one, so a saturated slot never holds global capacity
    while it waits. Hindsight also takes the per-op permit before the global one.
  - Permits are held for one HTTP attempt only and released before a retry's backoff. Hindsight does the
    same ("permits for one upstream attempt").
  - Waiting respects the caller's ctx.
- **Usage.**
  - Structured and Chat return their usage, summed over attempts. Its model is the one the gateway reported,
    else the binding's model. Engines attach this usage to operations.
  - `Config.OnUsage`, if set, is called once per successful HTTP call of any kind (chat, embeddings, rerank),
    for metrics.
  - Embedding and rerank usage can't reach operations through the ports, because those calls return none.
    wire.go can route `OnUsage` wherever it wants.
  - This needs no contract change: the spec's per-operation accounting covers LLM calls.

## Alternatives
- Limit only chat calls, as Hindsight's LLM semaphore does: rejected. Embeddings and rerank hit the same
  gateway and count toward its 429s.
- A semaphore from golang.org/x/sync: not needed. A buffered channel does the same with a ctx-aware select.

## Spec lines involved
Model routing ("Concurrency", "Usage accounting"); port.LLM doc ("process-wide semaphore plus one per slot");
domain.ModelSlotEmbed ("usage accounting only").

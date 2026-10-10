# 1200 — Synthesizer shape: Delta returns a draft, constructors take ModelDeps, agentic delta

## Context
SPEC (Engines, Function signatures) gives `Synthesizer.Delta(...) ([]EditOp, error)`,
`NewAgenticSynthesizer(rf Reflector)` and `NewSingleShotSynthesizer(rc, llm, router)`. A delta also has to
report the new source ids (they accumulate on the model), usage, tool calls and retrieval counts for the
trace, and the reject-and-retry-once rule needs the current document's ids to re-prompt the model. The
agentic strategy must also honour delta mode (SPEC "Both honour full / delta"), which the reflect loop
alone can't: it writes a document, not edit ops.

## Decision
- `Delta` returns `DeltaDraft{Ops, ContentMD, SourceIDs, Usage, ToolCalls, FactsRetrieved}`. The
  synthesizer applies its ops (`applyEdits`) so it can retry once with `DeltaData.Rejected` when every op
  was rejected; after the retry an all-rejected result returns the draft plus an error wrapping
  `ErrAllOpsRejected`, and the runner keeps the document. Partially applied ops are kept (only the
  all-rejected case is a failure, per SPEC 2001).
- Both constructors take `ModelDeps{LLM, Router, Clock, Directives}`: the agentic delta needs the
  MENTAL_MODEL_DELTA call too, and SYNTHESIZE renders the bank's directives.
- **Agentic delta** (Hindsight's flow, #5272): the reflect loop runs over the window
  (`Seq = (last_seq_seen, S]`), then the memories it cites go to the same MENTAL_MODEL_DELTA call the
  single-shot strategy uses. The loop's own draft is not shown to the delta call.
- **Agentic overrides.** The reflect request carries the already-merged effective config as `Overrides`.
  Merging an effective config again is idempotent, and it keeps the model's stored overrides, the trigger's
  knobs and the call's overrides in one value.
- `ErrNoNewFacts` lets a synthesizer say retrieval found nothing; the runner records NO_NEW_FACTS.
- SYNTHESIZE has no response schema (its template outputs the page), so it is a tool-less `LLM.Chat`.

## Alternatives
- Runner-side retry: it would redo the reflect loop or recall to re-prompt. Rejected.
- A separate agentic delta prompt over the loop's draft: Hindsight removed that (#5272) because the
  draft covers only the window and invites the model to rewrite the document.

## Spec lines involved
Engines (Synthesizer), Function signatures (mentalmodel), Behavioural rules (two strategies, delta never
partial), Model routing (SYNTHESIZE, MENTAL_MODEL_DELTA).

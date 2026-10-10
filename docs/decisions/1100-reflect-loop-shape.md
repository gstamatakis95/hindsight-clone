# 1100 — Reflect loop shape and failure classes

## Context
SPEC gives the loop's rules (max_iterations scaled by budget, retrieve before done, strict failure, malformed
calls back to the model) but not its exact turn structure. Hindsight's `reflect/agent.py` adds a forced tool
sequence, retries on LLM errors, a proactive context guard, map-reduce synthesis and a length rewrite.

## Decision
- **Iterations.** `max_iterations = max(1, base x {low 0.5, mid 1, high 2})`, base = `ReflectConfig.max_iterations`
  (default 10), unspecified budget = low (the Reflect RPC default). The loop makes at most that many
  tool-calling turns (`ChatRequest.Name = reflect_system`, tool_choice `auto`). If none of them ends in an
  accepted `done`, one closing turn (`reflect_done_request`, tool_choice `done`) asks for the answer; if the
  model doesn't comply, a tool-less synthesis call (`reflect_final`) answers from the gathered tool results.
  `Trace.Iterations` is the last loop turn reached. (Hindsight counts the closing call as the last of N; ours
  is N tool turns + 1, which is easier to state and test.)
- **No forced tool sequence.** Hindsight forces search_mental_models → search_observations → recall with named
  tool_choice. memd offers everything with `auto` and relies on the evidence guard (decision 1102). Known gap.
- **Plain text.** A turn with no tool call fails the call with `UNAVAILABLE` if the model has not made a single
  tool call yet ("a model that cannot drive tool calls"). After at least one tool call, prose ends the loop:
  the closing turn / synthesis produces the answer (stop reason `answered`); the prose itself is discarded.
- **Error classes.**
  - Back to the model as `{"error": ...}` (the loop continues): unknown or disabled tool, arguments that are
    not JSON for the tool, a missing query or id list, a query over recall's 500-token limit (checked by the
    tool before calling recall), a malformed `done` (bad JSON, missing answer/document) and `done` before any
    retrieval. An `ErrInvalidArgument` *from* recall is not sent back: it can also mean a misconfiguration (an
    unbound RERANK slot), and bouncing it would let reflect answer with no evidence.
  - Fail with `UNAVAILABLE`: any error from Recaller, PageReader, Reader or ChunkReader (database,
    embedder, reranker, configuration), any REFLECT chat error in a loop turn other than a context overflow
    (the gateway already retried),
    a model that never calls a tool, and an empty final answer (a `done` whose answer is blank, or a blank
    synthesis). These are failed dependencies from the caller's point of view.
  - Fail with `RESOURCE_EXHAUSTED`: input + output tokens of every LLM call so far (REFLECT and
    STRUCTURED_OUTPUT, failed calls included when the gateway reports their usage) above
    `max_total_tokens` (nil or <= 0 = unlimited).
  - The caller's own cancellation is returned as is.
- **Closing turn failure.** The closing `done` turn is optional and retrieves nothing, so if it fails for any
  reason other than the caller's ctx or the token budget, reflect synthesises instead (Hindsight does the
  same; it also covers OpenAI-compatible backends that reject a named tool_choice).
- **Prompt date.** The "Current Date and Time" section shows the request's `query_time` (else the clock's
  now), since `query_time` is the reference time for relative dates; Hindsight always shows the wall clock.
- **Several calls in one turn** run sequentially; tool messages follow the model's order. Blank or repeated
  call ids get unique replacements. A `done` in a turn wins and the turn's other calls are dropped (Hindsight).
- **Answer length.** `max_tokens` / `max_answer_tokens` is the soft length target in the prompts
  (`ReflectData.MaxTokens`), as in Hindsight; no hard output cap and no rewrite pass.

## Alternatives
- Retrying a failed chat turn (Hindsight retries twice): rejected, the gateway already retries transient
  errors with backoff, and SPEC makes reflect strict.
- Hindsight's map-reduce synthesis and proactive context guard: rejected for v1 (no context-size knob in
  ReflectConfig); overflow handling is decision 1101.

## Spec lines involved
Behavioural rules > Reflect; ReflectConfig (max_iterations, require_evidence, max_answer_tokens,
max_total_tokens); ReflectRequest.budget ("default BUDGET_LOW"); Engines > Reflector.

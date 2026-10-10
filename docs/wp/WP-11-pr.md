# WP-11 — Reflect engine

Branch `wp/WP-11-reflect`. Owns `internal/engine/reflect/**` (plus its generated mocks and the docs below).
Plan: `docs/wp/WP-11.md`.

## Built
- [x] Tools `search_mental_models`, `read_mental_models`, `search_observations`, `recall`, `expand` (`DefaultTools`)
  and `done` (built into the loop; plain, directive, document and document+directive variants from the
  prompt assets, the directive ones read through `prompts.DoneTool`). Consumer interfaces: `Recaller`
  (recall), `PageReader` (pages), `ChunkReader` (chunks, CCR WP-11-2).
- [x] The loop: `max_iterations` scaled by budget (LOW halves, HIGH doubles, unspecified = LOW, never below 1),
  then a closing `done` turn, then a tool-less synthesis; must retrieve before done (`require_evidence`,
  default on); citations it never retrieved are dropped.
- [x] A malformed tool call (unknown tool, bad JSON, missing query/ids, an over-long query, malformed done)
  goes back to the model as `{"error": ...}`.
- [x] Strict failure: UNAVAILABLE on retrieval errors (database, embedder, reranker, pages), on an empty
  answer and on a model that never calls a tool; a context-window overflow synthesises from what was gathered
  (stop reason `context_full`, older outputs dropped if the synthesis overflows too).
- [x] `max_total_tokens` → RESOURCE_EXHAUSTED (every REFLECT and STRUCTURED_OUTPUT call counts).
- [x] Directives selected by tags (untagged = global, tagged only on a matching tag filter, `apply_all`),
  `extra_directives` appended, never removing stored ones; rendered into the system prompt, the final
  synthesis prompt and the done tool.
- [x] `PromptBuilder` over the effective config (request overrides included), with disposition; the loop uses
  the same data (mission, disposition, directives, offered tools, budget, bank name, context, now).
- [x] The structured-output pass (STRUCTURED_OUTPUT slot, `structured_output_*` templates, fields in schema
  order); a failure lands in `structured_output_error` and the answer is kept. Trace (iterations, stop reason,
  directive ids, tool calls with arguments, outputs, errors and durations per include options) and usage
  aggregated per slot and model.
- [x] Document mode for the agentic mental-model refresh (`AsDocument`, `Context`): the structured `document`
  argument is rendered to markdown.

## Acceptance tests (`go test -race ./internal/engine/reflect`, all pass)
| Accept item | Test |
|---|---|
| Retrieval error → UNAVAILABLE (db, reranker, embedder, pages) | `TestReflectRetrievalErrorIsUnavailable` |
| Empty retrieval is not a failure | `TestReflectEmptyRetrievalIsNotFailure` |
| Empty final answer fails (done and synthesis) | `TestReflectEmptyAnswerFails` |
| Model that can't drive tools fails | `TestReflectModelWithoutToolCallsFails` |
| Context overflow synthesises from what it has | `TestReflectContextOverflowSynthesizes`, `TestReflectSynthesisFallbacks` |
| max_total_tokens → RESOURCE_EXHAUSTED (bank and request level) | `TestReflectMaxTotalTokensExhausted` |
| Malformed tool call goes back to the model | `TestReflectMalformedToolCallReturnsToModel` |
| Must retrieve before done | `TestReflectDoneBeforeRetrievalRejected` |
| Citation filtering | `TestReflectCitationFiltering`, `TestValidCitations` |
| Iteration cap per budget | `TestReflectIterationCapPerBudget` |
| Extra directives added, stored ones kept; tag selection | `TestReflectExtraDirectivesAdded`, `TestSelectDirectives` |
| Structured-output error in `structured_output_error` | `TestReflectStructuredOutputError` (success: `TestReflectStructuredOutput`) |
| PromptBuilder over the effective config, with disposition | `TestPromptBuilderUsesEffectiveConfig` |
| Trace and usage | `TestReflectTraceAndUsage` |
| Document mode | `TestReflectAsDocument`, `TestDocumentMarkdown` |
| Tools | `TestToolsRecallAndObservations`, `TestToolsMentalModels`, `TestToolExpand` |
| Validation, unknown bank | `TestReflectRequestValidation`; overflow detection `TestIsContextOverflow` |

Gates: `make gen` (no diff), `make lint BREAKING_AGAINST=contracts-v1` (0 issues), `make test` pass.

## Decisions and CCRs
- `docs/decisions/1100-reflect-loop-shape.md` — iteration counting, error classes, no forced tool sequence.
- `docs/decisions/1101-reflect-context-overflow.md` — overflow detected by provider error text meanwhile.
- `docs/decisions/1102-reflect-evidence-citations-directives.md` — evidence guard, BasedOn, directive selection.
- `docs/decisions/1103-reflect-api-additions.md` — additive API changes WP-12/WP-14 must know.
- `docs/decisions/1104-reflect-tool-defaults.md` — tool defaults, page scoping, chars/4 token estimate.
- `docs/ccr/WP-11-1.md` — `domain.ErrContextOverflow` (typed overflow from the gateway).
- `docs/ccr/WP-11-2.md` — `port.Reader.GetChunks` for expand (and recall include.chunks).

## Notes for WP-12 / WP-14
- `NewReflector(ReflectorDeps{Store, Config, Router, LLM, Clock, Tools})`; tools are
  `DefaultTools(recaller, pageReader, store.Read(), nil /* ChunkReader until CCR WP-11-2 */)`.
- `NewStructuredProjector(llm, router, configResolver)` (the spec's two-argument form can't resolve the binding).
- `NewPromptBuilder(clock)`.
- The agentic refresh should set `AsDocument`, `Context` (rendered `mm_refresh_context`), `Seq`, `Types`,
  `ExcludeMentalModels`/`ExcludeMentalModelIDs`, `RecallInclude` and the model's budget (refresh default MID).

## Known gaps
- No forced tool order (Hindsight forces mental models → observations → recall with named tool choice); the
  evidence guard is the only push to retrieve.
- Context overflow is recognised by error text until CCR WP-11-1; no proactive context-size guard and no
  map-reduce synthesis.
- `expand` returns no chunks until CCR WP-11-2 is implemented and wired.
- Token budgets of the page tools use a chars/4 estimate (no tokenizer in the module yet).
- The answer length is a prompt target only (no hard cap, no rewrite pass); recall's RERANK usage isn't in
  `ReflectResult.Usage` because `recall.RecallResult` carries no usage.

// Package reflect answers the Reflect RPC with a bounded tool-calling agent loop (REFLECT slot) over the
// bank's memory, and provides the STRUCTURED_OUTPUT projection used by reflect and mental-model refreshes.
//
// # Where it sits
//
// reflect is an engine package: it imports domain, port, prompts, jsonschema and recall (for its request and
// result types) and must never import adapters, server, worker, gen, pgx, net/http, the S3 SDK or Datadog
// (depguard). It never imports mentalmodel, which imports reflect; it declares the small [Recaller]
// (recall.Recaller satisfies it) and [PageReader] (search and read of mental-model pages, provided by
// cmd/memd from mentalmodel's PageSearcher) it consumes. cmd/memd/wire.go builds the [Reflector] with
// [NewReflector] and [DefaultTools]; the server's Reflect RPC and the agentic mental-model synthesizer call
// [Reflector.Reflect], and the refresh runner uses [NewStructuredProjector].
//
// # Flow of Reflect
//
//  1. Validate: an empty query, a malformed tag filter, or a response_schema_json that does not compile
//     (internal/jsonschema) or is not a JSON object is domain.ErrInvalidArgument.
//  2. Resolve the effective config with the request overrides, then read the bank's display name and active
//     directives in one short read transaction. Directives: inactive ones are skipped; with
//     apply_all_directives every active one applies; otherwise untagged directives always apply and a tagged
//     one applies only when the request's tag filter is satisfied by its tags (an untagged request gets
//     untagged directives only). The request's extra_directives are appended.
//  3. Prepare the run: the REFLECT binding; the offered tools (wired, enabled by reflect.enabled_tools,
//     allowed by the request's fact types: search_observations needs observations, recall needs a raw-fact
//     type; no page tools when mental models are excluded); the system prompt (reflect_system with mission,
//     disposition, directives, budget, tool flags, the bank name and "now"); and the done tool variant
//     (document form for as-document answers, directive form when a directive has rules, so the model must
//     report directive compliance). With require_evidence on and no search tool offered the call fails
//     fast with domain.ErrFailedPrecondition.
//  4. Loop up to max_iterations turns (reflect.max_iterations is the BUDGET_MID value, default 10; LOW,
//     the default budget, halves it and HIGH doubles it, never below 1). Until the model's first tool call
//     the tool choice is "required" (a prose reply is retried once; a model that never calls tools fails
//     with domain.ErrUnavailable); after that "auto". A turn with a done call ends the loop and drops the
//     turn's other calls; other calls run in order and their outputs go back as tool messages. A done before
//     any retrieval (with require_evidence) and a malformed done are sent back to the model with an
//     instruction; an unknown tool or malformed arguments are reported back to the model, not to the caller.
//  5. Finish without a done call (the model stopped calling tools, or the iteration cap): with
//     require_evidence and nothing retrieved, force one search turn; then, if the conversation ends on tool
//     results, ask once for the answer as a done call; otherwise, or if that fails, synthesize the answer in
//     one tool-less call (reflect_final) from the tool outputs gathered so far.
//  6. On a context-window overflow (domain.ErrContextOverflow from the gateway) the loop stops and
//     synthesizes; the synthesis drops the older half of the tool outputs and retries until it fits or
//     nothing is left. The stop reason is answered, max_iterations or context_full.
//  7. Build the result: BasedOn holds the done call's citations that some tool actually retrieved (ids the
//     agent never saw are dropped), or everything retrieved when the answer was synthesized; the applied
//     stored directive ids (never request-only extras); the trace (iterations, stop reason, tool calls and,
//     unless disabled, their outputs) and per-slot usage.
//  8. With response_schema_json set, one STRUCTURED_OUTPUT call projects the answer onto the schema. A failed
//     projection is reported in StructuredOutputError and the answer is still returned.
//
// # Tools
//
// [DefaultTools] returns the retrieval tools; a nil dependency leaves its tools out, and done is part of the
// loop. Tool names are the Tool* constants; their descriptions and parameter schemas come from the
// reflect_tools prompt asset.
//
//   - search_mental_models: ranks mental-model pages, returns the best one in full and snippets of the rest.
//   - read_mental_models: whole pages by id within a token budget.
//   - search_observations: a recall over observations only (reflect.search_observations_max_tokens, default
//     5000; entity state per reflect.search_observations_include_entities, default true).
//   - recall: a full recall over raw facts (default 2048 tokens).
//   - expand: the source facts behind observations and the chunks behind facts; it registers nothing as
//     retrieved, so its output can only be cited if a search returned the unit.
//
// Token arguments from the model are clamped to [1000, 16000]. Every retrieval honours the request's tag
// filter, fact types, seq bound and excluded models. A retrieval error is never answered around: it fails
// the call, keeping its class when it is already classified (cancellation, invalid argument, failed
// precondition, not found, unavailable) and otherwise as domain.ErrUnavailable.
//
// # Budgets and errors
//
// Every model call's usage is charged to the run; past reflect.max_total_tokens (unset = unlimited) the
// call fails with domain.ErrResourceExhausted (RESOURCE_EXHAUSTED). A blank final answer is
// domain.ErrUnavailable. The answer's soft length target is the request's max_tokens, else
// reflect.max_answer_tokens (default 4096). The query time defaults to port.Clock's now.
//
// # Internal callers
//
// [ReflectRequest] carries fields only the agentic mental-model refresh sets: Seq (bound every retrieval to
// the refresh snapshot), Types, ExcludeMentalModels / ExcludeMentalModelIDs (a page never reads itself),
// RecallInclude, AsDocument (the done tool takes a structured document rendered to markdown) and Context
// (refresh instructions added to the system prompt).
//
// [StructuredProjector.Project] resolves the bank's config with the request overrides for the
// STRUCTURED_OUTPUT binding and makes one structured call whose prompt lists the schema's top-level fields;
// the gateway validates the reply against the schema. [PromptBuilder] renders the same reflect_system prompt
// from a config and directives; it is not wired in production (the loop renders its prompt itself).
//
// # Invariants
//
// Reflect is read-only: it never writes and never opens a write transaction, so it must not be called inside
// Store.InTx. Tools only read through recall, the page reader and port.Reader.
//
// # Tests
//
// reflector_test.go and tools_test.go run the loop against fake.LLM with scripted turns over fake.Store:
// retrieval errors, empty retrieval, empty answers, models without tool calls, context overflow and
// synthesis fallbacks, the token budget, malformed calls, done before retrieval, citation filtering, the
// iteration cap per budget, extra directives, structured output and its failure, as-document answers, fact
// type limits, the closing done turn and fast failure without a search tool. SPEC "Engines" (reflect),
// "Behavioural rules" (Reflect) and "Model routing" (REFLECT, STRUCTURED_OUTPUT).
package reflect

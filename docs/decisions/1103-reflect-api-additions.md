# 1103 — Additions to the reflect package API

## Context
`internal/engine/reflect` is not frozen, but WP-12 (mentalmodel) and WP-14 (wiring) consume it. The spec's
signature list (`NewStructuredProjector(llm, router)`, `DefaultTools(rc, pages, r)`, `NewPromptBuilder()`) can't
express everything the card asks for.

## Decision
Additive changes only; no existing type or method changed shape.
- `ReflectRequest.Context string` (internal callers): additional context for the system prompt. The agentic
  mental-model refresh (WP-12) renders `prompts.MentalModelRefreshContext` and passes it here.
- `ReflectorDeps{Store, Config, Router, LLM, Clock, Tools}` and `NewReflector(d)`.
- `DefaultTools(rc Recaller, pages PageReader, r port.Reader, chunks ChunkReader)`: `chunks` may be nil
  (CCR WP-11-2); any nil dependency leaves its tools out.
- `NewPromptBuilder(clock port.Clock)`: the prompt shows the current time, and engines never call time.Now.
  `System(cfg, directives) string` keeps the spec signature and renders what a default reflect would send
  (every enabled tool, no budget section); a render failure returns "".
- `NewStructuredProjector(llm, router, config port.ConfigResolver)`: `Project` resolves the bank's effective
  config (with `ProjectRequest.Overrides`) for the STRUCTURED_OUTPUT binding. `Project` keeps its signature
  and does not return usage; the reflector calls the unexported `project`, which does, so
  `ReflectResult.Usage` has the STRUCTURED_OUTPUT entry. WP-12, if it needs that usage, can ask for an
  exported variant.
- `ToolResult.Error` (a malformed call's message, also in `Output` as `{"error": ...}`),
  `AgentState.Searched` (a retrieval tool ran; the evidence guard) and the `ChunkReader` interface.
- A `response_schema_json` that isn't a JSON object or doesn't compile with `internal/jsonschema` is
  `INVALID_ARGUMENT` before any model call. A schema the gateway can't satisfy, or output that doesn't
  match, sets `structured_output_error` and keeps the answer.

## Spec lines involved
Function signatures > internal/engine/reflect; ReflectResponse (structured_output_error, usage); Model
routing (usage accounting); AGENTS.md Time rule.

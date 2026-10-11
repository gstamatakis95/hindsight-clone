# WP-14a plan — E2E harness and emulated gateway

Branch `wp/WP-14a-e2e-harness`. Owns `test/e2e/**`, the `e2e` target of the root Makefile, decisions
1410-1449 and `docs/wp/WP-14a*.md`. WP-14b builds the scenarios (and `cmd/memd`) on top.

## Files

| Area | Files |
|---|---|
| Emulator | test/e2e/gatewayemu/{doc,server,faults,chat,structured,reflect,prompttext,schemagen,vectors}.go |
| Emulator tests (untagged, run in `make test`) | test/e2e/gatewayemu/{smoke,prompttext,server,helpers}_test.go |
| Harness | test/e2e/harness/{doc,compose,memd,client}.go, test/e2e/harness/compose.e2e.yml |
| E2E tests (`-tags e2e`) | test/e2e/stack_test.go |
| Package doc | test/e2e/doc.go |
| Makefile | the `e2e` target only |
| Decisions | docs/decisions/1410-gateway-emulator-routing.md, 1411-e2e-memd-process-contract.md, 1412-e2e-compose-isolation.md |

## Interfaces

- `gatewayemu.Server` implements `http.Handler` (`var _ http.Handler = (*Server)(nil)`); it serves
  `/v1/chat/completions`, `/v1/embeddings` and `/v1/rerank`, the routes `internal/adapter/gateway` uses.
- Structured calls are recognised by `response_format.json_schema.name` (the prompt template name);
  plain and tool chats by the slot model name (`emu-<slot>`, set through `MEMD_MODEL_<SLOT>`) and the tools.
- The harness is a library: `harness.StartStack`, `harness.BuildMemd`, `(*Stack).StartMemd`,
  `(*Proc).Kill/Stop/Restart`, `harness.Dial`, `(*Client).WaitIdle`.

## Acceptance tests

| Accept item | Test |
|---|---|
| Emulator returns schema-valid JSON for every prompt schema in `internal/prompts` (through the real gateway client, validated with `internal/jsonschema`) | `gatewayemu.TestSmokeEveryPromptSchema` |
| Extraction with optional dimensions, structured output with caller schemas | `gatewayemu.TestSmokeExtractOptionalDimensions`, `gatewayemu.TestSmokeStructuredOutputCallerSchemas` |
| Reflect: one recall call, then done quoting retrieved text and citing ids (every done variant) | `gatewayemu.TestSmokeReflectLoop` |
| Synthesize, markdown delta and reflect final (plain chat) | `gatewayemu.TestSmokePlainChat` |
| Embeddings: 768 dims, deterministic, shared-token similarity; rerank by token overlap | `gatewayemu.TestEmbeddingsSimilarity`, `gatewayemu.TestRerankOverlap` |
| Consolidation decide: one observation per fact, citing its id, exact quote | `gatewayemu.TestConsolidateOnePerFact` |
| Prompt parsing round-trips the rendered templates | `gatewayemu.TestPromptTextRoundTrip` |
| Scripted overrides: fail N times, delay (cancellable), invalid JSON | `gatewayemu.TestFaults` |
| Harness brings compose up and down cleanly, nothing left behind (`-tags e2e`) | `e2e.TestStackUpDown` |
| Harness skips with a clear reason until cmd/memd exists (`-tags e2e`) | `e2e.TestMemdSmoke` |
| Final check | `make gen && make lint && make test`; `make e2e` runs and leaves no containers or volumes |

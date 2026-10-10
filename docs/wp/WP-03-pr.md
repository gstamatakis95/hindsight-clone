# WP-03 · Gateway and S3 adapters

Branch `wp/WP-03-gateway-s3`. Owns `internal/adapter/gateway/**` and `internal/adapter/s3/**`. Plan:
`docs/wp/WP-03.md`. No frozen path is touched; `go.mod`/`go.sum` add aws-sdk-go-v2 (s3, credentials),
smithy-go, testcontainers-go and golang.org/x/sync, all on the approved list. x/sync is pinned to v0.22.0,
because v0.24.0 raises the `go` line to 1.26.

## Build
- [x] `Structured`: chat completions with `response_format: json_schema`. The output is validated against the
  schema, and invalid output is retried with the validation error, bounded by the binding's `max_retries`.
  `timeout_ms` is honoured per attempt. Usage is summed over attempts.
- [x] `Chat` with tools, `tool_choice` (keyword or tool name) and tool calls. Malformed arguments
  round-trip unchanged.
- [x] `EmbedDocuments` / `EmbedQuery`: batched (`EmbedBatchSize`, default 64) and reordered by `index`, with
  the `search_document: ` / `search_query: ` prefixes. Every vector is checked to have 768 dimensions.
- [x] `Rerank`: Cohere/Jina body on `/v1/rerank`. Scores pass through when all are in [0,1]; otherwise a
  sigmoid applies to every score. Unscored or NaN scores become 0.
- [x] Concurrency: a process-wide semaphore plus optional per-slot semaphores (slot first, then global),
  held per HTTP attempt. They cover chat, embeddings (EMBED slot) and rerank (RERANK slot).
- [x] Usage: Structured and Chat return it, and the optional `Config.OnUsage` hook is called once per
  successful HTTP call of any kind.
- [x] Errors: gateway failures wrap `domain.ErrUnavailable`. 408, 429, 5xx, transport errors and attempt
  timeouts retry; other 4xx responses don't. The caller's ctx error is returned as is.
- [x] S3 `Blob` over aws-sdk-go-v2. `DeletePrefix` lists with ListObjectsV2 and deletes each page with a
  quiet DeleteObjects batch.

## Acceptance tests (all pass, `go test -race`)
| Card item | Test |
|---|---|
| Schema retry | `TestStructuredRetriesWithValidationError`, `TestStructuredGivesUpAfterMaxRetries`, `TestStructuredRequestBody` |
| Tool calls | `TestChatToolCalls`, `TestChatMalformedToolArgumentsRoundTrip` |
| Timeouts | `TestTimeoutHonoursBindingTimeoutMS` (per-attempt timeout retried; caller deadline not retried) |
| Transient vs permanent errors | `TestTransientErrorsRetry`, `TestClientErrorNotRetried` |
| Prefixes, batching, 768 dims | `TestEmbedDocumentsPrefixAndBatches`, `TestEmbedQueryPrefix`, `TestEmbedRejectsWrongDimension` |
| Normalization rule both ways | `TestRerankNormalization` (pass-through, boundaries, one logit, >1), `TestRerankUnscoredIsZero` |
| Semaphore caps in-flight under 100 callers | `TestGlobalSemaphoreCapsInFlight` (peak == limit 5), `TestSlotSemaphoreCapsInFlight` (slot peak == 2, global <= 12) |
| Usage per call | `TestUsageHookPerCall` |
| Schema validator | `TestValidateSchema`, `TestCompileSchemaRejectsBadSchema`, `TestSchemaRefLoopBounded`, `TestStructuredRejectsMissingSchema` |
| S3 suite on MinIO testcontainer | `TestBlobContract` (`porttest.RunBlobSuite`, 7 subtests) |
| DeletePrefix over 2,500 keys | `TestDeletePrefix2500Keys` |
| Extra S3 checks | `TestPutNonSeekableReader`, `TestDeletePrefixEmpty`, `TestMissingBucketUnavailable`, `TestNewBlobRequiresBucket` |

The gateway tests use an `httptest` server that emulates `/v1/chat/completions`, `/v1/embeddings` and
`/v1/rerank`. The S3 tests start one MinIO container per test binary (the same image as docker-compose) and
terminate it.

## Decisions and CCRs
- `docs/decisions/300-gateway-wire-contract.md`
  - `Config` naming, because revive's stutter check rejects `GatewayConfig`/`S3Config`.
  - BaseURL includes `/v1`.
  - No `strict` in json_schema.
  - `max_tokens`.
  - `extra` is applied last.
  - Tool choice mapping.
  - Arguments travel as strings.
- `docs/decisions/301-gateway-retries-timeouts-errors.md`
  - The retry feedback is the validator's error text, so no prompt prose lives in Go.
  - One retry budget.
  - The timeout is per attempt.
  - Error mapping.
- `docs/decisions/302-gateway-concurrency-and-usage.md`: what the semaphores cover; the `OnUsage` hook.
- `docs/decisions/303-json-schema-subset.md`: a hand-written validator for the structured-output subset.
- `docs/decisions/304-s3-adapter.md`
  - The client is built without `LoadDefaultConfig`.
  - Checksums are sent only when required.
  - Non-seekable readers are buffered.
  - An empty prefix is rejected.
- CCRs: none.

## Known gaps
- **Context-window overflow.** It isn't distinguishable through the ports: it surfaces as a non-retried
  `ErrUnavailable` carrying the gateway body. WP-11 needs a typed domain error, through a CCR, if it wants to
  synthesise on overflow.
- **Embedding and rerank usage.** It reaches only `Config.OnUsage`, not operations, because
  port.Embedder/Reranker return no usage.
- **JSON Schema keywords.** Keywords outside the subset in decision 303 are ignored, notably `pattern`,
  `format` and tuple `items`.
- **Local lint toolchain.** `~/go/bin/golangci-lint` is v1 built with go1.25, which comes before the v2.14
  binary in `/opt/homebrew/bin` on PATH, so a plain `make lint` fails to load the config. The lint gates
  were run with the v2 binary directly: golangci-lint, ifacedoc, buf lint, buf breaking against
  contracts-v1, and ruff.

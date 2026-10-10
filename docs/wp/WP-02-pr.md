# WP-02 · Fakes

In-memory fakes for every port in `internal/fake`, for engine unit tests. The store and queue pass the same
porttest suites as the Postgres adapter.

## Built
- [x] In-memory Store/Tx with Seq and real isolation. Write transactions are serialized, each works on a
  private copy and commits by atomic publish. `Read()` and `Alive` are lock-free over the committed
  snapshot. All repos are covered: banks, units and archive, documents, chunks, entities, links, evidence,
  events, mental models, directives. All four Reader arms are implemented.
- [x] Queue (`tx.Queue()`) and Claimer (on `*Store`) with the spec's claim rules:
  - serial keys, run_at, dedupe/joinable and Upgrade merge;
  - cancel-requested flag, fencing, reclaim with max attempts, OPERATION_FAILED events;
  - tombstoned banks, Get/List/Cancel/Retry/Prune.
- [x] LLM fake:
  - scripted replies per `(slot, prompt name)`: FIFO, with the last reply sticky and `""` as a per-slot fallback;
  - records calls;
  - `Fail`, `Timeout`, `InvalidJSON`, and malformed tool calls pass through.
- [x] Embedder: deterministic 768-dim unit vectors from a sha256 stream (`EmbedHash`). `EmbedSimilar` builds
  vectors from shared token buckets, so texts sharing words are close and disjoint texts are orthogonal.
- [x] Reranker with per-(query, doc) and per-doc scripted scores, an optional scoring func, a default of
  token overlap, and the adapter's sigmoid rule.
- [x] Blob, and a controllable Clock (`NewClock`, `Set`, `Advance`).
- [x] Engine-author guide in `doc.go`, plus runnable examples.

## Acceptance tests
| Card item | Test(s) |
|---|---|
| Same porttest suites pass against the fakes | `TestStoreContract`, `TestSeqContract`, `TestQueueContract`, `TestBlobContract` (124 store/seq/queue subtests + 7 blob) |
| Documented well enough to use without reading the code | `internal/fake/doc.go`; `ExampleStore`, `ExampleStore_claimer`, `ExampleLLM`, `ExampleEmbedder`, `ExampleReranker`, `ExampleClock` |
| LLM: scripted, recorded, fail / time out / invalid JSON | `TestLLMFIFOAndStickyLast`, `TestLLMNameFallback`, `TestLLMNotScripted`, `TestLLMErrors`, `TestLLMInvalidJSONIncludesBody`, `TestLLMChatMalformedToolCall`, `TestLLMUsageDefaults`, `TestLLMCallsRecorded`, `TestLLMCancelledDoesNotConsume` |
| Embedder | `TestEmbedderShape`, `TestEmbedderDeterministicAndQueryEqualsDocument`, `TestEmbedderHashModeUnrelated`, `TestEmbedderSimilarMode`, `TestEmbedderFailWithAndCalls` |
| Reranker | `TestRerankerPrecedence`, `TestRerankerSigmoid`, `TestRerankerFailWithAndCalls` |
| Blob, Clock | `TestBlobSizeMismatch`, `TestBlobKeysAndContentType`, `TestBlobStoresCopies`, `TestBlobCancelledContext`, `TestClockDefaultStart`, `TestClockSetAdvance`, `TestClockConcurrent` |
| Store isolation and clock-driven queue | `TestInTxHonoursContext`, `TestFrozenClockDrivesQueue`, `TestEventAppendNeedsTxSeq`, `TestCompleteCancelledCommits` |

Carried-forward items:
- Cancel-requested semantics: the contract cases CancelProcessing* and CancelStaleFinishedByReclaim, plus
  `TestCompleteCancelledCommits`.
- Fail(cause, retryAt) on a cancel-requested row ends CANCELLED: `TestFailWithRetryOnCancelRequested`.
- Tombstoned-bank Fail goes FAILED without an event or seq bump (Fail and ReclaimStale):
  `TestFailOnTombstonedBank`.
- Tenant isolation on every write port: `TestTenantIsolationWritePorts`, `TestTenantIsolationBankWrites`,
  and the contract TenantIsolation cases.
- Refresh with DedupeNone is treated as Pending: the contract RefreshDedupeNone case.
- Enqueue/Upgrade race (a claim never lands between a join and its Upgrade): `TestEnqueueUpgradeRace`.
- A Tx used after its InTx returned panics instead of writing into committed state:
  `TestTxUnusableAfterCommit`.

Gates: `make gen` (no diff), `make lint BREAKING_AGAINST=contracts-v1` (0 issues), and `make test`
(`go test -race ./...`) all pass. `go test -race -count=3 ./internal/fake/` is stable.

## Decisions and CCRs
- `docs/decisions/200-fake-store-isolation.md`: one store-wide write lock with copy-and-publish
  transactions. The InTx callback must not nest InTx or Claimer writes.
- `docs/decisions/201-fake-store-strictness-and-search.md`:
  - FK checks, the event-seq check, the embedding-dim check, and Limit<=0 = no cap;
  - BM25, temporal and outgoing-graph scoring;
  - the trigram threshold for Candidates is 0.3.
- `docs/decisions/202-fake-llm-errors.md`: InvalidJSON and Timeout both wrap `domain.ErrUnavailable`, which
  matches the merged WP-03 gateway.
- No CCRs. No porttest assertion was found wrong against the spec.

## Known gaps
- `internal/porttest/porttest_test.go` still has `TestStoreSuiteFake` etc. skipping with "pending WP-02".
  That path isn't owned by WP-02. The suites actually run from `internal/fake/store_test.go` and
  `blob_test.go`, so the placeholders can be deleted when convenient.
- Transactions are serializable rather than READ COMMITTED, and an InTx callback that opens another
  InTx deadlocks until its ctx ends (documented).
- Search scores approximate pg_search, pgvector and pg_trgm. Order and set semantics match the contract
  suites, but absolute keyword and temporal scores differ from Postgres.
- When WP-01 adds new `Run*` entry points to internal/porttest, wire them into
  `internal/fake/store_test.go` too. Today the fakes run all four: Store, Seq, Queue and Blob.
- The fake does not check that ids are UUIDs. Postgres's uuid columns would reject an id like "u1".
- Archive and observation-version and audit rows have no read port, so they are stored but not
  inspectable beyond DeleteBatch.

## Review round 1 fixes
| Item | Fix | Test |
|---|---|---|
| HIGH 1 Structured ignores req.Schema | Validator moved to `internal/jsonschema` (gateway imports it); fake rejects empty/bad schema (ErrInvalidArgument) and validates output | `TestLLMStructuredValidatesSchema`; moved `TestValidateSchema`, `TestCompileSchemaRejectsBadSchema`, `TestSchemaRefLoopBounded` |
| HIGH 2 self-link | ErrInvalidArgument | `TestLinkRejectsSelfLink` |
| MED 3 label upsert | resolve by (bank, kind, label), then update | `TestLabelEntityUpsertUpdatesExisting` |
| MED 4 Restore takes Seq | requires prior Tx.Seq (ErrFailedPrecondition) | `TestRestoreRequiresSeq` |
| MED 5 ctx cancelled during fn | not published; error wraps ctx.Err() | `TestInTxContextCancelledDuringFn` |
| MED 6 un-normalized tags | rejected on unit/document writes (decision 203) | `TestUnnormalizedTagsRejected` |
| LOW Blob.FailWith | added | `TestBlobFailWith` |
| LOW fact-type checks | Update, UpsertObservation, Evidence.Add | `TestFactTypeChecks` |
| LOW stopword note | EmbedSimilar doc | — |
| LOW SetConfig/MarkDeleting unknown bank | decision 203 | existing contract tests |

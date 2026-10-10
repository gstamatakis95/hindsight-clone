# WP-08 · Retain engine — PR

Branch `wp/WP-08-retain`. Package `internal/engine/retain` (plus regenerated `internal/mocks/engine/retain`).

## Built
- [x] Ingestor, sync and async (`NewIngestor(IngestorDeps)`):
  - [x] validates overrides-dependent rules and scope fan-out. ALL_COMBINATIONS over `max_combinations` is
    `ErrTooManyScopes` (INVALID_ARGUMENT) before anything is stored. It uses the wired `ScopeExpander`, else
    a local 2^n−1 count;
  - [x] stores blobs at `{tenant}/{bank}/docs/{doc}/{hash}`;
  - [x] runs the Retainer inline, or enqueues `retain_document` children (serial key `doc:{id}`) under a
    `retain_batch` parent when it enqueues two or more;
  - [x] adds `IngestResult.Usage` (CCR WP-07-1). WP-14 copies it in `svc_memory.go`.
- [x] Chunker (`Chunk`, `ChunkWith`). It is token-budgeted and keeps JSON chat turns and JSONL lines whole up
  to `structured_chunk_max_tokens`; otherwise it does Hindsight's recursive split, with overlap.
- [x] Extraction modes concise / verbose / verbatim / custom / chunks; chunks mode makes no LLM call.
  - The schema comes from `prompts.ExtractSchema`.
  - Fact type follows the speaker: "assistant" maps to experience.
  - Entity hints are shown to the model and merged into resolution.
  - Labels are filtered against the declared labels, and `write_to_tags` labels go to the unit's tags.
- [x] Entity resolution: trigram + co-occurrence + temporal scoring, with a 0.6 inclusive threshold and
  literal hints.
- [x] Linker:
  - temporal up to `temporal_link_cap`;
  - semantic up to `semantic_link_k` at or above the min similarity;
  - causal from `caused_by`;
  - caps hold for both endpoints.
- [x] No-op on the same content + overrides hash. A changed document is replaced in the first batch's tx:
  observations deleted, co-sources reset, `graph_maintenance` queued. A retried job resumes by chunk hash.
- [x] `seq` stamping on units and the RETAIN_COMPLETED event. A deduplicated (Pending) `consolidate` is
  queued unless `skip_consolidation` is set, observations are disabled, or `auto_consolidation` is false.

## Acceptance tests (all pass, `go test -race`)
| Card item | Test |
|---|---|
| Unchanged re-retain makes zero LLM calls | `TestIngestUnchangedMakesNoLLMCall` |
| Crash after chunk 3 of 6, retry re-extracts only 4–6 | `TestRetainResumeAfterCrashReextractsOnlyRemainingChunks` |
| Data-loss trap (changed doc, crash midway, resume) | `TestRetainChangedDocumentCrashResumeKeepsAllFacts`, `TestRetainForceReextractCrashResume` |
| Replacement deletes old units' observations, resets co-sources | `TestRetainReplacementDeletesObservationsAndResetsCoSources` |
| Fan-out rejection stores nothing | `TestIngestFanOutRejectionStoresNothing` |
| Timeless and timestamp handling | `TestRetainTimelessAndTimestamp` |
| Entity merge versus split | `TestEntityResolverMergeVersusSplit` |
| Link caps hold | `TestLinkerCapsHold` |
| who/where/why: CCR if needed | not needed. Folded into the text like Hindsight (decision 804); `TestExtractorComposesDimensions` |

There are also supporting tests for:
- chunking: `TestChunk*`;
- extraction: `TestExtractor*`;
- labels: `TestLabelResolver`;
- async enqueue: `TestIngestAsyncEnqueuesChildrenUnderBatch`;
- validation: `TestIngestValidation`;
- seq and consolidate: `TestRetainStampsSeqAndEnqueuesConsolidate`;
- replay: `TestRetainCompletedJobReplayIsNoop`;
- timeless links: `TestLinkerTimelessAndBankConfig`.

The whole flow (sync retain, no-op, replacement with an observation, crash + resume, links) was also run once
against the real Postgres adapter via testcontainers, in a throwaway test that is not committed. It passed.

## Decisions and CCRs
- 800: the no-op check runs in the Ingestor before any write; the job payload pins content and item fields;
  the batch parent is created only for ≥2 async children.
- 801: chunk hashes are salted with the run (job) id. This fixes the data-loss trap.
- 802: replacement lists the document's units via `SetDocumentTags`, then `DeleteCiting`, then
  `DeleteByDocument`.
- 803: tuning defaults (Hindsight's), `ApproxTokens` until a tokenizer is wired, 4 chunks per tx.
- 804: fact text / dates / fact types; no CCR for who/where/why.
- 805: entity scoring.
- 806: symmetric temporal and semantic links; caps count neighbours; `Link` vs `LinkWith`.
- CCR WP-08-1 (optional): `UnitRepo.IDsByDocument`, to replace the `SetDocumentTags` trick.
- CCR WP-08-2: persist an item's `observation_scopes`. It is validated today but not stored, so
  consolidation only sees the bank default.

## Interface changes inside retain (non-frozen, mocks regenerated)
- `Extractor.Extract` also returns `[]domain.Usage`.
- `LabelResolver.Resolve` takes the label declarations.
- `ExtractionStrategy` gains `KeepsChunkText`, and `Prompt` returns `(ExtractPrompt, error)`.
- `Linker` gains `LinkWith(..., cfg)`. `Link` keeps maintenance's signature.

Constructors take deps structs:
- `NewIngestor(IngestorDeps)`;
- `NewRetainer(RetainerDeps)`;
- `NewLinker()`.

WP-14 wires:
- `config.Merge` as `Merge`;
- `timex.TimeNormalizer` as `Dates`;
- `consolidate.ScopeExpander` as `Scopes`;
- `worker.NewCheckpoint` as `Checkpoint`;
- optionally a real tokenizer as `Tokens`.

## Known gaps
- Usage carries EXTRACT only. `timex.Normalize` returns no usage, and `port.Embedder` reports none.
- Item observation scopes go through `DocumentScopesWriter`; nothing stores them until WP-15 lands and WP-14
  wires the swap (decision 808).
- Overlapping retains of one document: the newest one to take over wins, and an overtaken run stops without
  writing (decision 807). A sync Retain that fails midway (accepted crash window) leaves the document in
  flight with partial units; the client sees the error and a retry replaces them.
- The replacement does not tag the replaced units' audit rows with the reprocess operation id (no port
  method); WP-13's ReprocessDocument may handle it. Mental-model refreshes for deleted observations rely on
  WP-12's staleness check rather than a direct enqueue.
- Extraction within a batch is sequential.

## Review round 1 fixes
| Finding | Fix | Test |
|---|---|---|
| H1+H2 run ownership | ownership checks before any model call and inside every write tx after `Seq`; committed chunks trusted only if chunk 0 is this run's and the doc is in flight; superseded async runs end without writing, sync returns `ErrSuperseded` (ABORTED) (decision 807) | `TestRetainSupersededAfterInterveningRetain`, `TestRetainRetryOperationAfterNewerRetain` (reviewer probe), `TestRetainOverlappingRetainsOfOneDocument` |
| H3 linker starvation | cap only the linked unit's side (Hindsight `_cap_links_per_unit`) | `TestLinkerCapsHold` (30 units, cap 3, lower bound + exact cap for a new unit), `TestLinkerTimelessAndBankConfig` |
| H4 entity merge gates | trigram floor, `_tokens_are_compatible`, same-kind gate; `nearby` counts only would-merge candidates | `TestEntityResolverMergeVersusSplit` (John vs Jane, threshold cases, kind mismatch) |
| H5 item scopes | payload + `DocumentScopesWriter` (WP-15 swap), scopes in the no-op hash; CCR WP-08-2 superseded (decision 808) | `TestIngestStoresItemScopes` |
| M unknown bank Ensure | validate first, `Ensure` in the write tx | `TestIngestFanOutRejectionUnknownBankCreatesNothing` |
| M pinned chunking | payload pins chunk size / overlap / structured max | `TestIngestAsyncEnqueuesChildrenUnderBatch` (payload), resume tests |
| M chunk token counting, wrapper/separators, split at max-overlap | joined-buffer counts | `TestChunkDeterministicCompleteAndWithinBudget`, `TestChunkOverlap` |
| M label N/A / none / null | dropped | `TestLabelResolverDropsEmptyValues` |
| M custom mode blank prompt | falls back to concise | `TestExtractorCustomBlankPromptUsesConcise` |
| LOW dates / caused_by dedupe / cause range check | "2006-01-02 15:04:05" layout, dedupe, range-checked in prepare | `TestExtractorSpaceSeparatedDateAndDuplicateCauses` |
| LOW parallel extraction | not done (known gap) | — |

Decisions added in round 1: 807 (run ownership), 808 (item scopes, unknown banks, pinned chunking); 805 and 806 updated. CCR WP-08-1 approved in the WP-15 batch (switch after it merges); CCR WP-08-2 superseded by the batch.

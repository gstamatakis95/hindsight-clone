# WP-17: a retag during a retain or reprocess must not lose facts

Branch `wp/WP-17-retag-fixup`.

## Built
- [x] **HIGH.** The retain run's ownership check now splits two cases:
  - A different blob key or different overrides still means a newer retain owns the document, so the run
    supersedes.
  - A difference in tags alone is adopted: `run.tags` is set to the document's current tags. This happens in
    the pre-check and in `checkOwner`, under the Seq lock, inside every write transaction. Units written
    from then on carry the current tags.
  - The `retag_document` job on the same `doc:` key runs after the retain job and retags the earlier units.
  - `RetainResult.Scopes` is computed from the final tags.
  - Decision 807 is updated, and decision 1700 is new.
- [x] The same fix covers a reprocess that a retag hits before its first batch: the reprocess now runs to
  completion.
- [x] **MEDIUM.** The gateway `overflowMarkers` gain `context length exceeded`, `prompt_too_long` and
  `context window`.
- [x] **LOW.** The `reads_suite` `testRdErrorRegimes` doc comment now matches what is tested. I fixed the
  comment rather than adding an assertion: decision 1500 lets a tombstoned bank that still holds rows return
  them.
- [ ] **LOW (optional).** Reprocess dedupe: not done. See "Known gaps".

No interface or constructor signature changed.

## Acceptance tests
| Accept | Test | Result |
|---|---|---|
| (a) 3-chunk reprocess, retag between batches: all 3 facts, `extracted_at` set, every unit tagged new after retag_document | `TestReprocessRetaggedBetweenBatchesKeepsAllFacts` | pass |
| (b) The same for a multi-batch async retain of new content | `TestRetainRetaggedBetweenBatchesKeepsAllFacts` | pass |
| (c) Retag after the enqueue, before the first batch: the reprocess completes | `TestReprocessRetaggedBeforeFirstBatchCompletes` | pass |
| A blob key change mid-run still supersedes | `TestRetainOverlappingRetainsOfOneDocument`, `TestRetainSupersededAfterInterveningRetain`, `TestRetainRetryOperationAfterNewerRetain` (existing) | pass |
| An overrides change mid-run still supersedes | `TestRetainOverridesChangeMidRunSupersedes` (new) | pass |
| A gateway test per new marker | `TestContextOverflowClassified/{spaced_marker,prompt_too_long_code,context_window}` | pass |
| `make gen && make lint && make test`, `go test -race -count=1` on retain | | pass, no generated diff |

Before the fix, tests (a), (b) and (c) failed with `Superseded: true` and 0 units created.

Changed test: `TestRetainOlderRunCannotOverwriteNewerTags`. This is an older run of the same content and
overrides, resuming after a newer retain changed only the tags. It now completes under the adopted tags
instead of superseding. The test asserts the invariant it was written for: every unit carries the newer tags,
and none keeps the older ones.

## Decisions and CCRs
- `docs/decisions/807-retain-run-ownership.md` (updated)
- `docs/decisions/1700-retag-adopted-by-running-retain.md` (new)
- No CCRs.

## Known gaps
- ReprocessDocument still enqueues with `DedupeNone`, so two quick reprocesses run twice. That is harmless.
  Joining a pending job could lose an overrides change: the document row would take the new overrides while
  the joined job carries the old ones, and the job would then supersede itself. So it is left out (decision
  1700).

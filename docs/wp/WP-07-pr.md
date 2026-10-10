# WP-07 · Eval harness

Branch `wp/WP-07-eval`. This PR changes only `eval/**`, plus docs: `docs/wp/WP-07*.md`, decisions 700–703 and
CCR WP-07-1. It touches no frozen path and adds no dependencies; `go.mod` is unchanged.

## What was built
- [x] **LongMemEval and LoCoMo loaders** (`eval/longmemeval`, `eval/locomo`).
  - LongMemEval: one conversation per question instance, with sessions sorted by date and judge kinds for
    temporal, update, preference and abstention questions.
  - LoCoMo: one conversation per sample. Sessions are ordered by number, adversarial questions are skipped,
    and image captions are kept as text.
- [x] **Runner over the gRPC client** (`eval/harness`, `eval/target.Memd`).
  - One bank per conversation, named `<prefix><conversation id>`.
  - One synchronous `Retain` per session, with its timestamp and `document_id` = session id.
  - The eval barrier: polls `GetBankStats` until `pending_operations_by_kind` is empty on 2 consecutive polls,
    with a timeout.
  - Then `Recall` or `Reflect` per question, with the dataset's query time.
  - Bounded parallelism across conversations.
  - A conversation that fails to retain or settle marks only its own questions as errors.
- [x] **LLM judge through the gateway** (`eval/judge.LLM`).
  - OpenAI-compatible `/chat/completions` at temperature 0.
  - LongMemEval's per-type yes/no prompts as embedded templates in `eval/judge/templates/`, plus a recall-mode
    prompt.
  - The judge's tokens are reported under the `JUDGE` slot.
  - `judge.Contains` is the deterministic substring judge.
- [x] **Per-category report** in JSON and Markdown (`harness.Report`).
  - Accuracy per category and overall, token usage per slot, nearest-rank p50/p95 latency per operation
    (retain, barrier, recall/reflect, judge), failed background operations and conversation errors.
  - Each target gets its own column.
- [x] **Hindsight comparison flag.** `-hindsight-url` runs the same harness against stock Hindsight's HTTP API
  (`eval/target.Hindsight`: `/v1/default/banks/{bank}/memories`, `/memories/recall`, `/reflect` and
  `/operations`) and reports it next to memd. `-skip-memd` runs Hindsight alone.
- [x] **Golden fixtures** (`eval/golden/*.yaml`, 3 fixtures and 10 checks).
  - The CLI runs them as `go run ./eval/cmd/eval -dataset golden -addr <memd all>`, in recall mode with the
    substring judge, and exits 1 if any check fails.
  - `-stub` runs them against an in-process stub server.
- [x] **In-process stub memd** (`eval/stub`): Memory, BankStats and Operations, tenant-scoped, with scripted
  pending polls. It is used by the tests and by `-stub`.
- [x] `eval/README.md` covers the datasets, how to run against memd and Hindsight, and how to read the report.

## Acceptance tests
| Card "Accept" item | Test | Result |
|---|---|---|
| Runs end to end against a stub server | `eval/harness` `TestRunEndToEndAgainstStub`: real gRPC loopback to `eval/stub`, `target.Memd`, `judge.Contains`, reflect and recall modes, barrier with pending polls, Parallel 2 | PASS (`-race`) |
| Golden fixtures run in under 2 minutes | `eval/golden` `TestGoldenFixturesUnderTwoMinutes`: every fixture through the harness against the stub; asserts all pass and elapsed < 2 min (about 0.2 s) | PASS (`-race`) |
| Report includes per-category accuracy, token usage per slot, p50/p95 latency | `eval/harness` `TestReportSections` (JSON fields and Markdown sections), `TestPercentiles`, `TestRunReportsAccuracyUsageAndLatency` | PASS (`-race`) |

Supporting tests:
- `TestWaitIdleNeedsQuietPolls` and `TestWaitIdleTimeout`, with an injected fake clock.
- `TestFilter` and `TestBankID`.
- Loader tests: `TestLoadSample`, `TestParse*` and `TestParseDuplicateSessionIDsKeepsBoth`.
- `TestLLMJudgeVerdicts`, `TestLLMJudgeGatewayError` and `TestLLMJudgePromptPerKind`, against an `httptest`
  gateway, plus `TestContainsJudge`.
- `TestHindsight*`, against an `httptest` Hindsight.
- `TestMemd*` (tenant header, isolation, usage, failed count) and the stub tests.
- `TestLoadGolden` and `TestLoadGoldenRejectsBadFixtures`.

Gates run in the worktree:
- `make gen`: no diff.
- `/opt/homebrew/bin/golangci-lint run ./...`: 0 issues.
- `go run ./tools/ifacedoc .`: OK.
- `make lint-proto BREAKING_AGAINST=contracts-v1`: OK.
- `make lint-py`: OK.
- `make test` (`go test -race ./...`): pass.
- No Docker containers were started.

## Decisions and CCRs
- `docs/decisions/700-golden-fixture-format.md`: fixtures are YAML in JSON-compatible flow style, read with
  `encoding/json`, so no YAML dependency. They are graded by substring.
- `docs/decisions/701-eval-judge.md`: LongMemEval's yes/no judge prompts, embedded under `eval/judge/templates`
  rather than `internal/prompts`. There is no majority re-ask.
- `docs/decisions/702-eval-usage-and-barrier.md`: usage comes from what the API exposes, and Hindsight's
  totals are reported under the `RETAIN`/`REFLECT` slots. The barrier needs 2 idle polls in a row.
- `docs/decisions/703-eval-dataset-mapping.md`: how each dataset maps onto banks, documents, categories and
  query time.
- `docs/ccr/WP-07-1.md` (proposed, not blocking): add `repeated SlotUsage usage = 3` to `RetainResponse`, so the
  token cost of a sync retain is visible.

## Known gaps
- **Golden fixtures are proven against the stub only.** `memd all` and the fake gateway don't exist yet. It is
  unknown whether the fake extractor keeps the `accept` substrings (Lisbon, Ioniq, cortado, ...) in recalled
  fact text. WP-14 owns that wiring and may need to tune the fixtures or the fake extractor.
- **memd extraction tokens are invisible.** Sync `Retain` returns no usage, so EXTRACT, TIME_PARSE and EMBED
  tokens appear only if the server records them on an operation. A side-by-side run therefore understates
  memd's cost against Hindsight, which reports retain usage. CCR WP-07-1 fixes this.
- **Hindsight reflect gets no time anchor.** Its reflect API has no query-time field, so temporal categories in
  reflect mode aren't apples-to-apples. Hindsight recall does get `query_timestamp`.
- **The LoCoMo category mapping is unverified.** The names (1 multi-hop, 2 temporal, 3 open-domain, 4 single-hop)
  follow common usage but aren't confirmed by the dataset. The number is kept in each bucket name.
- **`make lint` as written fails on this machine.** The `golangci-lint` first on PATH (`~/go/bin`, v1, built with
  go1.25) rejects the config. The v2 binary at `/opt/homebrew/bin` passes. This is an environment issue outside
  WP-07.
- **No run against real datasets or a real gateway was done.** Only the stub and `httptest` fakes were
  exercised.

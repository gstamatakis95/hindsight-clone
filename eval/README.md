# memd eval

The eval harness runs LongMemEval, LoCoMo and the golden fixtures against memd over gRPC. A flag adds stock
Hindsight's HTTP API as a second target, so you can compare the two. The harness then writes a per-category
report in JSON and Markdown.

## How a run works
For each conversation the harness:
1. Creates one bank: `<bank-prefix><conversation id>`.
2. Retains each session with one synchronous `Retain` call, in time order. The session date is the timestamp
   and the session id is the `document_id`.
3. Waits at the eval barrier. It polls `GetBankStats` until `pending_operations_by_kind` is empty on two
   polls in a row, so consolidation and other background jobs have finished.
4. Asks each question:
   - `-mode reflect` (the default) calls `Reflect` and grades the answer.
   - `-mode recall` calls `Recall` and grades whether the recalled memories contain the answer.
5. Grades with the judge.
6. Reads the bank's operations to get background token usage and the number of failed jobs.

Conversations run in parallel (`-parallel`, default 4). A conversation that fails to retain or settle marks
its own questions as errors, and the run continues.

## Datasets
| `-dataset` | `-data` | Unit = bank | Categories |
|---|---|---|---|
| `longmemeval` | `longmemeval_s.json` / `_m` / `_oracle` ([LongMemEval](https://github.com/xiaowu0162/LongMemEval)) | one question instance | `question_type` (abstention questions are graded with the abstention prompt) |
| `locomo` | `locomo10.json` ([LoCoMo](https://github.com/snap-research/locomo)) | one sample | 1-multi-hop, 2-temporal, 3-open-domain, 4-single-hop (the number-to-name mapping is unverified; adversarial is skipped) |
| `golden` | `eval/golden` (default) | one fixture file | the check's `category`, otherwise the fixture name |

Download the datasets yourself; they are not vendored. See decision 703 for how each one maps onto banks.

## Running
Run all commands from the repo root.

```sh
# Golden fixtures against a local `memd all` (fake or real gateway). These run in recall mode with the
# deterministic substring judge, and the command exits 1 if any check fails.
go run ./eval/cmd/eval -dataset golden -addr localhost:50051

# Smoke-test the harness with no server at all (in-process stub).
go run ./eval/cmd/eval -dataset golden -stub

# LongMemEval, reflect mode, LLM judge through the AI gateway.
export MEMD_EVAL_JUDGE_URL=http://gateway:4000/v1 MEMD_EVAL_JUDGE_MODEL=gpt-4o MEMD_EVAL_JUDGE_API_KEY=...
go run ./eval/cmd/eval -dataset longmemeval -data ~/data/longmemeval_s.json -addr localhost:50051

# LoCoMo against memd and stock Hindsight, side by side; quick subset.
go run ./eval/cmd/eval -dataset locomo -data ~/data/locomo10.json \
  -hindsight-url http://localhost:8888 -limit 2 -questions 20
```

Useful flags:
- `-tenant`: the tenant sent as `x-tenant-id` (default `eval`).
- `-category a,b`: run only these categories.
- `-limit N`: run at most N conversations.
- `-questions N`: ask at most N questions per conversation.
- `-skip-memd`: run against Hindsight only.
- `-poll`, `-quiet-polls`, `-settle-timeout`: tune the barrier.
- `-bank-prefix`: the bank id prefix. The default includes the start time, so each run gets fresh banks.
- `-out-json`, `-out-md`: report paths (defaults `eval-report.json` and `eval-report.md`). The Markdown report
  is also printed to stdout.

Set `MEMD_EVAL_HINDSIGHT_API_KEY` if your Hindsight deployment needs a bearer token.

Use a different judge model from the one under test. A model grading its own answers mostly agrees with
itself.

## Reading the report
- **Accuracy by category:** `correct/total` per category, plus an overall row. Each target gets its own column.
  Errors (failed retain, barrier timeout, RPC failure) count as wrong and are listed with their count.
- **Token usage per slot:** input and output tokens per memd model slot:
  - `REFLECT` from Reflect responses.
  - `CONSOLIDATE`, `EXTRACT` and the other slots from the bank's background operations.
  - `JUDGE` for the judge's own tokens.

  Hindsight reports totals only, so its tokens show under `RETAIN` and `REFLECT`. Sync `Retain` returns no
  usage, so memd's extraction tokens show only when the server records them on an operation. See decision 702.
- **Latency:** nearest-rank p50 and p95 in milliseconds, with sample counts, for each operation: `retain`
  (per session), `barrier` (per bank), `recall` or `reflect` (per question) and `judge`.
- **Problems:** the number of failed background operations and the conversation-level errors.

The JSON report has everything in the Markdown, plus every question with its gold answer, response, verdict
and judge reason.

## Golden fixtures
`eval/golden/*.yaml` files use YAML's JSON-compatible flow style (decision 700). A fixture looks like this:

```yaml
{
  "name": "personal-facts",
  "sessions": [{"id": "pf-1", "time": "2024-03-04T09:15:00Z", "context": "...", "content": "user: ..."}],
  "checks": [{"id": "city", "category": "single-hop", "query": "Which city?", "accept": ["Lisbon"]}]
}
```

A check passes when any recalled memory contains one of its `accept` strings (case-insensitive).
`go test ./eval/golden/` runs every fixture against the in-process stub and fails if the run takes 2 minutes
or longer.

## Packages
- `harness`: the types, the runner, the barrier and the report.
- `target`: memd (gRPC) and Hindsight (HTTP).
- `judge`: the gateway LLM judge and the substring judge.
- `longmemeval`, `locomo`: the dataset loaders.
- `golden`: the fixtures and their loader.
- `stub`: the in-process stub memd server used by the tests and `-stub`.
- `cmd/eval`: the CLI.

# memd eval

Skeleton (WP-00); the harness is built by WP-07.

## Datasets
- **LongMemEval** (`eval/longmemeval`): long multi-session chat histories with per-category questions.
- **LoCoMo** (`eval/locomo`): long conversations with QA, event and summarization probes.
- **Golden fixtures** (`eval/golden/*.yaml`): small retain -> recall cases that CI runs against `memd all`
  with the fake gateway.

## Running
`go run ./eval/cmd/eval` will run one bank per conversation: sync Retain with timestamps, wait until
`BankStats.pending_operations_by_kind` is empty (the eval barrier), then Recall / Reflect per question and
score answers with an LLM judge (`eval/judge`). A flag runs the same harness against stock Hindsight's HTTP
API for comparison.

## Reading the report
The report (JSON plus Markdown) gives per-category accuracy, token usage per model slot and p50/p95
latency, for memd and, when requested, for Hindsight side by side.

// Command eval runs memd's benchmarks (LongMemEval, LoCoMo) and the golden retain -> recall fixtures against a running
// memd, optionally side by side with stock Hindsight, and writes a per-category report.
//
// # Where it sits
//
// eval is a client of memd, not part of the service: it talks to memd only over the public gRPC API
// (memd/gen/memory/v1) and imports nothing under internal/. It wires the eval packages together: a loader
// (eval/longmemeval, eval/locomo or eval/golden), a judge (eval/judge), one or two targets (eval/target) and the runner
// and report (eval/harness). eval/README.md has the full guide.
//
// # Usage
//
// Run from the repository root:
//
//	go run ./eval/cmd/eval -dataset golden -addr localhost:50051
//	go run ./eval/cmd/eval -dataset golden -stub   # in-process stub server, no memd needed
//	go run ./eval/cmd/eval -dataset longmemeval -data ~/data/longmemeval_s.json -addr localhost:50051
//	go run ./eval/cmd/eval -dataset locomo -data ~/data/locomo10.json -hindsight-url http://localhost:8888
//
// Flags (defaults in parentheses):
//
//   - -dataset longmemeval | locomo | golden (required); -data the dataset file, or the fixture directory for golden
//     (eval/golden).
//   - -mode reflect | recall (reflect; golden: recall); -judge llm | contains (llm; golden: contains).
//   - -addr memd's gRPC address (localhost:50051); -tenant sent as x-tenant-id (eval); -stub runs memd's side against
//     the in-process eval/stub server instead of -addr; -skip-memd runs only against Hindsight.
//   - -hindsight-url adds stock Hindsight's HTTP API as a second target; -hindsight-api-key its bearer token
//     (MEMD_EVAL_HINDSIGHT_API_KEY).
//   - -judge-url and -judge-model: the OpenAI-compatible gateway and model of the LLM judge (MEMD_EVAL_JUDGE_URL,
//     MEMD_EVAL_JUDGE_MODEL); the API key comes only from MEMD_EVAL_JUDGE_API_KEY.
//   - -category a,b keeps only those categories; -limit N conversations and -questions N per conversation (0 = all).
//   - -parallel conversations at once (4); -poll barrier poll interval (1s; golden: 200ms); -quiet-polls consecutive
//     idle polls that end the barrier (2); -settle-timeout per conversation (15m; golden: 90s); -call-timeout per
//     target or judge call (5m; 0 = none).
//   - -bank-prefix (eval-<unix time>-, so every run gets fresh banks); -out-json (eval-report.json) and -out-md
//     (eval-report.md), empty = not written.
//
// # Behaviour
//
// The command loads and filters the dataset, runs each target in turn with harness.Runner, prints the Markdown report
// to stdout and writes the JSON and Markdown files. It exits 1 on any error. A golden run also exits 1 unless every
// check of every target passed (and at least one ran), so CI can gate on it. Ctrl-C cancels the run; the partial report
// is still written.
package main

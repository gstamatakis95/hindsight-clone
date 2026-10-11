// Command eval runs memd's benchmarks (LoCoMo, LongMemEval and the golden fixtures, or any benchmark registered with
// eval/benchmark) against a running memd, optionally side by side with stock Hindsight, records each run in its own
// directory, resumes interrupted runs and compares runs.
//
// # Where it sits
//
// eval is a client of memd, not part of the service: it talks to memd only over the public gRPC API, through
// eval/target, and imports nothing under internal/. It wires the eval packages together: a benchmark (eval/benchmark;
// the implementations are imported in benchmarks.go), a judge and answer model (eval/judge), one or two targets
// (eval/target) and the runner, journal and report (eval/harness). eval/README.md has the full guide.
//
// # Usage
//
// Run from the repository root:
//
//	go run ./eval/cmd/eval list
//	go run ./eval/cmd/eval run -config eval/configs/locomo.yaml
//	go run ./eval/cmd/eval run -benchmark golden -stub        # in-process stub server, no memd needed
//	go run ./eval/cmd/eval run -resume eval-results/locomo-20261011-101500
//	go run ./eval/cmd/eval compare eval-results/<run A> eval-results/<run B>
//
// "run" is the default subcommand, so flags may come first. The Makefile wraps these as make bench, bench-resume,
// bench-compare and bench-list.
//
// # Configuration
//
// A run is configured by a YAML file (-config; see Config and eval/configs) and flags of the same meaning. Precedence,
// lowest first: built-in defaults (with MEMD_EVAL_JUDGE_URL, MEMD_EVAL_JUDGE_MODEL, MEMD_EVAL_ANSWER_URL and
// MEMD_EVAL_ANSWER_MODEL), the benchmark's defaults, the file, the flags. API keys come only from the environment
// (MEMD_EVAL_JUDGE_API_KEY, MEMD_EVAL_ANSWER_API_KEY, MEMD_EVAL_HINDSIGHT_API_KEY) and are never written to disk.
// Flags: -benchmark, -data, -data-dir, -mode, -out-dir, -bank-prefix, -parallel, -call-timeout, -addr, -tenant, -stub,
// -skip-memd, -hindsight-url, -judge, -judge-url, -judge-model, -answer-url, -answer-model, -category, -limit,
// -questions, -poll, -quiet-polls, -settle-timeout, -recall-budget, -recall-max-tokens, -recall-chunk-tokens.
//
// # Behaviour
//
// run resolves the dataset (-data, else the benchmark's download, cached under -data-dir and checked against its
// sha256), applies the subset filter, creates <out-dir>/<benchmark>-<UTC time>/ with config.yaml (the resolved config)
// and meta.json, runs each target with harness.Runner into the directory's journal (progress.jsonl, results.jsonl),
// then writes report.json, report.md and the final meta.json (commit of the checkout, dataset sha256, models the target
// reports per slot, judge and answer models, times, token usage) and prints the Markdown report. Ctrl-C stops the run;
// the partial report is still written and the error says how to resume. -resume reuses the run's config, banks and
// journal, refuses changes to what the run measures (benchmark, mode, bank prefix, judge and answer models, dataset),
// and does only the work left. compare prints per-category accuracy deltas, token and latency deltas, after warnings
// about anything besides the system under test that differs. A benchmark with a gate (golden) exits 1 when it fails.
package main

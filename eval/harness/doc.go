// Package harness is the dataset-neutral core of memd's eval: the conversation and question types the loaders produce,
// the [Target] (memd over gRPC, or stock Hindsight over HTTP) and [Judge] interfaces, the [Runner] that retains each
// conversation into its own bank, waits for the pending-operations barrier and asks every question, and the
// per-category [Report] (JSON plus Markdown).
//
// # Where it sits
//
// harness imports only the standard library. Loaders (eval/longmemeval, eval/locomo, eval/golden) produce
// [Conversation] values, eval/target implements [Target], eval/judge implements [Judge], and eval/cmd/eval wires them
// together. It is independent of memd's internal packages: every target is reached over its public API.
//
// # Types
//
// A [Conversation] is one benchmark unit: its [Session] values (one retained document each; the session id is the
// document id, so a re-run upserts) go into one fresh bank, and its [Question] values are asked against that bank. A
// question's Kind (KindDefault, KindTemporal, KindUpdate, KindPreference, KindAbstention) picks the judge prompt;
// Accept lists substrings for the deterministic judge. [Usage] is token usage per model slot (memd's slot names, or
// JUDGE for the judge). [Filter] narrows a dataset by category, number of conversations and questions per conversation.
// [Clock] is injected so tests control latency and polling; [SystemClock] is the wall clock.
//
// # Running
//
//	r := harness.NewRunner(tgt, judge, harness.SystemClock{}, harness.Config{Mode: harness.ModeRecall,
//		BankPrefix: "eval-1-", Parallel: 4})
//	rep, err := r.Run(ctx, convs)
//
// For each conversation, up to Config.Parallel at once (default 1), [Runner.Run]:
//
//   - derives the bank id with [BankID] (prefix plus conversation id, characters outside [A-Za-z0-9._-] replaced by
//     '-'); two conversations that map to the same bank id fail the run up front;
//   - retains the sessions in order, one synchronous Retain each;
//   - waits at the barrier: it polls Target.PendingOperations every PollInterval (default 1s) until QuietPolls (default
//     2) consecutive polls see no pending or processing operation, since one idle poll can fall between one job
//     finishing and the next being enqueued; it gives up after SettleTimeout (default 15m);
//   - asks every question with Recall (ModeRecall; the response is the recalled texts joined by newlines) or Reflect
//     (ModeReflect, the default), then grades it with the judge;
//   - reads the bank's background operation usage and failed-operation count whenever at least one Retain succeeded,
//     even if a later step failed.
//
// Every single target or judge call is bounded by CallTimeout (0 = only ctx). A conversation that fails to retain or
// settle marks its questions as errors and the run continues; Run itself fails only on an unknown mode, a bank-id
// collision or a cancelled context, and on a cancelled context it still returns the partial report.
//
// # Report
//
// [Report] holds one [TargetReport] per target: accuracy per category and overall ([CategoryStats]; errors count as
// wrong, not-applicable verdicts are left out of the total), token usage per slot (call responses, background
// operations and the judge), nearest-rank p50 and p95 latency per operation (retain, barrier, recall or reflect, judge;
// see [Percentile]), failed background operations, conversation-level errors and every [QuestionResult].
// [Report.WriteJSON] writes everything; [Report.WriteMarkdown] writes the tables with one column per target, so runs
// against memd and Hindsight read side by side.
//
// # Testing
//
// runner_test.go and runner_failure_test.go drive the runner with scripted targets and a fake clock (barrier, timeouts,
// failures, collisions, report sections); e2e_test.go runs it end to end against eval/stub through eval/target's memd
// client.
package harness

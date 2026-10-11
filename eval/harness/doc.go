// Package harness is the dataset-neutral core of memd's eval: the conversation and question types benchmarks
// produce, the [Target] (memd over gRPC, or stock Hindsight over HTTP), [Judge] and [Answerer] interfaces, the
// [Runner] that retains each conversation into its own bank, waits for the pending-operations barrier and asks every
// question, the [Journal] that records a run so it can be resumed, and the per-category [Report] (JSON plus
// Markdown) and run [Compare].
//
// # Where it sits
//
// harness imports only the standard library. Benchmarks (eval/benchmark and its implementations) produce
// [Conversation] values, eval/target implements [Target], eval/judge implements [Judge] and [Answerer], and
// eval/cmd/eval wires them together. It is independent of memd's internal packages: every target is reached over its
// public API.
//
// # Types
//
// A [Conversation] is one benchmark unit: its [Session] values (one retained document each; the session id is the
// document id, so a re-run upserts) go into one fresh bank, and its [Question] values are asked against that bank. A
// question's Kind (KindDefault, KindTemporal, KindUpdate, KindPreference, KindAbstention, KindLoCoMo) picks the judge
// prompt and, in rag mode, the answer prompt; Accept lists substrings for the deterministic judge. [Recalled] is a
// recall result in a target-neutral shape (field names follow Hindsight's recall response). [Usage] is token usage per
// model slot (memd's slot names, or ANSWER and JUDGE for the eval's own models). [Filter] narrows a dataset by
// category, number of conversations and questions per conversation. [Clock] is injected so tests control polling;
// [SystemClock] is the wall clock.
//
// # Running
//
//	j, _ := harness.OpenJournal(runDir)
//	r, _ := harness.NewRunner(harness.Deps{Target: tgt, Judge: jdg, Answerer: ans, Journal: j},
//		harness.Config{Mode: harness.ModeRAG, BankPrefix: "eval-1-", Parallel: 4})
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
//   - asks every question in the configured mode: ModeReflect (default) grades Reflect's answer; ModeRecall grades
//     whether the recalled texts, joined by newlines, contain the answer; ModeRAG recalls with Config.Recall, has the
//     Answerer answer from the recall result (rendered as JSON) and grades that answer, and counts an empty recall as
//     wrong without calling the answer model or the judge;
//   - reads the bank's cumulative background usage and failed-operation count once the bank exists.
//
// Every single target, answer or judge call is bounded by CallTimeout (0 = only ctx). A conversation that fails to
// retain or settle marks its questions as errors and the run continues; Run itself fails on a bank-id collision, a
// journal write error or a cancelled context, and then still returns the report so far.
//
// # Journal and resume
//
// The [Journal] is two append-only JSON-lines files in the run directory: [ProgressFile] (one [Step] per retained
// session, settled bank, background read or conversation failure) and [ResultsFile] (one [QuestionResult] per answered
// question, with its response, verdict, latency and usage). Each line is one write; a line cut short by a crash is
// dropped when the journal is reopened. A runner on a reopened journal, with the same bank prefix, skips sessions
// already retained, re-runs the cheap barrier only for banks not settled yet, asks only questions without a result or
// whose result was an error, and re-reads background usage (a cumulative read, so the latest wins). Work interrupted by
// cancellation is not recorded, so it is redone on resume.
//
// # Report and compare
//
// [Journal.Report] builds a [TargetReport] from the journal, so a resumed run reports the same as an uninterrupted
// one: accuracy per category and overall ([CategoryStats]; errors count as wrong, not-applicable verdicts are left out
// of the total, questions not run yet are counted as Pending), token usage per slot (every attempt's, since it was
// spent), nearest-rank p50 and p95 latency per operation (retain, barrier, recall, answer, reflect, judge), failed
// background operations, conversation-level errors still standing and every [QuestionResult]. [Report.WriteJSON] and
// [Report.WriteMarkdown] write a [Report] with one column per target, so runs against memd and Hindsight read side by
// side. [Compare] and [WriteComparison] put two reports side by side: accuracy deltas in percentage points per
// category, token totals per slot and p50/p95 latency with relative changes.
//
// # Testing
//
// e2e_test.go runs the harness end to end in rag mode against eval/stub and a fake gateway, crashes it partway and
// resumes it; barrier_test.go covers the barrier; compare_test.go the comparison arithmetic.
package harness

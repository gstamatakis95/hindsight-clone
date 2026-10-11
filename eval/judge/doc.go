// Package judge grades eval answers against gold answers. It provides two harness.Judge implementations: [LLM], which
// asks a chat model, and [Contains], a deterministic substring judge.
//
// # LLM
//
// [NewLLM] takes an OpenAI-compatible gateway base URL (for example http://gateway:4000/v1), a model, an optional API
// key (sent as a bearer token) and an optional *http.Client. [LLM.Grade] renders the prompt for the question with
// [Prompt], POSTs it as one user message to {base}/chat/completions at temperature 0, and counts the reply as correct
// when its first word, lower-cased and stripped of punctuation and markup, is "yes". The reply text is the verdict's
// reason and the judge's own tokens are reported under the [SlotJudge] ("JUDGE") usage slot. A non-2xx status, an
// undecodable reply or a reply with no choices is an error; replies are read up to 4 MiB.
//
//	j := judge.NewLLM(os.Getenv("MEMD_EVAL_JUDGE_URL"), os.Getenv("MEMD_EVAL_JUDGE_MODEL"), key, nil)
//	v, err := j.Grade(ctx, q, answer, false)
//
// The prompts are embedded from templates/*.tmpl, one per question kind (default, temporal, update, preference,
// abstention), after LongMemEval's official scorer, plus recall, which asks whether recalled memories contain the
// answer. Every target is graded with the same prompts, so runs of memd and stock Hindsight are scored the same way. In
// recall mode, abstention and preference questions are not applicable (an unanswerable question has no answer to find
// in memories, and a rubric is not a fact): Grade returns a not-applicable verdict without a model call, and the report
// leaves them out of accuracy. Use a different judge model from the one under test.
//
// # Contains
//
// [Contains] makes no model calls: a response is correct when it contains, case-insensitively, any of the question's
// Accept strings (or its Answer when Accept is empty). The golden fixtures use it, so CI can run them against memd
// behind the deterministic gateway emulator.
//
// # Testing
//
// judge_test.go checks Contains, the prompt rendering for every kind, and LLM against an httptest gateway (verdict
// parsing, gateway errors, not-applicable kinds in recall mode).
package judge

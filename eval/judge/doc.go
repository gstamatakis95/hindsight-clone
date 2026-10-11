// Package judge holds the eval's own model calls: grading answers against gold answers, and, in rag mode,
// answering questions from recalled memories. It provides two harness.Judge implementations, [LLM], which asks a chat
// model, and [Contains], a deterministic substring judge, and one harness.Answerer, [Answerer].
//
// # Chat
//
// [NewChat] takes an OpenAI-compatible gateway base URL (for example http://gateway:4000/v1), a model, an optional API
// key (sent as a bearer token) and an optional *http.Client. Each call POSTs one user message to
// {base}/chat/completions at temperature 0. A non-2xx status, an undecodable reply or a reply with no choices is an
// error; replies are read up to 4 MiB.
//
// # LLM
//
// [LLM.Grade] renders the prompt for the question with [Prompt] and parses the verdict: a JSON object with a boolean
// "correct" (and a "reason") wherever it appears in the reply, else a reply whose first word, lower-cased and stripped
// of punctuation and markup, is "yes". The judge's tokens are reported under the [SlotJudge] ("JUDGE") usage slot.
//
//	j := judge.NewLLM(judge.NewChat(url, model, key, nil))
//	v, err := j.Grade(ctx, q, answer, false)
//
// The prompts are embedded from templates/*.tmpl, one per question kind: default, temporal, update, preference and
// abstention after LongMemEval's official scorer; locomo, the LoCoMo judge prompt of Hindsight's published runs
// (generous on wording and date formats, verdict as JSON); and recall, which asks whether recalled memories contain the
// answer. Every target is graded with the same prompts, so runs of memd and stock Hindsight are scored the same way. In
// recall mode, abstention and preference questions are not applicable (an unanswerable question has no answer to find
// in memories, and a rubric is not a fact): Grade returns a not-applicable verdict without a model call, and the report
// leaves them out of accuracy. Use a different judge model from the one under test.
//
// # Answerer
//
// [Answerer.Answer] renders [AnswerPrompt]: the template under templates/answer named after the question's kind
// (locomo, Hindsight's LoCoMo answer prompt with the query date as the current date), else default, with the recall
// result as JSON for context. It asks for a JSON object {"reasoning", "answer"} and falls back to the whole reply as
// the answer. The answer model's tokens are reported under [SlotAnswer] ("ANSWER").
//
// # Contains
//
// [Contains] makes no model calls: a response is correct when it contains, case-insensitively, any of the question's
// Accept strings (or its Answer when Accept is empty). The golden fixtures use it, so CI can run them against memd
// behind the deterministic gateway emulator.
//
// # Testing
//
// judge_test.go checks verdict parsing and the prompt for every kind against an httptest gateway, not-applicable kinds
// in recall mode, the LoCoMo answer prompt and reply parsing, and Contains.
package judge

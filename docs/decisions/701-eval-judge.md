# 701 — The eval judge: LongMemEval's yes/no prompts, embedded in eval/judge/templates

## Context
The card asks for "an LLM judge through the gateway". AGENTS.md says prompts live in
`internal/prompts/templates` and never in Go, but WP-07 owns only `eval/**`, and the judge prompt is not a
service prompt: memd never uses it. Hindsight's in-repo LongMemEval/LoCoMo runners moved to an external
project (AMB) that isn't vendored, so its judge prompt can't be copied. Its remaining system-eval judge
(`hindsight-system-evals/judge.py`) grades free-form criteria with Gemini, not benchmark answers.

## Decision
- The judge calls `POST {judge-url}/chat/completions` (OpenAI-compatible, the same AI gateway memd uses) at
  temperature 0 with one user message. A reply counts as correct only when its **first word** is "yes"
  (case-insensitive, punctuation and markdown stripped). LongMemEval's official `evaluate_qa.py` instead
  checks whether "yes" appears anywhere in the reply, which also passes replies like "No, not yes".
- **Recall mode leaves two kinds of question out of accuracy.** LongMemEval abstention and preference
  questions are marked **N/A**, not wrong, and the judge makes no model call for them:
  - An abstention question has no answer to look for in recalled memories.
  - A preference rubric describes the kind of personalised answer wanted; it isn't a fact a memory could
    contain.

  The report shows them as `n/a` and leaves them out of each category's total. In reflect mode they are
  graded with their own prompts.
- Prompts are LongMemEval's per-type prompts, kept as text templates in `eval/judge/templates/*.tmpl` and
  embedded with `go:embed`: default, temporal (off-by-one tolerated), knowledge-update, preference (rubric),
  abstention. LoCoMo uses default/temporal. Recall mode uses `recall.tmpl` ("do the retrieved memories
  contain the answer").
- Both memd and Hindsight are graded by the same judge, model and prompt.
- Judge tokens are reported under the `JUDGE` slot so they are never confused with memd's own cost.

## Alternatives
- Put the judge templates in `internal/prompts/templates`. Rejected: not owned by WP-07, and it would ship eval
  prompts inside the service binary.
- Hindsight's majority re-ask on a "no" verdict. Deferred: it doubles cost on wrong answers; easy to add in
  `judge.LLM.Grade` if noise shows up.

## Spec lines involved
WP-07 card "An LLM judge through the gateway"; AGENTS.md "Prompts live in internal/prompts/templates".

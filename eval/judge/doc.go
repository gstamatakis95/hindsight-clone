// Package judge grades benchmark answers against gold answers. LLM asks a chat model through the
// OpenAI-compatible AI gateway with a fixed yes/no prompt per question kind (templates/*.tmpl, after
// LongMemEval's official scorer), so runs of memd and stock Hindsight are scored the same way. Contains
// is the deterministic substring judge the golden fixtures use.
package judge

package judge

import (
	"context"
	"embed"
	"fmt"
	"memd/eval/harness"
	"strings"
	"text/template"
	"unicode"
)

// SlotJudge is the usage slot the judge's own tokens are reported under.
const SlotJudge = "JUDGE"

//go:embed templates/*.tmpl templates/answer/*.tmpl
var templateFS embed.FS

// prompts holds one judge template per kind (default, temporal, update, preference, abstention, locomo)
// plus recall, which grades recalled memories instead of an answer.
var prompts = template.Must(template.ParseFS(templateFS, "templates/*.tmpl"))

// LLM grades answers with a chat model. Most prompts ask for a yes/no verdict; the LoCoMo prompt asks for
// a JSON object {"reason", "correct"}, the structured verdict Hindsight's published runs use.
type LLM struct {
	chat *Chat
}

var _ harness.Judge = (*LLM)(nil)

// NewLLM returns a judge asking chat.
func NewLLM(chat *Chat) *LLM {
	return &LLM{chat: chat}
}

// Grade renders the prompt for q's kind (the recall prompt when recall is set), asks the model and
// parses its verdict. In recall mode, abstention and preference questions are not applicable: an
// unanswerable question has no answer to find in memories, and a rubric is not a fact, so they are left
// out of accuracy without a model call.
func (l *LLM) Grade(ctx context.Context, q harness.Question, response string, recall bool) (harness.Verdict, error) {
	if recall && (q.Kind == harness.KindAbstention || q.Kind == harness.KindPreference) {
		return harness.Verdict{NotApplicable: true, Reason: "not gradable in recall mode: " + q.Kind}, nil
	}
	prompt, err := Prompt(q, response, recall)
	if err != nil {
		return harness.Verdict{}, err
	}
	reply, usage, err := l.chat.complete(ctx, SlotJudge, prompt)
	if err != nil {
		return harness.Verdict{}, fmt.Errorf("judge: %w", err)
	}
	correct, reason := verdict(reply)
	return harness.Verdict{Correct: correct, Reason: reason, Usage: []harness.Usage{usage}}, nil
}

// verdict parses a judge reply: a JSON object with a boolean "correct" (its "reason" is the reason), or
// a reply whose first word is yes or no (the whole reply is the reason). Anything else is wrong.
func verdict(reply string) (bool, string) {
	var obj struct {
		Correct *bool  `json:"correct"`
		Reason  string `json:"reason"`
	}
	if jsonObject(reply, &obj) && obj.Correct != nil {
		return *obj.Correct, obj.Reason
	}
	return firstWord(reply) == "yes", strings.TrimSpace(reply)
}

// firstWord returns the reply's first word, lower-cased and stripped of punctuation and markup.
func firstWord(reply string) string {
	fields := strings.Fields(strings.ToLower(reply))
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimFunc(fields[0], func(r rune) bool { return !unicode.IsLetter(r) })
}

// Prompt renders the judge prompt for q and response.
func Prompt(q harness.Question, response string, recall bool) (string, error) {
	name := q.Kind
	switch {
	case recall:
		name = "recall"
	case name == harness.KindDefault:
		name = "default"
	}
	var b strings.Builder
	data := map[string]string{"Question": q.Query, "Answer": q.Answer, "Response": response}
	if err := prompts.ExecuteTemplate(&b, name+".tmpl", data); err != nil {
		return "", fmt.Errorf("judge: prompt for kind %q: %w", q.Kind, err)
	}
	return b.String(), nil
}

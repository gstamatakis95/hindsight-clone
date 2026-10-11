package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/eval/harness"
	"strings"
	"text/template"
)

// SlotAnswer is the usage slot the answer model's tokens are reported under.
const SlotAnswer = "ANSWER"

// answerPrompts holds one rag answer template per question kind that has its own (locomo) plus default.
var answerPrompts = template.Must(template.ParseFS(templateFS, "templates/answer/*.tmpl"))

// Answerer answers questions from recalled memories with a chat model (rag mode): the recall result is
// rendered as JSON into the prompt, and the model replies with a JSON object {"reasoning", "answer"}.
type Answerer struct {
	chat *Chat
}

var _ harness.Answerer = (*Answerer)(nil)

// NewAnswerer returns an answerer asking chat.
func NewAnswerer(chat *Chat) *Answerer {
	return &Answerer{chat: chat}
}

// Answer renders the answer prompt for q's kind, asks the model and parses its reply. A reply without
// the JSON object is taken whole as the answer.
func (a *Answerer) Answer(ctx context.Context, q harness.Question, rec harness.Recalled) (harness.Answer, error) {
	prompt, err := AnswerPrompt(q, rec)
	if err != nil {
		return harness.Answer{}, err
	}
	reply, usage, err := a.chat.complete(ctx, SlotAnswer, prompt)
	if err != nil {
		return harness.Answer{}, fmt.Errorf("answer: %w", err)
	}
	out := harness.Answer{Text: strings.TrimSpace(reply), Usage: []harness.Usage{usage}}
	var obj struct {
		Reasoning string  `json:"reasoning"`
		Answer    *string `json:"answer"`
	}
	if jsonObject(reply, &obj) && obj.Answer != nil {
		out.Text, out.Reasoning = *obj.Answer, obj.Reasoning
	}
	return out, nil
}

// AnswerPrompt renders the rag answer prompt for q over the recall result rec. The template is the one
// named after q's kind when there is one, else the default.
func AnswerPrompt(q harness.Question, rec harness.Recalled) (string, error) {
	ctxJSON, err := json.Marshal(rec)
	if err != nil {
		return "", fmt.Errorf("answer: encode recall result: %w", err)
	}
	name := "default.tmpl"
	if q.Kind != "" && answerPrompts.Lookup(q.Kind+".tmpl") != nil {
		name = q.Kind + ".tmpl"
	}
	data := map[string]string{"Question": q.Query, "Context": string(ctxJSON)}
	if !q.QueryTime.IsZero() {
		data["QueryTime"] = q.QueryTime.UTC().Format("2006-01-02T15:04:05-07:00")
	}
	var b strings.Builder
	if err := answerPrompts.ExecuteTemplate(&b, name, data); err != nil {
		return "", fmt.Errorf("answer: prompt for kind %q: %w", q.Kind, err)
	}
	return b.String(), nil
}

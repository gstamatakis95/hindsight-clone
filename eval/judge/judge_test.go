package judge_test

import (
	"context"
	"encoding/json"
	"memd/eval/harness"
	"memd/eval/judge"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// gateway is an httptest OpenAI-compatible chat endpoint that replies with reply and records the prompt.
func gateway(t *testing.T, reply string) (*judge.Chat, *string) {
	t.Helper()
	var prompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("unexpected request %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) != 1 {
			t.Errorf("bad request body: %v %+v", err, req)
			return
		}
		prompt = req.Messages[0].Content
		content, _ := json.Marshal(reply)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":` + string(content) + `}}],` +
			`"usage":{"prompt_tokens":120,"completion_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)
	return judge.NewChat(srv.URL+"/v1/", "m", "k", srv.Client()), &prompt
}

func TestLLMJudgeVerdicts(t *testing.T) {
	tests := []struct {
		reply string
		want  bool
	}{
		{"Yes", true},
		{"**Yes**", true},
		{"No, the answer is not yes.", false},
		{"", false},
		{`{"reason": "same date", "correct": true}`, true},
		{"```json\n{\"reason\": \"different topic\", \"correct\": false}\n```", false},
	}
	for _, tt := range tests {
		chat, _ := gateway(t, tt.reply)
		v, err := judge.NewLLM(chat).Grade(context.Background(), harness.Question{Query: "q", Answer: "a"}, "r", false)
		if err != nil {
			t.Fatal(err)
		}
		if v.Correct != tt.want {
			t.Errorf("reply %q: correct = %v, want %v", tt.reply, v.Correct, tt.want)
		}
		if len(v.Usage) != 1 || v.Usage[0] != (harness.Usage{Slot: "JUDGE", InputTokens: 120, OutputTokens: 1}) {
			t.Errorf("usage = %+v", v.Usage)
		}
	}
}

func TestLLMJudgePromptPerKind(t *testing.T) {
	tests := []struct {
		kind   string
		recall bool
		want   string
	}{
		{harness.KindDefault, false, "Correct Answer: gold"},
		{harness.KindTemporal, false, "off-by-one"},
		{harness.KindUpdate, false, "updated answer"},
		{harness.KindPreference, false, "Rubric: gold"},
		{harness.KindAbstention, false, "unanswerable"},
		{harness.KindLoCoMo, false, "Gold answer: gold\nGenerated answer: resp {{x}}"},
		{harness.KindLoCoMo, true, "Retrieved Memories:\nresp {{x}}"},
	}
	for _, tt := range tests {
		chat, prompt := gateway(t, "yes")
		q := harness.Question{Query: "What?", Answer: "gold", Kind: tt.kind}
		if _, err := judge.NewLLM(chat).Grade(context.Background(), q, "resp {{x}}", tt.recall); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(*prompt, tt.want) || !strings.Contains(*prompt, "Question: What?") {
			t.Errorf("kind %q recall=%v: prompt lacks %q:\n%s", tt.kind, tt.recall, tt.want, *prompt)
		}
	}
}

func TestLLMJudgeRecallNotApplicable(t *testing.T) {
	// No gateway: a not-applicable question must not call the model.
	j := judge.NewLLM(judge.NewChat("http://127.0.0.1:1/v1", "m", "k", nil))
	for _, kind := range []string{harness.KindAbstention, harness.KindPreference} {
		v, err := j.Grade(context.Background(), harness.Question{Kind: kind}, "memories", true)
		if err != nil || !v.NotApplicable || v.Correct {
			t.Errorf("kind %q in recall mode: verdict %+v, err %v; want not applicable", kind, v, err)
		}
	}
}

// TestAnswererLoCoMo checks the rag answer: the LoCoMo prompt with the query date and the recall result
// as JSON, and the answer taken from the model's JSON reply.
func TestAnswererLoCoMo(t *testing.T) {
	chat, prompt := gateway(t, `Sure: {"reasoning": "memory 1 says so", "answer": "7 May 2023"}`)
	q := harness.Question{
		Query: "When did Caroline go?", Kind: harness.KindLoCoMo,
		QueryTime: time.Date(2023, 8, 25, 13, 33, 0, 0, time.UTC),
	}
	mem := harness.Memory{Text: "Caroline went on 7 May", MentionedAt: "2023-05-08T13:56:00Z"}
	rec := harness.Recalled{Results: []harness.Memory{mem}}
	a, err := judge.NewAnswerer(chat).Answer(context.Background(), q, rec)
	if err != nil {
		t.Fatal(err)
	}
	if a.Text != "7 May 2023" || a.Reasoning != "memory 1 says so" || a.Usage[0].Slot != "ANSWER" {
		t.Fatalf("answer = %+v", a)
	}
	for _, want := range []string{
		"The question is being asked on: 2023-08-25T13:33:00+00:00 UTC",
		`{"results":[{"text":"Caroline went on 7 May","mentioned_at":"2023-05-08T13:56:00Z"}]}`,
		"Question: When did Caroline go?",
	} {
		if !strings.Contains(*prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, *prompt)
		}
	}
}

func TestContainsJudge(t *testing.T) {
	tests := []struct {
		q    harness.Question
		resp string
		want bool
	}{
		{harness.Question{Answer: "Paris"}, "she lives in paris", true},
		{harness.Question{Answer: "Paris", Accept: []string{"France", "Lyon"}}, "Paris", false},
		{harness.Question{Accept: []string{"France", "Lyon"}}, "moved to LYON", true},
		{harness.Question{Answer: ""}, "anything", false},
	}
	for _, tt := range tests {
		v, err := judge.Contains{}.Grade(context.Background(), tt.q, tt.resp, true)
		if err != nil || v.Correct != tt.want {
			t.Errorf("Grade(%+v, %q) = %+v, %v; want %v", tt.q, tt.resp, v, err, tt.want)
		}
	}
}

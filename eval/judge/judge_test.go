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
)

// gateway is an httptest OpenAI-compatible chat endpoint that replies with reply and records the prompt.
func gateway(t *testing.T, status int, reply string) (*httptest.Server, *string) {
	t.Helper()
	var prompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("unexpected request %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var req struct {
			Model       string  `json:"model"`
			Temperature float64 `json:"temperature"`
			Messages    []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Model != "judge-model" {
			t.Errorf("bad request body: %v %+v", err, req)
		}
		if len(req.Messages) == 1 {
			prompt = req.Messages[0].Content
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"` + reply + `"}}],` +
			`"usage":{"prompt_tokens":120,"completion_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &prompt
}

func TestLLMJudgeVerdicts(t *testing.T) {
	tests := []struct {
		reply string
		want  bool
	}{
		{"Yes", true},
		{"yes.", true},
		{"**Yes**", true},
		{"No", false},
		{"no", false},
		{"No, the answer is not yes.", false},
		{"", false},
	}
	for _, tt := range tests {
		srv, _ := gateway(t, http.StatusOK, tt.reply)
		j := judge.NewLLM(srv.URL+"/v1/", "judge-model", "k", nil)
		v, err := j.Grade(context.Background(), harness.Question{Query: "q", Answer: "a"}, "r", false)
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

func TestLLMJudgeGatewayError(t *testing.T) {
	srv, _ := gateway(t, http.StatusTooManyRequests, "slow down")
	_, err := judge.NewLLM(srv.URL+"/v1", "judge-model", "k", nil).
		Grade(context.Background(), harness.Question{}, "r", false)
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v, want the gateway status", err)
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
		{harness.KindTemporal, true, "Retrieved Memories:\nresp {{x}}"},
	}
	for _, tt := range tests {
		srv, prompt := gateway(t, http.StatusOK, "yes")
		q := harness.Question{Query: "What?", Answer: "gold", Kind: tt.kind}
		if _, err := judge.NewLLM(srv.URL+"/v1", "judge-model", "k", nil).
			Grade(context.Background(), q, "resp {{x}}", tt.recall); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(*prompt, tt.want) || !strings.Contains(*prompt, "Question: What?") {
			t.Errorf("kind %q recall=%v: prompt lacks %q:\n%s", tt.kind, tt.recall, tt.want, *prompt)
		}
	}
	if _, err := judge.Prompt(harness.Question{Kind: "nope"}, "", false); err == nil {
		t.Error("unknown kind: want error")
	}
}

func TestLLMJudgeRecallNotApplicable(t *testing.T) {
	// No gateway: a not-applicable question must not call the model.
	j := judge.NewLLM("http://127.0.0.1:1/v1", "judge-model", "k", nil)
	for _, kind := range []string{harness.KindAbstention, harness.KindPreference} {
		v, err := j.Grade(context.Background(), harness.Question{Kind: kind}, "memories", true)
		if err != nil || !v.NotApplicable || v.Correct {
			t.Errorf("kind %q in recall mode: verdict %+v, err %v; want not applicable", kind, v, err)
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

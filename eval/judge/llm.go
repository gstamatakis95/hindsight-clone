package judge

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"memd/eval/harness"
	"net/http"
	"strings"
	"text/template"
)

// SlotJudge is the usage slot the judge's own tokens are reported under.
const SlotJudge = "JUDGE"

// maxReplyBytes bounds how much of a gateway reply is read.
const maxReplyBytes = 4 << 20

//go:embed templates/*.tmpl
var templateFS embed.FS

// prompts holds one template per judge kind (default, temporal, update, preference, abstention) plus
// recall, which grades recalled memories instead of an answer.
var prompts = template.Must(template.ParseFS(templateFS, "templates/*.tmpl"))

// LLM grades answers with a chat model behind an OpenAI-compatible gateway (POST {base}/chat/completions).
// It asks for a yes/no verdict at temperature 0 and, like LongMemEval's official scorer, counts a reply
// containing "yes" as correct.
type LLM struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

var _ harness.Judge = (*LLM)(nil)

// NewLLM returns a judge calling model at baseURL (for example http://gateway:4000/v1). apiKey is sent
// as a bearer token when non-empty; a nil client uses http.DefaultClient.
func NewLLM(baseURL, model, apiKey string, client *http.Client) *LLM {
	if client == nil {
		client = http.DefaultClient
	}
	return &LLM{baseURL: strings.TrimRight(baseURL, "/"), model: model, apiKey: apiKey, client: client}
}

// Grade renders the prompt for q's kind (the recall prompt when recall is set), asks the model and
// parses its yes/no verdict.
func (l *LLM) Grade(ctx context.Context, q harness.Question, response string, recall bool) (harness.Verdict, error) {
	prompt, err := Prompt(q, response, recall)
	if err != nil {
		return harness.Verdict{}, err
	}
	reply, usage, err := l.chat(ctx, prompt)
	if err != nil {
		return harness.Verdict{}, err
	}
	return harness.Verdict{
		Correct: strings.Contains(strings.ToLower(reply), "yes"),
		Reason:  strings.TrimSpace(reply),
		Usage:   []harness.Usage{usage},
	}, nil
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

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

// chat sends one user message and returns the reply text and its usage.
func (l *LLM) chat(ctx context.Context, prompt string) (string, harness.Usage, error) {
	body, err := json.Marshal(chatRequest{
		Model: l.model, Messages: []chatMessage{{Role: "user", Content: prompt}}, Temperature: 0,
	})
	if err != nil {
		return "", harness.Usage{}, fmt.Errorf("judge: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", harness.Usage{}, fmt.Errorf("judge: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if l.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+l.apiKey)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return "", harness.Usage{}, fmt.Errorf("judge: call gateway: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxReplyBytes))
	if err != nil {
		return "", harness.Usage{}, fmt.Errorf("judge: read reply: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return "", harness.Usage{}, fmt.Errorf("judge: gateway returned %d: %.300s", resp.StatusCode, raw)
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", harness.Usage{}, fmt.Errorf("judge: decode reply: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", harness.Usage{}, fmt.Errorf("judge: gateway reply has no choices: %.300s", raw)
	}
	usage := harness.Usage{
		Slot: SlotJudge, InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens,
	}
	return out.Choices[0].Message.Content, usage, nil
}

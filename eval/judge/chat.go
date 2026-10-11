package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"memd/eval/harness"
	"net/http"
	"strings"
)

// maxReplyBytes bounds how much of a gateway reply is read.
const maxReplyBytes = 4 << 20

// Chat calls one chat model behind an OpenAI-compatible gateway (POST {base}/chat/completions) at
// temperature 0. The judge and the answerer share it.
type Chat struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

// NewChat returns a client for model at baseURL (for example http://gateway:4000/v1). apiKey is sent as
// a bearer token when non-empty; a nil client uses http.DefaultClient.
func NewChat(baseURL, model, apiKey string, client *http.Client) *Chat {
	if client == nil {
		client = http.DefaultClient
	}
	return &Chat{baseURL: strings.TrimRight(baseURL, "/"), model: model, apiKey: apiKey, client: client}
}

// Model returns the model name the client sends.
func (c *Chat) Model() string { return c.model }

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

// complete sends prompt as one user message and returns the reply text and its usage under slot.
func (c *Chat) complete(ctx context.Context, slot, prompt string) (string, harness.Usage, error) {
	body, err := json.Marshal(chatRequest{
		Model: c.model, Messages: []chatMessage{{Role: "user", Content: prompt}}, Temperature: 0,
	})
	if err != nil {
		return "", harness.Usage{}, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", harness.Usage{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", harness.Usage{}, fmt.Errorf("call gateway: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxReplyBytes))
	if err != nil {
		return "", harness.Usage{}, fmt.Errorf("read reply: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return "", harness.Usage{}, fmt.Errorf("gateway returned %d: %.300s", resp.StatusCode, raw)
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", harness.Usage{}, fmt.Errorf("decode reply: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", harness.Usage{}, fmt.Errorf("gateway reply has no choices: %.300s", raw)
	}
	usage := harness.Usage{Slot: slot, InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens}
	return out.Choices[0].Message.Content, usage, nil
}

// jsonObject decodes the first JSON object in reply (models often wrap it in prose or a code fence) into
// v and reports whether it found one.
func jsonObject(reply string, v any) bool {
	start := strings.Index(reply, "{")
	end := strings.LastIndex(reply, "}")
	if start < 0 || end < start {
		return false
	}
	return json.Unmarshal([]byte(reply[start:end+1]), v) == nil
}

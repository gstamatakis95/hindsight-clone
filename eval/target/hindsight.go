package target

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"memd/eval/harness"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// maxResponseBytes caps how much of a Hindsight response body is read.
	maxResponseBytes = 32 << 20
	// maxErrorBody caps how much of a failed response's body goes into the error.
	maxErrorBody = 512
	// pendingLimit is the page size PendingOperations lists pending and processing operations with.
	pendingLimit = 100
)

var _ harness.Target = (*Hindsight)(nil)

// Hindsight is the harness.Target for a stock Hindsight server's HTTP API (the baseline memd is compared
// against).
type Hindsight struct {
	base   string
	apiKey string
	client *http.Client
}

// NewHindsight returns a Hindsight target for the server at baseURL. apiKey is sent as a bearer token when
// non-empty; a nil client means a plain http.Client.
func NewHindsight(baseURL, apiKey string, client *http.Client) *Hindsight {
	if client == nil {
		client = &http.Client{}
	}
	return &Hindsight{base: strings.TrimRight(baseURL, "/"), apiKey: apiKey, client: client}
}

// hsItem is Hindsight's MemoryItem.
type hsItem struct {
	Content    string `json:"content"`
	Context    string `json:"context,omitempty"`
	DocumentID string `json:"document_id,omitempty"`
	Timestamp  string `json:"timestamp,omitempty"`
}

// hsRetainRequest is Hindsight's RetainRequest.
type hsRetainRequest struct {
	Items []hsItem `json:"items"`
	Async bool     `json:"async"`
}

// hsTokenUsage is Hindsight's TokenUsage.
type hsTokenUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// hsUsageResponse is the usage part of Hindsight's RetainResponse and ReflectResponse.
type hsUsageResponse struct {
	Text  string        `json:"text"`
	Usage *hsTokenUsage `json:"usage"`
}

// hsRecallRequest is Hindsight's RecallRequest.
type hsRecallRequest struct {
	Query          string     `json:"query"`
	QueryTimestamp string     `json:"query_timestamp,omitempty"`
	Budget         string     `json:"budget,omitempty"`
	MaxTokens      int        `json:"max_tokens,omitempty"`
	Include        *hsInclude `json:"include,omitempty"`
}

// hsInclude is Hindsight's IncludeOptions; a nil Entities pointer is sent as null (entities off).
type hsInclude struct {
	Entities *struct{}       `json:"entities"`
	Chunks   *hsChunkInclude `json:"chunks,omitempty"`
}

// hsChunkInclude is Hindsight's ChunkIncludeOptions.
type hsChunkInclude struct {
	MaxTokens int `json:"max_tokens"`
}

// hsRecallResponse is Hindsight's RecallResponse; its field names match harness.Recalled.
type hsRecallResponse struct {
	Results []harness.Memory         `json:"results"`
	Chunks  map[string]harness.Chunk `json:"chunks"`
}

// hsReflectRequest is Hindsight's ReflectRequest.
type hsReflectRequest struct {
	Query string `json:"query"`
}

// hsOperations is Hindsight's OperationsListResponse.
type hsOperations struct {
	Total      int `json:"total"`
	Operations []struct {
		TaskType string `json:"task_type"`
	} `json:"operations"`
}

// Name returns "hindsight".
func (h *Hindsight) Name() string { return "hindsight" }

// Retain stores s with a synchronous retain. Hindsight reports one total for the call, returned under the
// RETAIN slot.
func (h *Hindsight) Retain(ctx context.Context, bank string, s harness.Session) ([]harness.Usage, error) {
	req := hsRetainRequest{Items: []hsItem{{
		Content: s.Content, Context: s.Context, DocumentID: s.ID, Timestamp: rfc3339(s.Time),
	}}}
	var resp hsUsageResponse
	if err := h.do(ctx, http.MethodPost, bank, "/memories", req, &resp); err != nil {
		return nil, err
	}
	return usage("RETAIN", resp.Usage), nil
}

// PendingOperations counts the bank's pending and processing operations by task type (at most 100 of
// each status are listed, which is enough for the barrier).
func (h *Hindsight) PendingOperations(ctx context.Context, bank string) (map[string]int64, error) {
	pending := map[string]int64{}
	for _, st := range []string{"pending", "processing"} {
		var resp hsOperations
		if err := h.do(ctx, http.MethodGet, bank, operationsQuery(st, pendingLimit), nil, &resp); err != nil {
			return nil, err
		}
		for _, op := range resp.Operations {
			pending[op.TaskType]++
		}
	}
	return pending, nil
}

// Recall returns the recalled facts in result order, with their chunks when opts asks for them. Entity
// observations are always off, as in Hindsight's own benchmark runs.
func (h *Hindsight) Recall(
	ctx context.Context, bank, query string, at time.Time, opts harness.RecallOptions,
) (harness.Recalled, error) {
	req := hsRecallRequest{
		Query: query, QueryTimestamp: rfc3339(at), Budget: strings.ToLower(opts.Budget), MaxTokens: opts.MaxTokens,
		Include: &hsInclude{},
	}
	if opts.ChunkTokens > 0 {
		req.Include.Chunks = &hsChunkInclude{MaxTokens: opts.ChunkTokens}
	}
	var resp hsRecallResponse
	if err := h.do(ctx, http.MethodPost, bank, "/memories/recall", req, &resp); err != nil {
		return harness.Recalled{}, err
	}
	return harness.Recalled(resp), nil
}

// Reflect returns Hindsight's answer and its usage under the REFLECT slot. Hindsight's reflect takes no
// query time, so the time is ignored.
func (h *Hindsight) Reflect(ctx context.Context, bank, query string, _ time.Time) (string, []harness.Usage, error) {
	var resp hsUsageResponse
	if err := h.do(ctx, http.MethodPost, bank, "/reflect", hsReflectRequest{Query: query}, &resp); err != nil {
		return "", nil, err
	}
	return resp.Text, usage("REFLECT", resp.Usage), nil
}

// Background returns the number of failed operations; Hindsight operations carry no usage.
func (h *Hindsight) Background(ctx context.Context, bank string) (harness.BackgroundStats, error) {
	var resp hsOperations
	if err := h.do(ctx, http.MethodGet, bank, operationsQuery("failed", 1), nil, &resp); err != nil {
		return harness.BackgroundStats{}, err
	}
	return harness.BackgroundStats{Failed: resp.Total}, nil
}

// do sends in (JSON, when non-nil) to the bank-scoped path suffix and decodes the response into out.
func (h *Hindsight) do(ctx context.Context, method, bank, suffix string, in, out any) error {
	target := h.base + "/v1/default/banks/" + url.PathEscape(bank) + suffix
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("hindsight: encode %s %s: %w", method, target, err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return fmt.Errorf("hindsight: build %s %s: %w", method, target, err)
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if h.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.apiKey)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("hindsight: %s %s: %w", method, target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("hindsight: read %s %s: %w", method, target, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("hindsight: %s %s: status %d: %s", method, target, resp.StatusCode, truncate(data))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("hindsight: decode %s %s: %w", method, target, err)
	}
	return nil
}

// operationsQuery is the path suffix that lists operations with status, at most limit of them.
func operationsQuery(status string, limit int) string {
	q := url.Values{"status": {status}, "limit": {fmt.Sprint(limit)}}
	return "/operations?" + q.Encode()
}

// usage converts Hindsight's per-call total into a one-slot usage list; nil when it reported none.
func usage(slot string, u *hsTokenUsage) []harness.Usage {
	if u == nil {
		return nil
	}
	return []harness.Usage{{Slot: slot, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens}}
}

// rfc3339 formats t for Hindsight; the zero time becomes "" (omitted).
func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// truncate shortens a response body for an error message.
func truncate(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > maxErrorBody {
		return s[:maxErrorBody] + "..."
	}
	return s
}

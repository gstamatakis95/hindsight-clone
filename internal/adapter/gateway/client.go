package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"memd/internal/domain"
	"memd/internal/port"
	"net/http"
	"strings"
	"time"
)

// Defaults applied by NewClient to zero Config fields.
const (
	// DefaultEmbedModel is the one server-wide embedding model.
	DefaultEmbedModel = "nomic-embed-text-v1.5"
	// DefaultEmbedBatchSize is how many texts one /embeddings request carries.
	DefaultEmbedBatchSize = 64
	// DefaultRerankPath is the rerank route under BaseURL (Cohere/Jina style).
	DefaultRerankPath = "/rerank"
	// DefaultMaxConcurrent is the process-wide cap on in-flight gateway calls.
	DefaultMaxConcurrent = 32
	// DefaultTimeout bounds one HTTP attempt when the binding sets no timeout_ms.
	DefaultTimeout = 60 * time.Second
	// DefaultMaxRetries is the retry budget when the binding sets no max_retries.
	DefaultMaxRetries = 2
	// DefaultRetryBackoff is the wait before the first retry; it doubles per retry up to maxBackoff.
	DefaultRetryBackoff = 500 * time.Millisecond
	// maxBackoff caps the wait between two attempts.
	maxBackoff = 8 * time.Second
	// maxResponseBytes caps how much of a response body the client reads.
	maxResponseBytes = 64 << 20
)

// Config configures the gateway client. Zero fields take the Default* values; MaxConcurrent, EmbedBatchSize
// and Timeout also take them when negative.
type Config struct {
	// BaseURL is the OpenAI-style base URL including the version, e.g. "http://gateway:4000/v1"; the client
	// posts to BaseURL+"/chat/completions", BaseURL+"/embeddings" and BaseURL+RerankPath.
	BaseURL string
	// APIKey is sent as "Authorization: Bearer <key>" when set.
	APIKey string
	// EmbedModel is the embedding model name at the gateway (nomic-embed-text-v1.5, 768 dims).
	EmbedModel string
	// EmbedBatchSize is the number of texts per /embeddings request.
	EmbedBatchSize int
	// RerankPath is the rerank route, relative to BaseURL.
	RerankPath string
	// MaxConcurrent caps in-flight calls across the whole process (every slot, embeddings and rerank too).
	MaxConcurrent int
	// SlotConcurrency optionally caps in-flight calls per slot (ModelSlotEmbed and ModelSlotRerank included);
	// slots without an entry are limited only by MaxConcurrent.
	SlotConcurrency map[domain.ModelSlot]int
	// Timeout bounds one HTTP attempt when the binding sets no timeout_ms (and for embeddings).
	Timeout time.Duration
	// MaxRetries is the retry budget when the binding sets no max_retries (and for embeddings); negative
	// means no retries.
	MaxRetries int
	// RetryBackoff is the wait before the first retry, doubled per retry; negative means no wait.
	RetryBackoff time.Duration
	// OnUsage, when set, is called once per successful HTTP call with that call's token usage (chat,
	// embeddings and rerank alike), e.g. for metrics. Structured and Chat also return their usage.
	OnUsage func(ctx context.Context, u domain.Usage)
}

// withDefaults returns cfg with every zero field replaced by its default.
func (cfg Config) withDefaults() Config {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	cfg.EmbedModel = orDefault(cfg.EmbedModel, DefaultEmbedModel)
	cfg.EmbedBatchSize = positiveOr(cfg.EmbedBatchSize, DefaultEmbedBatchSize)
	cfg.RerankPath = orDefault(cfg.RerankPath, DefaultRerankPath)
	cfg.MaxConcurrent = positiveOr(cfg.MaxConcurrent, DefaultMaxConcurrent)
	cfg.Timeout = positiveOr(cfg.Timeout, DefaultTimeout)
	cfg.MaxRetries = orDefault(cfg.MaxRetries, DefaultMaxRetries)
	cfg.RetryBackoff = orDefault(cfg.RetryBackoff, DefaultRetryBackoff)
	return cfg
}

// positiveOr returns def when v <= 0, else v.
func positiveOr[T int | time.Duration](v, def T) T {
	if v <= 0 {
		return def
	}
	return v
}

// orDefault returns def when v is the zero value, else v.
func orDefault[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

// Client is the AI gateway client: one value implements port.LLM, port.Embedder and port.Reranker over the
// gateway's OpenAI-compatible HTTP API. It is safe for concurrent use.
type Client struct {
	// cfg is the configuration with defaults applied.
	cfg Config
	// hc sends the requests; per-attempt timeouts come from contexts, not from hc.
	hc *http.Client
	// lim caps in-flight calls.
	lim *limiter
}

var (
	_ port.LLM      = (*Client)(nil)
	_ port.Embedder = (*Client)(nil)
	_ port.Reranker = (*Client)(nil)
)

// NewClient returns a client for the gateway at cfg.BaseURL. A nil hc uses a default http.Client.
func NewClient(cfg Config, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{}
	}
	cfg = cfg.withDefaults()
	return &Client{cfg: cfg, hc: hc, lim: newLimiter(cfg.MaxConcurrent, cfg.SlotConcurrency)}
}

// apiUsage is the OpenAI-style usage object; rerank routes may report only total_tokens.
type apiUsage struct {
	// PromptTokens is the input token count.
	PromptTokens int64 `json:"prompt_tokens"`
	// CompletionTokens is the output token count.
	CompletionTokens int64 `json:"completion_tokens"`
	// TotalTokens is input plus output; used as input when PromptTokens is absent.
	TotalTokens int64 `json:"total_tokens"`
}

// toUsage converts the wire usage for slot and model; a nil u is zero tokens.
func (u *apiUsage) toUsage(slot domain.ModelSlot, model string) domain.Usage {
	out := domain.Usage{Slot: slot, Model: model}
	if u == nil {
		return out
	}
	out.InputTokens, out.OutputTokens = u.PromptTokens, u.CompletionTokens
	if out.InputTokens == 0 && out.OutputTokens == 0 {
		out.InputTokens = u.TotalTokens
	}
	return out
}

// record reports one call's usage to the OnUsage hook, if any.
func (c *Client) record(ctx context.Context, u domain.Usage) {
	if c.cfg.OnUsage != nil {
		c.cfg.OnUsage(ctx, u)
	}
}

// addUsage adds the tokens of u to sum; sum keeps its slot, and takes u's model when sum has none.
func addUsage(sum *domain.Usage, u domain.Usage) {
	sum.InputTokens += u.InputTokens
	sum.OutputTokens += u.OutputTokens
	if sum.Model == "" {
		sum.Model = u.Model
	}
}

// attempt describes one HTTP call: where it goes, which slot's permits it takes and how long it may take.
type attempt struct {
	// slot selects the per-slot semaphore.
	slot domain.ModelSlot
	// path is appended to BaseURL.
	path string
	// timeout bounds the HTTP exchange (not the wait for permits).
	timeout time.Duration
}

// timeoutFor is the binding's timeout_ms, or the configured default.
func (c *Client) timeoutFor(b domain.ModelBinding) time.Duration {
	if b.TimeoutMS != nil && *b.TimeoutMS > 0 {
		return time.Duration(*b.TimeoutMS) * time.Millisecond
	}
	return c.cfg.Timeout
}

// retriesFor is the binding's max_retries, or the configured default (never negative).
func (c *Client) retriesFor(b domain.ModelBinding) int {
	n := c.cfg.MaxRetries
	if b.MaxRetries != nil {
		n = *b.MaxRetries
	}
	return max(n, 0)
}

// post makes one HTTP attempt: it takes the permits, POSTs body as JSON within the attempt's timeout and
// decodes the response into out. Errors worth retrying are marked transient.
func (c *Client) post(ctx context.Context, a attempt, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("%w: gateway %s: encode request: %w", domain.ErrInvalidArgument, a.path, err)
	}
	release, err := c.lim.acquire(ctx, a.slot)
	if err != nil {
		return fmt.Errorf("gateway %s: %w", a.path, err)
	}
	defer release()
	actx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	raw, err := c.send(actx, a.path, payload)
	if err != nil {
		return classify(ctx, actx, a, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return transient(fmt.Errorf("%w: gateway %s: decode response: %w", domain.ErrUnavailable, a.path, err))
	}
	return nil
}

// send POSTs payload to path and returns the 2xx response body; other statuses are a *statusError.
func (c *Client) send(ctx context.Context, path string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, &statusError{code: resp.StatusCode, body: snippet(raw)}
	}
	return raw, nil
}

// statusError is a non-2xx gateway response.
type statusError struct {
	// code is the HTTP status.
	code int
	// body is the start of the response body.
	body string
}

// Error implements error.
func (e *statusError) Error() string { return fmt.Sprintf("status %d: %s", e.code, e.body) }

// retryable reports whether the status is worth another attempt: timeouts, rate limits and server errors.
func (e *statusError) retryable() bool {
	return e.code == http.StatusRequestTimeout || e.code == http.StatusTooManyRequests || e.code >= 500
}

// snippet returns at most 512 bytes of a response body for error messages.
func snippet(raw []byte) string {
	const n = 512
	if len(raw) > n {
		raw = raw[:n]
	}
	return strings.TrimSpace(string(raw))
}

// classify maps a failed attempt to the error the caller sees. The caller's own cancellation or deadline is
// returned as is and never retried; an attempt timeout, a transport failure, 408, 429 and 5xx are transient
// domain.ErrUnavailable; any other status is a permanent domain.ErrUnavailable.
func classify(ctx, actx context.Context, a attempt, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("gateway %s: %w", a.path, ctx.Err())
	}
	if errors.Is(actx.Err(), context.DeadlineExceeded) {
		return transient(fmt.Errorf("%w: gateway %s: timed out after %s: %w",
			domain.ErrUnavailable, a.path, a.timeout, context.DeadlineExceeded))
	}
	var se *statusError
	if errors.As(err, &se) && !se.retryable() {
		if se.contextOverflow() {
			return fmt.Errorf("%w: gateway %s: %w", domain.ErrContextOverflow, a.path, err)
		}
		return fmt.Errorf("%w: gateway %s: %w", domain.ErrUnavailable, a.path, err)
	}
	return transient(fmt.Errorf("%w: gateway %s: %w", domain.ErrUnavailable, a.path, err))
}

// overflowMarkers are the (lowercased) body fragments providers use for a prompt over the context window.
var overflowMarkers = []string{
	"context_length_exceeded", "maximum context length", "prompt is too long", "input is too long",
}

// contextOverflow reports whether the response is a 400 or 413 whose body says the prompt did not fit the
// model's context window (CCR WP-11-1).
func (e *statusError) contextOverflow() bool {
	if e.code != http.StatusBadRequest && e.code != http.StatusRequestEntityTooLarge {
		return false
	}
	body := strings.ToLower(e.body)
	for _, m := range overflowMarkers {
		if strings.Contains(body, m) {
			return true
		}
	}
	return false
}

// transientError marks an error as worth retrying; it unwraps to the underlying error.
type transientError struct {
	// err is the underlying error.
	err error
}

// Error implements error.
func (e transientError) Error() string { return e.err.Error() }

// Unwrap returns the underlying error.
func (e transientError) Unwrap() error { return e.err }

// transient marks err as worth retrying.
func transient(err error) error { return transientError{err: err} }

// isTransient reports whether err was marked transient.
func isTransient(err error) bool {
	var te transientError
	return errors.As(err, &te)
}

// retry runs fn until it succeeds, returns a non-transient error, the caller's ctx is done or maxRetries
// retries are spent, waiting a doubling backoff between attempts. It returns fn's last error.
func (c *Client) retry(ctx context.Context, maxRetries int, fn func() error) error {
	for n := 0; ; n++ {
		err := fn()
		if err == nil || !isTransient(err) || n >= maxRetries || ctx.Err() != nil {
			return err
		}
		if werr := c.wait(ctx, n); werr != nil {
			return errors.Join(err, werr)
		}
	}
}

// wait sleeps the backoff before retry n+1, or returns early when ctx is done.
func (c *Client) wait(ctx context.Context, n int) error {
	if c.cfg.RetryBackoff <= 0 {
		return nil
	}
	d := c.cfg.RetryBackoff << min(n, 16)
	if d <= 0 || d > maxBackoff {
		d = maxBackoff
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("gateway retry backoff: %w", ctx.Err())
	}
}

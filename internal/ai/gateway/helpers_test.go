package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"memd/internal/ai/gateway"
	"memd/internal/domain"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeGateway is an httptest server emulating the gateway's /v1/chat/completions, /v1/embeddings and
// /v1/rerank routes. Each route answers with its handler and records every request body it received.
type fakeGateway struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests map[string][]map[string]any // route -> decoded request bodies, in arrival order

	chat   func(n int, body map[string]any) (int, any) // n counts calls to the route, from 0
	embed  func(n int, body map[string]any) (int, any)
	rerank func(n int, body map[string]any) (int, any)
	// delay, when set, is waited (or the request's context, whichever ends first) before each answer.
	delay time.Duration
	// onEnter and onExit, when set, bracket every request (in-flight counting).
	onEnter, onExit func()
}

// newFakeGateway starts the server; handlers are set on the returned value before use.
func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	g := &fakeGateway{requests: map[string][]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", g.serve("chat", func() func(int, map[string]any) (int, any) {
		return g.chat
	}))
	mux.HandleFunc("POST /v1/embeddings", g.serve("embed", func() func(int, map[string]any) (int, any) {
		return g.embed
	}))
	mux.HandleFunc("POST /v1/rerank", g.serve("rerank", func() func(int, map[string]any) (int, any) {
		return g.rerank
	}))
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

// serve records the body, waits the delay and answers with the route's handler.
func (g *fakeGateway) serve(route string, handler func() func(int, map[string]any) (int, any)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.onEnter != nil {
			g.onEnter()
		}
		if g.onExit != nil {
			defer g.onExit()
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		g.mu.Lock()
		n := len(g.requests[route])
		g.requests[route] = append(g.requests[route], body)
		g.mu.Unlock()
		if g.delay > 0 {
			select {
			case <-time.After(g.delay):
			case <-r.Context().Done():
				return
			}
		}
		status, resp := handler()(n, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// bodies returns the request bodies a route received.
func (g *fakeGateway) bodies(route string) []map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]map[string]any(nil), g.requests[route]...)
}

// client returns a gateway client for the fake with a 1ms retry backoff.
func (g *fakeGateway) client(cfg gateway.Config) *gateway.Client {
	cfg.BaseURL = g.srv.URL + "/v1"
	if cfg.RetryBackoff == 0 {
		cfg.RetryBackoff = time.Millisecond
	}
	return gateway.NewClient(cfg, g.srv.Client())
}

// chatReply builds a chat completion with text content and usage.
func chatReply(content string, in, out int) map[string]any {
	return map[string]any{
		"model": "served-model",
		"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": in, "completion_tokens": out, "total_tokens": in + out},
	}
}

// vec returns a dim-float vector whose first element is x.
func vec(x float64, dim int) []float64 {
	v := make([]float64, dim)
	v[0] = x
	return v
}

// embedEcho answers an embeddings request with one 768-dim vector per input whose first element is the
// input's position in the request plus offset, listed in reverse order to exercise reordering by index.
func embedEcho(offset float64) func(int, map[string]any) (int, any) {
	return func(_ int, body map[string]any) (int, any) {
		input, _ := body["input"].([]any)
		data := make([]any, 0, len(input))
		for i := len(input) - 1; i >= 0; i-- {
			data = append(data, map[string]any{"index": i, "embedding": vec(float64(i)+offset, domain.EmbeddingDim)})
		}
		return http.StatusOK, map[string]any{"model": "nomic", "data": data, "usage": map[string]any{
			"prompt_tokens": len(input), "total_tokens": len(input),
		}}
	}
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// binding returns a binding for model m with no retries unless set later.
func binding(m string) domain.ModelBinding { return domain.ModelBinding{Model: m} }

// mustJSON marshals v or fails the test.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// withTimeout returns a context that ends after d, cancelled at cleanup.
func withTimeout(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	t.Cleanup(cancel)
	return ctx
}

// str formats v for messages.
func str(v any) string { return fmt.Sprint(v) }

package gatewayemu

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// Routes served under the base path, matching internal/adapter/gateway's defaults.
const (
	// BasePath is the OpenAI-style version prefix; the gateway client's BaseURL is URL()+BasePath.
	BasePath = "/v1"
	// ChatPath is the chat completions route.
	ChatPath = BasePath + "/chat/completions"
	// EmbeddingsPath is the embeddings route.
	EmbeddingsPath = BasePath + "/embeddings"
	// RerankPath is the rerank route (the adapter's DefaultRerankPath under BaseURL).
	RerankPath = BasePath + "/rerank"
	// maxBody caps a request body.
	maxBody = 32 << 20
)

// Operation names: what a request was recognised as. Structured calls use their schema name (the prompt
// template name, e.g. "extract_concise" or "consolidate_system"); the others use these.
const (
	// OpEmbeddings is an /embeddings call.
	OpEmbeddings = "embeddings"
	// OpRerank is a /rerank call.
	OpRerank = "rerank"
	// OpReflectLoop is a chat that offers tools (a reflect or agentic-refresh turn).
	OpReflectLoop = "reflect_loop"
	// OpReflectFinal is the tool-less reflect final-answer chat (model ModelFor("reflect")).
	OpReflectFinal = "reflect_final"
	// OpSynthesize is the single-shot SYNTHESIZE chat (model ModelFor("synthesize")).
	OpSynthesize = "synthesize"
	// OpMarkdownDelta is the markdown MENTAL_MODEL_DELTA chat (model ModelFor("mental_model_delta")).
	OpMarkdownDelta = "markdown_delta"
	// OpChat is a plain chat whose model names no known slot; it gets a quoting answer.
	OpChat = "chat"
)

// ModelPrefix prefixes every slot model name the harness binds (MEMD_MODEL_<SLOT>=emu-<slot>).
const ModelPrefix = "emu-"

// ModelFor returns the gateway model name the harness binds slot to, e.g. "emu-synthesize" for "synthesize".
// The slot names are domain.ModelSlot values.
func ModelFor(slot string) string { return ModelPrefix + slot }

// Call is one request the emulator answered.
type Call struct {
	// Op is what the request was recognised as (an Op* constant or a schema name).
	Op string
	// Model is the requested model.
	Model string
	// Status is the HTTP status returned.
	Status int
	// Fault reports whether a scripted fault shaped the response.
	Fault bool
}

// Options tunes the emulator.
type Options struct {
	// Embed selects the embedding mode; the zero value is EmbedSharedTokens.
	Embed EmbedMode
}

// Server is the emulated gateway. It is safe for concurrent use.
type Server struct {
	opts   Options
	mux    *http.ServeMux
	mu     sync.Mutex
	faults []*faultState
	calls  []Call
}

var _ http.Handler = (*Server)(nil)

// New returns an emulator; serve it with Start or mount it as an http.Handler.
func New(opts Options) *Server {
	s := &Server{opts: opts, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST "+ChatPath, s.serveChat)
	s.mux.HandleFunc("POST "+EmbeddingsPath, s.serveEmbeddings)
	s.mux.HandleFunc("POST "+RerankPath, s.serveRerank)
	return s
}

// Start serves s on a loopback port. The caller closes the returned server (Close blocks until in-flight
// requests finish; scripted delays end when their request is cancelled).
func Start(opts Options) (*Server, *httptest.Server) {
	s := New(opts)
	return s, httptest.NewServer(s)
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// Calls returns every answered request in arrival order.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Count returns how many answered requests were recognised as op.
func (s *Server) Count(op string) int {
	n := 0
	for _, c := range s.Calls() {
		if c.Op == op {
			n++
		}
	}
	return n
}

// record appends a call to the log.
func (s *Server) record(c Call) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, c)
}

// readJSON decodes the request body into v.
func readJSON(r *http.Request, v any) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("decode body: %w", err)
	}
	return nil
}

// writeJSON writes v as a 200 JSON response.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes an OpenAI-style error body with status.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": msg, "type": "emulator"}})
}

// usage is the OpenAI-style usage object.
type usage struct {
	// PromptTokens is the input token estimate.
	PromptTokens int `json:"prompt_tokens"`
	// CompletionTokens is the output token estimate.
	CompletionTokens int `json:"completion_tokens"`
	// TotalTokens is their sum.
	TotalTokens int `json:"total_tokens"`
}

// newUsage estimates tokens as one per four bytes, at least one each.
func newUsage(in, out int) usage {
	u := usage{PromptTokens: in/4 + 1}
	if out >= 0 {
		u.CompletionTokens = out/4 + 1
	}
	u.TotalTokens = u.PromptTokens + u.CompletionTokens
	return u
}

// totalLen sums the lengths of texts.
func totalLen(texts []string) int {
	n := 0
	for _, t := range texts {
		n += len(t)
	}
	return n
}

// modelSlot returns the slot a model name binds ("emu-synthesize" -> "synthesize"), or "" for other models.
func modelSlot(model string) string {
	slot, ok := strings.CutPrefix(model, ModelPrefix)
	if !ok {
		return ""
	}
	return slot
}

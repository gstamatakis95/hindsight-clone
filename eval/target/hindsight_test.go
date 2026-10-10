package target_test

import (
	"context"
	"encoding/json"
	"io"
	"memd/eval/harness"
	"memd/eval/target"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeHindsight emulates the stock Hindsight endpoints the target uses for the bank "b/1" (escaped b%2F1).
type fakeHindsight struct {
	t      *testing.T
	mu     sync.Mutex
	bodies map[string]map[string]any
}

const bankPath = "/v1/default/banks/b%2F1"

func (f *fakeHindsight) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("Authorization"); got != "Bearer secret" {
		f.t.Errorf("Authorization = %q", got)
	}
	route := r.Method + " " + strings.TrimPrefix(r.URL.EscapedPath(), bankPath)
	if r.Method == http.MethodPost {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			f.t.Errorf("%s: bad JSON body %q: %v", route, raw, err)
		}
		f.mu.Lock()
		f.bodies[route] = body
		f.mu.Unlock()
	}
	reply := map[string]string{
		"POST /memories": `{"success":true,"bank_id":"b/1","items_count":1,"async":false,` +
			`"usage":{"input_tokens":50,"output_tokens":7,"total_tokens":57}}`,
		"POST /memories/recall": `{"results":[{"id":"1","text":"Alice likes tea"},{"id":"2","text":"Bob likes coffee"}]}`,
		"POST /reflect":         `{"text":"Tea.","usage":{"input_tokens":300,"output_tokens":4}}`,
	}[route]
	if route == "GET /operations" {
		reply = operationsReply(r.URL.Query())
	}
	if reply == "" {
		http.Error(w, "unexpected "+route, http.StatusNotFound)
		f.t.Errorf("unexpected request %s", route)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, reply)
}

func operationsReply(q map[string][]string) string {
	switch strings.Join(q["status"], ",") + " " + strings.Join(q["limit"], ",") {
	case "pending 100":
		return `{"total":2,"operations":[{"id":"a","task_type":"consolidation"},{"id":"b","task_type":"retain"}]}`
	case "processing 100":
		return `{"total":1,"operations":[{"id":"c","task_type":"consolidation"}]}`
	case "failed 1":
		return `{"total":3,"operations":[{"id":"d","task_type":"retain"}]}`
	}
	return ""
}

var at = time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)

// newFakeHindsight serves a fakeHindsight and returns a target pointed at it (with a trailing slash in the
// base URL, which the target must trim).
func newFakeHindsight(t *testing.T) (*target.Hindsight, *fakeHindsight) {
	t.Helper()
	f := &fakeHindsight{t: t, bodies: map[string]map[string]any{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return target.NewHindsight(srv.URL+"/", "secret", nil), f
}

func TestHindsightRetain(t *testing.T) {
	h, f := newFakeHindsight(t)
	if h.Name() != "hindsight" {
		t.Fatalf("Name = %q", h.Name())
	}
	s := harness.Session{ID: "s1", Time: at, Context: "chat", Content: "hi"}
	usage, err := h.Retain(context.Background(), "b/1", s)
	if err != nil {
		t.Fatalf("Retain: %v", err)
	}
	wantUsage := []harness.Usage{{Slot: "RETAIN", InputTokens: 50, OutputTokens: 7}}
	if !reflect.DeepEqual(usage, wantUsage) {
		t.Fatalf("Retain usage = %+v", usage)
	}
	wantBody := map[string]any{"async": false, "items": []any{map[string]any{
		"content": "hi", "context": "chat", "document_id": "s1", "timestamp": "2024-05-01T10:00:00Z",
	}}}
	if got := f.bodies["POST /memories"]; !reflect.DeepEqual(got, wantBody) {
		t.Fatalf("retain body = %v, want %v", got, wantBody)
	}
}

func TestHindsightRecall(t *testing.T) {
	h, f := newFakeHindsight(t)
	ctx := context.Background()
	texts, err := h.Recall(ctx, "b/1", "what does Alice like?", time.Time{})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if !reflect.DeepEqual(texts, []string{"Alice likes tea", "Bob likes coffee"}) {
		t.Fatalf("Recall = %q", texts)
	}
	got := f.bodies["POST /memories/recall"]
	if !reflect.DeepEqual(got, map[string]any{"query": "what does Alice like?"}) {
		t.Fatalf("recall body = %v (zero time must omit query_timestamp)", got)
	}
	if _, err := h.Recall(ctx, "b/1", "q", at); err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if got := f.bodies["POST /memories/recall"]["query_timestamp"]; got != "2024-05-01T10:00:00Z" {
		t.Fatalf("query_timestamp = %v", got)
	}
}

func TestHindsightReflect(t *testing.T) {
	h, f := newFakeHindsight(t)
	answer, usage, err := h.Reflect(context.Background(), "b/1", "what does Alice drink?", at)
	if err != nil {
		t.Fatalf("Reflect: %v", err)
	}
	want := []harness.Usage{{Slot: "REFLECT", InputTokens: 300, OutputTokens: 4}}
	if answer != "Tea." || !reflect.DeepEqual(usage, want) {
		t.Fatalf("Reflect = %q, %+v", answer, usage)
	}
	if got := f.bodies["POST /reflect"]; !reflect.DeepEqual(got, map[string]any{"query": "what does Alice drink?"}) {
		t.Fatalf("reflect body = %v", got)
	}
}

func TestHindsightOperations(t *testing.T) {
	h, _ := newFakeHindsight(t)
	ctx := context.Background()
	pending, err := h.PendingOperations(ctx, "b/1")
	if err != nil {
		t.Fatalf("PendingOperations: %v", err)
	}
	if !reflect.DeepEqual(pending, map[string]int64{"consolidation": 2, "retain": 1}) {
		t.Fatalf("PendingOperations = %v", pending)
	}

	bg, err := h.Background(ctx, "b/1")
	if err != nil {
		t.Fatalf("Background: %v", err)
	}
	if !reflect.DeepEqual(bg, harness.BackgroundStats{Failed: 3}) {
		t.Fatalf("Background = %+v", bg)
	}
}

func TestHindsightError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("Authorization sent without an api key: %q", r.Header.Get("Authorization"))
		}
		http.Error(w, strings.Repeat("boom ", 1000), http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	h := target.NewHindsight(srv.URL, "", srv.Client())
	_, err := h.Recall(context.Background(), "b", "q", time.Time{})
	if err == nil {
		t.Fatal("Recall on a 500 returned no error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "status 500") || !strings.Contains(msg, "boom") || len(msg) > 1000 {
		t.Fatalf("error = %q (want status code and a truncated body)", msg)
	}
}

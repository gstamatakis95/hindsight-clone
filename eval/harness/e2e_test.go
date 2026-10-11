package harness_test

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/eval/harness"
	"memd/eval/judge"
	"memd/eval/stub"
	"memd/eval/target"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func stubConversations() []harness.Conversation {
	day := time.Date(2024, 5, 1, 9, 0, 0, 0, time.UTC)
	var convs []harness.Conversation
	for _, id := range []string{"alice", "bob", "carol"} {
		convs = append(convs, harness.Conversation{
			ID: id,
			Sessions: []harness.Session{
				{ID: id + "-1", Time: day, Content: "user: My favourite city is Kyoto.\nassistant: Lovely."},
				{ID: id + "-2", Time: day.AddDate(0, 0, 7), Content: "user: I adopted a parrot named Mango."},
			},
			Questions: []harness.Question{
				{ID: "q1", Category: "single-hop", Query: "Which city is the favourite?", Answer: "Kyoto"},
				{ID: "q2", Category: "single-hop", Query: "What is the parrot named?", Answer: "Mango"},
				{ID: "q3", Category: "abstention", Query: "What is the cat named?", Answer: "Whiskers"},
			},
		})
	}
	for i := range convs {
		for j := range convs[i].Questions {
			convs[i].Questions[j].Kind = harness.KindLoCoMo
		}
	}
	return convs
}

var (
	contextRE = regexp.MustCompile(`(?s)Context:\n\n(.*)\n\nQuestion:`)
	gradeRE   = regexp.MustCompile(`Gold answer: (.*)\nGenerated answer: (.*)\n`)
)

// fakeGateway is the answer and judge model: the answer is the first recalled memory's text, and the
// judge says correct when the answer contains the gold answer.
func fakeGateway(t *testing.T) *judge.Chat {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) != 1 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		content, _ := json.Marshal(fakeReply(req.Messages[0].Content))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":` + string(content) + `}}],` +
			`"usage":{"prompt_tokens":10,"completion_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)
	return judge.NewChat(srv.URL+"/v1", "fake", "", srv.Client())
}

func fakeReply(prompt string) string {
	if m := gradeRE.FindStringSubmatch(prompt); m != nil {
		correct := strings.Contains(strings.ToLower(m[2]), strings.ToLower(m[1]))
		out, _ := json.Marshal(map[string]any{"reason": "fake", "correct": correct})
		return string(out)
	}
	var rec harness.Recalled
	if m := contextRE.FindStringSubmatch(prompt); m != nil && json.Unmarshal([]byte(m[1]), &rec) == nil &&
		len(rec.Results) > 0 {
		out, _ := json.Marshal(map[string]string{"reasoning": "first memory", "answer": rec.Results[0].Text})
		return string(out)
	}
	return `{"reasoning": "nothing", "answer": "I don't know"}`
}

// crashingTarget counts successful retains and recalls, and cancels the run once it has served
// crashAfter recalls, standing in for a process that dies mid-run.
type crashingTarget struct {
	harness.Target
	mu         sync.Mutex
	retains    int
	recalls    int
	crashAfter int
	crash      context.CancelFunc
}

func (c *crashingTarget) Retain(ctx context.Context, bank string, s harness.Session) ([]harness.Usage, error) {
	u, err := c.Target.Retain(ctx, bank, s)
	if err != nil {
		return nil, fmt.Errorf("crashing target: %w", err)
	}
	c.mu.Lock()
	c.retains++
	c.mu.Unlock()
	return u, nil
}

func (c *crashingTarget) Recall(
	ctx context.Context, bank, query string, at time.Time, opts harness.RecallOptions,
) (harness.Recalled, error) {
	c.mu.Lock()
	c.recalls++
	if c.crash != nil && c.recalls == c.crashAfter {
		c.crash()
	}
	c.mu.Unlock()
	rec, err := c.Target.Recall(ctx, bank, query, at, opts)
	if err != nil {
		return rec, fmt.Errorf("crashing target: %w", err)
	}
	return rec, nil
}

// runInto runs the stub conversations once against tgt in rag mode, journaling into dir, and reports
// whether the run was interrupted.
func runInto(ctx context.Context, t *testing.T, dir string, tgt harness.Target, chat *judge.Chat,
) (harness.TargetReport, bool) {
	t.Helper()
	j, err := harness.OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	r, err := harness.NewRunner(
		harness.Deps{Target: tgt, Judge: judge.NewLLM(chat), Answerer: judge.NewAnswerer(chat), Journal: j},
		harness.Config{
			Mode: harness.ModeRAG, BankPrefix: "e2e-" + strings.ReplaceAll(dir, "/", "") + "-",
			PollInterval: 5 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := r.Run(ctx, stubConversations())
	return rep, err != nil
}

func dialStub(t *testing.T) *grpc.ClientConn {
	t.Helper()
	addr, stop, err := stub.Start(stub.Options{PendingPolls: 3})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// TestResumeSkipsCompletedWork drives the whole harness (memd gRPC target, sync retain, barrier, rag
// answer, LLM judge, journal) against the stub, crashes it partway, and resumes it: no session is retained
// twice, only the unanswered questions are asked, and the final report equals an uninterrupted run's.
func TestResumeSkipsCompletedWork(t *testing.T) {
	conn := dialStub(t)
	chat := fakeGateway(t)
	want, interrupted := runInto(context.Background(), t, t.TempDir(), target.NewMemd(conn, "t1"), chat)
	if interrupted || want.Overall.Total != 9 || want.Overall.Correct != 6 || want.Pending != 0 {
		t.Fatalf("uninterrupted run: overall %+v pending %d errors %v", want.Overall, want.Pending, want.Errors)
	}

	dir := t.TempDir()
	ctx, crash := context.WithCancel(context.Background())
	tgt := &crashingTarget{Target: target.NewMemd(conn, "t1"), crashAfter: 5, crash: crash}
	partial, interrupted := runInto(ctx, t, dir, tgt, chat)
	if !interrupted || partial.Pending == 0 {
		t.Fatalf("crashed run: interrupted %v, pending %d; want an interrupted, partial run", interrupted, partial.Pending)
	}
	retainedBefore, unanswered := tgt.retains, partial.Pending

	tgt = &crashingTarget{Target: target.NewMemd(conn, "t1")}
	got, interrupted := runInto(context.Background(), t, dir, tgt, chat)
	if interrupted || retainedBefore+tgt.retains != 6 || tgt.recalls != unanswered {
		t.Errorf("resume: interrupted %v, sessions retained %d before + %d after (want 6 in all), "+
			"questions asked %d (want the %d unanswered)", interrupted, retainedBefore, tgt.retains, tgt.recalls, unanswered)
	}
	if !reflect.DeepEqual(got.Overall, want.Overall) || !reflect.DeepEqual(got.Categories, want.Categories) ||
		got.Pending != 0 || len(got.Errors) != 0 {
		t.Fatalf("resumed report: overall %+v categories %+v pending %d errors %v; want %+v %+v",
			got.Overall, got.Categories, got.Pending, got.Errors, want.Overall, want.Categories)
	}
}

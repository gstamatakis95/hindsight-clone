package harness_test

import (
	"bytes"
	"context"
	"memd/eval/harness"
	"memd/eval/judge"
	"memd/eval/stub"
	"memd/eval/target"
	"strings"
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
	return convs
}

// TestRunEndToEndAgainstStub drives the whole harness (memd gRPC target, sync retain, barrier, recall or
// reflect, judge, report) against the in-process stub server.
func TestRunEndToEndAgainstStub(t *testing.T) {
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

	for _, mode := range []string{harness.ModeReflect, harness.ModeRecall} {
		t.Run(mode, func(t *testing.T) {
			r := harness.NewRunner(target.NewMemd(conn, "t1"), judge.Contains{}, harness.SystemClock{}, harness.Config{
				Mode: mode, BankPrefix: "e2e-" + mode + "-", Parallel: 2, PollInterval: 5 * time.Millisecond,
			})
			tr, err := r.Run(context.Background(), stubConversations())
			if err != nil {
				t.Fatal(err)
			}
			checkEndToEnd(t, tr, mode)
		})
	}
}

func checkEndToEnd(t *testing.T, tr harness.TargetReport, mode string) {
	t.Helper()
	if tr.Target != "memd" || tr.Conversations != 3 || len(tr.Errors) != 0 {
		t.Fatalf("target=%s conversations=%d errors=%v", tr.Target, tr.Conversations, tr.Errors)
	}
	if tr.Overall.Total != 9 || tr.Overall.Correct != 6 {
		t.Fatalf("overall = %+v, want 6/9 (abstention answers are absent)", tr.Overall)
	}
	checkUsageAndLatency(t, tr, mode)
	var md bytes.Buffer
	if err := (harness.Report{Dataset: "stub", Mode: mode, Targets: []harness.TargetReport{tr}}).
		WriteMarkdown(&md); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md.String(), "| single-hop | 100.0% (6/6) |") {
		t.Fatalf("markdown:\n%s", md.String())
	}
}

func checkUsageAndLatency(t *testing.T, tr harness.TargetReport, mode string) {
	t.Helper()
	slots := map[string]bool{}
	for _, u := range tr.Usage {
		slots[u.Slot] = u.InputTokens > 0
	}
	if !slots["CONSOLIDATE"] || slots["REFLECT"] != (mode == harness.ModeReflect) {
		t.Fatalf("usage = %+v", tr.Usage)
	}
	ops := map[string]harness.LatencyStats{}
	for _, l := range tr.Latency {
		ops[l.Op] = l
	}
	if ops["retain"].Count != 6 || ops["barrier"].Count != 3 || ops[mode].Count != 9 || ops["judge"].Count != 9 {
		t.Fatalf("latency = %+v", tr.Latency)
	}
}

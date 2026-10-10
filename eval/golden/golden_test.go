package golden_test

import (
	"context"
	"memd/eval/golden"
	"memd/eval/harness"
	"memd/eval/judge"
	"memd/eval/stub"
	"memd/eval/target"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestLoadGolden(t *testing.T) {
	convs, err := golden.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(convs) < 3 {
		t.Fatalf("fixtures = %d, want >= 3", len(convs))
	}
	for _, c := range convs {
		if len(c.Sessions) == 0 || len(c.Questions) == 0 {
			t.Errorf("%s: empty fixture", c.ID)
		}
		for _, s := range c.Sessions {
			if s.Time.IsZero() {
				t.Errorf("%s/%s: no timestamp", c.ID, s.ID)
			}
		}
	}
}

func TestLoadGoldenRejectsBadFixtures(t *testing.T) {
	const ok = `"sessions":[{"id":"s","content":"c"}],"checks":[{"id":"q","query":"q","accept":["a"]}]`
	tests := map[string]string{
		"unknown field": `{"name":"x",` + ok + `,"extra":1}`,
		"no accept":     `{"name":"x","sessions":[{"id":"s","content":"c"}],"checks":[{"id":"q","query":"q"}]}`,
		"no sessions":   `{"name":"x","sessions":[],"checks":[{"id":"q","query":"q","accept":["a"]}]}`,
		"trailing":      `{"name":"x",` + ok + `} {}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "f.yaml"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := golden.Load(dir); err == nil {
				t.Fatal("want error")
			}
		})
	}
	if _, err := golden.Load(t.TempDir()); err == nil {
		t.Fatal("empty dir: want error")
	}
}

// TestGoldenFixturesUnderTwoMinutes runs every fixture through the real harness, memd gRPC target and
// substring judge against the in-process stub server, as CI does against `memd all`.
func TestGoldenFixturesUnderTwoMinutes(t *testing.T) {
	convs, err := golden.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	addr, stop, err := stub.Start(stub.Options{PendingPolls: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	start := time.Now()
	runner := harness.NewRunner(target.NewMemd(conn, "golden"), judge.Contains{}, harness.SystemClock{},
		harness.Config{Mode: harness.ModeRecall, BankPrefix: "golden-", Parallel: 4, PollInterval: 10 * time.Millisecond})
	rep, err := runner.Run(ctx, convs)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= 2*time.Minute {
		t.Fatalf("golden run took %s, want < 2m", elapsed)
	}
	for _, q := range rep.Questions {
		if !q.Correct {
			t.Errorf("%s/%s failed: %s %s\nresponse: %s", q.Conversation, q.ID, q.Reason, q.Error, q.Response)
		}
	}
	if rep.Overall.Total == 0 || rep.Overall.Correct != rep.Overall.Total {
		t.Fatalf("overall = %+v", rep.Overall)
	}
}

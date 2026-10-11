//go:build e2e

package e2e

import (
	"context"
	"memd/eval/golden"
	"memd/eval/harness"
	"memd/eval/judge"
	"memd/eval/target"
	"memd/test/e2e/gatewayemu"
	e2eharness "memd/test/e2e/harness"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestGoldenAgainstMemdAll runs every eval/golden fixture through the eval harness (sync retain, the
// BankStats barrier, recall mode, substring judge) against `memd all` with the emulated gateway. Every
// check must pass.
func TestGoldenAgainstMemdAll(t *testing.T) {
	bin := e2eharness.BuildMemd(t)
	convs, err := golden.Load("")
	if err != nil {
		t.Fatal(err)
	}
	s := e2eharness.StartStack(t)
	_, gw := e2eharness.StartEmulator(t, gatewayemu.Options{})
	all := s.StartMemd(t, bin, e2eharness.MemdOptions{
		GatewayURL: gw.URL + gatewayemu.BasePath, Env: fastWorker, Mode: e2eharness.ModeAll,
	})
	conn, err := grpc.NewClient(all.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	journal, err := harness.OpenJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	runner, err := harness.NewRunner(
		harness.Deps{Target: target.NewMemd(conn, "golden"), Judge: judge.Contains{}, Journal: journal},
		harness.Config{
			Mode: harness.ModeRecall, BankPrefix: "golden-", Parallel: 4, PollInterval: 200 * time.Millisecond,
			QuietPolls: 2, SettleTimeout: 90 * time.Second,
		})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := runner.Run(ctx, convs)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range rep.Questions {
		if !q.Correct {
			t.Errorf("%s/%s failed: %s %s\nresponse: %s", q.Conversation, q.ID, q.Reason, q.Error, q.Response)
		}
	}
	if rep.Overall.Total == 0 || rep.Overall.Correct != rep.Overall.Total {
		t.Fatalf("overall = %+v\n--- memd log tail ---\n%s", rep.Overall, tail(all.Logs(), 4000))
	}
}

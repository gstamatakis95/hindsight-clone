package target_test

import (
	"context"
	"memd/eval/harness"
	"memd/eval/stub"
	"memd/eval/target"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// dialStub starts a stub on loopback and returns a client connection to it; both are closed at cleanup.
func dialStub(t *testing.T, o stub.Options) *grpc.ClientConn {
	t.Helper()
	addr, stop, err := stub.Start(o)
	if err != nil {
		t.Fatalf("stub.Start: %v", err)
	}
	t.Cleanup(stop)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestMemdRetainRecallReflect(t *testing.T) {
	ctx := context.Background()
	m := target.NewMemd(dialStub(t, stub.Options{PendingPolls: 2}), "t1")
	if m.Name() != "memd" {
		t.Fatalf("Name = %q", m.Name())
	}
	s := harness.Session{
		ID:      "s1",
		Time:    time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC),
		Context: "chat",
		Content: "user: I moved to Lisbon\nuser: my sister lives in Porto",
	}
	usage, err := m.Retain(ctx, "bank-1", s)
	if err != nil {
		t.Fatalf("Retain: %v", err)
	}
	if len(usage) != 1 || usage[0] != (harness.Usage{Slot: "EXTRACT", InputTokens: 20, OutputTokens: 4}) {
		t.Fatalf("Retain usage = %v, want the response's EXTRACT usage", usage)
	}

	for i, want := range []int{1, 1, 0} {
		pending, err := m.PendingOperations(ctx, "bank-1")
		if err != nil {
			t.Fatalf("PendingOperations: %v", err)
		}
		if len(pending) != want {
			t.Fatalf("poll %d: pending = %v, want %d kinds", i, pending, want)
		}
	}

	texts, err := m.Recall(ctx, "bank-1", "Where did I move to, Lisbon?", time.Time{})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(texts) != 1 || texts[0] != "user: I moved to Lisbon" {
		t.Fatalf("Recall = %q", texts)
	}

	answer, usage, err := m.Reflect(ctx, "bank-1", "Where does my sister live?", s.Time)
	if err != nil {
		t.Fatalf("Reflect: %v", err)
	}
	if answer != "user: my sister lives in Porto" {
		t.Fatalf("Reflect = %q", answer)
	}
	want := []harness.Usage{{Slot: "REFLECT", InputTokens: 100, OutputTokens: 20}}
	if !reflect.DeepEqual(usage, want) {
		t.Fatalf("Reflect usage = %+v, want %+v", usage, want)
	}
}

func TestMemdBackground(t *testing.T) {
	ctx := context.Background()
	m := target.NewMemd(dialStub(t, stub.Options{FailedOps: 1}), "t1")
	for _, id := range []string{"s1", "s2"} {
		if _, err := m.Retain(ctx, "b", harness.Session{ID: id, Content: "hello"}); err != nil {
			t.Fatalf("Retain: %v", err)
		}
	}
	got, err := m.Background(ctx, "b")
	if err != nil {
		t.Fatalf("Background: %v", err)
	}
	want := harness.BackgroundStats{
		Usage:  []harness.Usage{{Slot: "CONSOLIDATE", InputTokens: 20, OutputTokens: 10}},
		Failed: 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Background = %+v, want %+v", got, want)
	}
}

func TestMemdTenantRequired(t *testing.T) {
	m := target.NewMemd(dialStub(t, stub.Options{}), "")
	_, err := m.Retain(context.Background(), "b", harness.Session{ID: "s1", Content: "x"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Retain without tenant: got %v, want InvalidArgument", err)
	}
}

func TestMemdTenantIsolation(t *testing.T) {
	ctx := context.Background()
	conn := dialStub(t, stub.Options{})
	if _, err := target.NewMemd(conn, "t1").Retain(ctx, "b", harness.Session{ID: "s1", Content: "x"}); err != nil {
		t.Fatalf("Retain: %v", err)
	}
	_, err := target.NewMemd(conn, "t2").PendingOperations(ctx, "b")
	if status.Code(err) != codes.NotFound {
		t.Fatalf("other tenant's bank: got %v, want NotFound", err)
	}
}

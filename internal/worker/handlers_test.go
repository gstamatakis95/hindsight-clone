package worker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"memd/internal/adapter/postgres"
	"memd/internal/adapter/postgres/pgtest"
	"memd/internal/domain"
	"memd/internal/engine/consolidate"
	"memd/internal/engine/retain"
	"memd/internal/fake"
	"memd/internal/port"
	"memd/internal/worker"
	"slices"
	"testing"
	"time"

	mockbank "memd/internal/mocks/engine/bank"
	mockconsolidate "memd/internal/mocks/engine/consolidate"
	mockmaintenance "memd/internal/mocks/engine/maintenance"
	mockretain "memd/internal/mocks/engine/retain"
)

// hBank is the bank the handler tests use.
var hBank = port.BankRef{Tenant: "t", Bank: "b"}

// capture is a worker.JobRunner that records the registered handlers.
type capture map[port.JobKind]worker.Handler

func (c capture) Handle(kind port.JobKind, h worker.Handler) { c[kind] = h }
func (c capture) Run(context.Context) error                  { return nil }

// handlerEnv is a store over the test's database with hBank created, and a clock frozen at the start.
type handlerEnv struct {
	store *postgres.Store
	clock *fake.Clock
}

func newHandlerEnv(t *testing.T) handlerEnv {
	t.Helper()
	clock := fake.NewClock(time.Now().UTC().Truncate(time.Microsecond))
	s := pgtest.NewStore(t)
	if err := s.InTx(t.Context(), func(tx port.Tx) error {
		_, err := tx.Banks().Ensure(t.Context(), hBank)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return handlerEnv{store: s, clock: clock}
}

// register registers h (with the env's store and clock) and returns the handlers by kind.
func (e handlerEnv) register(h worker.Handlers) capture {
	h.Store, h.Clock = e.store, e.clock
	c := capture{}
	worker.Register(c, h)
	return c
}

// ops lists the bank's operations of kind, newest first.
func (e handlerEnv) ops(t *testing.T, kind port.JobKind) []domain.Operation {
	t.Helper()
	var out []domain.Operation
	if err := e.store.InTx(t.Context(), func(tx port.Tx) error {
		var err error
		out, _, err = tx.Queue().List(t.Context(), hBank, port.OperationFilter{Kind: kind, PageSize: 100})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestRegisterEveryKindButRetainBatch checks every job kind except retain_batch gets a handler, and that a
// nil engine fails its job permanently.
func TestRegisterEveryKindButRetainBatch(t *testing.T) {
	e := newHandlerEnv(t)
	c := e.register(worker.Handlers{})
	for _, k := range []port.JobKind{
		domain.OperationKindRetainDocument, domain.OperationKindConsolidate, domain.OperationKindRefreshMentalModel,
		domain.OperationKindDeleteBank, domain.OperationKindRederiveMemory, domain.OperationKindRetagDocument,
		domain.OperationKindGraphMaintenance,
	} {
		h, ok := c[k]
		if !ok {
			t.Fatalf("no handler for %s", k)
		}
		if err := h(t.Context(), port.Job{Bank: hBank, Kind: k}); !errors.Is(err, worker.ErrPermanent) {
			t.Errorf("%s with no engine: %v, want ErrPermanent", k, err)
		}
	}
	if _, ok := c[domain.OperationKindRetainBatch]; ok {
		t.Error("retain_batch has a handler")
	}
}

// TestConsolidateHandlerNeverReenqueuesOnMore checks the handler leaves the continuation to Run (decision
// 1000): More=true queues nothing more.
func TestConsolidateHandlerNeverReenqueuesOnMore(t *testing.T) {
	e := newHandlerEnv(t)
	c := e.register(worker.Handlers{Consolidator: &mockconsolidate.Consolidator{
		RunFunc: func(context.Context, port.Job) (consolidate.ConsolidationResult, error) {
			return consolidate.ConsolidationResult{Processed: 100, More: true}, nil
		},
	}})
	if err := c[domain.OperationKindConsolidate](t.Context(), port.Job{Bank: hBank}); err != nil {
		t.Fatal(err)
	}
	if ops := e.ops(t, domain.OperationKindConsolidate); len(ops) != 0 {
		t.Fatalf("handler enqueued %d consolidate jobs", len(ops))
	}
}

// TestDeleteBankReenqueuesUntilDone checks a run with work left queues one continuation (DedupePending), and a
// done run queues none.
func TestDeleteBankReenqueuesUntilDone(t *testing.T) {
	e := newHandlerEnv(t)
	done := false
	c := e.register(worker.Handlers{Deleter: &mockbank.BankDeleter{
		RunFunc: func(context.Context, port.Job) (bool, error) { return done, nil },
	}})
	job := port.Job{Bank: hBank, Kind: domain.OperationKindDeleteBank, Payload: json.RawMessage(`{}`)}
	for range 2 {
		if err := c[domain.OperationKindDeleteBank](t.Context(), job); err != nil {
			t.Fatal(err)
		}
	}
	if ops := e.ops(t, domain.OperationKindDeleteBank); len(ops) != 1 || ops[0].SerialKey != "bank" {
		t.Fatalf("continuations: %+v, want one pending (second joined)", ops)
	}
	done = true
	if err := c[domain.OperationKindDeleteBank](t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if ops := e.ops(t, domain.OperationKindDeleteBank); len(ops) != 1 {
		t.Fatalf("a done run queued more: %+v", ops)
	}
}

// TestDeleteBankRetriesWithoutAttemptCap checks a transient failure on the last capped attempt still leaves a
// delayed continuation, so the bank delete is never abandoned; earlier attempts and permanent errors leave
// the retry to the runner.
func TestDeleteBankRetriesWithoutAttemptCap(t *testing.T) {
	cases := []struct {
		name    string
		attempt int
		err     error
		want    int
	}{
		{"first attempt: runner retries", 0, errors.New("db down"), 0},
		{"last attempt: continuation", domain.DefaultMaxAttempts - 1, errors.New("db down"), 1},
		{"permanent: no continuation", domain.DefaultMaxAttempts - 1, domain.ErrInvalidArgument, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newHandlerEnv(t)
			c := e.register(worker.Handlers{Deleter: &mockbank.BankDeleter{
				RunFunc: func(context.Context, port.Job) (bool, error) { return false, tc.err },
			}})
			job := port.Job{Bank: hBank, Kind: domain.OperationKindDeleteBank, Attempt: tc.attempt}
			err := c[domain.OperationKindDeleteBank](t.Context(), job)
			if !errors.Is(err, tc.err) {
				t.Fatalf("error %v, want %v", err, tc.err)
			}
			ops := e.ops(t, domain.OperationKindDeleteBank)
			if len(ops) != tc.want {
				t.Fatalf("%d continuations, want %d", len(ops), tc.want)
			}
			if tc.want == 1 && (ops[0].NextRunAt == nil || !ops[0].NextRunAt.After(e.clock.Now())) {
				t.Fatalf("continuation not delayed: %+v", ops[0])
			}
		})
	}
}

// TestGraphHandlerReenqueuesAndRechecks checks graph_maintenance re-enqueues when the budget runs out, and
// that a run reporting done still re-enqueues when graph_dirty has rows (work that arrived after the
// maintainer's final empty batch), but not when both queues are empty.
func TestGraphHandlerReenqueuesAndRechecks(t *testing.T) {
	e := newHandlerEnv(t)
	done := false
	c := e.register(worker.Handlers{Graph: &mockmaintenance.GraphMaintainer{
		RunFunc: func(context.Context, port.BankRef, time.Duration) (bool, error) { return done, nil },
	}})
	h := c[domain.OperationKindGraphMaintenance]
	job := port.Job{Bank: hBank, Kind: domain.OperationKindGraphMaintenance}
	if err := h(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if ops := e.ops(t, domain.OperationKindGraphMaintenance); len(ops) != 1 {
		t.Fatalf("budget ran out: %d continuations, want 1", len(ops))
	}
	e.drain(t, domain.OperationKindGraphMaintenance)
	done = true
	if err := h(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if ops := pending(e.ops(t, domain.OperationKindGraphMaintenance)); len(ops) != 0 {
		t.Fatalf("done with empty queues re-enqueued: %+v", ops)
	}
	e.dirtyNeighbour(t)
	if err := h(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if ops := pending(e.ops(t, domain.OperationKindGraphMaintenance)); len(ops) != 1 {
		t.Fatalf("done with graph_dirty rows: %d pending, want 1", len(ops))
	}
}

// pending keeps the PENDING operations.
func pending(ops []domain.Operation) []domain.Operation {
	return slices.DeleteFunc(ops, func(o domain.Operation) bool { return o.Status != domain.OperationStatusPending })
}

// drain claims and completes every due job of kind.
func (e handlerEnv) drain(t *testing.T, kind port.JobKind) {
	t.Helper()
	jobs, err := e.store.Claim(t.Context(), "w", []port.JobKind{kind}, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if err := e.store.Complete(t.Context(), j.ID, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// dirtyNeighbour inserts two linked facts and archives one, which records the other in graph_dirty.
func (e handlerEnv) dirtyNeighbour(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	vec := make([]float32, domain.EmbeddingDim)
	vec[0] = 1
	a := domain.Unit{
		ID: "11111111-1111-1111-1111-111111111111", Bank: hBank, Type: domain.FactTypeWorld,
		Text: "a", Embedding: vec,
	}
	b := domain.Unit{
		ID: "22222222-2222-2222-2222-222222222222", Bank: hBank, Type: domain.FactTypeWorld,
		Text: "b", Embedding: vec,
	}
	if err := e.store.InTx(ctx, func(tx port.Tx) error {
		seq, err := tx.Seq(ctx, hBank)
		if err != nil {
			return err
		}
		a.Seq, b.Seq = seq, seq
		if err := tx.Units().Insert(ctx, []domain.Unit{a, b}); err != nil {
			return err
		}
		return tx.Links().Insert(ctx, []domain.Link{
			{Bank: hBank, From: a.ID, To: b.ID, Kind: domain.LinkSemantic, Weight: 0.9},
			{Bank: hBank, From: b.ID, To: a.ID, Kind: domain.LinkSemantic, Weight: 0.9},
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.InTx(ctx, func(tx port.Tx) error {
		if _, err := tx.Seq(ctx, hBank); err != nil {
			return err
		}
		return tx.Units().Archive(ctx, hBank, a.ID, "test")
	}); err != nil {
		t.Fatal(err)
	}
}

// strandedSource is a worker.StrandedFactSource returning fixed units.
type strandedSource struct {
	units []domain.Unit
	after time.Duration
}

func (s *strandedSource) StrandedFacts(_ context.Context, retryAfter time.Duration, _ int) ([]domain.Unit, error) {
	s.after = retryAfter
	return s.units, nil
}

// TestMaintenanceRederivesStrandedFacts checks the tick re-enqueues rederive_memory for facts a failed rederive
// left with a NULL embedding, with curate's payload and serial key.
func TestMaintenanceRederivesStrandedFacts(t *testing.T) {
	e := newHandlerEnv(t)
	src := &strandedSource{units: []domain.Unit{
		{ID: "u1", Bank: hBank, DocumentID: "d1"}, {ID: "u2", Bank: hBank},
	}}
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() {
		stopped <- worker.RunMaintenance(ctx, worker.MaintenanceDeps{
			Claimer: e.store, Store: e.store, Clock: e.clock, Stranded: src, Interval: time.Hour,
		})
	}()
	var jobs []port.Job
	for deadline := time.Now().Add(5 * time.Second); len(jobs) < 2 && time.Now().Before(deadline); {
		got, err := e.store.Claim(t.Context(), "w", []port.JobKind{domain.OperationKindRederiveMemory}, 10)
		if err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, got...)
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, j := range jobs {
		var compact bytes.Buffer
		if err := json.Compact(&compact, j.Payload); err != nil {
			t.Fatal(err)
		}
		got[j.SerialKey] = compact.String()
	}
	want := map[string]string{"doc:d1": `{"unit_id":"u1"}`, "unit:u2": `{"unit_id":"u2"}`}
	if len(got) != 2 || got["doc:d1"] != want["doc:d1"] || got["unit:u2"] != want["unit:u2"] {
		t.Fatalf("rederives %v, want %v", got, want)
	}
	if src.after != worker.DefaultStrandedRetryAfter {
		t.Fatalf("retry-after %s", src.after)
	}
}

// TestRetainHandlerReportsUsage runs a retain_document job through the Runner and checks the result and the
// EXTRACT usage land on the operation row.
func TestRetainHandlerReportsUsage(t *testing.T) {
	e := newHandlerEnv(t)
	var id port.OperationID
	if err := e.store.InTx(t.Context(), func(tx port.Tx) error {
		var err error
		id, _, err = tx.Queue().Enqueue(t.Context(), port.Job{
			Bank: hBank, Kind: domain.OperationKindRetainDocument, SerialKey: "doc:d", Payload: json.RawMessage(`{}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	usage := domain.Usage{Slot: domain.ModelSlotExtract, Model: "m", InputTokens: 10, OutputTokens: 5}
	r := worker.NewRunner(e.store, worker.RunnerConfig{PollInterval: 10 * time.Millisecond})
	worker.Register(r, worker.Handlers{Store: e.store, Retainer: &mockretain.Retainer{
		ProcessDocumentFunc: func(context.Context, port.Job) (retain.RetainResult, error) {
			return retain.RetainResult{DocumentID: "d", UnitsCreated: 3, Usage: []domain.Usage{usage}}, nil
		},
	}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = r.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var op domain.Operation
		if err := e.store.InTx(t.Context(), func(tx port.Tx) error {
			var err error
			op, err = tx.Queue().Get(t.Context(), hBank, id)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if op.Status == domain.OperationStatusCompleted {
			if len(op.Usage) != 1 || op.Usage[0] != usage {
				t.Fatalf("usage %+v", op.Usage)
			}
			var res map[string]any
			if err := json.Unmarshal(op.Result, &res); err != nil || res["units_created"] != float64(3) {
				t.Fatalf("result %s: %v", op.Result, err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("retain job never completed")
}

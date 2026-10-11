package mentalmodel

import (
	"context"
	"errors"
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"sync"
	"testing"
	"time"
)

// fwChatHookLLM runs hook once, before the first Chat call, to change state while a full build is in flight.
type fwChatHookLLM struct {
	ai.LLM
	once *sync.Once
	hook func()
}

func (h fwChatHookLLM) Chat(ctx context.Context, req ai.ChatRequest) (ai.ChatResponse, error) {
	h.once.Do(h.hook)
	return h.LLM.Chat(ctx, req) //nolint:wrapcheck // test pass-through
}

// fwSingleShotRunner is a runner whose single-shot synthesizer calls llm.
func fwSingleShotRunner(e *env, llm ai.LLM) RefreshRunner {
	md := ModelDeps{LLM: llm, Router: routingRouter(), Clock: e.clock}
	return NewRefreshRunner(RefreshRunnerDeps{
		Store: e.store, Config: e.config, Scope: e.reads, Clock: e.clock,
		Synthesizers: []Synthesizer{NewSingleShotSynthesizer(e.rc, md)},
	})
}

// TestScopeEditDuringFirstBuildConflicts checks that an UpdateMentalModel changing the source query and tags
// while the first build drafts (last_seq_seen is 0 before and after, so only the scope moved) fails the run
// with ErrConflict: the new spec is kept and no old-scope content is committed.
func TestScopeEditDuringFirstBuildConflicts(t *testing.T) {
	e := newEnv(t)
	_, adm := newTriggers(e)
	e.addUnit(domain.FactTypeWorld, "Alice leads the project", "proj")
	e.addModel(deltaSpec("status", domain.RefreshStrategySingleShot))
	e.scriptSynthesize("## Status\n\nAlice leads the project.")
	edit := func() {
		_, err := adm.UpdateMentalModel(e.ctx, e.bank, "status", patchFn(func(s *domain.MentalModelSpec) {
			s.SourceQuery, s.Tags = "what is secret?", []string{"secret"}
		}))
		if err != nil {
			t.Error(err)
		}
	}
	runner := fwSingleShotRunner(e, fwChatHookLLM{LLM: e.llm, once: &sync.Once{}, hook: edit})
	_, err := runner.Run(e.ctx, e.bank, "status", RefreshOptions{Manual: true})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	m := e.model("status")
	if m.ContentMD != "" || m.Version != 0 || len(m.SourceIDs) != 0 || m.LastRefreshedAt != nil {
		t.Fatalf("old-scope build committed: v%d %q %v", m.Version, m.ContentMD, m.SourceIDs)
	}
	if m.Spec.SourceQuery != "what is secret?" || !slices.Equal(m.Spec.Tags, []string{"secret"}) {
		t.Fatalf("edit lost: %+v", m.Spec)
	}
}

// TestReconcileInterleavedWithWrite checks that a reconcile committing between a refresh's read and its
// write loses nothing (the pause and the failure survive the automatic run's write, which adds a version),
// that the failure is recorded once, and that two concurrent reconciles of a new failure record it once.
func TestReconcileInterleavedWithWrite(t *testing.T) {
	e := newEnv(t)
	builtModel(e, domain.RefreshStrategySingleShot)
	e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
	e.llm.RespondJSON(domain.ModelSlotMentalModel, deltaPrompt,
		opsJSON(map[string]any{"kind": "append_block", "section_id": "status", "blocks_md": []string{"Bob."}}))
	rec := NewRefreshFailureReconciler(e.reads, e.store, e.config, e.clock)
	e.clock.Advance(time.Minute)
	e.reads.failed = []store.FailedRefresh{{
		Bank: e.bank, ModelID: "status", OperationID: opID("op0"), FailedAt: e.clock.Now(), Error: "boom",
	}}
	var once sync.Once
	reconcile := func() {
		once.Do(func() {
			if n, err := rec.ReconcileFailedRefreshes(e.ctx); err != nil || n != 1 {
				t.Errorf("reconcile in flight: paused %d: %v", n, err)
			}
		})
	}
	runner := fwSingleShotRunner(e, hookLLM{LLM: e.llm, hook: reconcile})
	out, err := runner.Run(e.ctx, e.bank, "status", RefreshOptions{OperationID: opID("op1")})
	if err != nil || out.VersionWritten != 2 {
		t.Fatalf("run: version %d: %v", out.VersionWritten, err)
	}
	m := e.model("status")
	if !m.AutoRefreshPaused || m.LastRefreshFailedAt == nil || m.Version != 2 {
		t.Fatalf("lost a field: paused %v failed_at %v version %d", m.AutoRefreshPaused, m.LastRefreshFailedAt,
			m.Version)
	}
	if n, _ := rec.ReconcileFailedRefreshes(e.ctx); n != 0 {
		t.Fatal("failure recorded twice")
	}

	e.clock.Advance(time.Minute)
	e.reads.failed = []store.FailedRefresh{{
		Bank: e.bank, ModelID: "status", OperationID: opID("op2"), FailedAt: e.clock.Now(), Error: "boom",
	}}
	var wg sync.WaitGroup
	paused := make([]int, 2)
	for i := range paused {
		wg.Go(func() { paused[i], _ = rec.ReconcileFailedRefreshes(e.ctx) })
	}
	wg.Wait()
	if paused[0]+paused[1] != 1 || len(e.history("status", domain.HistoryKindRefreshFailed)) != 2 {
		t.Fatalf("concurrent reconciles paused %v, history %d", paused,
			len(e.history("status", domain.HistoryKindRefreshFailed)))
	}
}

// TestReconcileSkipsFailureOfEarlierModel checks that a failed refresh older than the model (it belonged to
// a deleted model with the same id) does not pause the recreated model.
func TestReconcileSkipsFailureOfEarlierModel(t *testing.T) {
	e := newEnv(t)
	m := e.addModel(deltaSpec("status", domain.RefreshStrategySingleShot))
	failedAt := m.CreatedAt.Add(-time.Minute) // created_at is the database's time
	e.reads.failed = []store.FailedRefresh{{Bank: e.bank, ModelID: "status", OperationID: opID("op0"), FailedAt: failedAt}}
	rec := NewRefreshFailureReconciler(e.reads, e.store, e.config, e.clock)
	if n, err := rec.ReconcileFailedRefreshes(e.ctx); err != nil || n != 0 {
		t.Fatalf("paused %d: %v", n, err)
	}
	if m := e.model("status"); m.AutoRefreshPaused || len(e.history("status", domain.HistoryKindRefreshFailed)) != 0 {
		t.Fatal("recreated model paused by an older failure")
	}
}

// TestRebuildAfterClearAddsNoEmptyVersionEntry checks that the refresh after a Clear (which recorded the
// cleared content itself) adds no VERSION entry with empty previous content.
func TestRebuildAfterClearAddsNoEmptyVersionEntry(t *testing.T) {
	e := newEnv(t)
	_, adm := newTriggers(e)
	e.addUnit(domain.FactTypeWorld, "Alice leads the project", "proj")
	e.addModel(deltaSpec("status", domain.RefreshStrategySingleShot))
	e.scriptSynthesize("## Status\n\nAlice leads the project.")
	if _, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{Manual: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := adm.ClearMentalModel(e.ctx, e.bank, "status"); err != nil {
		t.Fatal(err)
	}
	e.scriptSynthesize("## Status\n\nAlice still leads the project.")
	out, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{Manual: true})
	if err != nil || out.VersionWritten != 2 {
		t.Fatalf("rebuild: version %d: %v", out.VersionWritten, err)
	}
	h := e.history("status", domain.HistoryKindVersion)
	if len(h) != 1 || h[0].PreviousContent == "" {
		t.Fatalf("version history %+v", h)
	}
}

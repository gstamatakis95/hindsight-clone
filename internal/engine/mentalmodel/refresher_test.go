package mentalmodel

import (
	"context"
	"encoding/json"
	"errors"
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"strings"
	"sync"
	"testing"
)

// deltaSpec is a delta-mode model spec tagged "proj".
func deltaSpec(id string, strategy domain.RefreshStrategy) domain.MentalModelSpec {
	return domain.MentalModelSpec{
		ID: domain.MentalModelID(id), Name: "Project", SourceQuery: "what is the project status?",
		Tags: []string{"proj"}, Trigger: domain.RefreshTrigger{Mode: domain.RefreshModeDelta, Strategy: strategy},
	}
}

// opsJSON renders delta ops as the delta response.
func opsJSON(ops ...map[string]any) string {
	full := make([]map[string]any, len(ops))
	for i, op := range ops {
		o := map[string]any{
			"kind": "", "section_id": "", "block_id": "", "after_block_id": "", "heading": "",
			"blocks_md": []string{},
		}
		for k, v := range op {
			o[k] = v
		}
		full[i] = o
	}
	b, _ := json.Marshal(map[string]any{"operations": full})
	return string(b)
}

const deltaPrompt = "mm_structured_delta_system"

// TestBothStrategiesProduceVersionThroughRunner checks that agentic and single-shot both write a version
// (and then a second one with a history entry) through the same runner.
func TestBothStrategiesProduceVersionThroughRunner(t *testing.T) {
	for _, strategy := range []domain.RefreshStrategy{domain.RefreshStrategyAgentic, domain.RefreshStrategySingleShot} {
		t.Run(string(strategy), func(t *testing.T) {
			e := newEnv(t)
			u := e.addUnit(domain.FactTypeWorld, "Alice leads the project", "proj")
			spec := deltaSpec("status", strategy)
			spec.Trigger.Mode = domain.RefreshModeFull
			e.addModel(spec)
			e.scriptSynthesize("## Status\n\nAlice leads the project.")

			out, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{Manual: true, OperationID: opID("op1")})
			if err != nil {
				t.Fatal(err)
			}
			m := e.model("status")
			if out.VersionWritten != 1 || m.Version != 1 || !strings.Contains(m.ContentMD, "Alice") {
				t.Fatalf("version %d / %d, content %q", out.VersionWritten, m.Version, m.ContentMD)
			}
			if out.Trace.EffectiveStrategy != strategy || out.Trace.Outcome != domain.RefreshOutcomeContentWritten {
				t.Fatalf("trace %+v", out.Trace)
			}
			if !slices.Equal(m.SourceIDs, []domain.UnitID{u.ID}) || m.LastSeqSeen != u.Seq || m.LastRefreshedAt == nil {
				t.Fatalf("state: sources %v seq %d", m.SourceIDs, m.LastSeqSeen)
			}
			if len(m.Embedding) == 0 {
				t.Fatal("page embedding not stored")
			}

			assertSecondVersion(t, e, m)
		})
	}
}

// assertSecondVersion refreshes again after a new unit and checks the second version and its history.
func assertSecondVersion(t *testing.T, e *env, first domain.MentalModel) {
	t.Helper()
	e.addUnit(domain.FactTypeWorld, "Bob joined the project", "proj")
	e.scriptSynthesize("## Status\n\nAlice leads; Bob joined.")
	if _, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{}); err != nil {
		t.Fatal(err)
	}
	h := e.history("status", domain.HistoryKindVersion)
	if m2 := e.model("status"); m2.Version != 2 || len(h) != 1 || h[0].PreviousContent != first.ContentMD {
		t.Fatalf("second version %d, history %+v", m2.Version, h)
	}
}

// TestNothingInScopeMeansNoLLMCall checks that a refresh with nothing in scope (or in the delta window)
// completes without any model call and only advances the refresh time.
func TestNothingInScopeMeansNoLLMCall(t *testing.T) {
	for _, strategy := range []domain.RefreshStrategy{domain.RefreshStrategyAgentic, domain.RefreshStrategySingleShot} {
		t.Run(string(strategy), func(t *testing.T) {
			e := newEnv(t)
			e.addUnit(domain.FactTypeWorld, "unrelated", "other")
			e.addModel(deltaSpec("status", strategy))
			out, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{Manual: true})
			if err != nil {
				t.Fatal(err)
			}
			m := e.model("status")
			if out.Trace.Outcome != domain.RefreshOutcomeNoNewFacts || out.VersionWritten != 0 || m.Version != 0 {
				t.Fatalf("outcome %s version %d", out.Trace.Outcome, m.Version)
			}
			if e.llmCalls() != 0 || len(e.rf.reqs)+len(e.rc.reqs) != 0 {
				t.Fatalf("made %d llm calls, %d retrievals", e.llmCalls(), len(e.rf.reqs)+len(e.rc.reqs))
			}
			if m.LastRefreshedAt == nil {
				t.Fatal("last_refreshed_at not advanced")
			}
		})
	}
}

// seqStore makes Read().BankSeq return a stale value once, as if a writer that had already taken a
// higher seq committed while the refresh was reading the snapshot.
type seqStore struct {
	store.DB
	mu   sync.Mutex
	next []int64
}

func (s *seqStore) Read() store.Reader { return seqReader{s.DB.Read(), s} }

type seqReader struct {
	store.Reader
	s *seqStore
}

func (r seqReader) BankSeq(ctx context.Context, b domain.BankRef) (int64, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if len(r.s.next) > 0 {
		v := r.s.next[0]
		r.s.next = r.s.next[1:]
		return v, nil
	}
	return r.Reader.BankSeq(ctx, b)
}

// TestLateCommittingWriterPickedUpByNextRefresh checks the watermark: a unit whose seq is above the
// BankSeq a refresh read is not in that refresh, keeps the model stale and lands in the next delta.
func TestLateCommittingWriterPickedUpByNextRefresh(t *testing.T) {
	e := newEnv(t)
	first := e.addUnit(domain.FactTypeWorld, "Alice leads the project", "proj")
	late := e.addUnit(domain.FactTypeWorld, "Bob took over the project", "proj")
	st := &seqStore{DB: e.store, next: []int64{first.Seq}}
	md := ModelDeps{LLM: e.llm, Router: routingRouter(), Clock: e.clock}
	runner := NewRefreshRunner(RefreshRunnerDeps{
		Store: st, Config: e.config, Scope: e.reads, Clock: e.clock,
		Synthesizers: []Synthesizer{NewSingleShotSynthesizer(e.rc, md)},
	})
	e.addModel(deltaSpec("status", domain.RefreshStrategySingleShot))
	e.scriptSynthesize("## Status\n\nAlice leads the project.")
	if _, err := runner.Run(e.ctx, e.bank, "status", RefreshOptions{}); err != nil {
		t.Fatal(err)
	}
	m := e.model("status")
	if m.LastSeqSeen != first.Seq || slices.Contains(m.SourceIDs, late.ID) {
		t.Fatalf("watermark %d, sources %v", m.LastSeqSeen, m.SourceIDs)
	}
	stale, _, err := NewChangeDetector(e.store.Read(), e.reads).NeedsRefresh(e.ctx, m)
	if err != nil || !stale {
		t.Fatalf("model not stale after a late writer: %v %v", stale, err)
	}
	e.llm.RespondJSON(domain.ModelSlotMentalModel, deltaPrompt, opsJSON(map[string]any{
		"kind": "append_block", "section_id": "status", "blocks_md": []string{"Bob took over."},
	}))
	out, err := runner.Run(e.ctx, e.bank, "status", RefreshOptions{})
	if err != nil {
		t.Fatal(err)
	}
	calls := e.llm.CallsFor(domain.ModelSlotMentalModel, deltaPrompt)
	if out.Trace.EffectiveMode != domain.RefreshModeDelta || len(calls) != 1 ||
		!strings.Contains(calls[0].Structured.User, "Bob took over the project") {
		t.Fatalf("mode %s, delta calls %d", out.Trace.EffectiveMode, len(calls))
	}
	m = e.model("status")
	if !strings.Contains(m.ContentMD, "Bob took over.") || m.LastSeqSeen < late.Seq ||
		!slices.Equal(m.SourceIDs, []domain.UnitID{first.ID, late.ID}) {
		t.Fatalf("after delta: %q seq %d sources %v", m.ContentMD, m.LastSeqSeen, m.SourceIDs)
	}
}

// builtModel stores a model that already has content built from one unit, with the watermark at it.
func builtModel(e *env, strategy domain.RefreshStrategy) (domain.MentalModel, domain.Unit) {
	u := e.addUnit(domain.FactTypeWorld, "Alice leads the project", "proj")
	m := e.addModel(deltaSpec("status", strategy))
	m.ContentMD, m.Version, m.LastSeqSeen, m.SourceIDs = "## Status\n\nAlice leads the project.\n", 1, u.Seq,
		[]domain.UnitID{u.ID}
	e.tx(func(tx store.Tx) error { return tx.MentalModels().Update(e.ctx, m) })
	return e.model("status"), u
}

// TestDeltaAllOpsRejectedKeepsDocument checks the reject-and-retry-once rule: every op rejected twice
// fails the refresh and keeps the document, the version and the watermark.
func TestDeltaAllOpsRejectedKeepsDocument(t *testing.T) {
	for _, strategy := range []domain.RefreshStrategy{domain.RefreshStrategyAgentic, domain.RefreshStrategySingleShot} {
		t.Run(string(strategy), func(t *testing.T) {
			e := newEnv(t)
			before, _ := builtModel(e, strategy)
			e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
			e.llm.RespondJSON(domain.ModelSlotMentalModel, deltaPrompt, opsJSON(map[string]any{
				"kind": "append_block", "section_id": "no-such-section", "blocks_md": []string{"Bob joined."},
			}))
			out, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{OperationID: opID("op1")})
			if !errors.Is(err, ErrAllOpsRejected) || out.Trace.Outcome != domain.RefreshOutcomeFailed {
				t.Fatalf("err %v outcome %s", err, out.Trace.Outcome)
			}
			calls := e.llm.CallsFor(domain.ModelSlotMentalModel, deltaPrompt)
			if len(calls) != 2 || !strings.Contains(calls[1].Structured.User, "Previous attempt rejected") ||
				!strings.Contains(calls[1].Structured.User, "unknown section_id") {
				t.Fatalf("want one retry showing the rejected op, got %d calls", len(calls))
			}
			after := e.model("status")
			if after.ContentMD != before.ContentMD || after.Version != 1 || after.LastSeqSeen != before.LastSeqSeen {
				t.Fatalf("document changed: %q v%d seq %d", after.ContentMD, after.Version, after.LastSeqSeen)
			}
			if len(e.history("status", domain.HistoryKindUnspecified)) != 0 {
				t.Fatal("runner wrote history on failure; the reconcile owns failure entries")
			}
		})
	}
}

// TestDeltaRetrySucceedsAndSourcesAccumulate checks that the retry can recover, that source_ids
// accumulate across deltas, and that a full refresh prunes them.
func TestDeltaRetrySucceedsAndSourcesAccumulate(t *testing.T) {
	e := newEnv(t)
	_, first := builtModel(e, domain.RefreshStrategySingleShot)
	second := e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
	e.llm.Script(domain.ModelSlotMentalModel, deltaPrompt,
		fakeJSON(opsJSON(map[string]any{"kind": "remove_block", "section_id": "status", "block_id": "bnope"})),
		fakeJSON(opsJSON(map[string]any{"kind": "append_block", "section_id": "status", "blocks_md": []string{"Bob."}})))
	out, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := e.model("status")
	if out.VersionWritten != 2 || !strings.Contains(m.ContentMD, "Bob.") ||
		!slices.Equal(m.SourceIDs, []domain.UnitID{first.ID, second.ID}) {
		t.Fatalf("v%d %q sources %v", out.VersionWritten, m.ContentMD, m.SourceIDs)
	}
	if len(out.Trace.DeltaOperations) != 1 || !out.Trace.DeltaOperations[0].Applied {
		t.Fatalf("trace ops %+v", out.Trace.DeltaOperations)
	}

	e.archive(first.ID)
	e.scriptSynthesize("## Status\n\nBob joined.")
	out, err = e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m = e.model("status")
	if out.Trace.ModeFallbackReason != FallbackSourcesRetracted || !slices.Equal(m.SourceIDs, []domain.UnitID{second.ID}) {
		t.Fatalf("fallback %q sources %v", out.Trace.ModeFallbackReason, m.SourceIDs)
	}
}

// TestModeFallbackReasons checks force_full and no_baseline_content.
func TestModeFallbackReasons(t *testing.T) {
	e := newEnv(t)
	e.addUnit(domain.FactTypeWorld, "Alice leads the project", "proj")
	e.addModel(deltaSpec("status", domain.RefreshStrategySingleShot))
	e.scriptSynthesize("## Status\n\nAlice.")
	out, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{})
	if err != nil || out.RequestedMode != domain.RefreshModeDelta || out.Trace.EffectiveMode != domain.RefreshModeFull ||
		out.Trace.ModeFallbackReason != FallbackNoBaseline {
		t.Fatalf("err %v requested %s effective %s reason %q", err, out.RequestedMode, out.Trace.EffectiveMode,
			out.Trace.ModeFallbackReason)
	}
	e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
	out, err = e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{ForceFull: true})
	if err != nil || out.Trace.ModeFallbackReason != FallbackForceFull || out.WindowAfterSeq != 0 {
		t.Fatalf("err %v reason %q window after %d", err, out.Trace.ModeFallbackReason, out.WindowAfterSeq)
	}
}

// TestDryRunWritesNothing checks that a dry run returns the draft and a diff and writes nothing.
func TestDryRunWritesNothing(t *testing.T) {
	e := newEnv(t)
	before, _ := builtModel(e, domain.RefreshStrategySingleShot)
	e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
	e.llm.RespondJSON(domain.ModelSlotMentalModel, deltaPrompt, opsJSON(map[string]any{
		"kind": "append_block", "section_id": "status", "blocks_md": []string{"Bob joined."},
	}))
	seq, _ := e.store.Read().BankSeq(e.ctx, e.bank)
	out, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out.WouldPersist || out.VersionWritten != 0 || !strings.Contains(out.Diff, "+Bob joined.") ||
		!strings.Contains(out.ContentMD, "Bob joined.") || out.WindowAfterSeq != before.LastSeqSeen {
		t.Fatalf("dry run %+v", out)
	}
	after := e.model("status")
	after.IsStale, before.IsStale = false, false
	if after.ContentMD != before.ContentMD || after.Version != 1 || after.LastRefreshedAt != nil ||
		after.LastSeqSeen != before.LastSeqSeen {
		t.Fatalf("dry run wrote the model: %+v", after)
	}
	if seq2, _ := e.store.Read().BankSeq(e.ctx, e.bank); seq2 != seq {
		t.Fatalf("dry run bumped the bank seq %d -> %d", seq, seq2)
	}
	if len(e.history("status", domain.HistoryKindUnspecified)) != 0 {
		t.Fatal("dry run wrote history")
	}
}

// hookLLM runs hook before every Structured call, to change state while a refresh is in flight.
type hookLLM struct {
	ai.LLM
	hook func()
}

func (h hookLLM) Structured(ctx context.Context, req ai.StructuredRequest, out any) (domain.Usage, error) {
	h.hook()
	return h.LLM.Structured(ctx, req, out) //nolint:wrapcheck // test pass-through
}

// TestRefreshConflictWhenModelChanged checks that a model cleared during a refresh is not overwritten.
func TestRefreshConflictWhenModelChanged(t *testing.T) {
	e := newEnv(t)
	builtModel(e, domain.RefreshStrategySingleShot)
	e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
	e.llm.RespondJSON(domain.ModelSlotMentalModel, deltaPrompt,
		opsJSON(map[string]any{"kind": "append_block", "section_id": "status", "blocks_md": []string{"Bob."}}))
	clear := func() {
		e.tx(func(tx store.Tx) error {
			m, err := tx.MentalModels().Get(e.ctx, e.bank, "status")
			if err != nil {
				return err
			}
			m.ContentMD, m.LastSeqSeen = "", 0
			return tx.MentalModels().Update(e.ctx, m)
		})
	}
	md := ModelDeps{LLM: hookLLM{LLM: e.llm, hook: clear}, Router: routingRouter(), Clock: e.clock}
	runner := NewRefreshRunner(RefreshRunnerDeps{
		Store: e.store, Config: e.config, Scope: e.reads, Clock: e.clock,
		Synthesizers: []Synthesizer{NewSingleShotSynthesizer(e.rc, md)},
	})
	_, err := runner.Run(e.ctx, e.bank, "status", RefreshOptions{})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if m := e.model("status"); m.ContentMD != "" {
		t.Fatalf("cleared content overwritten: %q", m.ContentMD)
	}
}

package mentalmodel

import (
	"encoding/json"
	"fmt"
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/engine/consolidate"
	"memd/internal/jobs"
	"memd/internal/prompts"
	"memd/internal/store"
	"memd/internal/store/postgres/pgtest"
	"testing"
	"time"
)

// TestConsolidationTriggeredRefreshRunsAndStaysUnpaused wires the real Consolidator and this package's
// RunJob and reconciler over one store: the refresh that consolidation enqueues
// decodes, runs, writes a version, and the maintenance reconcile (over the store's own FailedRefreshes)
// pauses nothing.
func TestConsolidationTriggeredRefreshRunsAndStaysUnpaused(t *testing.T) {
	e := newEnv(t)
	spec := deltaSpec("status", domain.RefreshStrategySingleShot)
	spec.Trigger.Mode = domain.RefreshModeFull
	spec.Trigger.RefreshAfterConsolidation = true
	e.addModel(spec)
	f1 := e.addEmbeddedFact(pgtest.ID[domain.UnitID]("f1"), "Alice leads the project", "proj")

	n := 0
	cons := consolidate.NewConsolidator(consolidate.ConsolidatorDeps{
		Store: e.store, Config: e.config, Router: ai.NewRouter(), LLM: e.llm, Embedder: e.emb, Clock: e.clock,
		NewID: func() domain.UnitID {
			n++
			id := pgtest.ID[domain.UnitID](fmt.Sprintf("obs-%02d", n))
			e.reads.track(id)
			return id
		},
	})
	body, _ := json.Marshal(map[string]any{
		"creates": []map[string]any{{
			"text": "Alice leads the project", "source_fact_ids": []string{string(f1.ID)}, "reason": "new",
		}},
		"updates": []any{},
	})
	e.llm.Script(domain.ModelSlotConsolidate, prompts.ConsolidateSystem, fakeJSON(string(body)))
	job := store.Job{
		ID: opID("op-consolidate"), Bank: e.bank, Kind: domain.OperationKindConsolidate,
		SerialKey: jobs.SerialKeyConsolidate, Payload: []byte(`{}`),
	}
	if _, err := cons.Run(e.ctx, jobs.Inline(job)); err != nil {
		t.Fatal(err)
	}

	e.clock.Advance(time.Minute) // past the refresh debounce
	claimed := e.claimDue(domain.OperationKindRefreshMentalModel)
	if len(claimed) != 1 {
		t.Fatalf("claimed %d refresh jobs, want the one consolidation enqueued", len(claimed))
	}
	e.scriptSynthesize("## Status\n\nAlice leads the project.")
	out, runErr := RunJob(e.ctx, e.runner, claimed[0])
	if runErr != nil {
		if err := e.store.Fail(e.ctx, claimed[0].ID, runErr, nil); err != nil {
			t.Fatal(err)
		}
	} else if err := e.store.Complete(e.ctx, claimed[0].ID, nil, nil); err != nil {
		t.Fatal(err)
	}

	rec := NewRefreshFailureReconciler(e.store, e.store, e.config, e.clock)
	paused, err := rec.ReconcileFailedRefreshes(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := e.model("status")
	if runErr != nil || paused != 0 || m.AutoRefreshPaused || m.Version < 1 || out.VersionWritten < 1 {
		t.Fatalf("refresh err %v, paused %d (model paused %v), version %d (written %d)",
			runErr, paused, m.AutoRefreshPaused, m.Version, out.VersionWritten)
	}
}

// TestRunJobAcceptsLegacyModelIDKey: a refresh queued before the payload fix ({"model_id": ...}) still
// runs; the current key wins when both are present.
func TestRunJobAcceptsLegacyModelIDKey(t *testing.T) {
	for _, raw := range []string{`{"model_id":"m1"}`, `{"mental_model_id":"m1","model_id":"other"}`} {
		r := &recordingRunner{}
		if _, err := RunJob(t.Context(), r, store.Job{Payload: json.RawMessage(raw)}); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if len(r.calls) != 1 || r.calls[0].id != "m1" {
			t.Fatalf("%s ran %+v, want model m1 once", raw, r.calls)
		}
	}
}

// addEmbeddedFact commits a world fact with an embedding (so consolidation picks it up) and tracks it.
func (e *env) addEmbeddedFact(id domain.UnitID, text string, tags ...string) domain.Unit {
	e.t.Helper()
	u := domain.Unit{
		ID: id, Bank: e.bank, Type: domain.FactTypeWorld, Text: text, Tags: domain.NormalizeTags(tags),
		Embedding: e.emb.Vector(text),
	}
	e.tx(func(tx store.Tx) error {
		seq, err := tx.Seq(e.ctx, e.bank)
		if err != nil {
			return err
		}
		u.Seq = seq
		return tx.Units().Insert(e.ctx, []domain.Unit{u})
	})
	e.reads.track(u.ID)
	return u
}

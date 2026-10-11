package observation

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/store"
	"testing"
)

// hookAtStage is a progress hook that runs fn once, at the first report of stage.
type hookAtStage struct {
	stage string
	fired bool
	fn    func()
}

// Report runs the hook at the first report of h.stage.
func (h *hookAtStage) Report(_ context.Context, p domain.Progress) error {
	if !h.fired && p.Stage == h.stage {
		h.fired = true
		h.fn()
	}
	return nil
}

// beforeScope installs a checkpoint hook that runs fn right before the run's first batch in scope tags.
func (e *env) beforeScope(fn func(), tags ...string) *hookAtStage {
	h := &hookAtStage{stage: "scope " + scopeOf(tags...).String(), fn: fn}
	e.hook = h
	return h
}

// cite is a CONSOLIDATE reply creating one observation citing ids.
func (e *env) cite(text string, ids ...string) fake.Reply {
	return ok(reply([]create{{Text: text, SourceFactIDs: ids}}, nil))
}

// continuation claims the bank's one pending consolidate (the continuation the run enqueued) and runs it
// with no hook.
func (e *env) continuation() consolidationResult {
	e.t.Helper()
	e.wantPending(domain.OperationKindConsolidate, 1)
	jobs, err := e.store.Claim(e.t.Context(), "w", []domain.OperationKind{domain.OperationKindConsolidate}, 1)
	if err != nil || len(jobs) != 1 {
		e.t.Fatalf("claim continuation: %d jobs, %v", len(jobs), err)
	}
	e.hook = nil
	return e.run(jobs[0])
}

// wantUnstamped fails the test when any of ids is stamped consolidated.
func (e *env) wantUnstamped(ids ...string) {
	e.t.Helper()
	for _, id := range ids {
		if e.unit(id).ConsolidatedAt != nil {
			e.t.Fatalf("%s stamped although a scope this run wrote no longer cites it", id)
		}
	}
}

// wantStamped fails the test unless every one of ids is stamped consolidated.
func (e *env) wantStamped(ids ...string) {
	e.t.Helper()
	for _, id := range ids {
		if e.unit(id).ConsolidatedAt == nil {
			e.t.Fatalf("%s not stamped", id)
		}
	}
}

// citing returns the live observations of scope tags that cite unit id.
func (e *env) citing(id string, tags ...string) int {
	e.t.Helper()
	n := 0
	for _, o := range e.observations() {
		if domain.SameTagSet(o.Tags, tags) {
			for _, s := range o.SourceIDs {
				if s == uid(id) {
					n++
				}
			}
		}
	}
	return n
}

// TestRunClearedEarlierScopeIsNotStamped: per_tag facts tagged [a,b]; a
// ClearObservations of [a] commits after the run wrote [a] and before it decides [b]. The run must not
// stamp the facts at [b] (scope [a] no longer cites them): it reports More, and the continuation
// re-creates the [a] observation without re-deciding [b].
func TestRunClearedEarlierScopeIsNotStamped(t *testing.T) {
	e := newEnv(t, perTag())
	e.fact("f1", "Alice likes tea", "a", "b")
	e.fact("f2", "Carol likes tea", "a", "b")
	e.script(e.cite("Alice and Carol like tea", "f1", "f2"))
	h := e.beforeScope(func() {
		e.tx(func(tx store.Tx) error {
			if _, err := deleteScopes(t.Context(), tx, bank, []domain.ScopeKey{scopeOf("a")}); err != nil {
				return fmt.Errorf("clear scope a: %w", err)
			}
			return nil
		})
	}, "b")
	res := e.run(autoJob())
	if !h.fired {
		t.Fatal("hook never fired")
	}
	if !res.More || res.Processed != 0 {
		t.Fatalf("result %+v, want More and nothing processed", res)
	}
	e.wantUnstamped("f1", "f2")

	res = e.continuation()
	if res.More {
		t.Fatalf("continuation %+v, want done", res)
	}
	e.wantStamped("f1", "f2")
	for _, id := range []string{"f1", "f2"} {
		if a, b := e.citing(id, "a"), e.citing(id, "b"); a != 1 || b != 1 {
			t.Fatalf("%s cited by %d [a] and %d [b] observations, want one each", id, a, b)
		}
	}
	if n := len(e.calls(domain.ModelSlotConsolidate)); n != 3 {
		t.Fatalf("%d CONSOLIDATE calls, want 3 ([a], [b], then [a] again)", n)
	}
}

// TestRunCoSourceInvalidatedIsNotStamped: f1 [a,b] and f3 [a] share an [a] observation. Invalidating f3
// after [a] (DeleteCiting resets f1 without changing its seq) must keep f1 unstamped at [b]; the
// continuation re-creates f1's [a] observation.
func TestRunCoSourceInvalidatedIsNotStamped(t *testing.T) {
	e := newEnv(t, perTag())
	e.fact("f1", "Alice likes tea", "a", "b")
	e.fact("f3", "Dan likes tea", "a")
	e.script(e.cite("Alice and Dan like tea", "f1", "f3"), e.cite("Alice likes tea", "f1"))
	e.beforeScope(func() {
		e.tx(func(tx store.Tx) error {
			if _, err := tx.Seq(t.Context(), bank); err != nil {
				return err
			}
			if _, _, err := tx.Evidence().DeleteCiting(t.Context(), bank, []domain.UnitID{uid("f3")}); err != nil {
				return fmt.Errorf("clear for f3: %w", err)
			}
			return tx.Units().Archive(t.Context(), bank, uid("f3"), "wrong")
		})
	}, "b")
	if res := e.run(autoJob()); !res.More {
		t.Fatalf("result %+v, want More", res)
	}
	e.wantUnstamped("f1")

	if res := e.continuation(); res.More {
		t.Fatalf("continuation %+v, want done", res)
	}
	e.wantStamped("f1")
	if a, b := e.citing("f1", "a"), e.citing("f1", "b"); a != 1 || b != 1 {
		t.Fatalf("f1 cited by %d [a] and %d [b] observations, want one each", a, b)
	}
}

// TestRunCoSourceRetaggedIsNotStamped: as above, but the co-source f3 belongs to another document that is
// retagged [a] -> [c] after [a] (its observations cleared, f1 reset without a seq change).
func TestRunCoSourceRetaggedIsNotStamped(t *testing.T) {
	e := newEnv(t, perTag())
	e.fact("f1", "Alice likes tea", "a", "b")
	e.docFact("d2", "f3", "Dan likes tea", "a")
	e.script(
		e.cite("Alice and Dan like tea", "f1", "f3"), e.cite("Alice likes tea", "f1"),
		e.cite("Alice likes tea", "f1"), e.cite("Dan likes tea", "f3"),
	)
	e.beforeScope(func() {
		e.tx(func(tx store.Tx) error {
			seq, err := tx.Seq(t.Context(), bank)
			if err != nil {
				return err
			}
			ids, err := tx.Units().IDsByDocument(t.Context(), bank, "d2")
			if err != nil {
				return err
			}
			if _, _, err := tx.Evidence().DeleteCiting(t.Context(), bank, ids); err != nil {
				return fmt.Errorf("clear d2: %w", err)
			}
			_, err = tx.Units().SetDocumentTags(t.Context(), bank, "d2", []string{"a"}, []string{"c"}, seq)
			return err
		})
	}, "b")
	if res := e.run(autoJob()); !res.More {
		t.Fatalf("result %+v, want More", res)
	}
	e.wantUnstamped("f1")

	if res := e.continuation(); res.More {
		t.Fatalf("continuation %+v, want done", res)
	}
	e.wantStamped("f1", "f3")
	if a, b, c := e.citing("f1", "a"), e.citing("f1", "b"), e.citing("f3", "c"); a != 1 || b != 1 || c != 1 {
		t.Fatalf("f1 cited by %d [a] and %d [b], f3 by %d [c] observations, want one each", a, b, c)
	}
}

// docFact inserts a world fact of document doc (created with the fact's tags).
func (e *env) docFact(doc domain.DocumentID, id, text string, tags ...string) {
	e.t.Helper()
	e.texts[domain.UnitID(id)] = text
	tags = domain.NormalizeTags(tags)
	e.tx(func(tx store.Tx) error {
		if err := tx.Documents().Upsert(e.t.Context(), domain.Document{ID: doc, Bank: bank, Tags: tags}); err != nil {
			return err
		}
		seq, err := tx.Seq(e.t.Context(), bank)
		if err != nil {
			return err
		}
		return tx.Units().Insert(e.t.Context(), []domain.Unit{{
			ID: uid(id), Bank: bank, DocumentID: doc, Type: domain.FactTypeWorld, Text: text,
			Tags: tags, Embedding: e.emb.Vector(text), Seq: seq,
		}})
	})
}

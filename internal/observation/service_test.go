package observation

import (
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"slices"
	"testing"
)

// adminEnv seeds facts f1 and f2 (scope team) cited by observation o1, which stamps them consolidated, plus
// 2 uncited observations in scope a, 3 in b, 4 in c and 1 in the shared scope.
func adminEnv(t *testing.T) (*env, *Service) {
	t.Helper()
	e := newEnv(t, domain.BankConfig{})
	e.fact("f1", "Alice likes green tea", "team")
	e.fact("f2", "Bob drinks coffee daily", "team")
	e.observation("o1", "Alice and Bob enjoy hot drinks", []string{"team"}, "f1", "f2")
	e.tx(func(tx store.Tx) error {
		seq, err := tx.Seq(t.Context(), bank)
		if err != nil {
			return err
		}
		for _, s := range []struct {
			tags []string
			n    int
		}{{[]string{"a"}, 2}, {[]string{"b"}, 3}, {[]string{"c"}, 4}, {[]string{}, 1}} {
			for i := range s.n {
				text := fmt.Sprintf("observation %v %d", s.tags, i)
				o := domain.Unit{
					ID: uid(fmt.Sprintf("obs-%v-%d", s.tags, i)), Bank: bank, Type: domain.FactTypeObservation,
					Text: text, Tags: s.tags, Embedding: e.emb.Vector(text), Seq: seq,
				}
				if err := tx.Units().UpsertObservation(t.Context(), o); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return e, e.service()
}

// allOps lists the bank's operations of kind, any status.
func (e *env) allOps(kind domain.OperationKind) []domain.Operation {
	e.t.Helper()
	var ops []domain.Operation
	e.tx(func(tx store.Tx) error {
		var err error
		ops, _, err = tx.Queue().List(e.t.Context(), bank, store.OperationFilter{Kind: kind, PageSize: 100})
		return err
	})
	return ops
}

// consolidatePayload claims (and completes) consolidate jobs one at a time, as their serial key allows,
// until it finds id, and returns its payload.
func (e *env) consolidatePayload(id domain.OperationID) jobs.ConsolidatePayload {
	e.t.Helper()
	kinds := []domain.OperationKind{domain.OperationKindConsolidate}
	for range 10 {
		claimed, err := e.store.Claim(e.t.Context(), "w", kinds, 1)
		if err != nil {
			e.t.Fatal(err)
		}
		if len(claimed) == 0 {
			break
		}
		if err := e.store.Complete(e.t.Context(), claimed[0].ID, nil, nil); err != nil {
			e.t.Fatal(err)
		}
		if claimed[0].ID == id {
			var p jobs.ConsolidatePayload
			if err := json.Unmarshal(claimed[0].Payload, &p); err != nil {
				e.t.Fatal(err)
			}
			return p
		}
	}
	e.t.Fatalf("consolidate job %s never claimed", id)
	return jobs.ConsolidatePayload{}
}

// TestConsolidate: automatic consolidations join one pending job; one with scopes (normalized) or overrides
// never joins; an unknown bank is created.
func TestConsolidate(t *testing.T) {
	e, s := adminEnv(t)
	ctx := t.Context()
	op1, err := s.Consolidate(ctx, bank, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	op2, err := s.Consolidate(ctx, bank, nil, nil)
	if err != nil || op2.ID != op1.ID {
		t.Errorf("automatic consolidations did not dedupe: %s vs %s (%v)", op1.ID, op2.ID, err)
	}
	scoped, err := s.Consolidate(ctx, bank, []domain.TagSet{{Tags: []string{" B ", "a"}}}, nil)
	if err != nil || scoped.ID == op1.ID {
		t.Fatalf("scoped consolidate joined the automatic one: %v", err)
	}
	over, err := s.Consolidate(ctx, bank, nil, &domain.BankConfig{})
	if err != nil || over.ID == op1.ID || over.ID == scoped.ID {
		t.Fatalf("consolidate with overrides joined another: %v", err)
	}
	payload := e.consolidatePayload(scoped.ID)
	if len(payload.Scopes) != 1 || !slices.Equal(payload.Scopes[0].Tags, []string{"a", "b"}) {
		t.Errorf("scoped payload = %+v", payload)
	}
	fresh := domain.NewBankRef("acme", "org", "new")
	if _, err := s.Consolidate(ctx, fresh, nil, nil); err != nil {
		t.Errorf("consolidate on an unknown bank should create it: %v", err)
	}
}

// listScopeKeys pages through ListObservationScopes at size and returns the scopes' canonical keys; it fails
// after 20 pages (a pager that restarts at the shared scope never terminates).
func listScopeKeys(t *testing.T, s *Service, size int) ([]string, map[string]int64) {
	t.Helper()
	var keys []string
	counts := map[string]int64{}
	tok := ""
	for range 20 {
		page, err := s.ListObservationScopes(t.Context(), bank, size, tok)
		if err != nil {
			t.Fatal(err)
		}
		for _, sc := range page.Items {
			k := domain.NewScopeKey(bank, sc.Scope.Tags).String()
			keys = append(keys, k)
			counts[k] = sc.Observations
		}
		if tok = page.NextPageToken; tok == "" {
			return keys, counts
		}
	}
	t.Fatalf("ListObservationScopes(size %d) did not terminate: %v", size, keys)
	return nil, nil
}

// TestListObservationScopes pages at sizes 1, 2 and 10: every scope once, the shared scope first (a page
// ending at it continues instead of restarting) and with its count.
func TestListObservationScopes(t *testing.T) {
	_, s := adminEnv(t)
	key := func(tags ...string) string { return domain.NewScopeKey(bank, tags).String() }
	want := []string{key(), key("a"), key("b"), key("c"), key("team")}
	for _, size := range []int{1, 2, 10} {
		keys, counts := listScopeKeys(t, s, size)
		if !slices.Equal(keys, want) {
			t.Errorf("size %d: scopes = %q, want %q", size, keys, want)
		}
		if counts[key()] != 1 || counts[key("c")] != 4 {
			t.Errorf("size %d: counts = %v", size, counts)
		}
	}
}

// TestListObservationHistoryNewestFirst: an observation's versions come back newest first, two per page, and
// the last page carries no token.
func TestListObservationHistoryNewestFirst(t *testing.T) {
	e, s := adminEnv(t)
	e.tx(func(tx store.Tx) error {
		for n := int64(1); n <= 3; n++ {
			v := domain.ObservationVersion{Version: n, Text: fmt.Sprintf("v%d", n), SourceFactIDs: []domain.UnitID{uid("f1")}}
			if err := tx.Units().RecordObservationVersion(t.Context(), bank, uid("o1"), v); err != nil {
				return err
			}
		}
		return nil
	})
	var got [][]int64
	tok := ""
	for range 5 {
		page, err := s.ListObservationHistory(t.Context(), bank, uid("o1"), 2, tok)
		if err != nil {
			t.Fatal(err)
		}
		var nums []int64
		for _, v := range page.Items {
			nums = append(nums, v.Version)
		}
		got = append(got, nums)
		if tok = page.NextPageToken; tok == "" {
			break
		}
	}
	if want := [][]int64{{3, 2}, {1}}; !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("pages = %v, want %v", got, want)
	}
}

// TestClearObservationsScopesAndAll: a scope clear deletes only that scope and reports how many it deleted;
// All clears every scope. Each clear queues graph_maintenance, and only the reconsolidate one queues a
// consolidate.
func TestClearObservationsScopesAndAll(t *testing.T) {
	e, s := adminEnv(t)
	res, err := s.ClearObservations(t.Context(), ClearRequest{
		Bank: bank, Scopes: []domain.TagSet{{Tags: []string{"B"}}}, Reconsolidate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 3 || res.Operation == nil {
		t.Errorf("clear b = %+v", res)
	}
	_, counts := listScopeKeys(t, s, 10)
	if b := domain.NewScopeKey(bank, []string{"b"}).String(); counts[b] != 0 || len(counts) != 5 {
		t.Errorf("after clearing b: counts %v (b listed with 0, the rest kept)", counts)
	}
	all, err := s.ClearObservations(t.Context(), ClearRequest{Bank: bank, All: true})
	if err != nil || all.Deleted != 8 || all.Operation != nil { // a 2 + c 4 + shared 1 + team (o1) 1
		t.Errorf("clear all = %+v, %v", all, err)
	}
	if left := e.observations(); len(left) != 0 {
		t.Errorf("clear all left %d observations", len(left))
	}
	if e.unit("f1").ConsolidatedAt != nil {
		t.Error("o1's source f1 not reset")
	}
	if ops := e.allOps(domain.OperationKindConsolidate); len(ops) != 1 || ops[0].ID != res.Operation.ID {
		t.Errorf("consolidate ops = %+v, want only %s", ops, res.Operation.ID)
	}
	if ops := e.allOps(domain.OperationKindGraphMaintenance); len(ops) != 1 {
		t.Errorf("graph_maintenance ops = %d, want 1 (the second clear joins the pending one)", len(ops))
	}
}

// TestClearObservationsMemory: clearing f1's observations deletes o1, resets the co-source f2 and f1 itself
// (embedding kept), queues graph_maintenance and the automatic consolidate; an unknown memory is NotFound.
func TestClearObservationsMemory(t *testing.T) {
	e, s := adminEnv(t)
	res, err := s.ClearObservations(t.Context(), ClearRequest{Bank: bank, MemoryID: uid("f1"), Reconsolidate: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 1 || res.Operation == nil || res.Operation.Kind != domain.OperationKindConsolidate {
		t.Errorf("clear f1 = %+v", res)
	}
	if left := e.observations(); slices.ContainsFunc(left, func(u domain.Unit) bool { return u.ID == uid("o1") }) {
		t.Error("observation citing f1 survived")
	}
	f1, f2 := e.unit("f1"), e.unit("f2")
	if f2.ConsolidatedAt != nil {
		t.Error("co-source f2 not reset")
	}
	if f1.ConsolidatedAt != nil || f1.ConsolidationFailedAt != nil || f1.Embedding == nil {
		t.Errorf("f1 after clear: consolidated_at %v, failed_at %v, embedding set %v",
			f1.ConsolidatedAt, f1.ConsolidationFailedAt, f1.Embedding != nil)
	}
	if ops := e.allOps(domain.OperationKindGraphMaintenance); len(ops) != 1 {
		t.Errorf("graph_maintenance ops = %d, want 1", len(ops))
	}
	_, err = s.ClearObservations(t.Context(), ClearRequest{Bank: bank, MemoryID: uid("nope")})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown memory err = %v", err)
	}
}

// TestClearMemoryLeavesPendingRederiveAlone: an edited fact waits for its rederive with no embedding and stays
// stamped consolidated; clearing its observations must not make it consolidatable on a NULL embedding.
func TestClearMemoryLeavesPendingRederiveAlone(t *testing.T) {
	e, s := adminEnv(t)
	e.tx(func(tx store.Tx) error {
		seq, err := tx.Seq(t.Context(), bank)
		if err != nil {
			return err
		}
		u := e.unit("f1")
		u.Embedding, u.Seq = nil, seq
		if err := tx.Units().Update(t.Context(), u); err != nil {
			return err
		}
		return tx.Units().MarkConsolidated(t.Context(), []domain.UnitID{u.ID}, e.clock.Now())
	})
	if _, err := s.ClearObservations(t.Context(), ClearRequest{Bank: bank, MemoryID: uid("f1")}); err != nil {
		t.Fatal(err)
	}
	var waiting []domain.Unit
	e.tx(func(tx store.Tx) error {
		var err error
		waiting, err = tx.Units().Unconsolidated(t.Context(), bank, nil, 100)
		return err
	})
	if slices.ContainsFunc(waiting, func(u domain.Unit) bool { return u.ID == uid("f1") }) {
		t.Error("f1 is consolidatable with a NULL embedding after the clear")
	}
}

// TestClearObservationsNeedsOneTarget: none or more than one of scopes, memory id and all is INVALID_ARGUMENT.
func TestClearObservationsNeedsOneTarget(t *testing.T) {
	_, s := adminEnv(t)
	for _, req := range []ClearRequest{
		{Bank: bank},
		{Bank: bank, All: true, MemoryID: uid("f1")},
		{Bank: bank, All: true, Scopes: []domain.TagSet{{Tags: []string{"a"}}}},
	} {
		if _, err := s.ClearObservations(t.Context(), req); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("%+v: err = %v, want INVALID_ARGUMENT", req, err)
		}
	}
}

// TestRunConsolidateJobResult pins the result summary the consolidate job records: GetOperation shows it, so
// its keys are part of the API.
func TestRunConsolidateJobResult(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.fact("f1", "Alice likes tea")
	e.script(e.createEach("f1"))
	run := jobs.Inline(autoJob())
	if err := e.service().RunConsolidateJob(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	result, usage := run.Result()
	if want := `{"changed_scopes":1,"more":false,"processed":1}`; string(result) != want || len(usage) != 1 ||
		usage[0].Slot != domain.ModelSlotConsolidate || usage[0].InputTokens != 10 {
		t.Fatalf("result %s usage %+v, want %s and the CONSOLIDATE usage", result, usage, want)
	}
}

// TestStrategyPreview: each draft strategy lists the scopes it wins and those an earlier one shadows, over
// the scopes Reader.ListObservationScopes returns (every scope that holds an observation). The preview pages the
// scopes, and the pages add up to the whole bank's verdicts.
func TestStrategyPreview(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	for i, tags := range [][]string{{"team", "x"}, {"team"}, {"other"}, {}} {
		id := fmt.Sprintf("f%d", i)
		e.fact(id, "fact "+id, tags...)
		e.observation("obs-"+id, "obs "+id, tags, id)
	}
	draft := []domain.ConsolidationStrategy{
		{Name: "exact-team", Scopes: []domain.TagFilter{{Tags: []string{"team"}, Match: domain.TagsMatchExact}}},
		{Name: "team", Scopes: []domain.TagFilter{{Tags: []string{"team"}}}},
	}
	p := StrategyPreview{Matches: make([]StrategyMatch, len(draft))}
	token, pages := "", 0
	for ; pages == 0 || token != ""; pages++ {
		page, err := e.service().PreviewConsolidationStrategies(t.Context(), bank, draft, 1, token)
		if err != nil || len(page.Matches) != 2 {
			t.Fatalf("preview page %d = %+v, %v", pages, page, err)
		}
		for i, m := range page.Matches {
			p.Matches[i].Strategy = m.Strategy
			p.Matches[i].MatchedScopes = append(p.Matches[i].MatchedScopes, m.MatchedScopes...)
			p.Matches[i].ShadowedScopes = append(p.Matches[i].ShadowedScopes, m.ShadowedScopes...)
		}
		token = page.NextPageToken
	}
	if pages != 4 {
		t.Errorf("preview took %d pages of one scope, want 4", pages)
	}
	keys := func(ss []domain.ScopeKey) []string {
		var out []string
		for _, s := range ss {
			out = append(out, s.String())
		}
		return out
	}
	if got := keys(p.Matches[0].MatchedScopes); len(got) != 1 || got[0] != scopeOf("team").String() ||
		len(p.Matches[0].ShadowedScopes) != 0 || p.Matches[0].Strategy != "exact-team" {
		t.Errorf("exact-team = %+v", p.Matches[0])
	}
	if got := keys(p.Matches[1].MatchedScopes); len(got) != 1 || got[0] != scopeOf("team", "x").String() {
		t.Errorf("team matched = %v", got)
	}
	if got := keys(p.Matches[1].ShadowedScopes); len(got) != 1 || got[0] != scopeOf("team").String() {
		t.Errorf("team shadowed = %v", got)
	}
}

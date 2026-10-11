package curate

import (
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/consolidate"
	"memd/internal/store"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"testing"
)

// adminEnv is an ObservationAdmin over the seeded env, with the store's store.Reader as ScopeSource and the
// real consolidate.Invalidator as ObservationClearer. On top of the seed (o1 in scope team) it stores 2
// observations in scope a, 3 in b, 4 in c and 1 in the shared scope.
func adminEnv(t *testing.T) (*env, ObservationAdmin) {
	e := newEnv(t)
	e.inTx(func(tx store.Tx) error {
		seq, err := tx.Seq(e.ctx, testBank)
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
					ID: pgtest.ID[domain.UnitID](fmt.Sprintf("obs-%v-%d", s.tags, i)), Bank: testBank,
					Type: domain.FactTypeObservation, Text: text, Tags: s.tags, Embedding: e.emb.Vector(text), Seq: seq,
				}
				if err := tx.Units().UpsertObservation(e.ctx, o); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return e, NewObservationAdmin(ObservationAdminDeps{
		Store: e.store, Scopes: e.fake.Read(), Clearer: consolidate.NewInvalidator(),
	})
}

// consolidatePayload claims (and completes) consolidate jobs one at a time, as their serial key allows,
// until it finds id, and returns its payload.
func (e *env) consolidatePayload(id domain.OperationID) ConsolidatePayload {
	e.t.Helper()
	for range 10 {
		jobs := e.claim(domain.OperationKindConsolidate)
		if len(jobs) == 0 {
			break
		}
		if err := e.fake.Complete(e.ctx, jobs[0].ID, nil, nil); err != nil {
			e.t.Fatal(err)
		}
		if jobs[0].ID == id {
			var p ConsolidatePayload
			if err := json.Unmarshal(jobs[0].Payload, &p); err != nil {
				e.t.Fatal(err)
			}
			return p
		}
	}
	e.t.Fatalf("consolidate job %s never claimed", id)
	return ConsolidatePayload{}
}

func TestObservationAdminConsolidate(t *testing.T) {
	e, a := adminEnv(t)
	op1, err := a.Consolidate(e.ctx, testBank, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	op2, err := a.Consolidate(e.ctx, testBank, nil, nil)
	if err != nil || op2.ID != op1.ID {
		t.Errorf("automatic consolidations did not dedupe: %s vs %s (%v)", op1.ID, op2.ID, err)
	}
	scoped, err := a.Consolidate(e.ctx, testBank, []domain.TagSet{{Tags: []string{" B ", "a"}}}, nil)
	if err != nil || scoped.ID == op1.ID {
		t.Fatalf("scoped consolidate joined the automatic one: %v", err)
	}
	over, err := a.Consolidate(e.ctx, testBank, nil, &domain.BankConfig{})
	if err != nil || over.ID == op1.ID || over.ID == scoped.ID {
		t.Fatalf("consolidate with overrides joined another: %v", err)
	}
	payload := e.consolidatePayload(scoped.ID)
	if len(payload.Scopes) != 1 || !slices.Equal(payload.Scopes[0].Tags, []string{"a", "b"}) {
		t.Errorf("scoped payload = %+v", payload)
	}
	fresh := domain.BankRef{Tenant: "acme", Bank: "new"}
	if _, err := a.Consolidate(e.ctx, fresh, nil, nil); err != nil {
		t.Errorf("consolidate on an unknown bank should create it: %v", err)
	}
}

// listScopeKeys pages through ListScopes at size and returns the scopes' canonical keys; it fails after 20
// pages (a pager that restarts at the shared scope never terminates).
func listScopeKeys(t *testing.T, e *env, a ObservationAdmin, size int) ([]string, map[string]int64) {
	t.Helper()
	var keys []string
	counts := map[string]int64{}
	tok := ""
	for range 20 {
		page, err := a.ListScopes(e.ctx, testBank, size, tok)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range page.Items {
			k := domain.NewScopeKey(testBank, s.Scope.Tags).String()
			keys = append(keys, k)
			counts[k] = s.Observations
		}
		if tok = page.NextPageToken; tok == "" {
			return keys, counts
		}
	}
	t.Fatalf("ListScopes(size %d) did not terminate: %v", size, keys)
	return nil, nil
}

// TestObservationAdminListScopes pages Reader.ListScopes at sizes 1, 2 and 10: every scope once,
// the shared scope first and with its count.
func TestObservationAdminListScopes(t *testing.T) {
	e, a := adminEnv(t)
	key := func(tags ...string) string { return domain.NewScopeKey(testBank, tags).String() }
	want := []string{key(), key("a"), key("b"), key("c"), key("team")}
	for _, size := range []int{1, 2, 10} {
		keys, counts := listScopeKeys(t, e, a, size)
		if !slices.Equal(keys, want) {
			t.Errorf("size %d: scopes = %q, want %q", size, keys, want)
		}
		if counts[key()] != 1 || counts[key("c")] != 4 {
			t.Errorf("size %d: counts = %v", size, counts)
		}
	}
}

// TestListScopesPageSizeOneTerminates: a page ending at the shared scope (key "") continues with the "0"
// cursor instead of restarting the listing (decision 1560).
func TestListScopesPageSizeOneTerminates(t *testing.T) {
	e, a := adminEnv(t)
	first, err := a.ListScopes(e.ctx, testBank, 1, "")
	if err != nil || len(first.Items) != 1 || len(first.Items[0].Scope.Tags) != 0 || first.NextPageToken == "" {
		t.Fatalf("first page = %+v, %v; want the shared scope and a next token", first, err)
	}
	second, err := a.ListScopes(e.ctx, testBank, 1, first.NextPageToken)
	if err != nil || len(second.Items) != 1 || !slices.Equal(second.Items[0].Scope.Tags, []string{"a"}) {
		t.Fatalf("second page = %+v, %v; want scope a", second, err)
	}
}

// TestObservationAdminClears: ClearObservations deletes through the real Invalidator and reports how many
// observations it deleted (len(deleted), not a count read beforehand); an empty list clears every scope.
func TestObservationAdminClears(t *testing.T) {
	e, a := adminEnv(t)
	res, err := a.ClearObservations(e.ctx, testBank, []domain.TagSet{{Tags: []string{"B"}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 3 || res.Operation == nil {
		t.Errorf("clear b = %+v", res)
	}
	_, counts := listScopeKeys(t, e, a, 10)
	if b := domain.NewScopeKey(testBank, []string{"b"}).String(); counts[b] != 0 || len(counts) != 5 {
		t.Errorf("after clearing b: counts %v (b listed with 0, the rest kept)", counts)
	}
	all, err := a.ClearObservations(e.ctx, testBank, nil, false)
	if err != nil || all.Deleted != 8 || all.Operation != nil { // a 2 + c 4 + shared 1 + team (o1) 1
		t.Errorf("clear all = %+v, %v", all, err)
	}
	if _, ok := e.unit(idO1); ok {
		t.Error("clear all left o1")
	}
	// The caller owns the consolidate enqueue (Invalidator.ClearScopes enqueues none): exactly one, from the
	// reconsolidate=true clear, and none from the reconsolidate=false one.
	if ops := e.ops(domain.OperationKindConsolidate); len(ops) != 1 || ops[0].ID != res.Operation.ID {
		t.Errorf("consolidate ops = %+v, want only %s", ops, res.Operation.ID)
	}
}

func TestObservationAdminClearMemory(t *testing.T) {
	e, a := adminEnv(t)
	mem, err := a.ClearMemoryObservations(e.ctx, testBank, idF1, true)
	if err != nil {
		t.Fatal(err)
	}
	if mem.Deleted != 1 || mem.Operation == nil || mem.Operation.Kind != domain.OperationKindConsolidate {
		t.Errorf("clear f1 = %+v", mem)
	}
	if _, ok := e.unit(idO1); ok {
		t.Error("observation citing f1 survived")
	}
	if f2, _ := e.unit(idF2); f2.ConsolidatedAt != nil {
		t.Error("co-source f2 not reset")
	}
	if _, err := a.ClearMemoryObservations(e.ctx, testBank, idNope, false); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown memory err = %v", err)
	}
}

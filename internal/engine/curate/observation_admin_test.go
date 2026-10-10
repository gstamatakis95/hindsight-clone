package curate

import (
	"context"
	"encoding/json"
	"errors"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"testing"
)

// scopeSourceStub serves fixed scopes ordered by their canonical key.
type scopeSourceStub struct {
	scopes []domain.ScopeInfo
}

// ListScopes pages the scopes after the given key.
func (s scopeSourceStub) ListScopes(
	_ context.Context, b port.BankRef, after string, limit int,
) ([]domain.ScopeInfo, error) {
	var out []domain.ScopeInfo
	for _, sc := range s.scopes {
		if domain.NewScopeKey(b, sc.Scope.Tags).String() > after && len(out) < limit {
			out = append(out, sc)
		}
	}
	return out, nil
}

// clearerStub records the scopes it was asked to clear.
type clearerStub struct {
	calls [][]port.ScopeKey
}

// ClearScopes records the call.
func (c *clearerStub) ClearScopes(_ context.Context, tx port.Tx, _ port.BankRef, scopes []port.ScopeKey) error {
	if tx == nil {
		return errors.New("no transaction")
	}
	c.calls = append(c.calls, scopes)
	return nil
}

// adminEnv is an ObservationAdmin over the seeded env.
func adminEnv(t *testing.T) (*env, ObservationAdmin, *clearerStub) {
	e := newEnv(t)
	cl := &clearerStub{}
	src := scopeSourceStub{scopes: []domain.ScopeInfo{
		{Scope: domain.TagSet{Tags: []string{"a"}}, Observations: 2},
		{Scope: domain.TagSet{Tags: []string{"b"}}, Observations: 3},
		{Scope: domain.TagSet{Tags: []string{"c"}}, Observations: 4},
	}}
	return e, NewObservationAdmin(ObservationAdminDeps{Store: e.store, Scopes: src, Clearer: cl}), cl
}

// consolidatePayload claims (and completes) consolidate jobs one at a time, as their serial key allows,
// until it finds id, and returns its payload.
func (e *env) consolidatePayload(id port.OperationID) ConsolidatePayload {
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
	e, a, _ := adminEnv(t)
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
	fresh := port.BankRef{Tenant: "acme", Bank: "new"}
	if _, err := a.Consolidate(e.ctx, fresh, nil, nil); err != nil {
		t.Errorf("consolidate on an unknown bank should create it: %v", err)
	}
}

func TestObservationAdminListScopes(t *testing.T) {
	e, a, _ := adminEnv(t)
	var tags []string
	tok := ""
	for {
		page, err := a.ListScopes(e.ctx, testBank, 2, tok)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range page.Items {
			tags = append(tags, s.Scope.Tags...)
		}
		if tok = page.NextPageToken; tok == "" {
			break
		}
	}
	if !slices.Equal(tags, []string{"a", "b", "c"}) {
		t.Errorf("scopes = %v", tags)
	}
}

func TestObservationAdminClears(t *testing.T) {
	e, a, cl := adminEnv(t)
	res, err := a.ClearObservations(e.ctx, testBank, []domain.TagSet{{Tags: []string{"B"}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 3 || res.Operation == nil || len(cl.calls) != 1 || len(cl.calls[0]) != 1 ||
		!slices.Equal(cl.calls[0][0].Tags, []string{"b"}) {
		t.Errorf("clear b = %+v, calls %v", res, cl.calls)
	}
	all, err := a.ClearObservations(e.ctx, testBank, nil, false)
	if err != nil || all.Deleted != 9 || all.Operation != nil {
		t.Errorf("clear all = %+v, %v", all, err)
	}
}

func TestObservationAdminClearMemory(t *testing.T) {
	e, a, _ := adminEnv(t)
	mem, err := a.ClearMemoryObservations(e.ctx, testBank, "f1", true)
	if err != nil {
		t.Fatal(err)
	}
	if mem.Deleted != 1 || mem.Operation == nil || mem.Operation.Kind != domain.OperationKindConsolidate {
		t.Errorf("clear f1 = %+v", mem)
	}
	if _, ok := e.unit("o1"); ok {
		t.Error("observation citing f1 survived")
	}
	if f2, _ := e.unit("f2"); f2.ConsolidatedAt != nil {
		t.Error("co-source f2 not reset")
	}
	if _, err := a.ClearMemoryObservations(e.ctx, testBank, "nope", false); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown memory err = %v", err)
	}
}

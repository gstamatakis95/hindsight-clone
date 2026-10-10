package consolidate

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"testing"
)

// searchClearer is the test ObservationClearer over the fake store's searches. DeleteScopes deletes the
// observations citing the scopes' sources, which equals deleting the scopes' observations when no source
// is shared with another scope (true in these tests).
type searchClearer struct{}

var _ ObservationClearer = searchClearer{}

// all lists b's live units of types through a zero-vector semantic search (the fake returns every unit
// with an embedding).
func all(ctx context.Context, tx port.Tx, b port.BankRef, types ...domain.FactType) ([]domain.Unit, error) {
	hits, err := tx.SearchVector(ctx, port.VectorQuery{
		Bank: b, Embedding: make([]float32, domain.EmbeddingDim), Types: types,
	})
	if err != nil {
		return nil, err
	}
	ids := make([]port.UnitID, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	return tx.GetUnits(ctx, b, ids)
}

// CitingScopes returns the scopes of observations citing any of ids.
func (searchClearer) CitingScopes(
	ctx context.Context, tx port.Tx, b port.BankRef, ids []port.UnitID,
) ([]port.ScopeKey, error) {
	obs, err := all(ctx, tx, b, domain.FactTypeObservation)
	if err != nil {
		return nil, err
	}
	var sets [][]string
	for _, o := range obs {
		if slices.ContainsFunc(o.SourceIDs, func(id port.UnitID) bool { return slices.Contains(ids, id) }) {
			sets = append(sets, o.Tags)
		}
	}
	return scopeKeys(b, sets), nil
}

// DeleteScopes deletes the observations of scopes (all when empty) through DeleteCiting of their sources.
func (searchClearer) DeleteScopes(
	ctx context.Context, tx port.Tx, b port.BankRef, scopes []port.ScopeKey,
) ([]port.UnitID, []port.UnitID, error) {
	obs, err := all(ctx, tx, b, domain.FactTypeObservation)
	if err != nil {
		return nil, nil, err
	}
	var sources []port.UnitID
	for _, o := range obs {
		inScope := slices.ContainsFunc(scopes, func(s port.ScopeKey) bool { return domain.SameTagSet(s.Tags, o.Tags) })
		if len(scopes) == 0 || inScope {
			sources = append(sources, o.SourceIDs...)
		}
	}
	deleted, _, err := tx.Evidence().DeleteCiting(ctx, b, sources)
	if err != nil {
		return nil, nil, err
	}
	if err := resetStamps(ctx, tx, sources); err != nil {
		return nil, nil, err
	}
	return deleted, sources, nil
}

// resetStamps clears consolidated_at on ids by re-writing them with Update (the only port that clears it).
func resetStamps(ctx context.Context, tx port.Tx, ids []port.UnitID) error {
	us, err := tx.GetUnits(ctx, bank, ids)
	if err != nil {
		return err
	}
	for _, u := range us {
		if err := tx.Units().Update(ctx, u); err != nil {
			return err
		}
	}
	return nil
}

// DocumentUnits returns the live facts of doc.
func (searchClearer) DocumentUnits(
	ctx context.Context, tx port.Tx, b port.BankRef, doc port.DocumentID,
) ([]port.UnitID, error) {
	facts, err := all(ctx, tx, b, domain.FactTypeWorld, domain.FactTypeExperience)
	if err != nil {
		return nil, err
	}
	var out []port.UnitID
	for _, f := range facts {
		if f.DocumentID == doc {
			out = append(out, f.ID)
		}
	}
	return out, nil
}

// TestInvalidatorClearForMemory: the observations citing the unit are deleted, their other sources reset,
// the automatic consolidate queued and the scopes returned.
func TestInvalidatorClearForMemory(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.fact("f1", "Kim sails", "x")
	e.fact("f2", "Kim sails often", "x")
	e.fact("f3", "Lee rows", "y")
	e.observation("obs-sail", "Kim sails", []string{"x"}, "f1", "f2")
	e.observation("obs-row", "Lee rows", []string{"y"}, "f3")
	var scopes []port.ScopeKey
	scopes = e.scopesTx(func(tx port.Tx) ([]port.ScopeKey, error) {
		return NewInvalidator(searchClearer{}).ClearForMemory(t.Context(), tx, bank, "f1")
	})
	if len(scopes) != 1 || scopes[0].String() != scopeOf("x").String() {
		t.Fatalf("scopes = %v", scopes)
	}
	obs := e.observations()
	if len(obs) != 1 || obs[0].ID != "obs-row" {
		t.Fatalf("observations left = %+v", obs)
	}
	if e.unit("f2").ConsolidatedAt != nil || e.unit("f3").ConsolidatedAt == nil {
		t.Fatal("want f2 reset and f3 untouched")
	}
	if n := len(e.jobs(domain.OperationKindConsolidate)); n != 1 {
		t.Fatalf("pending consolidate = %d, want 1", n)
	}
	// Nothing to clear: no job, no scopes.
	scopes = e.scopesTx(func(tx port.Tx) ([]port.ScopeKey, error) {
		return NewInvalidator(searchClearer{}).ClearForMemory(t.Context(), tx, bank, "f3x")
	})
	if len(scopes) != 0 || len(e.jobs(domain.OperationKindConsolidate)) != 1 {
		t.Fatalf("no-op clear: scopes %v", scopes)
	}
}

// TestInvalidatorClearScopesAndDocument: ClearScopes removes only the named scopes; ClearDocumentUnits
// clears what the document's units support.
func TestInvalidatorClearScopesAndDocument(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.tx(func(tx port.Tx) error {
		return tx.Documents().Upsert(t.Context(), domain.Document{ID: "d1", Bank: bank})
	})
	e.fact("f1", "Max surfs", "x")
	e.fact("f2", "Ned skis", "y")
	e.texts["f3"] = "Ola dives"
	e.tx(func(tx port.Tx) error {
		seq, err := tx.Seq(t.Context(), bank)
		if err != nil {
			return err
		}
		return tx.Units().Insert(t.Context(), []domain.Unit{{
			ID: "f3", Bank: bank, DocumentID: "d1", Type: domain.FactTypeWorld, Text: "Ola dives",
			Tags: []string{"z"}, Embedding: e.emb.Vector("Ola dives"), Seq: seq,
		}})
	})
	e.observation("obs-x", "Max surfs", []string{"x"}, "f1")
	e.observation("obs-y", "Ned skis", []string{"y"}, "f2")
	e.observation("obs-z", "Ola dives", []string{"z"}, "f3")
	inv := NewInvalidator(searchClearer{})
	e.tx(func(tx port.Tx) error { return inv.ClearScopes(t.Context(), tx, bank, []port.ScopeKey{scopeOf("x")}) })
	if ids := obsIDs(e.observations()); !slices.Equal(ids, []port.UnitID{"obs-y", "obs-z"}) {
		t.Fatalf("after ClearScopes(x): %v", ids)
	}
	if e.unit("f1").ConsolidatedAt != nil {
		t.Fatal("f1 not reset")
	}
	scopes := e.scopesTx(func(tx port.Tx) ([]port.ScopeKey, error) {
		return inv.ClearDocumentUnits(t.Context(), tx, bank, "d1")
	})
	if ids := obsIDs(e.observations()); !slices.Equal(ids, []port.UnitID{"obs-y"}) || len(scopes) != 1 ||
		scopes[0].String() != scopeOf("z").String() {
		t.Fatalf("after ClearDocumentUnits(d1): %v, scopes %v", ids, scopes)
	}
	if n := len(e.jobs(domain.OperationKindConsolidate)); n != 1 {
		t.Fatalf("pending consolidate = %d, want 1 (deduplicated)", n)
	}
}

// obsIDs returns sorted observation ids.
func obsIDs(us []domain.Unit) []port.UnitID {
	var ids []port.UnitID
	for _, u := range us {
		ids = append(ids, u.ID)
	}
	slices.Sort(ids)
	return ids
}

package retain

import (
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"testing"
	"time"
)

// wrap adds test context to an error (nil stays nil).
func wrap(err error) error {
	if err != nil {
		return fmt.Errorf("test: %w", err)
	}
	return nil
}

// inTx runs fn in a transaction on a bank that exists, failing the test on error.
func (e *env) inTx(fn func(tx port.Tx) error) {
	e.t.Helper()
	err := e.store.InTx(e.ctx, func(tx port.Tx) error {
		if _, err := tx.Banks().Ensure(e.ctx, testBank); err != nil {
			return err
		}
		return fn(tx)
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

// seedEntity creates an entity last seen at seen; with partner set, a unit mentions both so they co-occur.
func (e *env) seedEntity(name, partner string, seen time.Time) (port.EntityID, port.EntityID) {
	e.t.Helper()
	var id, pid port.EntityID
	e.inTx(func(tx port.Tx) error {
		var err error
		id, err = tx.Entities().Upsert(e.ctx, domain.Entity{Bank: testBank, CanonicalName: name, LastSeen: &seen})
		if err != nil {
			return err
		}
		if partner == "" {
			return nil
		}
		pid, err = tx.Entities().Upsert(e.ctx, domain.Entity{Bank: testBank, CanonicalName: partner, LastSeen: &seen})
		if err != nil {
			return err
		}
		u := domain.Unit{ID: "seed-" + port.UnitID(name), Bank: testBank, Type: domain.FactTypeWorld, Text: name}
		if err := tx.Units().Insert(e.ctx, []domain.Unit{u}); err != nil {
			return err
		}
		return tx.Entities().Attach(e.ctx, u.ID, []port.EntityID{id, pid})
	})
	return id, pid
}

// TestEntityResolverMergeVersusSplit covers when a mention merges into an existing entity and when it
// becomes a new one.
func TestEntityResolverMergeVersusSplit(t *testing.T) {
	seen := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		partner bool         // seed "Alice Chen" co-occurring with "Bob Stone"
		in      ResolveInput // Threshold 0.6 unless set
		merge   bool         // the first id is the seeded Alice Chen
	}{
		{"identical name merges", false, ResolveInput{Mentions: []Mention{{Name: "Alice Chen"}}}, true},
		{"case-insensitive identical merges", false, ResolveInput{Mentions: []Mention{{Name: "alice  chen"}}}, true},
		{"similar name alone splits", false, ResolveInput{Mentions: []Mention{{Name: "Alicia Chen"}}}, false},
		{"similar name, recent, co-occurring merges", true, ResolveInput{
			Mentions: []Mention{{Name: "Alicia Chen"}, {Name: "Bob Stone"}}, At: seen.Add(24 * time.Hour),
		}, true},
		{"similar name, recent but no co-occurrence splits", false, ResolveInput{
			Mentions: []Mention{{Name: "Alicia Chen"}, {Name: "Bob Stone"}}, At: seen.Add(24 * time.Hour),
		}, false},
		{"unrelated name splits", false, ResolveInput{Mentions: []Mention{{Name: "Zed Quark"}}}, false},
		{"literal hint matches exact name", false, ResolveInput{
			Hints: []domain.EntityHint{{Text: "ALICE CHEN"}}, Literal: true,
		}, true},
		{"literal hint never fuzzy-merges", true, ResolveInput{
			Hints: []domain.EntityHint{{Text: "Alicia Chen"}}, Literal: true, At: seen,
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, 0)
			partner := ""
			if tc.partner {
				partner = "Bob Stone"
			}
			alice, _ := e.seedEntity("Alice Chen", partner, seen)
			if tc.in.Threshold == 0 {
				tc.in.Threshold = DefaultEntityMatchThreshold
			}
			var ids []port.EntityID
			e.inTx(func(tx port.Tx) error {
				var err error
				ids, err = NewEntityResolver(ResolverConfig{}).Resolve(e.ctx, tx, testBank, tc.in)
				return wrap(err)
			})
			if len(ids) == 0 {
				t.Fatal("no ids")
			}
			if got := ids[0] == alice; got != tc.merge {
				t.Errorf("merged = %v, want %v (ids %v, alice %s)", got, tc.merge, ids, alice)
			}
		})
	}
}

func TestEntityResolverMergesHintsAndCreatesOnce(t *testing.T) {
	e := newEnv(t, 0)
	at := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	var first, second []port.EntityID
	in := ResolveInput{
		Mentions:  []Mention{{Name: "Paris", Kind: "Place"}, {Name: "paris"}, {Name: "  "}},
		Hints:     []domain.EntityHint{{Text: "Eiffel Tower", Type: "landmark"}, {Text: "PARIS"}},
		At:        at,
		Threshold: DefaultEntityMatchThreshold,
	}
	e.inTx(func(tx port.Tx) error {
		var err error
		first, err = NewEntityResolver(ResolverConfig{}).Resolve(e.ctx, tx, testBank, in)
		return wrap(err)
	})
	e.inTx(func(tx port.Tx) error {
		var err error
		second, err = NewEntityResolver(ResolverConfig{}).Resolve(e.ctx, tx, testBank, in)
		return wrap(err)
	})
	if len(first) != 2 || !slices.Equal(first, second) {
		t.Fatalf("first %v second %v, want the same two entities", first, second)
	}
}

// TestLabelResolver keeps declared labels only, canonicalizes values, and writes write_to_tags tags.
func TestLabelResolver(t *testing.T) {
	e := newEnv(t, 0)
	defs := []domain.EntityLabel{
		{
			Key: "priority", Type: domain.LabelTypeValue, Values: []domain.LabelValue{{Value: "High"}, {Value: "Low"}},
			WriteToTags: true,
		},
		{Key: "note", Type: domain.LabelTypeText},
		{Key: "person", Type: domain.LabelTypeMap, Fields: map[string]domain.EntityLabel{
			"role": {Type: domain.LabelTypeText, WriteToTags: true},
		}},
	}
	labels := []domain.Label{
		{Key: "priority", Value: "high"},
		{Key: "priority", Value: "urgent"},
		{Key: "note", Value: "free text"},
		{Key: "unknown", Value: "x"},
		{Key: "person:Role", Value: "engineer"},
		{Key: "person:age", Value: "3"},
		{Key: "priority", Value: "HIGH"},
	}
	var ids []port.EntityID
	var tags []string
	e.inTx(func(tx port.Tx) error {
		var err error
		ids, tags, err = NewLabelResolver().Resolve(e.ctx, tx, testBank, defs, labels)
		return wrap(err)
	})
	if len(ids) != 3 {
		t.Errorf("%d label entities, want 3 (priority:High, note, person:role)", len(ids))
	}
	if !slices.Equal(tags, []string{"priority:High", "person:role:engineer"}) {
		t.Errorf("tags = %v", tags)
	}
	var again []port.EntityID
	e.inTx(func(tx port.Tx) error {
		var err error
		again, _, err = NewLabelResolver().Resolve(e.ctx, tx, testBank, defs, labels[:1])
		return wrap(err)
	})
	if len(again) != 1 || again[0] != ids[0] {
		t.Errorf("label entity not reused: %v vs %v", again, ids)
	}
}

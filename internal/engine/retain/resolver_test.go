package retain

import (
	"fmt"
	"math"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/pgtest"
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
func (e *env) inTx(fn func(tx store.Tx) error) {
	e.t.Helper()
	err := e.store.InTx(e.ctx, func(tx store.Tx) error {
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
func (e *env) seedEntity(name, partner string, seen time.Time) (domain.EntityID, domain.EntityID) {
	e.t.Helper()
	var id, pid domain.EntityID
	e.inTx(func(tx store.Tx) error {
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
		u := domain.Unit{ID: pgtest.ID[domain.UnitID]("seed-" + name), Bank: testBank, Type: domain.FactTypeWorld, Text: name}
		if err := tx.Units().Insert(e.ctx, []domain.Unit{u}); err != nil {
			return err
		}
		return tx.Entities().Attach(e.ctx, u.ID, []domain.EntityID{id, pid})
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
		{"identical name merges", false, ResolveInput{Mentions: []domain.Mention{{Name: "Alice Chen"}}}, true},
		{"case-insensitive identical merges", false, ResolveInput{Mentions: []domain.Mention{{Name: "alice  chen"}}}, true},
		{"similar name alone splits", false, ResolveInput{Mentions: []domain.Mention{{Name: "Alicia Chen"}}}, false},
		{"similar name, recent, co-occurring merges", true, ResolveInput{
			Mentions: []domain.Mention{{Name: "Alicia Chen"}, {Name: "Bob Stone"}}, At: seen.Add(24 * time.Hour),
		}, true},
		{"similar name, recent but no co-occurrence splits", false, ResolveInput{
			Mentions: []domain.Mention{{Name: "Alicia Chen"}, {Name: "Bob Stone"}}, At: seen.Add(24 * time.Hour),
		}, false},
		{"unrelated name splits", false, ResolveInput{Mentions: []domain.Mention{{Name: "Zed Quark"}}}, false},
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
			var ids []domain.EntityID
			e.inTx(func(tx store.Tx) error {
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
	var first, second []domain.EntityID
	in := ResolveInput{
		Mentions:  []domain.Mention{{Name: "Paris", Kind: "Place"}, {Name: "paris"}, {Name: "  "}},
		Hints:     []domain.EntityHint{{Text: "Eiffel Tower", Type: "landmark"}, {Text: "PARIS"}},
		At:        at,
		Threshold: DefaultEntityMatchThreshold,
	}
	e.inTx(func(tx store.Tx) error {
		var err error
		first, err = NewEntityResolver(ResolverConfig{}).Resolve(e.ctx, tx, testBank, in)
		return wrap(err)
	})
	e.inTx(func(tx store.Tx) error {
		var err error
		second, err = NewEntityResolver(ResolverConfig{}).Resolve(e.ctx, tx, testBank, in)
		return wrap(err)
	})
	if len(first) != 2 || !slices.Equal(first, second) {
		t.Fatalf("first %v second %v, want the same two entities", first, second)
	}
}

// seedKindEntity creates an entity of the given kind last seen at seen; with partner set, a unit mentions
// both so they co-occur.
func (e *env) seedKindEntity(name, kind, partner string, seen time.Time) domain.EntityID {
	e.t.Helper()
	var id domain.EntityID
	e.inTx(func(tx store.Tx) error {
		ents := []domain.EntityID{}
		for _, n := range []string{name, partner} {
			if n == "" {
				continue
			}
			k := ""
			if n == name {
				k = kind
			}
			eid, err := tx.Entities().Upsert(e.ctx, domain.Entity{Bank: testBank, CanonicalName: n, Kind: k, LastSeen: &seen})
			if err != nil {
				return err
			}
			ents = append(ents, eid)
		}
		id = ents[0]
		if partner == "" {
			return nil
		}
		u := domain.Unit{ID: pgtest.ID[domain.UnitID]("seed-" + name), Bank: testBank, Type: domain.FactTypeWorld, Text: name}
		if err := tx.Units().Insert(e.ctx, []domain.Unit{u}); err != nil {
			return err
		}
		return tx.Entities().Attach(e.ctx, u.ID, ents)
	})
	return id
}

// TestEntityResolverMergeGates covers the gates applied before scoring: a candidate that differs word by
// word, by number or by kind never merges, however recent and co-occurring it is.
func TestEntityResolverMergeGates(t *testing.T) {
	seen := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	at := seen.Add(24 * time.Hour)
	tests := []struct {
		name                string
		seed, kind, partner string
		mentions            []domain.Mention
		merge               bool
	}{
		{
			"different first name splits despite co-occurrence and recency", "John Smith", "", "Acme",
			[]domain.Mention{{Name: "Jane Smith"}, {Name: "Acme"}},
			false,
		},
		{
			"different number splits despite co-occurrence and recency", "Room 101", "", "Acme",
			[]domain.Mention{{Name: "Room 102"}, {Name: "Acme"}},
			false,
		},
		{
			"abbreviated word merges with co-occurrence and recency", "Acme Corporation", "", "Bob Stone",
			[]domain.Mention{{Name: "Acme Corp"}, {Name: "Bob Stone"}},
			true,
		},
		{
			"identical name of another kind merges", "Kubernetes", "technology", "",
			[]domain.Mention{{Name: "Kubernetes", Kind: "concept"}},
			true,
		},
		{
			"similar name of another kind splits", "Alice Chen", "person", "Bob Stone",
			[]domain.Mention{{Name: "Alicia Chen", Kind: "organization"}, {Name: "Bob Stone"}},
			false,
		},
		{
			"identical name of the same kind merges (case-insensitive)", "Alice Chen", "person", "",
			[]domain.Mention{{Name: "Alice Chen", Kind: "Person"}},
			true,
		},
		{
			"unknown mention kind merges", "Alice Chen", "person", "Bob Stone",
			[]domain.Mention{{Name: "Alicia Chen"}, {Name: "Bob Stone"}},
			true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, 0)
			seeded := e.seedKindEntity(tc.seed, tc.kind, tc.partner, seen)
			in := ResolveInput{Mentions: tc.mentions, At: at, Threshold: DefaultEntityMatchThreshold}
			var ids []domain.EntityID
			e.inTx(func(tx store.Tx) error {
				var err error
				ids, err = NewEntityResolver(ResolverConfig{}).Resolve(e.ctx, tx, testBank, in)
				return wrap(err)
			})
			if len(ids) != len(tc.mentions) {
				t.Fatalf("ids %v, want %d", ids, len(tc.mentions))
			}
			if got := ids[0] == seeded; got != tc.merge {
				t.Errorf("merged = %v, want %v (ids %v, seeded %s)", got, tc.merge, ids, seeded)
			}
		})
	}
}

// TestEntityResolverNearbyNeedsIdenticalName checks a fact's other name counts towards co-occurrence only
// through an entity it would itself merge into by name: "Acme Labs" co-occurs with "Acme" but is not it.
func TestEntityResolverNearbyNeedsIdenticalName(t *testing.T) {
	seen := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	e := newEnv(t, 0)
	alice := e.seedKindEntity("Alice Chen", "", "Acme Labs", seen)
	in := ResolveInput{
		Mentions: []domain.Mention{{Name: "Alicia Chen"}, {Name: "Acme"}}, At: seen.Add(24 * time.Hour),
		Threshold: DefaultEntityMatchThreshold,
	}
	var ids []domain.EntityID
	e.inTx(func(tx store.Tx) error {
		var err error
		ids, err = NewEntityResolver(ResolverConfig{}).Resolve(e.ctx, tx, testBank, in)
		return wrap(err)
	})
	if len(ids) == 0 || ids[0] == alice {
		t.Errorf("ids %v: Alicia Chen merged into %s through a merely similar co-occurring entity", ids, alice)
	}
}

// TestScoreCandidateThreshold checks the merge threshold is inclusive on the rounded score, whichever signals
// produce it.
func TestScoreCandidateThreshold(t *testing.T) {
	at := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	cand := func(sim float64, seen *time.Time) domain.EntityCandidate {
		return domain.EntityCandidate{Entity: domain.Entity{CanonicalName: "Alice Chen", LastSeen: seen}, NameSimilarity: sim}
	}
	tests := []struct {
		name       string
		c          domain.EntityCandidate
		cooc, nOth int
		merge      bool
	}{
		{"0.3 name + 0.3 co-occurrence is exactly 0.6 and merges", cand(0.6, nil), 1, 1, true},
		{"0.4 name + 0.2 recency is 0.6 and merges", cand(0.8, &at), 0, 0, true},
		{"0.295 name + 0.3 co-occurrence splits", cand(0.59, nil), 1, 1, false},
		{"0.3 name + co-occurrence with 1 of 2 other names splits", cand(0.6, nil), 1, 2, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := scoreCandidate(tc.c, "Alicia Chen", tc.cooc, tc.nOth, at)
			if got := meetsThreshold(s, DefaultEntityMatchThreshold); got != tc.merge {
				t.Errorf("score %v: merge = %v, want %v", s, got, tc.merge)
			}
		})
	}
}

// TestResolverTokensCompatible pins the word-by-word gate on Hindsight's own examples.
func TestResolverTokensCompatible(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"John Smith", "Jane Smith", false},
		{"Room 101", "Room 102", false},
		{"UA123", "UA124", false},
		{"UA0123", "UA123", true},
		{"Version 4.0", "Version 4", true},
		{"Python 3.10", "Python 3.1", false},
		{"Q3 earnings", "Q3 2024 earnings", true},
		{"Boeing 737 MAX", "Boeing 737 MAX 8", true},
		{"Python", "Python 3", true},
		{"Nick", "Nicolas", true},
		{"Acme Corp", "Acme Corporation", true},
		{"São Paulo", "Sao Paulo", true},
		{"Dr Waler", "Dr Wall", true},
		{"Ann Arbor", "Ann Arbour", true},
		{"Alice", "Alice Chen", true},
	}
	for _, tc := range tests {
		if got := tokensCompatible(tc.a, tc.b); got != tc.want {
			t.Errorf("tokensCompatible(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
	for _, tc := range []struct {
		a, b string
		want float64
	}{{"john", "jane", 0.5}, {"são", "sao", 2.0 / 3}, {"abcd", "bcda", 0.75}, {"", "", 1}} {
		if got := sequenceRatio([]rune(tc.a), []rune(tc.b)); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("sequenceRatio(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
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
	var ids []domain.EntityID
	var tags []string
	e.inTx(func(tx store.Tx) error {
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
	var again []domain.EntityID
	e.inTx(func(tx store.Tx) error {
		var err error
		again, _, err = NewLabelResolver().Resolve(e.ctx, tx, testBank, defs, labels[:1])
		return wrap(err)
	})
	if len(again) != 1 || again[0] != ids[0] {
		t.Errorf("label entity not reused: %v vs %v", again, ids)
	}
}

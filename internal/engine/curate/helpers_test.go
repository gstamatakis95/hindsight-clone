package curate

import (
	"context"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/graph"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"testing"
	"time"
)

// testBank is the bank every curate test works in.
var testBank = domain.BankRef{Tenant: "acme", Bank: "notes"}

// Fixture unit ids: Postgres stores unit ids as uuid. idNope is never written.
var (
	idF1   = pgtest.ID[domain.UnitID]("f1")
	idF2   = pgtest.ID[domain.UnitID]("f2")
	idO1   = pgtest.ID[domain.UnitID]("o1")
	idNope = pgtest.ID[domain.UnitID]("nope")
)

// env is a store with a seeded bank: document d1 with facts f1 (Alice, tagged team, causal link to
// f2) and f2, and observation o1 citing both.
type env struct {
	t     *testing.T
	ctx   context.Context
	clock *clock.Manual
	fake  *postgres.DB
	store store.DB
	emb   *fake.Embedder
}

// newEnv seeds the store and returns the environment.
func newEnv(t *testing.T) *env {
	t.Helper()
	clock := clock.NewManual(time.Time{})
	fs := pgtest.NewStore(t)
	e := &env{
		t: t, ctx: context.Background(), clock: clock, fake: fs, store: fs,
		emb: fake.NewEmbedder(fake.EmbedSimilar),
	}
	e.seed()
	return e
}

// seed writes the bank, document, facts, observation, entity and links.
func (e *env) seed() {
	consolidated := e.clock.Now()
	e.inTx(func(tx store.Tx) error {
		if _, err := tx.Banks().Ensure(e.ctx, testBank); err != nil {
			return err
		}
		seq, err := tx.Seq(e.ctx, testBank)
		if err != nil {
			return err
		}
		if err := tx.Documents().Upsert(e.ctx, domain.Document{ID: "d1", Bank: testBank}); err != nil {
			return err
		}
		f1 := e.fact(idF1, "Alice likes green tea", []string{"team"}, seq)
		f2 := e.fact(idF2, "Bob drinks coffee daily", []string{"team"}, seq)
		f2.ConsolidatedAt = &consolidated
		o1 := domain.Unit{
			ID: idO1, Bank: testBank, Type: domain.FactTypeObservation, Text: "Alice and Bob enjoy hot drinks",
			Tags: []string{"team"}, Embedding: e.emb.Vector("Alice and Bob enjoy hot drinks"), Seq: seq,
		}
		if err := tx.Units().Insert(e.ctx, []domain.Unit{f1, f2, o1}); err != nil {
			return err
		}
		if err := tx.Evidence().Add(e.ctx, idO1, []domain.Evidence{{Unit: idF1}, {Unit: idF2}}); err != nil {
			return err
		}
		ent, err := tx.Entities().Upsert(e.ctx, domain.Entity{Bank: testBank, CanonicalName: "Alice"})
		if err != nil {
			return err
		}
		if err := tx.Entities().Attach(e.ctx, idF1, []domain.EntityID{ent}); err != nil {
			return err
		}
		return tx.Graph().AddLinks(e.ctx, []graph.Link{
			{Bank: testBank, From: idF1, To: idF2, Kind: graph.KindCausal, Weight: 1},
			{Bank: testBank, From: idF1, To: idF2, Kind: graph.KindTemporal, Weight: 0.5},
		})
	})
}

// fact builds a seeded fact of document d1.
func (e *env) fact(id domain.UnitID, text string, tags []string, seq int64) domain.Unit {
	at := e.clock.Now()
	return domain.Unit{
		ID: id, Bank: testBank, DocumentID: "d1", Type: domain.FactTypeWorld, Text: text, Tags: tags,
		Embedding: e.emb.Vector(text), MentionedAt: &at, Seq: seq,
	}
}

// inTx runs fn in a transaction and fails the test on error.
func (e *env) inTx(fn func(tx store.Tx) error) {
	e.t.Helper()
	if err := e.fake.InTx(e.ctx, fn); err != nil {
		e.t.Fatal(err)
	}
}

// unit reads a live unit; ok=false when it is not live.
func (e *env) unit(id domain.UnitID) (domain.Unit, bool) {
	e.t.Helper()
	us, err := e.fake.Read().GetUnits(e.ctx, testBank, []domain.UnitID{id})
	if err != nil {
		e.t.Fatal(err)
	}
	if len(us) == 0 {
		return domain.Unit{}, false
	}
	return us[0], true
}

// ops lists the bank's operations of kind (any status).
func (e *env) ops(kind domain.OperationKind) []domain.Operation {
	e.t.Helper()
	var out []domain.Operation
	e.inTx(func(tx store.Tx) error {
		out = nil
		ops, _, err := tx.Queue().List(e.ctx, testBank, store.OperationFilter{Kind: kind, PageSize: 1000})
		out = ops
		return err
	})
	return out
}

// claim claims every due job of kind.
func (e *env) claim(kind domain.OperationKind) []store.Job {
	e.t.Helper()
	jobs, err := e.fake.Claim(e.ctx, "w1", []domain.OperationKind{kind}, 100)
	if err != nil {
		e.t.Fatal(err)
	}
	return jobs
}

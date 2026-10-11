package memory

import (
	"context"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/graph"
	"memd/internal/linking"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Fixture unit ids: Postgres stores unit ids as uuid. idNope is never written.
var (
	idF1   = pgtest.ID[domain.UnitID]("f1")
	idF2   = pgtest.ID[domain.UnitID]("f2")
	idO1   = pgtest.ID[domain.UnitID]("o1")
	idNope = pgtest.ID[domain.UnitID]("nope")
)

// curateEnv is a store with a seeded bank: document d1 with facts f1 (Alice, tagged team, causal link to
// f2) and f2, and observation o1 citing both. Curation runs over store, which records audit rows.
type curateEnv struct {
	t     *testing.T
	ctx   context.Context
	clock *clock.Manual
	fake  *postgres.DB
	store *auditStore
	emb   *fake.Embedder
}

// newCurateEnv seeds the store and returns the environment.
func newCurateEnv(t *testing.T) *curateEnv {
	t.Helper()
	clock := clock.NewManual(time.Time{})
	fs := pgtest.NewStore(t)
	e := &curateEnv{
		t: t, ctx: context.Background(), clock: clock, fake: fs, store: &auditStore{DB: fs},
		emb: fake.NewEmbedder(fake.EmbedSimilar),
	}
	e.seed()
	return e
}

// seed writes the bank, document, facts, observation, entity and links.
func (e *curateEnv) seed() {
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
func (e *curateEnv) fact(id domain.UnitID, text string, tags []string, seq int64) domain.Unit {
	at := e.clock.Now()
	return domain.Unit{
		ID: id, Bank: testBank, DocumentID: "d1", Type: domain.FactTypeWorld, Text: text, Tags: tags,
		Embedding: e.emb.Vector(text), MentionedAt: &at, Seq: seq,
	}
}

// inTx runs fn in a transaction and fails the test on error.
func (e *curateEnv) inTx(fn func(tx store.Tx) error) {
	e.t.Helper()
	if err := e.fake.InTx(e.ctx, fn); err != nil {
		e.t.Fatal(err)
	}
}

// service returns the Service over the env with embedder emb (nil = the env's).
func (e *curateEnv) service(emb ai.Embedder) *Service {
	if emb == nil {
		emb = e.emb
	}
	return New(Deps{
		DB: e.store, Maintenance: e.fake, Embedder: emb, Config: config.NewResolver(e.fake, domain.BankConfig{}),
		Linker: linking.NewLinker(), Entities: linking.NewResolver(linking.ResolverConfig{}), Clock: e.clock,
	})
}

// curator returns the Service's curator.
func (e *curateEnv) curator() *curator { return e.service(nil).curate }

// edit returns the curation editing the fields of ed that fields names on unit id of the test bank.
func edit(id domain.UnitID, ed Edit, fields ...EditField) curation {
	return curation{UpdateRequest: UpdateRequest{Bank: testBank, ID: id, Edit: ed, Fields: fields}, act: actionEdit}
}

// rederiver returns the Service's rederiver embedding with emb (nil = the env's embedder).
func (e *curateEnv) rederiver(emb ai.Embedder) *rederiver { return e.service(emb).rederive }

// unit reads a live unit; ok=false when it is not live.
func (e *curateEnv) unit(id domain.UnitID) (domain.Unit, bool) {
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
func (e *curateEnv) ops(kind domain.OperationKind) []domain.Operation {
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
func (e *curateEnv) claim(kind domain.OperationKind) []store.Job {
	e.t.Helper()
	jobs, err := e.fake.Claim(e.ctx, "w1", []domain.OperationKind{kind}, 100)
	if err != nil {
		e.t.Fatal(err)
	}
	return jobs
}

// drain claims and completes every due job of kind.
func (e *curateEnv) drain(kind domain.OperationKind) {
	e.t.Helper()
	for _, j := range e.claim(kind) {
		if err := e.fake.Complete(e.ctx, j.ID, nil, nil); err != nil {
			e.t.Fatal(err)
		}
	}
}

// events lists the bank's lifecycle events.
func (e *curateEnv) events() []domain.LifecycleEvent {
	e.t.Helper()
	var out []domain.LifecycleEvent
	e.inTx(func(tx store.Tx) error {
		var err error
		out, _, err = tx.Events().List(e.ctx, testBank, 0, 0, nil)
		return err
	})
	return out
}

// hitIDs returns the unit ids of hits.
func hitIDs[H store.Hit | graph.Hit](hs []H) []domain.UnitID {
	ids := make([]domain.UnitID, len(hs))
	for i, h := range hs {
		switch h := any(h).(type) {
		case store.Hit:
			ids[i] = h.ID
		case graph.Hit:
			ids[i] = h.ID
		}
	}
	return ids
}

// auditStore wraps a Store to record the audit rows its transactions append (the ports have no audit read).
type auditStore struct {
	store.DB
	mu     sync.Mutex
	audits []domain.AuditEntry
	// open counts transactions in flight, so stubs can assert they are not called inside one.
	open atomic.Int32
}

// InTx wraps the transaction so Units().AppendAudit is recorded.
func (s *auditStore) InTx(ctx context.Context, fn func(store.Tx) error) error {
	s.open.Add(1)
	defer s.open.Add(-1)
	return s.DB.InTx(ctx, func(tx store.Tx) error { return fn(auditTx{Tx: tx, s: s}) })
}

// recorded returns the audit rows recorded so far.
func (s *auditStore) recorded() []domain.AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.audits)
}

// auditTx is a Tx whose unit repo records audits.
type auditTx struct {
	store.Tx
	s *auditStore
}

// Units returns the recording unit repo.
func (t auditTx) Units() store.UnitRepo { return auditUnits{UnitRepo: t.Tx.Units(), s: t.s} }

// auditUnits records AppendAudit calls.
type auditUnits struct {
	store.UnitRepo
	s *auditStore
}

// AppendAudit records a and forwards it.
func (u auditUnits) AppendAudit(ctx context.Context, a domain.AuditEntry) error {
	u.s.mu.Lock()
	u.s.audits = append(u.s.audits, a)
	u.s.mu.Unlock()
	return u.UnitRepo.AppendAudit(ctx, a)
}

// addModels creates mental models in the test bank, each citing (source_ids) the units it maps to.
func (e *curateEnv) addModels(cites map[domain.MentalModelID][]domain.UnitID) {
	e.t.Helper()
	e.inTx(func(tx store.Tx) error {
		for id, units := range cites {
			m := domain.MentalModel{
				Bank: testBank, Spec: domain.MentalModelSpec{ID: id, Name: string(id), SourceQuery: "q"},
			}
			m.SourceIDs = units
			if err := tx.MentalModels().Create(e.ctx, m); err != nil {
				return err
			}
		}
		return nil
	})
}

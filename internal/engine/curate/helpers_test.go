package curate

import (
	"context"
	"encoding/json"
	"errors"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/engine/retain"
	"memd/internal/fake"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"sync"
	"sync/atomic"
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
	t      *testing.T
	ctx    context.Context
	clock  *clock.Manual
	fake   *postgres.DB
	store  *auditStore
	emb    *fake.Embedder
	citing *citingStub
	linker *linkerStub
}

// newEnv seeds the store and returns the environment.
func newEnv(t *testing.T) *env {
	t.Helper()
	clock := clock.NewManual(time.Time{})
	fs := pgtest.NewStore(t)
	e := &env{
		t: t, ctx: context.Background(), clock: clock, fake: fs, store: &auditStore{DB: fs},
		emb: fake.NewEmbedder(fake.EmbedSimilar), linker: &linkerStub{},
	}
	e.citing = &citingStub{cites: map[domain.UnitID][]domain.UnitID{idO1: {idF1, idF2}}, store: e.store}
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
		return tx.Links().Insert(e.ctx, []domain.Link{
			{Bank: testBank, From: idF1, To: idF2, Kind: domain.LinkCausal, Weight: 1},
			{Bank: testBank, From: idF1, To: idF2, Kind: domain.LinkTemporal, Weight: 0.5},
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

// curator returns a Curator over the env with the refresh stub.
func (e *env) curator() Curator {
	return NewCurator(CuratorDeps{
		Store: e.store, Clock: e.clock, Citing: e.citing, Refresh: refreshStub{}, Resolver: resolverStub{},
		Config: configStub{},
	})
}

// rederiver returns a Rederiver over the env.
func (e *env) rederiver(emb ai.Embedder) Rederiver {
	if emb == nil {
		emb = e.emb
	}
	return NewRederiver(RederiverDeps{
		Store: e.store, Embedder: emb, Linker: e.linker,
	})
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

// drain claims and completes every due job of kind.
func (e *env) drain(kind domain.OperationKind) {
	e.t.Helper()
	for _, j := range e.claim(kind) {
		if err := e.fake.Complete(e.ctx, j.ID, nil, nil); err != nil {
			e.t.Fatal(err)
		}
	}
}

// events lists the bank's lifecycle events.
func (e *env) events() []domain.LifecycleEvent {
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
func hitIDs(hs []store.Hit) []domain.UnitID {
	ids := make([]domain.UnitID, len(hs))
	for i, h := range hs {
		ids[i] = h.ID
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

// citingStub maps cited unit ids to the models citing them; cites is the seeded evidence (observation ->
// the facts it cites), so it returns the models citing ids or an observation citing one of ids.
// It fails when called while a transaction of store is open.
type citingStub struct {
	by    map[domain.UnitID][]domain.MentalModelID
	cites map[domain.UnitID][]domain.UnitID
	store *auditStore
}

// ModelsCiting returns the models citing any of ids or any observation citing one of ids.
func (c *citingStub) ModelsCiting(
	_ context.Context, _ domain.BankRef, ids []domain.UnitID,
) ([]domain.MentalModelID, error) {
	if c.store != nil && c.store.open.Load() > 0 {
		return nil, errors.New("ModelsCiting called while a transaction is open")
	}
	all := slices.Clone(ids)
	for obs, facts := range c.cites {
		for _, f := range facts {
			if slices.Contains(ids, f) {
				all = append(all, obs)
				break
			}
		}
	}
	var out []domain.MentalModelID
	for _, id := range all {
		out = append(out, c.by[id]...)
	}
	return out, nil
}

// refreshStub enqueues a refresh_mental_model job in the caller's tx, as the mental-model queuer would.
type refreshStub struct{}

// QueueRefresh enqueues {"model_id": id} under serial key mm:{id}.
func (refreshStub) QueueRefresh(
	ctx context.Context, tx store.Tx, b domain.BankRef, id domain.MentalModelID,
) (domain.OperationID, error) {
	raw, _ := json.Marshal(map[string]string{"model_id": string(id)})
	op, _, err := tx.Queue().Enqueue(ctx, store.Job{
		Bank: b, Kind: domain.OperationKindRefreshMentalModel, SerialKey: "mm:" + string(id), Payload: raw,
		Dedupe: store.DedupePending,
	})
	return op, err
}

// resolverStub resolves each mention to the entity with exactly that name, creating it when missing.
type resolverStub struct{}

// Resolve upserts one entity per mention.
func (resolverStub) Resolve(
	ctx context.Context, tx store.Tx, b domain.BankRef, in retain.ResolveInput,
) ([]domain.EntityID, error) {
	var ids []domain.EntityID
	for _, m := range in.Mentions {
		cands, err := tx.Entities().Candidates(ctx, b, m.Name, 5)
		if err != nil {
			return nil, err
		}
		id := domain.EntityID("")
		for _, c := range cands {
			if c.Entity.CanonicalName == m.Name {
				id = c.Entity.ID
			}
		}
		if id == "" {
			if id, err = tx.Entities().Upsert(ctx, domain.Entity{Bank: b, CanonicalName: m.Name}); err != nil {
				return nil, err
			}
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// linkerStub records the units it links and adds a temporal link from each to f2 when both are live.
type linkerStub struct {
	mu    sync.Mutex
	calls [][]domain.UnitID
}

// Link records the call and inserts a temporal edge to f2.
func (l *linkerStub) Link(
	ctx context.Context, tx store.Tx, b domain.BankRef, units []domain.Unit, _ retain.CauseMap,
) error {
	ids := make([]domain.UnitID, len(units))
	for i, u := range units {
		ids[i] = u.ID
	}
	l.mu.Lock()
	l.calls = append(l.calls, ids)
	l.mu.Unlock()
	var ls []domain.Link
	for _, u := range units {
		if u.ID != idF2 {
			ls = append(ls, domain.Link{Bank: b, From: u.ID, To: idF2, Kind: domain.LinkTemporal, Weight: 0.9})
		}
	}
	return tx.Links().Insert(ctx, ls)
}

// configStub is a config.Resolver returning the empty config.
type configStub struct{}

// Effective returns the zero config.
func (configStub) Effective(context.Context, domain.BankRef, ...domain.BankConfig) (domain.BankConfig, error) {
	return domain.BankConfig{}, nil
}

// ForScope returns the zero config.
func (configStub) ForScope(context.Context, domain.ScopeKey, *domain.BankConfig) (domain.BankConfig, error) {
	return domain.BankConfig{}, nil
}

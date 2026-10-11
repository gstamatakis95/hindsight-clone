package consolidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/jobs"
	"memd/internal/prompts"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// bank is the test bank.
var bank = domain.BankRef{Tenant: "t1", Bank: "b1"}

// errPoison is what a scripted CONSOLIDATE call fails with for a batch holding a poison unit.
var errPoison = errors.New("poison input")

// create is one create of a scripted CONSOLIDATE reply.
type create struct {
	Text          string   `json:"text"`
	SourceFactIDs []string `json:"source_fact_ids"`
	Reason        string   `json:"reason"`
}

// update is one update of a scripted CONSOLIDATE reply.
type update struct {
	ObservationID string   `json:"observation_id"`
	Text          string   `json:"text"`
	SourceFactIDs []string `json:"source_fact_ids"`
	Reason        string   `json:"reason"`
}

// uid is the unit id of a readable fixture name (Postgres stores unit ids as uuid).
func uid(name string) domain.UnitID { return pgtest.ID[domain.UnitID](name) }

// uids maps uid over names.
func uids(names ...string) []domain.UnitID { return pgtest.IDs[domain.UnitID](names...) }

// names maps unit ids back to their fixture names.
func names(ids []domain.UnitID) []string { return pgtest.Names(ids) }

// sortedNames is names, sorted: Postgres returns an observation's sources in id order, which is not the
// order of their names.
func sortedNames(ids []domain.UnitID) []string {
	out := names(ids)
	slices.Sort(out)
	return out
}

// toID maps a fixture name to its unit id; an id passes through.
func toID(s string) string {
	if pgtest.Name(s) != s {
		return s
	}
	return string(uid(s))
}

// toIDs maps toID over ss.
func toIDs(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = toID(s)
	}
	return out
}

// reply renders a CONSOLIDATE reply; fact and observation names become their unit ids.
func reply(creates []create, updates []update) string {
	if creates == nil {
		creates = []create{}
	}
	if updates == nil {
		updates = []update{}
	}
	for i := range creates {
		creates[i].SourceFactIDs = toIDs(creates[i].SourceFactIDs)
	}
	for i := range updates {
		updates[i].SourceFactIDs = toIDs(updates[i].SourceFactIDs)
		updates[i].ObservationID = toID(updates[i].ObservationID)
	}
	b, _ := json.Marshal(map[string]any{"creates": creates, "updates": updates})
	return string(b)
}

// env is a consolidation test environment over a store on its own database and the fakes (LLM, Embedder,
// Clock) with the real
// config resolver, router and StrategyMatcher.
type env struct {
	hook   reporter // receives the run's progress reports; nil = an inline run
	t      *testing.T
	clock  *clock.Manual
	store  *postgres.DB
	pool   *pgxpool.Pool
	llm    *fake.LLM
	emb    *fake.Embedder
	deps   ConsolidatorDeps
	texts  map[domain.UnitID]string
	nextID int
}

// newEnv creates the bank with cfg as its own config.
func newEnv(t *testing.T, cfg domain.BankConfig) *env {
	t.Helper()
	clock := clock.NewManual(time.Time{})
	db := pgtest.New(t)
	e := &env{
		t: t, clock: clock, store: db.Store(), pool: db.Pool, llm: fake.NewLLM(),
		emb: fake.NewEmbedder(fake.EmbedSimilar), texts: map[domain.UnitID]string{},
	}
	server, err := config.ServerDefaults(domain.BankConfig{Models: &domain.ModelBindings{
		Default: &domain.ModelBinding{Model: "base-model"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.tx(func(tx store.Tx) error {
		if _, err := tx.Banks().Ensure(t.Context(), bank); err != nil {
			return err
		}
		return tx.Banks().SetConfig(t.Context(), bank, cfg)
	})
	e.deps = ConsolidatorDeps{
		Store:    e.store,
		Config:   config.NewResolver(e.store, NewStrategyMatcher(nil), server),
		Router:   ai.NewRouter(),
		LLM:      e.llm,
		Embedder: e.emb,
		Clock:    clock,
		NewID: func() domain.UnitID {
			e.nextID++
			return uid(fmt.Sprintf("obs-%02d", e.nextID))
		},
	}
	return e
}

// consolidator builds the Consolidator from e.deps.
func (e *env) consolidator() Consolidator { return NewConsolidator(e.deps) }

// tx runs fn in a write transaction and fails the test on error.
func (e *env) tx(fn func(tx store.Tx) error) {
	e.t.Helper()
	if err := e.store.InTx(e.t.Context(), fn); err != nil {
		e.t.Fatal(err)
	}
}

// script queues CONSOLIDATE replies, one per call in order (the last one repeats).
func (e *env) script(replies ...fake.Reply) {
	e.llm.Script(domain.ModelSlotConsolidate, prompts.ConsolidateSystem, replies...)
}

// ok is a CONSOLIDATE reply with body (10 input and 2 output tokens).
func ok(body string) fake.Reply {
	return fake.Reply{JSON: body, Usage: domain.Usage{InputTokens: 10, OutputTokens: 2}}
}

// fail is a failing CONSOLIDATE reply.
func fail() fake.Reply { return fake.Reply{Err: errPoison} }

// createEach is a CONSOLIDATE reply creating one observation per fact.
func (e *env) createEach(ids ...string) fake.Reply {
	var cs []create
	for _, id := range ids {
		cs = append(cs, create{Text: "Observed: " + e.texts[domain.UnitID(id)], SourceFactIDs: []string{toID(id)}})
	}
	return ok(reply(cs, nil))
}

// dedupReply queues a DEDUP_CHECK reply.
func (e *env) dedupReply(body string) {
	e.llm.RespondJSON(domain.ModelSlotDedupCheck, prompts.DedupSystem, body)
}

// calls returns the recorded requests of slot.
func (e *env) calls(slot domain.ModelSlot) []ai.StructuredRequest {
	var out []ai.StructuredRequest
	for _, c := range e.llm.Calls() {
		if c.Slot == slot && c.Structured != nil {
			out = append(out, *c.Structured)
		}
	}
	return out
}

// batches returns the fact ids of every CONSOLIDATE call, in call order.
func (e *env) batches() [][]string {
	var out [][]string
	for _, c := range e.calls(domain.ModelSlotConsolidate) {
		out = append(out, pgtest.Names(schemaFactIDs(c.Schema)))
	}
	return out
}

// schemaFactIDs reads the per-call source_fact_ids enum (the batch's fact ids) from a CONSOLIDATE schema.
func schemaFactIDs(raw json.RawMessage) []string {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	items, ok := dig(root, "properties", "creates", "items", "properties", "source_fact_ids", "items")
	if !ok {
		return nil
	}
	enum, _ := items["enum"].([]any)
	out := make([]string, 0, len(enum))
	for _, v := range enum {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

// fact inserts a world fact with tags (normalized) and an embedding of its text.
func (e *env) fact(id, text string, tags ...string) {
	e.t.Helper()
	e.texts[domain.UnitID(id)] = text
	e.tx(func(tx store.Tx) error {
		seq, err := tx.Seq(e.t.Context(), bank)
		if err != nil {
			return err
		}
		return tx.Units().Insert(e.t.Context(), []domain.Unit{{
			ID: uid(id), Bank: bank, Type: domain.FactTypeWorld, Text: text,
			Tags: domain.NormalizeTags(tags), Embedding: e.emb.Vector(text), Seq: seq,
		}})
	})
}

// observation inserts an observation of scope tags citing sources (which it stamps consolidated).
func (e *env) observation(id, text string, tags []string, sources ...string) {
	e.t.Helper()
	e.seedObservation(id, text, tags, true, sources...)
}

// seedObservation inserts an observation of scope tags citing sources, stamping them when stamp is set.
func (e *env) seedObservation(id, text string, tags []string, stamp bool, sources ...string) {
	e.t.Helper()
	e.tx(func(tx store.Tx) error {
		ctx := e.t.Context()
		seq, err := tx.Seq(ctx, bank)
		if err != nil {
			return err
		}
		err = tx.Units().UpsertObservation(ctx, domain.Unit{
			ID: uid(id), Bank: bank, Type: domain.FactTypeObservation, Text: text,
			Tags: domain.NormalizeTags(tags), Embedding: e.emb.Vector(text), Seq: seq, Version: 1,
			ProofCount: len(sources),
		})
		if err != nil {
			return err
		}
		var ev []domain.Evidence
		var ids []domain.UnitID
		for _, s := range sources {
			ev = append(ev, domain.Evidence{Unit: uid(s), Quote: e.texts[domain.UnitID(s)]})
			ids = append(ids, uid(s))
		}
		if err := tx.Evidence().Add(ctx, uid(id), ev); err != nil || !stamp {
			return err
		}
		return tx.Units().MarkConsolidated(ctx, ids, e.clock.Now())
	})
}

// unit reads one live unit.
func (e *env) unit(id string) domain.Unit {
	e.t.Helper()
	us, err := e.store.Read().GetUnits(e.t.Context(), bank, []domain.UnitID{uid(id)})
	if err != nil || len(us) != 1 {
		e.t.Fatalf("unit %s: %v (%d found)", id, err, len(us))
	}
	return us[0]
}

// observations returns every live observation of the bank.
func (e *env) observations() []domain.Unit {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `SELECT id::text FROM memory_units
		WHERE tenant_id = $1 AND bank_id = $2 AND fact_type = 'observation' ORDER BY id`, bank.Tenant, bank.Bank)
	if err != nil {
		e.t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[domain.UnitID])
	if err != nil {
		e.t.Fatal(err)
	}
	us, err := e.store.Read().GetUnits(e.t.Context(), bank, ids)
	if err != nil {
		e.t.Fatal(err)
	}
	return us
}

// jobs lists the bank's pending operations of kind.
func (e *env) jobs(kind domain.OperationKind) []domain.Operation {
	e.t.Helper()
	var ops []domain.Operation
	e.tx(func(tx store.Tx) error {
		var err error
		ops, _, err = tx.Queue().List(e.t.Context(), bank, store.OperationFilter{
			Status: domain.OperationStatusPending, Kind: kind, PageSize: 100,
		})
		return err
	})
	return ops
}

// wantPending fails the test unless the bank has n pending operations of kind.
func (e *env) wantPending(kind domain.OperationKind, n int) {
	e.t.Helper()
	if got := len(e.jobs(kind)); got != n {
		e.t.Fatalf("pending %s operations = %d, want %d", kind, got, n)
	}
}

// reporter is a test hook on a run's progress reports.
type reporter interface {
	Report(ctx context.Context, p domain.Progress) error
}

// hookClaimer is a jobs.Claimer serving only a jobs.Run's progress reports: Progress goes to the hook and
// the job is always alive.
type hookClaimer struct {
	jobs.Claimer
	hook reporter
}

// Progress hands p to the hook.
func (h hookClaimer) Progress(ctx context.Context, _ domain.OperationID, p domain.Progress) error {
	return h.hook.Report(ctx, p) //nolint:wrapcheck // the hook's error is the run's checkpoint error
}

// Alive is always true.
func (hookClaimer) Alive(context.Context, domain.OperationID) (bool, error) { return true, nil }

// jobRun is the run of j: its progress reports go to e.hook when one is set; without one it is an inline run.
func (e *env) jobRun(j store.Job) *jobs.Run {
	if e.hook == nil {
		return jobs.Inline(j)
	}
	return jobs.NewRun(hookClaimer{hook: e.hook}, j)
}

// newJob is a consolidate job of the test bank as the catalogue builds it: payload {} or the scopes, DedupeNone
// with scopes or overrides, else DedupePending.
func newJob(scopes []domain.TagSet, overrides *domain.BankConfig) store.Job {
	j := store.Job{
		Bank: bank, Kind: domain.OperationKindConsolidate, SerialKey: jobs.SerialKeyConsolidate,
		Payload: []byte(`{}`), Overrides: overrides, Dedupe: store.DedupePending,
	}
	if len(scopes) > 0 {
		j.Payload, _ = json.Marshal(jobs.ConsolidatePayload{Scopes: scopes})
	}
	if len(scopes) > 0 || overrides != nil {
		j.Dedupe = store.DedupeNone
	}
	return j
}

// run runs j and fails the test on error.
func (e *env) run(j store.Job) ConsolidationResult {
	e.t.Helper()
	res, err := e.consolidator().Run(e.t.Context(), e.jobRun(j))
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

// autoJob is the automatic consolidate job of the test bank.
func autoJob() store.Job {
	j := newJob(nil, nil)
	j.ID = pgtest.ID[domain.OperationID]("op-1")
	return j
}

// scopeOf builds a scope key of the test bank.
func scopeOf(tags ...string) domain.ScopeKey { return domain.NewScopeKey(bank, tags) }

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// consolidateModel binds the CONSOLIDATE slot to model.
func consolidateModel(model string) *domain.ModelBinding {
	return &domain.ModelBinding{Model: model}
}

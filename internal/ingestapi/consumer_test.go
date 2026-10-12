package ingestapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/ingest"
	"memd/internal/ingest/memq"
	"memd/internal/linking"
	"memd/internal/memory"
	"memd/internal/org"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"memd/internal/timex"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "memd/gen/memory/v1"
)

// The consumer over a real memory and org service (pgtest, fake blob and models) and an in-memory queue.

// gatedBlob is a fake.Blob whose Puts wait for open, so a test can hold a RetainBatch call in flight.
type gatedBlob struct {
	*fake.Blob
	entered chan struct{} // closed by the first Put
	open    chan struct{} // Puts proceed once closed
	once    sync.Once
}

// Put signals entered, waits for open, then stores the object.
func (g *gatedBlob) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	g.once.Do(func() { close(g.entered) })
	<-g.open
	return g.Blob.Put(ctx, key, r, size, contentType) //nolint:wrapcheck // test passthrough
}

// env is a consumer's world: the store, the services, the queue and the counts the Count hook saw.
type env struct {
	store *postgres.DB
	blob  *gatedBlob
	mem   *memory.Service
	orgs  *org.Service
	q     *memq.Queue

	mu     sync.Mutex
	counts map[string]int // event/reason -> messages
}

// tenant and orgID are where the tests write; the org exists.
const (
	tenant = "acme"
	orgID  = "o"
)

// newEnv builds the services over a fresh database with the test org created, and an open blob gate.
func newEnv(t *testing.T) *env {
	t.Helper()
	server, err := config.ServerDefaults(domain.BankConfig{Models: &domain.ModelBindings{
		Default: &domain.ModelBinding{Model: "test-model"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{
		store: pgtest.NewStore(t), q: memq.New(memq.WithPollWait(20 * time.Millisecond)),
		blob:   &gatedBlob{Blob: fake.NewBlob(), entered: make(chan struct{}), open: make(chan struct{})},
		counts: map[string]int{},
	}
	close(e.blob.open)
	pgtest.CreateOrgs(t, e.store, domain.NewBankRef(tenant, orgID, ""))
	clk, llm, router := clock.NewManual(time.Time{}), fake.NewLLM(), ai.NewRouter()
	e.mem = memory.New(memory.Deps{
		DB: e.store, Maintenance: e.store, Blob: e.blob, LLM: llm, Embedder: fake.NewEmbedder(fake.EmbedSimilar),
		Router: router, Config: config.NewResolver(e.store, server), Times: timex.New(llm, router, clk),
		Linker: linking.NewLinker(), Entities: linking.NewResolver(linking.ResolverConfig{}), Clock: clk,
	})
	e.orgs = org.New(org.Deps{DB: e.store, Maintenance: e.store, Blob: e.blob, Clock: clk})
	return e
}

// consumer returns a consumer over a new source of the queue, pinned to tenant.
func (e *env) consumer() (*Consumer, *memq.Source) {
	src := e.q.Source()
	return New(Config{Tenant: tenant, FlushInterval: 10 * time.Millisecond}, Deps{
		Source: src, Memory: e.mem, Orgs: e.orgs, Logger: slog.New(slog.DiscardHandler),
		Hooks: Hooks{Count: func(event, reason string, n int) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.counts[event+"/"+reason] += n
		}},
	}), src
}

// count returns how many messages reached event with reason.
func (e *env) count(event, reason string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.counts[event+"/"+reason]
}

// run runs a consumer until every message is settled, then stops it.
func (e *env) run(t *testing.T) {
	t.Helper()
	c, _ := e.consumer()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	deadline := time.Now().Add(30 * time.Second)
	for e.q.Pending() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := e.q.Pending(); n > 0 {
		t.Fatalf("%d messages still unsettled", n)
	}
}

// envelope is a valid envelope for the test org's bank b and document doc.
func envelope(b, doc string) *pb.IngestEnvelope {
	return &pb.IngestEnvelope{
		OrgId: orgID, BankId: b, Item: &pb.RetainItem{DocumentId: doc, Content: "Alice moved to Lisbon in 2021."},
	}
}

// jsonMsg is env encoded as protojson, keyed by its document id.
func jsonMsg(t *testing.T, env *pb.IngestEnvelope) ingest.Message {
	t.Helper()
	body, err := protojson.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return ingest.Message{Key: env.GetItem().GetDocumentId(), Body: body}
}

// document reads a document of bank b, or fails.
func (e *env) document(t *testing.T, b, doc string) domain.Document {
	t.Helper()
	var d domain.Document
	err := e.store.InTx(t.Context(), func(tx store.Tx) error {
		var err error
		d, err = tx.Documents().Get(t.Context(), domain.NewBankRef(tenant, orgID, b), domain.DocumentID(doc))
		return err
	})
	if err != nil {
		t.Fatalf("document %s/%s: %v", b, doc, err)
	}
	return d
}

// TestConsumerSettlesByEnvelope publishes valid envelopes in both encodings and the invalid kinds the consumer
// dead-letters itself or through RetainBatch's REJECTED, and checks each message's settlement and reason.
func TestConsumerSettlesByEnvelope(t *testing.T) {
	e := newEnv(t)
	binary, err := proto.Marshal(envelope("b", "d-proto"))
	if err != nil {
		t.Fatal(err)
	}
	other := envelope("b", "d-other")
	other.TenantId = "intruder"
	noDoc := envelope("b", "")
	badBank := envelope("a/b", "d-bad")
	e.q.Publish(
		jsonMsg(t, envelope("b", "d-json")),
		ingest.Message{Key: "d-proto", Body: binary, Attributes: map[string]string{
			ingest.AttrContentType: ingest.ContentTypeProto,
		}},
		ingest.Message{Key: "x", Body: []byte(`{"org_id": "o", "bankid": "typo"}`)},
		jsonMsg(t, other), jsonMsg(t, noDoc), jsonMsg(t, badBank),
	)
	e.run(t)

	e.document(t, "b", "d-json")
	e.document(t, "b", "d-proto")
	if n := e.count(EventAcked, ""); n != 2 {
		t.Fatalf("acked %d, want 2", n)
	}
	var reasons []string
	for _, d := range e.q.DeadLetters() {
		reasons = append(reasons, strings.SplitN(d.Reason, ":", 2)[0])
	}
	want := []string{"INVALID_ARGUMENT", "INVALID_ARGUMENT", reasonTenantMismatch, reasonUndecodable}
	slices.Sort(reasons)
	if !slices.Equal(reasons, want) {
		t.Fatalf("dead-letter reasons %v, want %v", reasons, want)
	}
}

// TestConsumerDropsDeletedOrg checks that a message for an org deleted after it was published is acked and dropped,
// not dead-lettered or retained, so a deleted org's content never comes back; a message for an org never created
// is retried instead.
func TestConsumerDropsDeletedOrg(t *testing.T) {
	e := newEnv(t)
	gone := domain.OrgRef{Tenant: tenant, Org: "gone"}
	pgtest.CreateOrgs(t, e.store, domain.NewBankRef(tenant, gone.Org, ""))
	if _, _, err := e.orgs.DeleteOrg(t.Context(), gone); err != nil {
		t.Fatal(err)
	}
	msg := envelope("b", "d1")
	msg.OrgId = gone.Org
	e.q.Publish(jsonMsg(t, msg))
	e.run(t)
	if n := e.count(EventDropped, "ORG_NOT_FOUND"); n != 0 {
		t.Fatalf("dropped as plain ORG_NOT_FOUND %d times", n)
	}
	if e.count(EventDropped, reasonOrgDeleted)+e.count(EventDropped, "ORG_DELETING") != 1 || len(e.q.DeadLetters()) > 0 {
		t.Fatalf("counts %v, dead letters %v: want one drop", e.counts, e.q.DeadLetters())
	}

	unknown := envelope("b", "d2")
	unknown.OrgId = "not-yet"
	e.q.Publish(jsonMsg(t, unknown))
	c, _ := e.consumer()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	for e.count(EventNacked, "ORG_NOT_FOUND") == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if e.q.Pending() != 1 || len(e.q.DeadLetters()) > 0 {
		t.Fatalf("an unknown org's message must wait for redelivery: pending %d, dead letters %v", e.q.Pending(),
			e.q.DeadLetters())
	}
}

// TestConsumerShutdownSettlesInFlight stops the consumer while a RetainBatch call is in flight: the call finishes
// within the grace, its messages are acked, and only then is the source closed.
func TestConsumerShutdownSettlesInFlight(t *testing.T) {
	e := newEnv(t)
	e.blob.open = make(chan struct{}) // hold every Put
	e.q.Publish(jsonMsg(t, envelope("b", "d1")), jsonMsg(t, envelope("b", "d2")), jsonMsg(t, envelope("b", "d3")))
	c, src := e.consumer()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	<-e.blob.entered
	cancel()
	time.Sleep(50 * time.Millisecond) // the stop signal is in; the call still waits on the blob store
	close(e.blob.open)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := e.q.Pending(); n != 0 {
		t.Fatalf("%d messages unsettled after shutdown", n)
	}
	for _, d := range []string{"d1", "d2", "d3"} {
		e.document(t, "b", d)
	}
	if _, err := src.Receive(t.Context(), 1); !errors.Is(err, ingest.ErrClosed) {
		t.Fatalf("source not closed after Run: %v", err)
	}
}

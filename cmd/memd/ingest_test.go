package main

import (
	"context"
	"memd/internal/ingest"
	"memd/internal/ingest/memq"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	pb "memd/gen/memory/v1"
)

// lostAck is a source whose first Ack is lost (it reports success but settles nothing): the commit-then-crash
// window, after which the broker redelivers a message whose retain already committed.
type lostAck struct {
	ingest.Source
	mu    sync.Mutex
	acks  int
	acked int // messages really acked
}

// Ack drops the first call and forwards the others.
func (s *lostAck) Ack(ctx context.Context, msgs ...ingest.Message) error {
	s.mu.Lock()
	s.acks++
	first := s.acks == 1
	if !first {
		s.acked += len(msgs)
	}
	s.mu.Unlock()
	if first {
		return nil
	}
	return s.Source.Ack(ctx, msgs...) //nolint:wrapcheck // test passthrough
}

// TestIngestRedeliveryRetainsOnce delivers one queue message twice on the wired stack (the first Ack is lost, so the
// lease runs out and the message comes back) and checks it yields one document, one retain_document operation and
// one extraction: the redelivery is unchanged or joins the retain in flight.
func TestIngestRedeliveryRetainsOnce(t *testing.T) {
	w := startWired(t)
	const bank = "queued"
	ctx := w.ctx(t) // creates the org of tenant "t"
	body, err := protojson.Marshal(&pb.IngestEnvelope{
		TenantId: "t", OrgId: testOrg, BankId: bank,
		Item: &pb.RetainItem{DocumentId: "d1", Content: "Alice moved to Lisbon in 2021. Alice works as a nurse."},
	})
	if err != nil {
		t.Fatal(err)
	}
	q := memq.New(memq.WithLease(time.Second), memq.WithPollWait(20*time.Millisecond))
	q.Publish(ingest.Message{Key: "d1", Body: body})
	src := &lostAck{Source: q.Source()}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- newConsumer(w.deps, src).Run(runCtx) }()
	for q.Pending() > 0 && ctx.Err() == nil {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if src.acks < 2 || src.acked != 1 || len(q.DeadLetters()) > 0 {
		t.Fatalf("want the message delivered twice and acked once: ack calls %d, acked %d, dead letters %v",
			src.acks, src.acked, q.DeadLetters())
	}
	w.idle(t, bank)

	docs, err := pb.NewDocumentServiceClient(w.conn).ListDocuments(ctx, &pb.ListDocumentsRequest{
		OrgId: testOrg, BankId: bank,
	})
	if err != nil || len(docs.GetDocuments()) != 1 {
		t.Fatalf("documents: %v %v", docs, err)
	}
	ops, err := pb.NewOperationServiceClient(w.conn).ListOperations(ctx, &pb.ListOperationsRequest{
		OrgId: testOrg, BankId: bank, Kind: pb.OperationKind_OPERATION_KIND_RETAIN_DOCUMENT,
	})
	if err != nil || len(ops.GetOperations()) != 1 {
		t.Fatalf("retain_document operations: %v %v", ops, err)
	}
	extractions := 0
	for _, c := range w.emu.Calls() {
		if strings.HasPrefix(c.Op, "extract_") {
			extractions++
		}
	}
	if extractions != 1 {
		t.Fatalf("%d extraction calls, want 1", extractions)
	}
}

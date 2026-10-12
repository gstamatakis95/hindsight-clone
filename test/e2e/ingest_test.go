//go:build e2e

package e2e

import (
	"memd/internal/ingest"
	"memd/internal/ingest/kafka"
	"memd/test/e2e/harness"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "memd/gen/memory/v1"
)

// queueIngestKafka: a memd ingest process, pinned to the e2e tenant, consumes a Kafka topic. Envelopes in both
// encodings become documents with memories through the worker; an undecodable record and one for another tenant
// land in the dead-letter topic with their reasons; and a redelivered document (published twice) is extracted once.
func (e *env) queueIngestKafka(t *testing.T) {
	const bank, topic, dlq = "sc-queue", "memd-ingest", "memd-ingest-dlq"
	k := harness.StartKafka(t)
	k.CreateTopic(t, topic, 2)
	k.CreateTopic(t, dlq, 1)
	e.stack.StartMemd(t, harness.BuildMemd(t), harness.MemdOptions{
		Mode: harness.ModeIngest, Config: fastWorker,
		Env: map[string]string{
			"MEMD_INGEST_PROVIDER": "kafka", "MEMD_INGEST_TENANT": e2eTenant,
			"MEMD_INGEST_KAFKA_BROKERS": k.Brokers, "MEMD_INGEST_KAFKA_TOPIC": topic,
			"MEMD_INGEST_KAFKA_DLQ_TOPIC": dlq, "MEMD_INGEST_KAFKA_GROUP": "memd-e2e",
		},
	})
	extractsBefore := e.extractCalls()
	envelope := func(doc, content string) *pb.IngestEnvelope {
		return &pb.IngestEnvelope{
			OrgId: e.c.Org, BankId: bank, Item: &pb.RetainItem{DocumentId: doc, Content: content, Revision: proto.Int64(1)},
		}
	}
	jsonOf := func(env *pb.IngestEnvelope) []byte {
		b, err := protojson.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	k.Produce(t, topic, "q-alice", jsonOf(envelope("q-alice", aliceText)), nil)
	k.Produce(t, topic, "q-alice", jsonOf(envelope("q-alice", aliceText)), nil) // a producer retry
	binary, err := proto.Marshal(envelope("q-bob", "Bob plays chess on Sundays."))
	if err != nil {
		t.Fatal(err)
	}
	k.Produce(t, topic, "q-bob", binary, map[string]string{ingest.AttrContentType: ingest.ContentTypeProto})
	k.Produce(t, topic, "junk", []byte("not an envelope"), nil)
	other := envelope("q-other", "Carol likes tea.")
	other.TenantId = "someone-else"
	k.Produce(t, topic, "q-other", jsonOf(other), nil)

	dead := k.ReadAll(t, dlq, 2, 2*time.Minute)
	var reasons []string
	for _, r := range dead {
		for _, h := range r.Headers {
			if h.Key == kafka.HeaderReason {
				reason, _, _ := strings.Cut(string(h.Value), ":")
				reasons = append(reasons, reason)
			}
		}
	}
	slices.Sort(reasons)
	if !slices.Equal(reasons, []string{"TENANT_MISMATCH", "UNDECODABLE"}) {
		t.Fatalf("dead-letter reasons %q, want TENANT_MISMATCH and UNDECODABLE", reasons)
	}
	e.waitDocuments(t, bank, 2)
	e.idle(t, bank)
	if len(e.facts(t, bank, "q-alice")) == 0 || len(e.facts(t, bank, "q-bob")) == 0 {
		t.Fatal("queued documents have no memories")
	}
	if n := e.extractCalls() - extractsBefore; n != 2 {
		t.Fatalf("%d extractions for two documents, one published twice; want 2", n)
	}
}

// extractCalls counts the emulator's extraction calls so far.
func (e *env) extractCalls() int {
	n := 0
	for _, c := range e.emu.Calls() {
		if strings.HasPrefix(c.Op, "extract_") {
			n++
		}
	}
	return n
}

// waitDocuments polls ListDocuments until the bank has n documents.
func (e *env) waitDocuments(t *testing.T, bank string, n int) {
	t.Helper()
	ctx := e.ctx(t)
	for {
		resp, err := e.c.Documents.ListDocuments(ctx, &pb.ListDocumentsRequest{OrgId: e.c.Org, BankId: bank})
		if err == nil && len(resp.GetDocuments()) >= n {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("bank %s: %v documents, want %d (err %v)", bank, len(resp.GetDocuments()), n, err)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

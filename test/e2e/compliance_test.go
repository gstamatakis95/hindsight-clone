//go:build e2e

package e2e

import (
	"memd/internal/blob"
	"memd/internal/domain"
	"memd/internal/observability"
	"memd/test/e2e/harness"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "memd/gen/memory/v1"
)

// orgDeleteCompliance: an org with three banks is filled through the queue (a memd ingest process pinned to the
// tenant and the org, two documents per bank over Kafka), consolidated, and given a mental model; then it is
// deleted. The receipt reaches COMPLETED with a clean verification; no row carrying the org's id is left in any
// table that has one (read from the schema), no blob is left under its prefix in MinIO, and GetOrg and GetBank are
// NOT_FOUND. A message for the org published after the delete is dropped (the memd.ingest.dropped metric with
// reason ORG_DELETED), creates nothing and is not dead-lettered.
func (e *env) orgDeleteCompliance(t *testing.T) {
	const org, topic, dlq = "e2e-org-delete", "memd-org-ingest", "memd-org-ingest-dlq"
	banks := []string{"od-1", "od-2", "od-3"}
	if _, err := e.c.Orgs.CreateOrg(e.ctx(t), &pb.CreateOrgRequest{OrgId: org}); err != nil {
		t.Fatal(err)
	}
	oe := e.inOrg(org)
	k, stats := oe.startPinnedIngest(t, org, topic, dlq)
	docs := map[string]string{"alice": aliceText, "bob": "Bob plays chess on Sundays. Bob lives in Porto."}
	for _, b := range banks {
		for doc, content := range docs {
			k.Produce(t, topic, b+"/"+doc, envelopeJSON(t, b, doc, content), nil)
		}
	}
	oe.waitConsolidated(t, banks, len(docs))
	oe.buildModel(t, banks[0])

	prefix := blob.OrgPrefix(domain.OrgRef{Tenant: e2eTenant, Org: org})
	if n := e.orgRows(t, org); n == 0 {
		t.Fatal("no rows of the org before the delete")
	}
	if keys := e.blobKeys(t, prefix); len(keys) != len(banks)*len(docs) {
		t.Fatalf("%d blobs under %q before the delete, want %d", len(keys), prefix, len(banks)*len(docs))
	}
	del, err := e.c.Orgs.DeleteOrg(e.ctx(t), &pb.DeleteOrgRequest{OrgId: org})
	if err != nil || int(del.GetReceipt().GetBanks()) != len(banks) {
		t.Fatalf("DeleteOrg = %v, %v", del, err)
	}
	rc := e.waitReceipt(t, org)
	if rc.GetRowsRemaining() != 0 || rc.GetBlobsRemaining() != 0 || rc.GetTablesChecked() == 0 {
		t.Fatalf("completed receipt = %v", rc)
	}
	e.wantOrgGone(t, org, banks, prefix)

	k.Produce(t, topic, banks[0]+"/late", envelopeJSON(t, banks[0], "late", "Carol joined in March."), nil)
	stats.wait(t, observability.MetricIngestDropped, "reason:ORG_DELETED", 2*time.Minute)
	e.wantOrgGone(t, org, banks, prefix)
	if dead := k.ReadAll(t, dlq, 1, 3*time.Second); len(dead) != 0 {
		t.Fatalf("%d dead letters, want none", len(dead))
	}
	if after := e.waitReceipt(t, org); after.GetId() != rc.GetId() {
		t.Fatalf("receipt %s after the late message, want %s", after.GetId(), rc.GetId())
	}
}

// inOrg returns a copy of e whose client sends org as org_id, so the bank helpers work in that org.
func (e *env) inOrg(org string) *env {
	c := *e.c
	c.Org = org
	oe := *e
	oe.c = &c
	return &oe
}

// startPinnedIngest starts a Kafka broker with topic and its dead-letter topic, and a memd ingest process pinned to
// the e2e tenant and org that sends its metrics to the returned DogStatsD stand-in.
func (e *env) startPinnedIngest(t *testing.T, org, topic, dlq string) (*harness.Kafka, *statsd) {
	t.Helper()
	k := harness.StartKafka(t)
	k.CreateTopic(t, topic, 2)
	k.CreateTopic(t, dlq, 1)
	stats := listenStatsd(t)
	e.stack.StartMemd(t, harness.BuildMemd(t), harness.MemdOptions{
		Mode: harness.ModeIngest, Config: fastWorker,
		Env: map[string]string{
			"MEMD_INGEST_PROVIDER": "kafka", "MEMD_INGEST_TENANT": e2eTenant, "MEMD_INGEST_ORG": org,
			"MEMD_INGEST_KAFKA_BROKERS": k.Brokers, "MEMD_INGEST_KAFKA_TOPIC": topic,
			"MEMD_INGEST_KAFKA_DLQ_TOPIC": dlq, "MEMD_INGEST_KAFKA_GROUP": "memd-e2e-org",
			// Metrics only, to the stand-in: no trace agent, no runtime metrics.
			"MEMD_DATADOG_ENABLED": "true", "DD_DOGSTATSD_URL": "udp://" + stats.addr,
			"DD_TRACE_ENABLED": "false", "DD_RUNTIME_METRICS_ENABLED": "false",
		},
	})
	return k, stats
}

// envelopeJSON is the protojson IngestEnvelope of one document for bank, with no tenant or org: the pinned ones
// apply.
func envelopeJSON(t *testing.T, bank, doc, content string) []byte {
	t.Helper()
	b, err := protojson.Marshal(&pb.IngestEnvelope{
		BankId: bank, Item: &pb.RetainItem{DocumentId: doc, Content: content, Revision: proto.Int64(1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// waitConsolidated waits until each bank holds docs documents and no pending work, and checks consolidation left
// observations in each.
func (e *env) waitConsolidated(t *testing.T, banks []string, docs int) {
	t.Helper()
	for _, b := range banks {
		e.waitDocuments(t, b, docs)
		e.idle(t, b)
		if obs := e.memories(t, b, "", pb.FactType_FACT_TYPE_OBSERVATION); len(obs) == 0 {
			t.Fatalf("bank %s: no observations after consolidation", b)
		}
	}
}

// buildModel creates a mental model in bank and waits for its first build.
func (e *env) buildModel(t *testing.T, bank string) {
	t.Helper()
	created, err := e.c.MentalModels.CreateMentalModel(e.ctx(t), &pb.CreateMentalModelRequest{
		OrgId: e.c.Org, BankId: bank, Spec: &pb.MentalModelSpec{
			Id: "people", Name: "People", SourceQuery: "Who are the people we know about?",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.waitOp(t, bank, created.GetOperation().GetId(), pb.OperationStatus_OPERATION_STATUS_COMPLETED)
}

// wantOrgGone checks a deleted org left nothing: no row with its org_id, no blob under prefix, and GetOrg and
// GetBank of every bank NOT_FOUND.
func (e *env) wantOrgGone(t *testing.T, org string, banks []string, prefix string) {
	t.Helper()
	if n := e.orgRows(t, org); n != 0 {
		t.Fatalf("%d rows of the deleted org left", n)
	}
	if keys := e.blobKeys(t, prefix); len(keys) != 0 {
		t.Fatalf("blobs left under %q: %v", prefix, keys)
	}
	_, err := e.c.Orgs.GetOrg(e.ctx(t), &pb.GetOrgRequest{OrgId: org})
	wantCode(t, "GetOrg of the deleted org", err, codes.NotFound)
	for _, b := range banks {
		_, err = e.c.Banks.GetBank(e.ctx(t), &pb.GetBankRequest{OrgId: org, BankId: b})
		wantCode(t, "GetBank of a deleted org's bank", err, codes.NotFound)
	}
}

// waitReceipt polls GetDeletionReceipt for org until it is COMPLETED.
func (e *env) waitReceipt(t *testing.T, org string) *pb.DeletionReceipt {
	t.Helper()
	ctx := e.ctx(t)
	for {
		resp, err := e.c.Orgs.GetDeletionReceipt(ctx, &pb.GetDeletionReceiptRequest{OrgId: org})
		if err != nil {
			t.Fatalf("GetDeletionReceipt: %v", err)
		}
		if resp.GetReceipt().GetState() == pb.DeletionReceipt_STATE_COMPLETED {
			return resp.GetReceipt()
		}
		select {
		case <-ctx.Done():
			t.Fatalf("org delete not completed: %v\n--- worker log tail ---\n%s", resp.GetReceipt(),
				tail(e.worker.Logs(), 4000))
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// orgRows counts the rows of the e2e tenant's org in every table of the memd schema that has an org_id column.
func (e *env) orgRows(t *testing.T, org string) int64 {
	t.Helper()
	ctx := e.ctx(t)
	conn, err := pgx.Connect(ctx, e.stack.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	rows, err := conn.Query(ctx, `SELECT table_name FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name = 'org_id'`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(tables) == 0 {
		t.Fatalf("tables with org_id: %v, %v", tables, err)
	}
	var total int64
	for _, table := range tables {
		var n int64
		sql := "SELECT count(*) FROM " + pgx.Identifier{table}.Sanitize() + " WHERE tenant_id = $1 AND org_id = $2"
		if err := conn.QueryRow(ctx, sql, e2eTenant, org).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		total += n
	}
	return total
}

// statsd is a DogStatsD stand-in: it keeps every metric line memd sends to it over UDP.
type statsd struct {
	addr  string
	mu    sync.Mutex
	lines []string
}

// listenStatsd listens on a free local UDP port until the test ends.
func listenStatsd(t *testing.T) *statsd {
	t.Helper()
	pc, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	s := &statsd{addr: pc.LocalAddr().String()}
	go func() {
		buf := make([]byte, 64<<10)
		for {
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			s.mu.Lock()
			s.lines = append(s.lines, strings.Split(string(buf[:n]), "\n")...)
			s.mu.Unlock()
		}
	}()
	return s
}

// wait polls until a line of metric (without the memd. namespace) carries tag, and fails after within.
func (s *statsd) wait(t *testing.T, metric, tag string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !s.has(observability.MetricNamespace+metric+":", tag) {
		if time.Now().After(deadline) {
			s.mu.Lock()
			defer s.mu.Unlock()
			t.Fatalf("no %s%s with %s within %s; received %q", observability.MetricNamespace, metric, tag, within,
				s.lines)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// has reports whether a received line starts with prefix and carries tag.
func (s *statsd) has(prefix, tag string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.lines {
		if strings.HasPrefix(l, prefix) && strings.Contains(l, tag) {
			return true
		}
	}
	return false
}

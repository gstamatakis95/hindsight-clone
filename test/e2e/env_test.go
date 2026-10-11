//go:build e2e

package e2e

import (
	"context"
	"memd/internal/blob/s3"
	"memd/test/e2e/gatewayemu"
	"memd/test/e2e/harness"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "memd/gen/memory/v1"
)

// Fast worker timings for the scenarios: a crashed worker's job is reclaimed within seconds instead of the
// production 2 minutes (one value feeds the runner and the reclaim, as in production).
var fastWorker = map[string]any{"worker": map[string]any{
	"poll_interval": "100ms", "heartbeat_interval": "1s", "stale_after": "6s", "maintenance_interval": "2s",
}}

// env is one running memd (api and worker as separate processes) over one compose stack and the emulator.
type env struct {
	stack  *harness.Stack
	emu    *gatewayemu.Server
	api    *harness.Proc
	worker *harness.Proc
	c      *harness.Client
}

// startEnv brings up the stack, the emulator, a memd api and a memd worker.
func startEnv(t *testing.T) *env {
	t.Helper()
	bin := harness.BuildMemd(t)
	s := harness.StartStack(t)
	emu, gw := harness.StartEmulator(t, gatewayemu.Options{})
	opts := harness.MemdOptions{GatewayURL: gw.URL + gatewayemu.BasePath, Config: fastWorker, Mode: harness.ModeAPI}
	api := s.StartMemd(t, bin, opts)
	opts.Mode = harness.ModeWorker
	w := s.StartMemd(t, bin, opts)
	return &env{stack: s, emu: emu, api: api, worker: w, c: harness.Dial(t, api.Addr, e2eTenant)}
}

// ctx is a per-step context.
func (e *env) ctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// idle waits until the bank has no pending or processing operation.
func (e *env) idle(t *testing.T, bank string) {
	t.Helper()
	if err := e.c.WaitIdle(e.ctx(t), bank, 200*time.Millisecond); err != nil {
		t.Fatalf("%v\n--- worker log tail ---\n%s", err, tail(e.worker.Logs(), 4000))
	}
}

// tail returns the last n bytes of s.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// waitOp polls an operation until its status is one of want and returns it.
func (e *env) waitOp(t *testing.T, bank, id string, want ...pb.OperationStatus) *pb.Operation {
	t.Helper()
	ctx := e.ctx(t)
	var last *pb.Operation
	for {
		resp, err := e.c.Operations.GetOperation(ctx, &pb.GetOperationRequest{BankId: bank, OperationId: id})
		if err != nil {
			t.Fatalf("get operation %s: %v", id, err)
		}
		last = resp.GetOperation()
		if slices.Contains(want, last.GetStatus()) {
			return last
		}
		select {
		case <-ctx.Done():
			t.Fatalf("operation %s is %s (%s), want %v\n--- worker log tail ---\n%s", id, last.GetStatus(),
				last.GetError(), want, tail(e.worker.Logs(), 4000))
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// retain retains one document synchronously and returns its result.
func (e *env) retain(t *testing.T, bank, doc, content string, tags ...string) *pb.RetainedDocument {
	t.Helper()
	resp, err := e.c.Memory.Retain(e.ctx(t), &pb.RetainRequest{BankId: bank, Items: []*pb.RetainItem{
		{Content: content, DocumentId: doc, Tags: tags},
	}})
	if err != nil {
		t.Fatalf("retain %s: %v", doc, err)
	}
	if len(resp.GetDocuments()) != 1 {
		t.Fatalf("retain %s: %d documents", doc, len(resp.GetDocuments()))
	}
	return resp.GetDocuments()[0]
}

// memories lists the bank's live memories of the given types (none = all), optionally of one document.
func (e *env) memories(t *testing.T, bank, doc string, types ...pb.FactType) []*pb.Memory {
	t.Helper()
	resp, err := e.c.Units.ListMemories(e.ctx(t), &pb.ListMemoriesRequest{
		BankId: bank, Types: types, DocumentId: doc, State: pb.MemoryState_MEMORY_STATE_VALID, PageSize: 500,
	})
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	return resp.GetItems()
}

// facts lists the bank's live world and experience facts.
func (e *env) facts(t *testing.T, bank, doc string) []*pb.Memory {
	t.Helper()
	return e.memories(t, bank, doc, pb.FactType_FACT_TYPE_WORLD, pb.FactType_FACT_TYPE_EXPERIENCE)
}

// recall runs a recall and returns its result texts by id.
func (e *env) recall(t *testing.T, bank, query string) map[string]string {
	t.Helper()
	resp, err := e.c.Memory.Recall(e.ctx(t), &pb.RecallRequest{BankId: bank, Query: query, MaxTokens: 4096})
	if err != nil {
		t.Fatalf("recall %q: %v", query, err)
	}
	out := map[string]string{}
	for _, m := range resp.GetResults() {
		out[m.GetId()] = m.GetText()
	}
	return out
}

// anyContains reports whether any text contains sub.
func anyContains(texts map[string]string, sub string) bool {
	for _, s := range texts {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// e2eTenant is the tenant every scenario's client sends.
const e2eTenant = "e2e"

// blobKeys lists the MinIO keys under prefix (up to 1000), straight from the bucket memd writes to.
func (e *env) blobKeys(t *testing.T, prefix string) []string {
	t.Helper()
	blob, err := s3.New(e.ctx(t), s3.Config{
		Endpoint: e.stack.S3Endpoint, Region: harness.S3Region, Bucket: harness.S3Bucket,
		AccessKeyID: harness.S3AccessKey, SecretAccessKey: harness.S3SecretKey, UsePathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := blob.List(e.ctx(t), prefix, "", 1000)
	if err != nil {
		t.Fatalf("list %q: %v", prefix, err)
	}
	return keys
}

// codeOf is err's gRPC code (OK for nil).
func codeOf(err error) codes.Code { return status.Code(err) }

// wantCode fails unless err has code.
func wantCode(t *testing.T, what string, err error, code codes.Code) {
	t.Helper()
	if status.Code(err) != code {
		t.Fatalf("%s: %v, want %s", what, err, code)
	}
}

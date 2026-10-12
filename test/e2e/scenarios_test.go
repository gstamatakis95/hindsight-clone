//go:build e2e

package e2e

import (
	"memd/internal/blob"
	"memd/internal/domain"
	"memd/test/e2e/gatewayemu"
	"memd/test/e2e/harness"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	pb "memd/gen/memory/v1"
)

// The scenarios run against one stack (real Postgres + MinIO), the emulated gateway and memd as separate api
// and worker processes. Each scenario uses its own bank; they run one after another because gateway faults
// are global to the emulator.

const (
	aliceDoc  = "doc-alice"
	aliceText = "Alice moved to Lisbon in 2021. Alice works as a nurse at the city hospital. " +
		"Alice drinks a cortado every morning."
)

// TestScenarios is the end-to-end suite: one scenario per subtest, against one stack.
func TestScenarios(t *testing.T) {
	e := startEnv(t)
	t.Run("RetainSync", e.retainSync)
	t.Run("RetainAsync", e.retainAsync)
	t.Run("ConsolidationRecallReflect", e.consolidationRecallReflect)
	t.Run("MentalModelBothStrategies", e.mentalModels)
	t.Run("ConsolidationTriggersRefresh", e.consolidationTriggersRefresh)
	t.Run("InvalidateRestore", e.invalidateRestore)
	t.Run("RetagReprocess", e.retagReprocess)
	t.Run("CancelRetry", e.cancelRetry)
	t.Run("WorkerCrashReclaim", e.crashReclaim)
	t.Run("BankDelete", e.bankDelete)
}

// retainSync: a sync retain returns once units commit, reports EXTRACT usage, and an identical retain is a
// no-op.
func (e *env) retainSync(t *testing.T) {
	const bank = "sc-retain-sync"
	doc := e.retain(t, bank, aliceDoc, aliceText)
	if doc.GetMemoriesCreated() < 1 || doc.GetUnchanged() {
		t.Fatalf("retain: %+v", doc)
	}
	if got := len(e.facts(t, bank, aliceDoc)); got != int(doc.GetMemoriesCreated()) {
		t.Fatalf("listed %d facts right after the sync retain, want %d", got, doc.GetMemoriesCreated())
	}
	again := e.retain(t, bank, aliceDoc, aliceText)
	if !again.GetUnchanged() {
		t.Fatalf("identical retain re-extracted: %+v", again)
	}
	resp, err := e.c.Memory.Retain(e.ctx(t), &pb.RetainRequest{OrgId: e.c.Org, BankId: bank, Items: []*pb.RetainItem{
		{Content: "Bob plays chess on Sundays.", DocumentId: "doc-bob"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetUsage()) == 0 {
		t.Fatal("sync retain reported no usage")
	}
	e.idle(t, bank)
}

// retainAsync: run_async returns the enqueued operations (with their bank) under a retain_batch parent; the
// barrier drains, every child completes and the units are there.
func (e *env) retainAsync(t *testing.T) {
	const bank = "sc-retain-async"
	resp, err := e.c.Memory.Retain(e.ctx(t), &pb.RetainRequest{OrgId: e.c.Org, BankId: bank, RunAsync: true, Items: []*pb.RetainItem{
		{Content: aliceText, DocumentId: aliceDoc},
		{Content: "Bob plays chess on Sundays. Bob lives in Porto.", DocumentId: "doc-bob"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	parent := resp.GetBatchOperation()
	if parent.GetId() == "" || parent.GetBankId() != bank ||
		parent.GetKind() != pb.OperationKind_OPERATION_KIND_RETAIN_BATCH || len(resp.GetDocuments()) != 2 {
		t.Fatalf("async retain: %+v", resp)
	}
	e.idle(t, bank)
	for _, d := range resp.GetDocuments() {
		op := d.GetOperation()
		if op.GetId() == "" || op.GetParentId() != parent.GetId() ||
			op.GetKind() != pb.OperationKind_OPERATION_KIND_RETAIN_DOCUMENT {
			t.Fatalf("document %s operation: %+v", d.GetDocumentId(), op)
		}
		e.waitOp(t, bank, op.GetId(), pb.OperationStatus_OPERATION_STATUS_COMPLETED)
	}
	e.waitOp(t, bank, parent.GetId(), pb.OperationStatus_OPERATION_STATUS_COMPLETED)
	if len(e.facts(t, bank, "")) < 4 {
		t.Fatalf("async retain stored %d facts", len(e.facts(t, bank, "")))
	}
}

// consolidationRecallReflect: automatic consolidation creates observations; recall finds the fact; reflect
// answers from retrieved evidence.
func (e *env) consolidationRecallReflect(t *testing.T) {
	const bank = "sc-recall"
	e.retain(t, bank, aliceDoc, aliceText)
	e.idle(t, bank)
	if obs := e.memories(t, bank, "", pb.FactType_FACT_TYPE_OBSERVATION); len(obs) == 0 {
		t.Fatal("no observations after consolidation")
	}
	stats, err := e.c.Banks.GetBankStats(e.ctx(t), &pb.GetBankStatsRequest{OrgId: e.c.Org, BankId: bank})
	if err != nil || stats.GetStats().GetUnconsolidatedMemories() != 0 {
		t.Fatalf("stats after consolidation: %+v %v", stats.GetStats(), err)
	}
	if got := e.recall(t, bank, "Where does Alice live? Lisbon"); !anyContains(got, "Lisbon") {
		t.Fatalf("recall missed Lisbon: %v", got)
	}
	resp, err := e.c.Memory.Reflect(e.ctx(t), &pb.ReflectRequest{
		OrgId: e.c.Org, BankId: bank, Query: "Where does Alice live?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(resp.GetText()) == "" || len(resp.GetBasedOn().GetMemories()) == 0 {
		t.Fatalf("reflect: %q based on %d memories", resp.GetText(), len(resp.GetBasedOn().GetMemories()))
	}
	_, err = e.c.Memory.Recall(e.ctx(t), &pb.RecallRequest{OrgId: e.c.Org, BankId: "sc-never-written", Query: "x"})
	wantCode(t, "recall of an unknown bank", err, codes.NotFound)
	_, err = e.c.Memory.Retain(e.ctx(t), &pb.RetainRequest{OrgId: "sc-no-such-org", BankId: bank,
		Items: []*pb.RetainItem{{Content: "x"}}})
	wantCode(t, "retain under an org that was never created", err, codes.NotFound)
}

// mentalModels: a model's first build runs agentic; a manual single-shot refresh writes the next version.
func (e *env) mentalModels(t *testing.T) {
	const bank = "sc-mm"
	e.retain(t, bank, aliceDoc, aliceText)
	e.idle(t, bank)
	created, err := e.c.MentalModels.CreateMentalModel(e.ctx(t), &pb.CreateMentalModelRequest{
		OrgId: e.c.Org, BankId: bank, Spec: &pb.MentalModelSpec{
			Id: "alice", Name: "Alice", SourceQuery: "What do we know about Alice?",
			Strategy: pb.RefreshStrategy_REFRESH_STRATEGY_AGENTIC,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.waitOp(t, bank, created.GetOperation().GetId(), pb.OperationStatus_OPERATION_STATUS_COMPLETED)
	first := e.model(t, bank, "alice")
	if first.GetVersion() < 1 || strings.TrimSpace(first.GetContentMd()) == "" {
		t.Fatalf("agentic build: version %d, content %q", first.GetVersion(), first.GetContentMd())
	}
	ref, err := e.c.MentalModels.RefreshMentalModel(e.ctx(t), &pb.RefreshMentalModelRequest{
		OrgId: e.c.Org, BankId: bank, MentalModelId: "alice", ForceFull: true,
		Strategy: pb.RefreshStrategy_REFRESH_STRATEGY_SINGLE_SHOT,
	})
	if err != nil {
		t.Fatal(err)
	}
	op := e.waitOp(t, bank, ref.GetOperation().GetId(), pb.OperationStatus_OPERATION_STATUS_COMPLETED)
	second := e.model(t, bank, "alice")
	if second.GetVersion() <= first.GetVersion() || strings.TrimSpace(second.GetContentMd()) == "" {
		t.Fatalf("single-shot refresh: version %d -> %d, summary %s", first.GetVersion(), second.GetVersion(),
			op.GetDebugSummary())
	}
	if !strings.Contains(op.GetDebugSummary(), "single_shot") {
		t.Fatalf("refresh debug summary %s, want strategy single_shot", op.GetDebugSummary())
	}
}

// consolidationTriggersRefresh: a model scheduled after_consolidation is refreshed
// by the consolidation a later retain causes. The build completes first, so the triggered refresh is its
// own row (not joined to the build's payload). The version advances, no refresh fails and, after a
// maintenance tick has reconciled failures, the model is not paused.
func (e *env) consolidationTriggersRefresh(t *testing.T) {
	const bank = "sc-mm-after-consolidation"
	debounce := int32(1)
	_, err := e.c.Banks.CreateBank(e.ctx(t), &pb.CreateBankRequest{OrgId: e.c.Org, BankId: bank, Initial: &pb.CreateBankRequest_Config{
		Config: &pb.BankConfig{MentalModels: &pb.MentalModelConfig{RefreshDebounceSeconds: &debounce}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.retain(t, bank, aliceDoc, aliceText)
	e.idle(t, bank)
	created, err := e.c.MentalModels.CreateMentalModel(e.ctx(t), &pb.CreateMentalModelRequest{
		OrgId: e.c.Org, BankId: bank, Spec: &pb.MentalModelSpec{
			Id: "alice", Name: "Alice", SourceQuery: "What do we know about Alice?",
			Strategy: pb.RefreshStrategy_REFRESH_STRATEGY_SINGLE_SHOT,
			Schedule: &pb.RefreshSchedule{When: &pb.RefreshSchedule_AfterConsolidation{AfterConsolidation: true}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.waitOp(t, bank, created.GetOperation().GetId(), pb.OperationStatus_OPERATION_STATUS_COMPLETED)
	built := e.model(t, bank, "alice").GetVersion()
	e.retain(t, bank, "doc-alice-2", "Alice adopted a cat named Miso. Alice plays the violin on weekends.")
	e.idle(t, bank)
	if failed := e.refreshOps(t, bank, pb.OperationStatus_OPERATION_STATUS_FAILED); len(failed) != 0 {
		t.Fatalf("%d refreshes failed, first: %s", len(failed), failed[0].GetError())
	}
	done := e.refreshOps(t, bank, pb.OperationStatus_OPERATION_STATUS_COMPLETED)
	if len(done) < 2 {
		t.Fatalf("%d completed refreshes, want the build and the one consolidation triggered", len(done))
	}
	if v := e.model(t, bank, "alice").GetVersion(); v <= built {
		t.Fatalf("version %d after the triggered refresh, want above the build's %d; refreshes: %v", v, built, done)
	}
	time.Sleep(3 * time.Second) // one maintenance interval (2 s), so a failure would have been reconciled
	if m := e.model(t, bank, "alice"); m.GetAutoRefreshPaused() {
		t.Fatalf("model paused after a consolidation-triggered refresh: %+v", m)
	}
}

// refreshOps lists the bank's refresh_mental_model operations in status.
func (e *env) refreshOps(t *testing.T, bank string, status pb.OperationStatus) []*pb.Operation {
	t.Helper()
	resp, err := e.c.Operations.ListOperations(e.ctx(t), &pb.ListOperationsRequest{
		OrgId: e.c.Org, BankId: bank, Status: status, Kind: pb.OperationKind_OPERATION_KIND_REFRESH_MENTAL_MODEL, PageSize: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetOperations()
}

// model reads a mental model at full detail.
func (e *env) model(t *testing.T, bank, id string) *pb.MentalModel {
	t.Helper()
	resp, err := e.c.MentalModels.GetMentalModel(e.ctx(t), &pb.GetMentalModelRequest{OrgId: e.c.Org, BankId: bank, MentalModelId: id})
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetMentalModel()
}

// invalidateRestore: invalidate archives a fact (gone from recall and the live listing) and restore brings it
// back through a rederive.
func (e *env) invalidateRestore(t *testing.T) {
	const bank = "sc-curate"
	e.retain(t, bank, aliceDoc, aliceText)
	e.idle(t, bank)
	var target *pb.Memory
	for _, m := range e.facts(t, bank, aliceDoc) {
		if strings.Contains(m.GetText(), "Lisbon") {
			target = m
		}
	}
	if target == nil {
		t.Fatal("no Lisbon fact")
	}
	seq := e.changeSeq(t, bank)
	inv, err := e.c.Memory.InvalidateMemory(e.ctx(t), &pb.InvalidateMemoryRequest{
		OrgId: e.c.Org, BankId: bank, MemoryId: target.GetId(), Reason: "wrong",
	})
	if err != nil {
		t.Fatal(err)
	}
	if inv.GetMemory().GetState() != pb.MemoryState_MEMORY_STATE_INVALIDATED {
		t.Fatalf("invalidate: %+v", inv)
	}
	if _, ok := e.recall(t, bank, "Alice Lisbon")[target.GetId()]; ok {
		t.Fatal("recall returned an invalidated fact")
	}
	seq = e.wantSeqAfter(t, bank, seq, "invalidate")
	e.idle(t, bank)
	res, err := e.c.Memory.RestoreMemory(e.ctx(t), &pb.RestoreMemoryRequest{
		OrgId: e.c.Org, BankId: bank, MemoryId: target.GetId(), Reason: "right after all",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetOperation().GetId() == "" {
		t.Fatal("restore queued no rederive")
	}
	e.waitOp(t, bank, res.GetOperation().GetId(), pb.OperationStatus_OPERATION_STATUS_COMPLETED)
	e.idle(t, bank)
	if _, ok := e.recall(t, bank, "Alice Lisbon")[target.GetId()]; !ok {
		t.Fatal("restored fact not recalled")
	}
	seq = e.wantSeqAfter(t, bank, seq, "restore")
	e.editFact(t, bank, target.GetId(), seq)
}

// editFact edits a fact's text: the edit commits in the RPC (new text, change seq bumped), the rederive job
// re-embeds it, and recall then finds it by its new text.
func (e *env) editFact(t *testing.T, bank, id string, seq int64) {
	t.Helper()
	text := "Alice moved to Porto in 2023."
	ed, err := e.c.Memory.UpdateMemory(e.ctx(t), &pb.UpdateMemoryRequest{
		OrgId: e.c.Org, BankId: bank, MemoryId: id, Memory: &pb.MemoryEdit{Text: text}, Reason: "she moved again",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"text"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ed.GetMemory().GetText() != text || ed.GetOperation().GetId() == "" || ed.GetMemory().GetEditedAt() == nil {
		t.Fatalf("edit: %+v", ed)
	}
	e.wantSeqAfter(t, bank, seq, "edit")
	e.waitOp(t, bank, ed.GetOperation().GetId(), pb.OperationStatus_OPERATION_STATUS_COMPLETED)
	e.idle(t, bank)
	if got := e.recall(t, bank, "Alice Porto 2023"); got[id] != text {
		t.Fatalf("edited fact not recalled with its new text: %v", got)
	}
}

// changeSeq reads the bank's change_seq.
func (e *env) changeSeq(t *testing.T, bank string) int64 {
	t.Helper()
	resp, err := e.c.Banks.GetBankStats(e.ctx(t), &pb.GetBankStatsRequest{OrgId: e.c.Org, BankId: bank})
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetStats().GetChangeSeq()
}

// wantSeqAfter fails unless the bank's change_seq moved past before (every curation calls tx.Seq).
func (e *env) wantSeqAfter(t *testing.T, bank string, before int64, what string) int64 {
	t.Helper()
	now := e.changeSeq(t, bank)
	if now <= before {
		t.Fatalf("%s left change_seq at %d (was %d)", what, now, before)
	}
	return now
}

// retagReprocess: RetagDocument rewrites the memories' tags; ReprocessDocument re-extracts the document.
func (e *env) retagReprocess(t *testing.T) {
	const bank = "sc-docs"
	e.retain(t, bank, aliceDoc, aliceText)
	e.idle(t, bank)
	up, err := e.c.Documents.RetagDocument(e.ctx(t), &pb.RetagDocumentRequest{
		OrgId: e.c.Org, BankId: bank, DocumentId: aliceDoc, Tags: &pb.TagSet{Tags: []string{"people"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.waitOp(t, bank, up.GetOperation().GetId(), pb.OperationStatus_OPERATION_STATUS_COMPLETED)
	for _, m := range e.facts(t, bank, aliceDoc) {
		if len(m.GetTags()) != 1 || m.GetTags()[0] != "people" {
			t.Fatalf("unit %s tags %v after retag", m.GetId(), m.GetTags())
		}
	}
	e.idle(t, bank)
	before := e.facts(t, bank, aliceDoc)
	rp, err := e.c.Documents.ReprocessDocument(e.ctx(t), &pb.ReprocessDocumentRequest{OrgId: e.c.Org, BankId: bank, DocumentId: aliceDoc})
	if err != nil {
		t.Fatal(err)
	}
	e.waitOp(t, bank, rp.GetOperation().GetId(), pb.OperationStatus_OPERATION_STATUS_COMPLETED)
	e.idle(t, bank)
	after := e.facts(t, bank, aliceDoc)
	if len(after) != len(before) || after[0].GetId() == before[0].GetId() && len(before) == 1 {
		t.Fatalf("reprocess: %d facts before, %d after", len(before), len(after))
	}
	doc, err := e.c.Documents.GetDocument(e.ctx(t), &pb.GetDocumentRequest{
		OrgId: e.c.Org, BankId: bank, DocumentId: aliceDoc, IncludeText: true,
	})
	if err != nil || doc.GetDocument().GetExtractedAt() == nil || doc.GetDocument().GetOriginalText() != aliceText {
		t.Fatalf("document after reprocess: %+v %v", doc.GetDocument(), err)
	}
}

// cancelRetry: a running retain is cancelled at its checkpoint (the delayed extraction is abandoned), and
// RetryOperation re-queues the cancelled row, which then completes.
func (e *env) cancelRetry(t *testing.T) {
	const bank = "sc-cancel"
	e.emu.Inject(gatewayemu.Fault{Op: "extract_concise", Delay: time.Minute, Times: 1})
	defer e.emu.ClearFaults()
	id := e.retainAsync1(t, bank, aliceDoc, aliceText)
	e.waitOp(t, bank, id, pb.OperationStatus_OPERATION_STATUS_PROCESSING)
	if _, err := e.c.Operations.CancelOperation(e.ctx(t), &pb.CancelOperationRequest{
		OrgId: e.c.Org, BankId: bank, OperationId: id,
	}); err != nil {
		t.Fatal(err)
	}
	e.waitOp(t, bank, id, pb.OperationStatus_OPERATION_STATUS_CANCELLED)
	if _, err := e.c.Operations.RetryOperation(e.ctx(t), &pb.RetryOperationRequest{
		OrgId: e.c.Org, BankId: bank, OperationId: id,
	}); err != nil {
		t.Fatal(err)
	}
	e.waitOp(t, bank, id, pb.OperationStatus_OPERATION_STATUS_COMPLETED)
	if len(e.facts(t, bank, aliceDoc)) == 0 {
		t.Fatal("retried retain stored nothing")
	}
}

// retainAsync1 retains one document asynchronously and returns its operation id.
func (e *env) retainAsync1(t *testing.T, bank, doc, content string) string {
	t.Helper()
	resp, err := e.c.Memory.Retain(e.ctx(t), &pb.RetainRequest{OrgId: e.c.Org, BankId: bank, RunAsync: true, Items: []*pb.RetainItem{
		{Content: content, DocumentId: doc},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetDocuments()[0].GetOperation().GetId()
}

// crashReclaim: a worker killed mid-job leaves the row PROCESSING; once its heartbeat is stale the
// maintenance tick of the restarted worker returns it to PENDING and the job completes on its second
// attempt.
func (e *env) crashReclaim(t *testing.T) {
	const bank = "sc-crash"
	e.emu.Inject(gatewayemu.Fault{Op: "extract_concise", Delay: 5 * time.Minute, Times: 1})
	defer e.emu.ClearFaults()
	id := e.retainAsync1(t, bank, aliceDoc, aliceText)
	e.waitOp(t, bank, id, pb.OperationStatus_OPERATION_STATUS_PROCESSING)
	if err := e.worker.Kill(); err != nil {
		t.Fatal(err)
	}
	e.worker.Restart()
	op := e.waitOp(t, bank, id, pb.OperationStatus_OPERATION_STATUS_COMPLETED)
	if op.GetAttempts() != 1 {
		t.Fatalf("reclaimed job attempts = %d, want 1", op.GetAttempts())
	}
	if len(e.facts(t, bank, aliceDoc)) == 0 {
		t.Fatal("reclaimed retain stored nothing")
	}
	e.idle(t, bank)
}

// bankDelete: DeleteBank tombstones at once (GetBank reads NOT_FOUND, writes FAILED_PRECONDITION) while
// GetOperation keeps answering for the returned delete_bank operation; the delete_bank job removes everything,
// including the bank's blob prefix in MinIO, after which GetOperation of that operation is NOT_FOUND (the client's
// completion signal), and a later write creates a fresh, empty bank. The worker is stopped across the DeleteBank
// call, so the delete is still pending when the write arrives and FAILED_PRECONDITION is the only acceptable answer.
func (e *env) bankDelete(t *testing.T) {
	const bank = "sc-delete"
	e.retain(t, bank, aliceDoc, aliceText)
	e.idle(t, bank)
	prefix := blob.BankPrefix(domain.NewBankRef(e2eTenant, harness.Org, bank))
	if keys := e.blobKeys(t, prefix); len(keys) == 0 {
		t.Fatalf("no blob under %q after a retain", prefix)
	}
	if err := e.worker.Stop(); err != nil {
		t.Fatal(err)
	}
	del, err := e.c.Banks.DeleteBank(e.ctx(t), &pb.DeleteBankRequest{OrgId: e.c.Org, BankId: bank})
	if err != nil {
		t.Fatal(err)
	}
	opID := del.GetOperation().GetId()
	_, err = e.c.Memory.Retain(e.ctx(t), &pb.RetainRequest{OrgId: e.c.Org, BankId: bank, Items: []*pb.RetainItem{{Content: "x"}}})
	wantCode(t, "write to a deleting bank", err, codes.FailedPrecondition)
	_, err = e.c.Banks.GetBank(e.ctx(t), &pb.GetBankRequest{OrgId: e.c.Org, BankId: bank})
	wantCode(t, "GetBank of a deleting bank", err, codes.NotFound)
	op, err := e.c.Operations.GetOperation(e.ctx(t), &pb.GetOperationRequest{OrgId: e.c.Org, BankId: bank, OperationId: opID})
	if err != nil || op.GetOperation().GetStatus() != pb.OperationStatus_OPERATION_STATUS_PENDING {
		t.Fatalf("delete_bank operation while the worker is stopped: %v %v", op, err)
	}
	e.worker.Restart()
	ctx := e.ctx(t)
	for {
		_, err := e.c.Operations.GetOperation(ctx, &pb.GetOperationRequest{OrgId: e.c.Org, BankId: bank, OperationId: opID})
		if codeOf(err) == codes.NotFound {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("delete not done: %v\n%s", err, tail(e.worker.Logs(), 4000))
		}
		time.Sleep(200 * time.Millisecond)
	}
	if keys := e.blobKeys(t, prefix); len(keys) != 0 {
		t.Fatalf("blob prefix %q not empty after the delete: %v", prefix, keys)
	}
	e.retain(t, bank, "doc-new", "Carol paints murals.")
	if got := e.facts(t, bank, aliceDoc); len(got) != 0 {
		t.Fatalf("recreated bank still has %d of the deleted bank's facts", len(got))
	}
	_, err = e.c.Documents.GetDocument(e.ctx(t), &pb.GetDocumentRequest{OrgId: e.c.Org, BankId: bank, DocumentId: aliceDoc})
	wantCode(t, "deleted document", err, codes.NotFound)
	e.idle(t, bank)
}

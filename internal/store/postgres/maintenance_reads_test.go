package postgres_test

import (
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"strings"
	"testing"
	"time"
)

// Extra banks of the maintenance subtests. Factories hand out empty databases, so fixed names never collide.
var (
	// mtBankC is a tenant-1 bank that the subtests tombstone, so the cross-bank reads must skip it.
	mtBankC = domain.BankRef{Tenant: "tenant-1", Bank: "bank-c"}
	// mtBankD is a tenant-1 bank whose only unconsolidated fact has no embedding.
	mtBankD = domain.BankRef{Tenant: "tenant-1", Bank: "bank-d"}
	// mtBankE is a tenant-1 bank that holds an observation only.
	mtBankE = domain.BankRef{Tenant: "tenant-1", Bank: "bank-e"}
	// mtBankF is a tenant-1 bank whose only unconsolidated fact is archived.
	mtBankF = domain.BankRef{Tenant: "tenant-1", Bank: "bank-f"}
)

// mtCron is the refresh_cron of the cron models.
const mtCron = "0 * * * *"

// TestMaintenanceReads covers the cross-bank maintenance reads CronModels, FailedRefreshes,
// UnconsolidatedBanks and QueueStats, plus UnitRepo.Unconsolidated's embedding filter (decision 1500).
// Refresh operations reach terminal states through the Claimer.
func TestMaintenanceReads(t *testing.T) {
	t.Parallel()
	runQueueCases(t, []queueCase{
		{"cron_models", testMtCronModels},
		{"failed_refreshes", testMtFailedRefreshes},
		{"unconsolidated_banks", testMtUnconsolidatedBanks},
		{"unconsolidated_skips_unembedded", testMtUnconsolidatedSkipsUnembedded},
		{"cross_bank_tenants", testMtCrossBankTenants},
		{"queue_stats", testMtQueueStats},
	})
}

// ---------- fixtures ----------

// mtCreateModel creates mental model id in bank b with the given refresh_cron ("" = no schedule).
func mtCreateModel(t *testing.T, s store.DB, b domain.BankRef, id domain.MentalModelID, cron string) {
	t.Helper()
	m := sampleModel(b, id)
	m.Spec.Trigger.RefreshCron = cron
	m.Spec.Trigger.RefreshAfterConsolidation = cron == ""
	inTx(t, s, func(tx store.Tx) error { return tx.MentalModels().Create(t.Context(), m) })
}

// mtDeleteModel deletes mental model id of bank b.
func mtDeleteModel(t *testing.T, s store.DB, b domain.BankRef, id domain.MentalModelID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error { return tx.MentalModels().Delete(t.Context(), b, id) })
}

// mtTombstone marks bank b deleting.
func mtTombstone(t *testing.T, s store.DB, b domain.BankRef) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error { return tx.Banks().MarkDeleting(t.Context(), b, day(1)) })
}

// mtRefresh enqueues a refresh_mental_model operation for model m of bank b (serial key "mm:"+m, decision
// 1500) due at runAt (zero = now). It must insert a new row: callers enqueue only once the model's earlier
// refreshes are terminal.
func (q queueEnv) mtRefresh(
	t *testing.T, b domain.BankRef, m domain.MentalModelID, runAt time.Time,
) domain.OperationID {
	t.Helper()
	j := job(b, domain.OperationKindRefreshMentalModel, "mm:"+string(m), fmt.Sprintf(`{"model_id":%q}`, m))
	j.Dedupe, j.RunAt = store.DedupePending, runAt
	return q.add(t, j)
}

// mtPending enqueues a refresh of model m of bank b parked an hour ahead, so it stays PENDING and no later
// claim takes it.
func (q queueEnv) mtPending(t *testing.T, b domain.BankRef, m domain.MentalModelID) domain.OperationID {
	t.Helper()
	return q.mtRefresh(t, b, m, future())
}

// mtCompleted enqueues, claims and completes a refresh of model m of bank b.
func (q queueEnv) mtCompleted(t *testing.T, b domain.BankRef, m domain.MentalModelID) domain.OperationID {
	t.Helper()
	id := q.mtRefresh(t, b, m, time.Time{})
	q.claimOne(t, id)
	q.complete(t, id)
	return id
}

// mtFailed enqueues and claims a refresh of model m of bank b, then fails it for good (Fail with a nil
// retryAt writes FAILED; the runner owns the attempt cap, decision 012 R10') with cause msg.
func (q queueEnv) mtFailed(t *testing.T, b domain.BankRef, m domain.MentalModelID, msg string) domain.OperationID {
	t.Helper()
	id := q.mtRefresh(t, b, m, time.Time{})
	q.claimOne(t, id)
	if err := q.c.Fail(t.Context(), id, errors.New(msg), nil); err != nil {
		t.Fatalf("Fail(%s): %v", id, err)
	}
	return id
}

// mtCancelled enqueues a parked refresh of model m of bank b and cancels it while PENDING (CANCELLED at
// once, with finished_at set).
func (q queueEnv) mtCancelled(t *testing.T, b domain.BankRef, m domain.MentalModelID) domain.OperationID {
	t.Helper()
	id := q.mtPending(t, b, m)
	if err := q.queueTx(t, func(qu store.Queue) error {
		_, err := qu.Cancel(t.Context(), b, id)
		return err
	}); err != nil {
		t.Fatalf("Cancel(%s): %v", id, err)
	}
	return id
}

// mtEmbedded builds a world fact of bank b with an embedding, so consolidation may pick it.
func mtEmbedded(b domain.BankRef, text string) domain.Unit {
	u := fact(b, text)
	u.Embedding = angleVec(0)
	return u
}

// mtMark stamps consolidated as consolidated and parked as consolidation-failed, in one transaction.
func mtMark(t *testing.T, s store.DB, consolidated, parked []domain.UnitID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		if err := tx.Units().MarkConsolidated(t.Context(), consolidated, day(10)); err != nil {
			return err
		}
		return tx.Units().MarkConsolidationFailed(t.Context(), parked, day(10))
	})
}

// mtArchive archives unit id of bank b in a transaction that takes b's Seq.
func mtArchive(t *testing.T, s store.DB, b domain.BankRef, id domain.UnitID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Seq(t.Context(), b); err != nil {
			return err
		}
		return tx.Units().Archive(t.Context(), b, id, "wrong fact")
	})
}

// mtUpdate replaces u's curated fields and embedding with UnitRepo.Update, stamped with a new Seq.
func mtUpdate(t *testing.T, s store.DB, u domain.Unit) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		var err error
		if u.Seq, err = tx.Seq(t.Context(), u.Bank); err != nil {
			return err
		}
		return tx.Units().Update(t.Context(), u)
	})
}

// ---------- reads ----------

// mtMaintenance returns s as the store.Maintenance it implements.
func mtMaintenance(t *testing.T, s store.DB) store.Maintenance {
	t.Helper()
	m, ok := s.(store.Maintenance)
	if !ok {
		t.Fatalf("store is %T, want a store.Maintenance", s)
	}
	return m
}

// mtCronModels reads CronModels and fails the test on error.
func mtCronModels(t *testing.T, s store.DB) []store.CronModel {
	t.Helper()
	ms, err := mtMaintenance(t, s).CronModels(t.Context())
	if err != nil {
		t.Fatalf("CronModels: %v", err)
	}
	return ms
}

// mtFailedRefreshes reads FailedRefreshes and fails the test on error.
func mtFailedRefreshes(t *testing.T, s store.DB) []store.FailedRefresh {
	t.Helper()
	fs, err := mtMaintenance(t, s).FailedRefreshes(t.Context())
	if err != nil {
		t.Fatalf("FailedRefreshes: %v", err)
	}
	return fs
}

// mtUnconsolidatedBanks reads UnconsolidatedBanks with a cut-off past every fixture's created_at and fails
// the test on error.
func mtUnconsolidatedBanks(t *testing.T, s store.DB) []domain.BankRef {
	t.Helper()
	st, ok := s.(*postgres.DB)
	if !ok {
		t.Fatalf("store is %T, want *postgres.DB", s)
	}
	bs, err := st.UnconsolidatedBanks(t.Context(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("UnconsolidatedBanks: %v", err)
	}
	out := make([]domain.BankRef, len(bs))
	for i, b := range bs {
		out[i] = b.Bank
	}
	return out
}

// mtModelKey names a model of a bank as "tenant/bank/model".
func mtModelKey(b domain.BankRef, id domain.MentalModelID) string {
	return b.Tenant + "/" + b.Bank + "/" + string(id)
}

// mtBankKeys names banks as "tenant/bank", in order.
func mtBankKeys(bs []domain.BankRef) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Tenant + "/" + b.Bank
	}
	return out
}

// mtCronKeys names the cron models in order.
func mtCronKeys(ms []store.CronModel) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = mtModelKey(m.Model.Bank, m.Model.Spec.ID)
	}
	return out
}

// mtFailedKeys names the failed refreshes' models in order.
func mtFailedKeys(fs []store.FailedRefresh) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = mtModelKey(f.Bank, f.ModelID)
	}
	return out
}

// mtWantCron fails the test unless ms holds exactly the models of want (keyed by mtModelKey, in order), each
// with its refresh_cron and the expected LastRefreshEnqueuedAt (zero = no operation).
func mtWantCron(t *testing.T, ms []store.CronModel, want []string, last map[string]time.Time) {
	t.Helper()
	wantSeq(t, "CronModels", mtCronKeys(ms), want)
	for _, m := range ms {
		key := mtModelKey(m.Model.Bank, m.Model.Spec.ID)
		if m.Model.Spec.Trigger.RefreshCron != mtCron {
			t.Errorf("CronModels %s refresh_cron = %q, want %q", key, m.Model.Spec.Trigger.RefreshCron, mtCron)
		}
		if w := last[key]; !m.LastRefreshEnqueuedAt.Equal(w) {
			t.Errorf("CronModels %s LastRefreshEnqueuedAt = %v, want %v", key, m.LastRefreshEnqueuedAt, w)
		}
	}
}

// mtWantFailed fails the test unless fs reports exactly the operations of want (keyed by mtModelKey, in
// order), each with FailedAt = its finished_at and its recorded error. Fail stamps finished_at and
// updated_at with the same clock reading and nothing touches the row afterwards, so the operation's
// UpdatedAt is its finished_at.
func (q queueEnv) mtWantFailed(t *testing.T, fs []store.FailedRefresh, want []string,
	ops map[string]domain.OperationID, msgs map[string]string,
) {
	t.Helper()
	wantSeq(t, "FailedRefreshes", mtFailedKeys(fs), want)
	for _, f := range fs {
		key := mtModelKey(f.Bank, f.ModelID)
		if f.OperationID != ops[key] {
			t.Errorf("FailedRefreshes %s OperationID = %s, want %s", key, f.OperationID, ops[key])
			continue
		}
		op := q.get(t, f.Bank, f.OperationID)
		if op.Status != domain.OperationStatusFailed {
			t.Errorf("FailedRefreshes %s reports operation %s in status %q", key, op.ID, op.Status)
		}
		if !f.FailedAt.Equal(op.UpdatedAt) {
			t.Errorf("FailedRefreshes %s FailedAt = %v, want finished_at %v", key, f.FailedAt, op.UpdatedAt)
		}
		if f.Error != op.Error || !strings.Contains(f.Error, msgs[key]) {
			t.Errorf("FailedRefreshes %s Error = %q, want the recorded %q (containing %q)", key, f.Error, op.Error,
				msgs[key])
		}
	}
}

// ---------- subtests ----------

// testMtCronModels: CronModels lists every model with refresh_cron on a live bank, ordered by (tenant, bank,
// model id). LastRefreshEnqueuedAt is the newest created_at of the model's refresh operations whatever their
// status (another kind with the same key and another bank's operation don't count), zero without one.
// Models without refresh_cron and tombstoned banks are skipped.
func testMtCronModels(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA, bankB, mtBankC)
	for _, m := range []domain.MentalModelID{"m-busy", "m-cancelled", "m-idle"} {
		mtCreateModel(t, q.s, bankA, m, mtCron)
	}
	mtCreateModel(t, q.s, bankA, "m-manual", "")
	mtCreateModel(t, q.s, mtBankC, "m-gone", mtCron)

	q.mtFailed(t, bankA, "m-busy", "boom")
	busy := q.mtPending(t, bankA, "m-busy")
	q.mtCompleted(t, bankA, "m-cancelled")
	cancelled := q.mtCancelled(t, bankA, "m-cancelled")
	q.mtCompleted(t, bankA, "m-manual")
	q.mtCompleted(t, mtBankC, "m-gone")
	q.mtPending(t, bankB, "m-busy") // a newer refresh of another bank's model with the same id
	other := job(bankA, domain.OperationKindConsolidate, "mm:m-idle", `{}`)
	other.RunAt = future()
	q.add(t, other) // another kind with the model's serial key
	mtTombstone(t, q.s, mtBankC)

	mtWantCron(t, mtCronModels(t, q.s),
		[]string{mtModelKey(bankA, "m-busy"), mtModelKey(bankA, "m-cancelled"), mtModelKey(bankA, "m-idle")},
		map[string]time.Time{
			mtModelKey(bankA, "m-busy"):      q.get(t, bankA, busy).CreatedAt,
			mtModelKey(bankA, "m-cancelled"): q.get(t, bankA, cancelled).CreatedAt,
		})
}

// testMtFailedRefreshes: a model is reported when its latest COMPLETED or FAILED refresh failed; a newer
// pending or cancelled operation does not hide the failure, a newer completed one does; deleted models and
// tombstoned banks are skipped.
func testMtFailedRefreshes(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA, mtBankC)
	for _, m := range []domain.MentalModelID{"m-a", "m-b", "m-c", "m-d", "m-e", "m-f"} {
		mtCreateModel(t, q.s, bankA, m, "")
	}
	mtCreateModel(t, q.s, mtBankC, "m-a", "")

	fa := q.mtFailed(t, bankA, "m-a", "boom-a")
	q.mtPending(t, bankA, "m-a")
	q.mtFailed(t, bankA, "m-b", "boom-b")
	q.mtCompleted(t, bankA, "m-b")
	fc := q.mtFailed(t, bankA, "m-c", "boom-c")
	q.mtCancelled(t, bankA, "m-c")
	q.mtFailed(t, bankA, "m-d", "boom-d")
	mtDeleteModel(t, q.s, bankA, "m-d")
	q.mtCompleted(t, bankA, "m-e")
	fe := q.mtFailed(t, bankA, "m-e", "boom-e")
	q.mtFailed(t, mtBankC, "m-a", "boom-gone")
	mtTombstone(t, q.s, mtBankC)

	a, c, e := mtModelKey(bankA, "m-a"), mtModelKey(bankA, "m-c"), mtModelKey(bankA, "m-e")
	q.mtWantFailed(t, mtFailedRefreshes(t, q.s), []string{a, c, e},
		map[string]domain.OperationID{a: fa, c: fc, e: fe},
		map[string]string{a: "boom-a", c: "boom-c", e: "boom-e"})
}

// testMtUnconsolidatedBanks: only live banks with an unconsolidated fact that has an embedding are listed,
// ordered by (tenant, bank). Banks whose facts are all consolidated or parked, a tombstoned bank, a bank
// whose only pending fact has no embedding, a bank with an observation only and a bank
// whose only pending fact is archived are not.
func testMtUnconsolidatedBanks(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankOtherTenant, mtBankF, mtBankE, mtBankD, mtBankC, bankB, bankA)
	if got := mtUnconsolidatedBanks(t, q.s); len(got) != 0 {
		t.Errorf("UnconsolidatedBanks with no facts = %v, want none", mtBankKeys(got))
	}
	insertUnits(t, q.s, mtEmbedded(bankOtherTenant, "other tenant"))
	insertUnits(t, q.s, mtEmbedded(bankA, "pending a"))
	done, parked := mtEmbedded(bankB, "consolidated"), mtEmbedded(bankB, "parked")
	insertUnits(t, q.s, done, parked)
	mtMark(t, q.s, []domain.UnitID{done.ID}, []domain.UnitID{parked.ID})
	insertUnits(t, q.s, mtEmbedded(mtBankC, "tombstoned"))
	mtTombstone(t, q.s, mtBankC)
	insertUnits(t, q.s, fact(mtBankD, "no embedding"))
	insertUnits(t, q.s, observation(mtBankE, "observation only"))
	archived := mtEmbedded(mtBankF, "archived")
	insertUnits(t, q.s, archived)
	mtArchive(t, q.s, mtBankF, archived.ID)

	wantSeq(t, "UnconsolidatedBanks", mtBankKeys(mtUnconsolidatedBanks(t, q.s)),
		mtBankKeys([]domain.BankRef{bankA, bankOtherTenant}))
}

// testMtUnconsolidatedSkipsUnembedded: UnitRepo.Unconsolidated (and UnconsolidatedBanks) skip a fact without
// an embedding, whether inserted without one or cleared by Update, and pick it up again once Update sets
// one (Update also clears the consolidation stamps).
func testMtUnconsolidatedSkipsUnembedded(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	f1, f2 := mtEmbedded(bankA, "embedded"), fact(bankA, "not embedded")
	f1.CreatedAt, f2.CreatedAt = day(1), day(2)
	insertUnits(t, q.s, f1, f2)
	wantSeq(t, "Unconsolidated with f2 unembedded", unitIDs(unconsolidated(t, q.s, bankA, nil, 10)),
		[]domain.UnitID{f1.ID})

	f1.Embedding = nil
	mtUpdate(t, q.s, f1)
	if got := unconsolidated(t, q.s, bankA, nil, 10); len(got) != 0 {
		t.Errorf("Unconsolidated after clearing f1's embedding = %v, want none", unitIDs(got))
	}
	if got := mtUnconsolidatedBanks(t, q.s); len(got) != 0 {
		t.Errorf("UnconsolidatedBanks with no embedded pending fact = %v, want none", mtBankKeys(got))
	}

	mtMark(t, q.s, []domain.UnitID{f2.ID}, nil)
	f2.Embedding = angleVec(10)
	mtUpdate(t, q.s, f2)
	wantSeq(t, "Unconsolidated after embedding f2 (stamps cleared by Update)",
		unitIDs(unconsolidated(t, q.s, bankA, nil, 10)), []domain.UnitID{f2.ID})
	f1.Embedding = angleVec(20)
	mtUpdate(t, q.s, f1)
	wantSeq(t, "Unconsolidated after embedding f1 again", unitIDs(unconsolidated(t, q.s, bankA, nil, 10)),
		[]domain.UnitID{f1.ID, f2.ID})
	wantSeq(t, "UnconsolidatedBanks after rederive", mtBankKeys(mtUnconsolidatedBanks(t, q.s)),
		mtBankKeys([]domain.BankRef{bankA}))
}

// testMtCrossBankTenants: the cross-bank reads return the same bank id under two tenants as two banks, each
// with its own BankRef, models and operations: a newer completed refresh of the other tenant's model never
// hides this tenant's failure, and each cron model sees only its own operations.
func testMtCrossBankTenants(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankOtherTenant, bankA)
	mtCreateModel(t, q.s, bankA, "m", mtCron)
	mtCreateModel(t, q.s, bankOtherTenant, "m", mtCron)
	failed := q.mtFailed(t, bankA, "m", "boom-tenant-1")
	q.mtCompleted(t, bankOtherTenant, "m")
	newest := q.mtPending(t, bankOtherTenant, "m")
	insertUnits(t, q.s, mtEmbedded(bankOtherTenant, "theirs"))
	insertUnits(t, q.s, mtEmbedded(bankA, "mine"))

	mine, theirs := mtModelKey(bankA, "m"), mtModelKey(bankOtherTenant, "m")
	mtWantCron(t, mtCronModels(t, q.s), []string{mine, theirs}, map[string]time.Time{
		mine:   q.get(t, bankA, failed).CreatedAt,
		theirs: q.get(t, bankOtherTenant, newest).CreatedAt,
	})
	q.mtWantFailed(t, mtFailedRefreshes(t, q.s), []string{mine},
		map[string]domain.OperationID{mine: failed}, map[string]string{mine: "boom-tenant-1"})
	wantSeq(t, "UnconsolidatedBanks", mtBankKeys(mtUnconsolidatedBanks(t, q.s)),
		mtBankKeys([]domain.BankRef{bankA, bankOtherTenant}))
}

// testMtQueueStats: QueueStats counts pending and processing jobs by kind and status across banks, leaves out
// terminal rows and retain_batch parents, and ages only due pending jobs: a parked job counts but never ages.
func testMtQueueStats(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA, bankOtherTenant)
	q.add(t, job(bankA, domain.OperationKindRetainBatch, "", `{"documents":2}`))
	done := q.add(t, doc("d0"))
	q.claimOne(t, done)
	q.complete(t, done)
	q.claimOne(t, q.add(t, doc("d1")))
	parked := doc("d2")
	parked.RunAt = future()
	q.add(t, parked)
	overdue := job(bankOtherTenant, domain.OperationKindRetainDocument, "doc:d3", `{"document_id":"d3"}`)
	overdue.RunAt = past()
	q.add(t, overdue)

	stats, err := mtMaintenance(t, q.s).QueueStats(t.Context())
	if err != nil {
		t.Fatalf("QueueStats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("QueueStats = %+v, want retain_document pending and processing only", stats)
	}
	pending, processing := stats[0], stats[1]
	if pending.Kind != domain.OperationKindRetainDocument || pending.Status != domain.OperationStatusPending ||
		pending.Jobs != 2 || pending.OldestDue < farAhead-time.Minute || pending.OldestDue > farAhead+time.Minute {
		t.Fatalf("pending entry = %+v, want 2 jobs, the oldest due about %s ago", pending, farAhead)
	}
	if processing.Status != domain.OperationStatusProcessing || processing.Jobs != 1 || processing.OldestDue != 0 {
		t.Fatalf("processing entry = %+v, want 1 job and no age", processing)
	}
}

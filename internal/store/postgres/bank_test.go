package postgres_test

import (
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"sync"
	"testing"
	"time"
)

// ---------- BankRepo ----------

// getBank reads a bank in its own transaction and returns Get's error unchanged.
func getBank(t *testing.T, s store.DB, b domain.BankRef) (domain.Bank, error) {
	t.Helper()
	var bank domain.Bank
	err := s.InTx(t.Context(), func(tx store.Tx) error {
		var err error
		bank, err = tx.Banks().Get(t.Context(), b)
		return err
	})
	return bank, err
}

// mustBank reads a bank and fails the test on error.
func mustBank(t *testing.T, s store.DB, b domain.BankRef) domain.Bank {
	t.Helper()
	bank, err := getBank(t, s, b)
	if err != nil {
		t.Fatalf("Banks().Get(%v): %v", b, err)
	}
	return bank
}

// ensureBank runs Ensure in its own transaction and returns its result unchanged.
func ensureBank(t *testing.T, s store.DB, b domain.BankRef) (domain.Bank, error) {
	t.Helper()
	var bank domain.Bank
	err := s.InTx(t.Context(), func(tx store.Tx) error {
		var err error
		bank, err = tx.Banks().Ensure(t.Context(), b)
		return err
	})
	return bank, err
}

// richConfig is a BankConfig with scalars, pointers, nested structs, a map and model bindings. It avoids
// ModelBinding.Extra (numbers would come back as float64) and empty non-nil slices (omitempty drops them).
func richConfig() domain.BankConfig {
	return domain.BankConfig{
		Retain: &domain.RetainConfig{
			Mission: "remember recipes", ExtractionMode: domain.ExtractionModeVerbose,
			EntityLabels: []domain.EntityLabel{{
				Key: "cuisine", Type: domain.LabelTypeValue, Optional: ptr(true),
				Values: []domain.LabelValue{{Value: "thai"}, {Value: "greek", Description: "Greek food"}},
			}},
			DefaultObservationScopes: &domain.ObservationScopes{
				Mode: domain.ScopeModeCustom, Custom: []domain.TagSet{{Tags: []string{"a", "b"}}},
			},
		},
		Observations: &domain.ObservationConfig{
			AutoConsolidation: ptr(false), DedupThreshold: ptr(0.95),
			MaxUnitsPerRun: ptr(50),
		},
		Recall: &domain.RecallConfig{
			BudgetFunction: domain.BudgetFunctionAdaptive, BudgetMid: ptr(250), Rerank: ptr(true),
			ArmBoosts: map[string]float64{"vector": 1.5, "graph": 0.5}, RecencyAlpha: ptr(0.3),
		},
		Reflect: &domain.ReflectConfig{
			Mission:     "answer briefly",
			Disposition: &domain.Disposition{Skepticism: 4, Empathy: 2},
		},
		MentalModels: &domain.MentalModelConfig{DefaultMode: domain.RefreshModeDelta, HistoryMaxEntries: ptr(7)},
		Models: &domain.ModelBindings{
			Default: &domain.ModelBinding{Model: "gpt-x", Temperature: ptr(0.2)},
			Reflect: &domain.ModelBinding{Model: "claude-y", MaxOutputTokens: ptr(2048)},
		},
	}
}

// testBankUnknown: Get and Delete of an unknown bank are ErrNotFound.
func testBankUnknown(t *testing.T, s store.DB) {
	_, err := getBank(t, s, bankA)
	wantErrIs(t, "Banks().Get(unknown)", err, domain.ErrNotFound)
	err = txErr(t, s, func(tx store.Tx) error { return tx.Banks().Delete(t.Context(), bankA) })
	wantErrIs(t, "Banks().Delete(unknown)", err, domain.ErrNotFound)
}

// testBankEnsure: Ensure creates a bank at ChangeSeq 0 and is idempotent; tenants are separate.
func testBankEnsure(t *testing.T, s store.DB) {
	first, err := ensureBank(t, s, bankA)
	if err != nil {
		t.Fatalf("Ensure(new): %v", err)
	}
	if first.Ref != bankA || first.ChangeSeq != 0 || first.DeletingAt != nil {
		t.Errorf("Ensure(new) = %+v, want ref %v, change_seq 0, not deleting", first, bankA)
	}
	inTx(t, s, func(tx store.Tx) error {
		_, err := tx.Seq(t.Context(), bankA)
		return err
	})
	again, err := ensureBank(t, s, bankA)
	if err != nil {
		t.Fatalf("Ensure(existing): %v", err)
	}
	if again.ChangeSeq != 1 || !again.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("Ensure(existing) = seq %d created %v, want the existing bank (seq 1, created %v)",
			again.ChangeSeq, again.CreatedAt, first.CreatedAt)
	}
	if got := mustBank(t, s, bankA); got.Ref != bankA {
		t.Errorf("Get after Ensure = %+v", got)
	}
	_, err = getBank(t, s, bankOtherTenant)
	wantErrIs(t, "Get(same bank id, other tenant)", err, domain.ErrNotFound)
}

// testBankCreate: Create stores name and config; a second Create is ErrAlreadyExists; Ensure keeps them.
func testBankCreate(t *testing.T, s store.DB) {
	bank := domain.Bank{
		Ref: bankA, Name: "Research", Config: domain.BankConfig{Retain: &domain.RetainConfig{Mission: "papers"}},
	}
	inTx(t, s, func(tx store.Tx) error { return tx.Banks().Create(t.Context(), bank) })
	got := mustBank(t, s, bankA)
	if got.Name != "Research" || got.ChangeSeq != 0 {
		t.Errorf("Get after Create = name %q seq %d, want Research, 0", got.Name, got.ChangeSeq)
	}
	wantJSON(t, "config after Create", got.Config, bank.Config)
	err := txErr(t, s, func(tx store.Tx) error { return tx.Banks().Create(t.Context(), bank) })
	wantErrIs(t, "Create(duplicate)", err, domain.ErrAlreadyExists)
	ensured, err := ensureBank(t, s, bankA)
	if err != nil || ensured.Name != "Research" {
		t.Errorf("Ensure after Create = %q, %v; want the created bank", ensured.Name, err)
	}
	inTx(t, s, func(tx store.Tx) error {
		return tx.Banks().Create(t.Context(), domain.Bank{Ref: bankOtherTenant, Name: "Other tenant"})
	})
	if a, o := mustBank(t, s, bankA).Name, mustBank(t, s, bankOtherTenant).Name; a != "Research" || o != "Other tenant" {
		t.Errorf("bank names after creating the same id under another tenant = %q, %q; want Research, Other tenant",
			a, o)
	}
}

// testBankSetConfig: SetConfig round-trips a rich config and replaces (does not merge) the previous one.
func testBankSetConfig(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	cfg := richConfig()
	inTx(t, s, func(tx store.Tx) error { return tx.Banks().SetConfig(t.Context(), bankA, cfg) })
	wantJSON(t, "config after SetConfig", mustBank(t, s, bankA).Config, cfg)
	small := domain.BankConfig{Observations: &domain.ObservationConfig{Mission: "only this"}}
	inTx(t, s, func(tx store.Tx) error { return tx.Banks().SetConfig(t.Context(), bankA, small) })
	wantJSON(t, "config after second SetConfig", mustBank(t, s, bankA).Config, small)
}

// testBankMarkDeleting: MarkDeleting sets DeletingAt once; Ensure then fails with ErrBankDeleting.
func testBankMarkDeleting(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB)
	at1, at2 := day(10), day(11)
	inTx(t, s, func(tx store.Tx) error { return tx.Banks().MarkDeleting(t.Context(), bankA, at1) })
	got := mustBank(t, s, bankA)
	if !sameTimePtr(got.DeletingAt, &at1) || !got.Deleting() {
		t.Errorf("DeletingAt after MarkDeleting = %v, want %v", got.DeletingAt, at1)
	}
	inTx(t, s, func(tx store.Tx) error { return tx.Banks().MarkDeleting(t.Context(), bankA, at2) })
	if got := mustBank(t, s, bankA); !sameTimePtr(got.DeletingAt, &at1) {
		t.Errorf("DeletingAt after second MarkDeleting = %v, want the first time %v", got.DeletingAt, at1)
	}
	_, err := ensureBank(t, s, bankA)
	wantErrIs(t, "Ensure(tombstoned)", err, domain.ErrBankDeleting)
	wantErrIs(t, "Ensure(tombstoned) class", err, domain.ErrFailedPrecondition)
	if got := mustBank(t, s, bankB); got.DeletingAt != nil {
		t.Errorf("bank B DeletingAt = %v, want nil", got.DeletingAt)
	}
}

// testBankDelete: Delete removes the tombstoned bank; a later Ensure creates a new, empty one.
func testBankDelete(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	inTx(t, s, func(tx store.Tx) error {
		_, err := tx.Seq(t.Context(), bankA)
		return err
	})
	inTx(t, s, func(tx store.Tx) error { return tx.Banks().MarkDeleting(t.Context(), bankA, day(1)) })
	inTx(t, s, func(tx store.Tx) error { return tx.Banks().Delete(t.Context(), bankA) })
	_, err := getBank(t, s, bankA)
	wantErrIs(t, "Get after Delete", err, domain.ErrNotFound)
	_, err = s.Read().BankSeq(t.Context(), bankA)
	wantErrIs(t, "BankSeq after Delete", err, domain.ErrNotFound)
	fresh, err := ensureBank(t, s, bankA)
	if err != nil || fresh.ChangeSeq != 0 || fresh.DeletingAt != nil {
		t.Errorf("Ensure after Delete = %+v, %v; want a new bank at change_seq 0", fresh, err)
	}
}

// tenantConfig reads a tenant's config.
func tenantConfig(t *testing.T, s store.DB, tenant string) domain.BankConfig {
	t.Helper()
	var cfg domain.BankConfig
	inTx(t, s, func(tx store.Tx) error {
		var err error
		cfg, err = tx.Banks().TenantConfig(t.Context(), tenant)
		return err
	})
	return cfg
}

// testTenantConfig: an unset tenant config is zero; SetTenantConfig round-trips and replaces per tenant.
func testTenantConfig(t *testing.T, s store.DB) {
	wantJSON(t, "TenantConfig(unset)", tenantConfig(t, s, "tenant-1"), domain.BankConfig{})
	cfg := richConfig()
	inTx(t, s, func(tx store.Tx) error { return tx.Banks().SetTenantConfig(t.Context(), "tenant-1", cfg) })
	wantJSON(t, "TenantConfig after set", tenantConfig(t, s, "tenant-1"), cfg)
	wantJSON(t, "TenantConfig(other tenant)", tenantConfig(t, s, "tenant-2"), domain.BankConfig{})
	small := domain.BankConfig{Retain: &domain.RetainConfig{Mission: "tenant default"}}
	inTx(t, s, func(tx store.Tx) error { return tx.Banks().SetTenantConfig(t.Context(), "tenant-1", small) })
	wantJSON(t, "TenantConfig after replace", tenantConfig(t, s, "tenant-1"), small)
}

// tenantRMW runs LockTenantConfig, TenantConfig, then SetTenantConfig with mission appended to the
// RetainMission it read, in one transaction. It reports on locked once it holds the lock and waits for
// release before writing.
func tenantRMW(
	t *testing.T, s store.DB, tenant, mission string, locked chan<- struct{}, release <-chan struct{},
) error {
	var once sync.Once
	return s.InTx(t.Context(), func(tx store.Tx) error {
		if err := tx.Banks().LockTenantConfig(t.Context(), tenant); err != nil {
			return err
		}
		cur, err := tx.Banks().TenantConfig(t.Context(), tenant)
		if err != nil {
			return err
		}
		once.Do(func() { close(locked) })
		<-release
		retGrp := cur.GetRetain()
		retGrp.Mission += mission
		cur.Retain = &retGrp
		return tx.Banks().SetTenantConfig(t.Context(), tenant, cur)
	})
}

// testTenantConfigLock: while tx A holds a tenant's config lock (no config stored yet), tx B's
// LockTenantConfig blocks; once A commits, B reads A's write, so neither read-modify-write is lost.
func testTenantConfigLock(t *testing.T, s store.DB) {
	const tenant = "tenant-lock"
	lockedA, lockedB := make(chan struct{}), make(chan struct{})
	releaseA, releaseB := make(chan struct{}), make(chan struct{})
	close(releaseB)
	var once sync.Once
	freeA := func() { once.Do(func() { close(releaseA) }) }
	defer freeA()
	doneA, doneB := make(chan error, 1), make(chan error, 1)
	go func() { doneA <- tenantRMW(t, s, tenant, "A", lockedA, releaseA) }()
	waitFor(t, "tx A's lock", lockedA)
	go func() { doneB <- tenantRMW(t, s, tenant, "B", lockedB, releaseB) }()
	select {
	case <-lockedB:
		t.Errorf("tx B took the tenant config lock while tx A held it; want it blocked")
	case <-time.After(blockedFor):
	}
	freeA()
	if err := waitFor(t, "tx A's commit", doneA); err != nil {
		t.Fatalf("tx A: %v", err)
	}
	if err := waitFor(t, "tx B's commit", doneB); err != nil {
		t.Fatalf("tx B: %v", err)
	}
	if got := tenantConfig(t, s, tenant).GetRetain().Mission; got != "AB" {
		t.Errorf("RetainMission after two locked read-modify-writes = %q, want %q", got, "AB")
	}
}

// ---------- MentalModelRepo ----------

// sampleModel is a mental model exercising every spec field.
func sampleModel(b domain.BankRef, id domain.MentalModelID) domain.MentalModel {
	return domain.MentalModel{
		Bank: b,
		Spec: domain.MentalModelSpec{
			ID: id, Name: "Team overview", SourceQuery: "Who is on the team?", FolderPath: "teams/eng",
			Tags: []string{"eng", "team"}, MaxTokens: ptr(2000),
			Schedule: domain.RefreshSchedule{AfterConsolidation: true, MinIntervalSeconds: ptr(60)},
			Scope: domain.MentalModelScope{
				Tags: domain.TagFilter{Match: domain.TagsMatchAllStrict}, FactTypes: []domain.FactType{domain.FactTypeWorld},
			},
			Mode: domain.RefreshModeDelta, Strategy: domain.RefreshStrategySingleShot,
			IncludeChunks: ptr(false), Budget: domain.BudgetHigh, KeepTrace: true,
			Overrides: &domain.BankConfig{
				Recall: &domain.RecallConfig{BudgetLow: ptr(10)}, Reflect: &domain.ReflectConfig{Mission: "be brief"},
			},
		},
		ContentMD:   "# Team\n\n- Alice\n",
		Version:     1,
		LastSeqSeen: 5,
		SourceIDs:   []domain.UnitID{newID[domain.UnitID]()},
	}
}

// getModel reads a mental model in its own transaction and returns Get's error unchanged.
func getModel(t *testing.T, s store.DB, id domain.MentalModelID) (domain.MentalModel, error) {
	t.Helper()
	var m domain.MentalModel
	err := s.InTx(t.Context(), func(tx store.Tx) error {
		var err error
		m, err = tx.MentalModels().Get(t.Context(), bankA, id)
		return err
	})
	return m, err
}

// compareModel reports differing stored fields of a mental model.
func compareModel(t *testing.T, step string, got, want domain.MentalModel) {
	t.Helper()
	wantJSON(t, step+": spec", got.Spec, want.Spec)
	if got.Bank != want.Bank || got.ContentMD != want.ContentMD || got.Version != want.Version {
		t.Errorf("%s: bank/content/version = %v %q %d, want %v %q %d", step, got.Bank, got.ContentMD, got.Version,
			want.Bank, want.ContentMD, want.Version)
	}
	if got.LastSeqSeen != want.LastSeqSeen || got.AutoRefreshPaused != want.AutoRefreshPaused {
		t.Errorf("%s: last_seq_seen/paused = %d %v, want %d %v", step, got.LastSeqSeen, got.AutoRefreshPaused,
			want.LastSeqSeen, want.AutoRefreshPaused)
	}
	if !sameTimePtr(got.LastRefreshedAt, want.LastRefreshedAt) ||
		!sameTimePtr(got.LastRefreshFailedAt, want.LastRefreshFailedAt) {
		t.Errorf("%s: last refreshed/failed = %v/%v, want %v/%v", step, got.LastRefreshedAt,
			got.LastRefreshFailedAt, want.LastRefreshedAt, want.LastRefreshFailedAt)
	}
	wantSet(t, step+": source ids", got.SourceIDs, want.SourceIDs)
}

// testModelCreateGet: Create then Get round-trips spec, content and refresh state.
func testModelCreateGet(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	m := sampleModel(bankA, "team-overview")
	inTx(t, s, func(tx store.Tx) error { return tx.MentalModels().Create(t.Context(), m) })
	got, err := getModel(t, s, m.Spec.ID)
	if err != nil {
		t.Fatalf("Get after Create: %v", err)
	}
	compareModel(t, "after Create", got, m)
	_, err = getModel(t, s, "missing")
	wantErrIs(t, "Get(unknown)", err, domain.ErrNotFound)
}

// testModelDuplicate: a second Create with the same id is ErrAlreadyExists.
func testModelDuplicate(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB)
	m := sampleModel(bankA, "dup")
	inTx(t, s, func(tx store.Tx) error { return tx.MentalModels().Create(t.Context(), m) })
	err := txErr(t, s, func(tx store.Tx) error { return tx.MentalModels().Create(t.Context(), m) })
	wantErrIs(t, "Create(duplicate)", err, domain.ErrAlreadyExists)
	other := sampleModel(bankB, "dup")
	inTx(t, s, func(tx store.Tx) error { return tx.MentalModels().Create(t.Context(), other) })
}

// testModelUpdate: Update replaces spec, content and refresh state; unknown ids are ErrNotFound.
func testModelUpdate(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	m := sampleModel(bankA, "team-overview")
	inTx(t, s, func(tx store.Tx) error { return tx.MentalModels().Create(t.Context(), m) })
	u := m
	u.Spec.Name, u.Spec.Tags, u.Spec.Schedule = "Renamed", []string{"eng"}, domain.RefreshSchedule{Cron: "0 * * * *"}
	u.Spec.Overrides = nil
	u.ContentMD, u.Version, u.LastSeqSeen, u.AutoRefreshPaused = "# Team v2", 2, 9, true
	u.LastRefreshedAt, u.LastRefreshFailedAt = ptr(day(5)), ptr(day(6))
	u.SourceIDs = []domain.UnitID{newID[domain.UnitID](), newID[domain.UnitID]()}
	inTx(t, s, func(tx store.Tx) error { return tx.MentalModels().Update(t.Context(), u) })
	got, err := getModel(t, s, m.Spec.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	compareModel(t, "after Update", got, u)
	missing := sampleModel(bankA, "missing")
	err = txErr(t, s, func(tx store.Tx) error { return tx.MentalModels().Update(t.Context(), missing) })
	wantErrIs(t, "Update(unknown)", err, domain.ErrNotFound)
}

// historyEntry builds a version or failure entry numbered n, written at day(n).
func historyEntry(kind domain.HistoryKind, n int) domain.MentalModelHistoryEntry {
	e := domain.MentalModelHistoryEntry{Kind: kind, ChangedAt: day(n)}
	if kind == domain.HistoryKindVersion {
		e.Version, e.PreviousContent, e.Outcome = int64(n), fmt.Sprintf("content %d", n), domain.RefreshOutcomeContentWritten
	} else {
		e.Outcome, e.FailureReason, e.ErrorMessage = domain.RefreshOutcomeFailed, "llm_error", fmt.Sprintf("failure %d", n)
	}
	return e
}

// history reads a model's history.
func history(t *testing.T, s store.DB, id domain.MentalModelID, kind domain.HistoryKind, limit int) []string {
	t.Helper()
	var es []domain.MentalModelHistoryEntry
	inTx(t, s, func(tx store.Tx) error {
		var err error
		es, err = tx.MentalModels().History(t.Context(), bankA, id, kind, limit)
		return err
	})
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = describeEntry(e)
	}
	return out
}

// describeEntry renders the stored fields of a history entry for comparison.
func describeEntry(e domain.MentalModelHistoryEntry) string {
	return fmt.Sprintf("%s v%d %q %s %q %q @%s", e.Kind, e.Version, e.PreviousContent, e.Outcome, e.FailureReason,
		e.ErrorMessage, e.ChangedAt.UTC().Format("2006-01-02"))
}

// describeEntries renders entries numbered ns of the given kinds (v = version, f = failure).
func describeEntries(ns ...int) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		kind := domain.HistoryKindVersion
		if n < 0 {
			kind, n = domain.HistoryKindRefreshFailed, -n
		}
		out[i] = describeEntry(historyEntry(kind, n))
	}
	return out
}

// testModelHistory: AppendHistory caps versions and failures separately; History is newest first and
// filters by kind; keep <= 0 keeps all.
func testModelHistory(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	m := sampleModel(bankA, "history")
	inTx(t, s, func(tx store.Tx) error { return tx.MentalModels().Create(t.Context(), m) })
	// Interleaved: v1 v2 f3 v4 f5 v6 f7 (negative = failure), each in its own tx, keep 2 per kind.
	for _, n := range []int{1, 2, -3, 4, -5, 6, -7} {
		kind := domain.HistoryKindVersion
		if n < 0 {
			kind, n = domain.HistoryKindRefreshFailed, -n
		}
		e := historyEntry(kind, n)
		inTx(t, s, func(tx store.Tx) error {
			return tx.MentalModels().AppendHistory(t.Context(), bankA, m.Spec.ID, e, 2)
		})
	}
	id := m.Spec.ID
	wantSeq(t, "History(version)", history(t, s, id, domain.HistoryKindVersion, 10), describeEntries(6, 4))
	wantSeq(t, "History(refresh_failed)", history(t, s, id, domain.HistoryKindRefreshFailed, 10),
		describeEntries(-7, -5))
	wantSeq(t, "History(all)", history(t, s, id, domain.HistoryKindUnspecified, 10), describeEntries(-7, 6, -5, 4))
	wantSeq(t, "History(all, limit 2)", history(t, s, id, domain.HistoryKindUnspecified, 2), describeEntries(-7, 6))
	e := historyEntry(domain.HistoryKindVersion, 8)
	inTx(t, s, func(tx store.Tx) error { return tx.MentalModels().AppendHistory(t.Context(), bankA, id, e, 0) })
	wantSeq(t, "History(version) after keep=0", history(t, s, id, domain.HistoryKindVersion, 10),
		describeEntries(8, 6, 4))
}

// testModelDelete: Delete removes the model and its history; a second Delete is ErrNotFound.
func testModelDelete(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	m := sampleModel(bankA, "doomed")
	inTx(t, s, func(tx store.Tx) error { return tx.MentalModels().Create(t.Context(), m) })
	e := historyEntry(domain.HistoryKindVersion, 1)
	inTx(t, s, func(tx store.Tx) error { return tx.MentalModels().AppendHistory(t.Context(), bankA, m.Spec.ID, e, 0) })
	inTx(t, s, func(tx store.Tx) error { return tx.MentalModels().Delete(t.Context(), bankA, m.Spec.ID) })
	_, err := getModel(t, s, m.Spec.ID)
	wantErrIs(t, "Get after Delete", err, domain.ErrNotFound)
	err = txErr(t, s, func(tx store.Tx) error { return tx.MentalModels().Delete(t.Context(), bankA, m.Spec.ID) })
	wantErrIs(t, "Delete(unknown)", err, domain.ErrNotFound)
	// Re-creating the id starts with an empty history.
	inTx(t, s, func(tx store.Tx) error { return tx.MentalModels().Create(t.Context(), m) })
	if h := history(t, s, m.Spec.ID, domain.HistoryKindUnspecified, 10); len(h) != 0 {
		t.Errorf("History of a re-created model = %v, want empty (Delete removes history)", h)
	}
}

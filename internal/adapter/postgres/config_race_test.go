package postgres_test

import (
	"fmt"
	"memd/internal/adapter/postgres"
	"memd/internal/adapter/postgres/pgtest"
	"memd/internal/domain"
	"memd/internal/engine/bank"
	"memd/internal/port"
	"sync"
	"testing"
	"time"
)

// overlapWait is how long the first config patch waits for the second to read the stored config too. With
// the read-modify-write serialized the second can't read until the first commits, so this wait times out.
const overlapWait = 500 * time.Millisecond

// racePatches runs two config patches the way two concurrent admins would: patch B starts once patch A
// has read the stored config, and A writes only after B has read it too (or after overlapWait, when B is
// blocked behind A). update applies one patch; it must run the patch inside its transaction.
func racePatches(
	t *testing.T, update func(bank.ConfigPatch) error, setA, setB func(*domain.BankConfig),
) {
	t.Helper()
	aRead, bRead := make(chan struct{}), make(chan struct{})
	var aOnce, bOnce sync.Once
	patchA := func(cur domain.BankConfig) (domain.BankConfig, error) {
		aOnce.Do(func() { close(aRead) })
		select {
		case <-bRead:
		case <-time.After(overlapWait):
		}
		setA(&cur)
		return cur, nil
	}
	patchB := func(cur domain.BankConfig) (domain.BankConfig, error) {
		bOnce.Do(func() { close(bRead) })
		setB(&cur)
		return cur, nil
	}
	errA, errB := make(chan error, 1), make(chan error, 1)
	go func() { errA <- update(patchA) }()
	<-aRead
	go func() { errB <- update(patchB) }()
	if err := <-errA; err != nil {
		t.Fatalf("patch A: %v", err)
	}
	if err := <-errB; err != nil {
		t.Fatalf("patch B: %v", err)
	}
}

// TestConcurrentBankConfigPatchesKeepBoth: two concurrent UpdateBankConfig calls patching different fields
// both land. UpdateBankConfig takes the bank's seq (row lock) before reading the overrides (decision 2500).
func TestConcurrentBankConfigPatchesKeepBoth(t *testing.T) {
	s := postgres.New(pgtest.Fresh(t))
	b := port.BankRef{Tenant: "t-review", Bank: "b-cfg"}
	admin := bank.NewBankAdmin(s)
	if _, err := admin.UpdateBankConfig(t.Context(), b, func(c domain.BankConfig) (domain.BankConfig, error) {
		return c, nil
	}); err != nil {
		t.Fatalf("create bank: %v", err)
	}
	racePatches(t, func(p bank.ConfigPatch) error {
		if _, err := admin.UpdateBankConfig(t.Context(), b, p); err != nil {
			return fmt.Errorf("update bank config: %w", err)
		}
		return nil
	}, func(c *domain.BankConfig) { c.RetainMission = "A" }, func(c *domain.BankConfig) { c.ReflectMission = "B" })
	got, err := admin.GetBank(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.RetainMission != "A" || got.Config.ReflectMission != "B" {
		t.Fatalf("lost update: stored config is retain_mission=%q reflect_mission=%q, want A and B",
			got.Config.RetainMission, got.Config.ReflectMission)
	}
}

// TestConcurrentTenantConfigPatchesKeepBoth: the same for UpdateTenantConfig on a tenant with no stored
// config yet; it takes the tenant config lock before reading (decision 2500, CCR WP-25-1).
func TestConcurrentTenantConfigPatchesKeepBoth(t *testing.T) {
	s := postgres.New(pgtest.Fresh(t))
	const tenant = "t-review"
	admin := bank.NewTenantAdmin(s)
	racePatches(t, func(p bank.ConfigPatch) error {
		if _, err := admin.UpdateTenantConfig(t.Context(), tenant, p); err != nil {
			return fmt.Errorf("update tenant config: %w", err)
		}
		return nil
	}, func(c *domain.BankConfig) { c.RetainMission = "A" }, func(c *domain.BankConfig) { c.ReflectMission = "B" })
	got, err := admin.TenantConfig(t.Context(), tenant)
	if err != nil {
		t.Fatal(err)
	}
	if got.RetainMission != "A" || got.ReflectMission != "B" {
		t.Fatalf("lost update: stored tenant config is retain_mission=%q reflect_mission=%q, want A and B",
			got.RetainMission, got.ReflectMission)
	}
}

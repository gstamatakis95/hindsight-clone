package configsvc

import (
	"fmt"
	"memd/internal/domain"
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
	t *testing.T, update func(Patch) error, setA, setB func(*domain.BankConfig),
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
// both land. UpdateBankConfig takes the bank's seq (row lock) before reading the overrides.
func TestConcurrentBankConfigPatchesKeepBoth(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := domain.BankRef{Tenant: "t-race", Bank: "b-cfg"}
	admin := e.svc
	if _, err := admin.UpdateBankConfig(t.Context(), b, func(c domain.BankConfig) (domain.BankConfig, error) {
		return c, nil
	}); err != nil {
		t.Fatalf("create bank: %v", err)
	}
	racePatches(t, func(p Patch) error {
		if _, err := admin.UpdateBankConfig(t.Context(), b, p); err != nil {
			return fmt.Errorf("update bank config: %w", err)
		}
		return nil
	}, setRetainMissionA, setReflectMissionB)
	got, err := admin.GetBankConfig(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Own.GetRetain().Mission != "A" || got.Own.GetReflect().Mission != "B" {
		t.Fatalf("lost update: stored config is retain.mission=%q reflect.mission=%q, want A and B",
			got.Own.GetRetain().Mission, got.Own.GetReflect().Mission)
	}
}

// TestConcurrentTenantConfigPatchesKeepBoth: the same for UpdateTenantConfig on a tenant with no stored
// config yet; it takes the tenant config lock before reading.
func TestConcurrentTenantConfigPatchesKeepBoth(t *testing.T) {
	t.Parallel()
	const tenant = "t-race"
	admin := newEnv(t).svc
	racePatches(t, func(p Patch) error {
		if _, err := admin.UpdateTenantConfig(t.Context(), tenant, p); err != nil {
			return fmt.Errorf("update tenant config: %w", err)
		}
		return nil
	}, setRetainMissionA, setReflectMissionB)
	got, err := admin.GetTenantConfig(t.Context(), tenant)
	if err != nil {
		t.Fatal(err)
	}
	if got.Own.GetRetain().Mission != "A" || got.Own.GetReflect().Mission != "B" {
		t.Fatalf("lost update: stored tenant config is retain.mission=%q reflect.mission=%q, want A and B",
			got.Own.GetRetain().Mission, got.Own.GetReflect().Mission)
	}
}

// setRetainMissionA is one racing patch: retain.mission = "A".
func setRetainMissionA(c *domain.BankConfig) {
	r := c.GetRetain()
	r.Mission = "A"
	c.Retain = &r
}

// setReflectMissionB is the other racing patch: reflect.mission = "B".
func setReflectMissionB(c *domain.BankConfig) {
	r := c.GetReflect()
	r.Mission = "B"
	c.Reflect = &r
}

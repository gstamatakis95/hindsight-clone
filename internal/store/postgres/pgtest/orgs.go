package pgtest

import (
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"testing"
)

// CreateOrgs creates the org of each of banks (orgs are never created on first use), skipping one that already
// exists, in one transaction on s. It fails the test on any other error.
func CreateOrgs(t testing.TB, s store.DB, banks ...domain.BankRef) {
	t.Helper()
	err := s.InTx(t.Context(), func(tx store.Tx) error {
		return createOrgs(t, tx, banks)
	})
	if err != nil {
		t.Fatalf("pgtest: create orgs: %v", err)
	}
}

// EnsureBanks creates the org of each of banks (as CreateOrgs does) and then each bank with BankRepo.Ensure, in
// one transaction on s. It fails the test on any error.
func EnsureBanks(t testing.TB, s store.DB, banks ...domain.BankRef) {
	t.Helper()
	err := s.InTx(t.Context(), func(tx store.Tx) error {
		if err := createOrgs(t, tx, banks); err != nil {
			return err
		}
		for _, b := range banks {
			if _, err := tx.Banks().Ensure(t.Context(), b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("pgtest: ensure banks: %v", err)
	}
}

// createOrgs creates the orgs of banks in tx, skipping existing ones.
func createOrgs(t testing.TB, tx store.Tx, banks []domain.BankRef) error {
	for _, b := range banks {
		if _, err := tx.Orgs().Get(t.Context(), b.OrgRef()); err == nil {
			continue
		} else if !errors.Is(err, domain.ErrOrgNotFound) {
			return err
		}
		if err := tx.Orgs().Create(t.Context(), domain.Org{Ref: b.OrgRef()}); err != nil {
			return err
		}
	}
	return nil
}

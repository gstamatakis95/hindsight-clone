package bank

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"strings"
	"testing"
)

// racingStore makes every bank look unknown and every Create fail as the Postgres adapter does on a unique
// violation, as when a concurrent CreateBank commits between the check and the insert.
type racingStore struct{ store.DB }

func (s racingStore) InTx(ctx context.Context, fn func(store.Tx) error) error {
	return s.DB.InTx(ctx, func(tx store.Tx) error { return fn(racingTx{tx}) })
}

type racingTx struct{ store.Tx }

func (t racingTx) Banks() store.BankRepo { return racingBanks{t.Tx.Banks()} }

type racingBanks struct{ store.BankRepo }

func (racingBanks) Get(context.Context, domain.BankRef) (domain.Bank, error) {
	return domain.Bank{}, domain.ErrNotFound
}

func (racingBanks) Create(context.Context, domain.Bank) error {
	return fmt.Errorf("%w: create bank: ERROR: duplicate key value violates unique constraint \"banks_pkey\" "+
		"(SQLSTATE 23505)", domain.ErrAlreadyExists)
}

// TestCreateBank: a create stores the name and config; on an existing bank, and when it loses a race to a
// concurrent create, it is ALREADY_EXISTS without SQL text; on a deleting bank it is FAILED_PRECONDITION
// (ErrBankDeleting), while GetBank still returns the tombstoned bank.
func TestCreateBank(t *testing.T) {
	e := newEnv(t)
	s := e.svc(DeleteConfig{})
	b := domain.BankRef{Tenant: "t", Bank: "b"}
	created, ops, err := s.CreateBank(e.ctx, CreateRequest{Bank: domain.Bank{
		Ref: b, Name: "B", Config: domain.BankConfig{Reflect: &domain.ReflectConfig{Mission: "m"}},
	}})
	if err != nil || ops != nil || created.Name != "B" || created.Config.GetReflect().Mission != "m" {
		t.Fatalf("created = %+v %v %v", created, ops, err)
	}
	noSQL := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, domain.ErrAlreadyExists) {
			t.Fatalf("%s: %v, want ErrAlreadyExists", what, err)
		}
		if msg := err.Error(); strings.Contains(msg, "SQLSTATE") || strings.Contains(msg, "duplicate key") {
			t.Fatalf("%s leaks SQL: %q", what, msg)
		}
	}
	_, _, err = s.CreateBank(e.ctx, CreateRequest{Bank: domain.Bank{Ref: b}})
	noSQL("existing bank", err)
	_, _, err = e.svcOver(racingStore{e.store}, e.blob, DeleteConfig{}).CreateBank(e.ctx,
		CreateRequest{Bank: domain.Bank{Ref: b}})
	noSQL("lost race", err)

	if _, err := s.DeleteBank(e.ctx, b); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.CreateBank(e.ctx, CreateRequest{Bank: domain.Bank{Ref: b}})
	if !errors.Is(err, domain.ErrBankDeleting) || !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Fatalf("CreateBank on a deleting bank = %v, want ErrBankDeleting (FAILED_PRECONDITION)", err)
	}
	got, err := s.GetBank(e.ctx, b)
	if err != nil || !got.Deleting() {
		t.Errorf("GetBank of a tombstoned bank = %+v %v, want it with DeletingAt", got, err)
	}
}

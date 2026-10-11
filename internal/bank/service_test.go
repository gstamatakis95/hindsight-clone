package bank

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
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

// TestCreateBank: a create stores the name and config; on an existing bank (with or without a template), and when
// it loses a race to a concurrent create, it is ALREADY_EXISTS without SQL text; on a deleting bank it is
// FAILED_PRECONDITION (ErrBankDeleting).
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
	_, _, err = s.CreateBank(e.ctx, CreateRequest{Bank: domain.Bank{Ref: b}, Template: &domain.BankTemplate{}})
	noSQL("existing bank, from a template", err)
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
}

// TestGetBankDeleting: GetBank of a bank being deleted is NOT_FOUND with the BANK_NOT_FOUND reason, as for an
// unknown bank.
func TestGetBankDeleting(t *testing.T) {
	e := newEnv(t)
	s := e.svc(DeleteConfig{})
	b := domain.BankRef{Tenant: "t", Bank: "b"}
	if _, _, err := s.CreateBank(e.ctx, CreateRequest{Bank: domain.Bank{Ref: b}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteBank(e.ctx, b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetBank(e.ctx, b); !errors.Is(err, domain.ErrBankNotFound) {
		t.Errorf("GetBank of a deleting bank = %v, want ErrBankNotFound", err)
	}
}

// TestListBanks pages a tenant's banks in id order, one at a time, leaving out a deleting bank, another tenant's
// banks and, with a prefix, the banks outside it.
func TestListBanks(t *testing.T) {
	e := newEnv(t)
	s := e.svc(DeleteConfig{})
	for _, ref := range []domain.BankRef{
		{Tenant: "t", Bank: "b2"},
		{Tenant: "t", Bank: "a1"},
		{Tenant: "t", Bank: "b1"},
		{Tenant: "t", Bank: "gone"},
		{Tenant: "u", Bank: "b0"},
	} {
		if _, _, err := s.CreateBank(e.ctx, CreateRequest{Bank: domain.Bank{Ref: ref}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DeleteBank(e.ctx, domain.BankRef{Tenant: "t", Bank: "gone"}); err != nil {
		t.Fatal(err)
	}
	list := func(prefix string) []string {
		req := ListBanksRequest{Tenant: "t", IDPrefix: prefix, PageSize: 1}
		var ids []string
		for range 10 {
			page, err := s.ListBanks(e.ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			for _, b := range page.Banks {
				ids = append(ids, b.Ref.Bank)
			}
			if page.NextPageToken == "" {
				return ids
			}
			req.PageToken = page.NextPageToken
		}
		t.Fatal("bank paging did not end")
		return nil
	}
	if got := list(""); !slices.Equal(got, []string{"a1", "b1", "b2"}) {
		t.Errorf("ListBanks = %v, want [a1 b1 b2]", got)
	}
	if got := list("b"); !slices.Equal(got, []string{"b1", "b2"}) {
		t.Errorf("ListBanks(prefix b) = %v, want [b1 b2]", got)
	}
	if _, err := s.ListBanks(e.ctx, ListBanksRequest{Tenant: "t", PageToken: "junk"}); !errors.Is(err,
		domain.ErrInvalidArgument) {
		t.Errorf("malformed page token: %v, want ErrInvalidArgument", err)
	}
}

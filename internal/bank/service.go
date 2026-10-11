package bank

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/blob"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/store"
)

// Deps are the Service's dependencies. DB, Blob and Clock are required; Maintenance is needed only by
// ResumeStalledDeletes.
type Deps struct {
	// DB reads and writes banks, their rows and the job queue in short transactions.
	DB store.DB
	// Maintenance lists tombstoned banks whose delete_bank stopped (StalledBankDeletes).
	Maintenance store.Maintenance
	// Blob holds the documents' original texts, deleted with the bank.
	Blob blob.Store
	// Clock stamps deleting_at and dates the delete's continuations and the template models' first builds.
	Clock clock.Clock
	// Delete bounds one delete_bank run; zero fields take the defaults.
	Delete DeleteConfig
}

// Service serves the bank part of BankService (create, get, delete, stats, tags, templates), runs the delete_bank
// job and the tick's resume_bank_deletes sweep.
type Service struct {
	d Deps
}

// New returns the Service over d, filling the delete bounds' defaults.
func New(d Deps) *Service {
	d.Delete = d.Delete.withDefaults()
	return &Service{d: d}
}

// CreateRequest is a CreateBank or CreateBankFromTemplate request.
type CreateRequest struct {
	// Bank is the bank to create: Ref, Name, and Config (ignored with a Template, which carries its own).
	Bank domain.Bank
	// Template, when set, creates the bank from a template: its config, directives and mental models.
	Template *domain.BankTemplate
}

// CreateBank creates a bank and returns it as stored. Without a template it inserts Bank with its own config
// (an existing bank is domain.ErrAlreadyExists, a tombstoned one domain.ErrBankDeleting) and returns no
// operations. With a template it creates the bank, its directives and its mental models in one transaction and
// returns each model's first-build operation, in template order.
func (s *Service) CreateBank(ctx context.Context, req CreateRequest) (domain.Bank, []domain.Operation, error) {
	if req.Template != nil {
		return s.createFromTemplate(ctx, req.Bank.Ref, req.Bank.Name, *req.Template)
	}
	bank, err := s.createBank(ctx, req.Bank)
	return bank, nil, err
}

// createBank inserts the bank and returns it as stored. A bank being deleted is domain.ErrBankDeleting
// (FAILED_PRECONDITION: writes wait for the delete to finish); an existing one is domain.ErrAlreadyExists with a
// message of its own, never the store's (no SQL text reaches the client).
func (s *Service) createBank(ctx context.Context, bank domain.Bank) (domain.Bank, error) {
	var out domain.Bank
	b := bank.Ref
	exists := fmt.Errorf("%w: bank %s/%s already exists", domain.ErrAlreadyExists, b.Tenant, b.Bank)
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		cur, err := tx.Banks().Get(ctx, b)
		switch {
		case err == nil && cur.Deleting():
			return fmt.Errorf("%w: bank %s/%s", domain.ErrBankDeleting, b.Tenant, b.Bank)
		case err == nil:
			return exists
		case !errors.Is(err, domain.ErrNotFound):
			return err
		}
		err = tx.Banks().Create(ctx, domain.Bank{Ref: b, Name: bank.Name, Config: bank.Config})
		if errors.Is(err, domain.ErrAlreadyExists) {
			return exists // lost a race with a concurrent create
		}
		if err != nil {
			return err
		}
		out, err = tx.Banks().Get(ctx, b)
		return err
	})
	if err != nil {
		return domain.Bank{}, err
	}
	return out, nil
}

// GetBank returns the bank, a tombstoned one included (DeletingAt set); unknown = domain.ErrNotFound.
func (s *Service) GetBank(ctx context.Context, b domain.BankRef) (domain.Bank, error) {
	var out domain.Bank
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		out, err = tx.Banks().Get(ctx, b)
		return err
	})
	if err != nil {
		return domain.Bank{}, err
	}
	return out, nil
}

// liveBank returns bank b for a read: unknown or tombstoned is domain.ErrNotFound (SPEC: every read of a
// deleting bank returns NOT_FOUND).
func liveBank(ctx context.Context, tx store.Tx, b domain.BankRef) (domain.Bank, error) {
	bank, err := tx.Banks().Get(ctx, b)
	if err != nil {
		return domain.Bank{}, err
	}
	if bank.Deleting() {
		return domain.Bank{}, fmt.Errorf("%w %s/%s is being deleted", domain.ErrBankNotFound, b.Tenant, b.Bank)
	}
	return bank, nil
}

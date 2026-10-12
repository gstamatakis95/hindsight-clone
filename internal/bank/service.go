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

// CreateRequest is a CreateBank request.
type CreateRequest struct {
	// Bank is the bank to create: Ref, Name, and Config (ignored with a Template, which carries its own).
	Bank domain.Bank
	// Template, when set, creates the bank from a template: its config, directives and mental models.
	Template *domain.BankTemplate
}

// CreateBank creates a bank and returns it as stored. An existing bank is domain.ErrAlreadyExists and a tombstoned
// one domain.ErrBankDeleting, with or without a template. Without a template it inserts Bank with its own config and
// returns no operations. With a template it creates the bank with the template's config, its directives (new ids)
// and its mental models in one transaction, and returns each model's first-build operation, in template order; a
// template directive without a name, two with one name, or a model without an id is domain.ErrInvalidArgument,
// checked before any write.
func (s *Service) CreateBank(ctx context.Context, req CreateRequest) (domain.Bank, []domain.Operation, error) {
	bank := req.Bank
	var t domain.BankTemplate
	if req.Template != nil {
		var err error
		if t, err = checkTemplate(*req.Template); err != nil {
			return domain.Bank{}, nil, err
		}
		bank.Config = t.Config
	}
	var (
		out domain.Bank
		ops []domain.Operation
	)
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		out, ops = domain.Bank{}, nil
		if err := createRow(ctx, tx, bank); err != nil {
			return err
		}
		if req.Template != nil {
			var err error
			if ops, err = s.applyTemplate(ctx, tx, bank.Ref, t); err != nil {
				return err
			}
		}
		var err error
		out, err = tx.Banks().Get(ctx, bank.Ref)
		return err
	})
	if err != nil {
		return domain.Bank{}, nil, err
	}
	return out, ops, nil
}

// createRow inserts the bank row in tx. A bank being deleted is domain.ErrBankDeleting (FAILED_PRECONDITION: writes
// wait for the delete to finish); an existing one is domain.ErrAlreadyExists with a message of its own, never the
// store's (no SQL text reaches the client).
func createRow(ctx context.Context, tx store.Tx, bank domain.Bank) error {
	b := bank.Ref
	exists := fmt.Errorf("%w: bank %s already exists", domain.ErrAlreadyExists, b)
	cur, err := tx.Banks().Get(ctx, b)
	switch {
	case err == nil && cur.Deleting():
		return fmt.Errorf("%w: bank %s", domain.ErrBankDeleting, b)
	case err == nil:
		return exists
	case !errors.Is(err, domain.ErrNotFound):
		return err
	}
	err = tx.Banks().Create(ctx, domain.Bank{Ref: b, Name: bank.Name, Config: bank.Config})
	if errors.Is(err, domain.ErrAlreadyExists) {
		return exists // lost a race with a concurrent create
	}
	return err
}

// GetBank returns a live bank; an unknown or tombstoned one is domain.ErrBankNotFound (every read of a bank being
// deleted is NOT_FOUND; DeleteBank's operation tells when the delete is done).
func (s *Service) GetBank(ctx context.Context, b domain.BankRef) (domain.Bank, error) {
	var out domain.Bank
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		out, err = liveBank(ctx, tx, b)
		return err
	})
	if err != nil {
		return domain.Bank{}, err
	}
	return out, nil
}

// ListBanksRequest mirrors memory.v1.ListBanksRequest.
type ListBanksRequest struct {
	// Org is the org listed.
	Org domain.OrgRef
	// IDPrefix keeps banks whose id starts with it; empty = all.
	IDPrefix string
	// PageSize caps the page (domain.ListPage); 0 = the default.
	PageSize int
	// PageToken continues a previous listing; empty = first page.
	PageToken string
}

// Page is one page of banks.
type Page struct {
	// Banks are the page's banks, in id order.
	Banks []domain.Bank
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}

// ListBanks returns one keyset page of the org's live banks in byte order of id (tombstoned banks are left out).
// The page size follows domain.ListPage, and the page token carries the last id of the previous page; a malformed
// one is domain.ErrInvalidArgument. An unknown or tombstoned org is domain.ErrOrgNotFound.
func (s *Service) ListBanks(ctx context.Context, req ListBanksRequest) (Page, error) {
	size, err := domain.ListPage.Size(req.PageSize)
	if err != nil {
		return Page{}, fmt.Errorf("list banks: %w", err)
	}
	var tok bankToken
	if req.PageToken != "" {
		if err := domain.DecodePageToken(req.PageToken, &tok); err != nil {
			return Page{}, fmt.Errorf("list banks: %w", err)
		}
	}
	if err := s.d.DB.InTx(ctx, func(tx store.Tx) error { return liveOrg(ctx, tx, req.Org) }); err != nil {
		return Page{}, fmt.Errorf("list banks: %w", err)
	}
	banks, err := s.d.DB.Read().ListBanks(ctx, store.BankQuery{
		Org: req.Org, IDPrefix: req.IDPrefix, After: tok.After, Limit: size + 1,
	})
	if err != nil {
		return Page{}, fmt.Errorf("list banks: %w", err)
	}
	page := Page{Banks: banks}
	if len(banks) > size {
		page.Banks = banks[:size]
		if page.NextPageToken, err = domain.EncodePageToken(bankToken{After: banks[size-1].Ref.Bank}); err != nil {
			return Page{}, fmt.Errorf("list banks: %w", err)
		}
	}
	return page, nil
}

// bankToken is the decoded form of a ListBanks page token.
type bankToken struct {
	// After is the last bank id of the previous page.
	After string `json:"a"`
}

// liveOrg checks org o for a read: unknown or tombstoned is domain.ErrOrgNotFound.
func liveOrg(ctx context.Context, tx store.Tx, o domain.OrgRef) error {
	org, err := tx.Orgs().Get(ctx, o)
	if err != nil {
		return err
	}
	if org.Deleting() {
		return fmt.Errorf("%w %s is being deleted", domain.ErrOrgNotFound, o)
	}
	return nil
}

// liveBank returns bank b for a read: unknown or tombstoned is domain.ErrNotFound (SPEC: every read of a
// deleting bank returns NOT_FOUND).
func liveBank(ctx context.Context, tx store.Tx, b domain.BankRef) (domain.Bank, error) {
	bank, err := tx.Banks().Get(ctx, b)
	if err != nil {
		return domain.Bank{}, err
	}
	if bank.Deleting() {
		return domain.Bank{}, fmt.Errorf("%w %s is being deleted", domain.ErrBankNotFound, b)
	}
	return bank, nil
}

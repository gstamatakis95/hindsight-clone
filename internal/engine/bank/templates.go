package bank

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
)

// TemplateCodec exports a bank's config, directives and model specs, and creates banks from them.
type TemplateCodec interface {
	// Export returns bank b's own config, directives and mental-model specs (no memories); an unknown or
	// tombstoned bank is domain.ErrNotFound.
	Export(ctx context.Context, b port.BankRef) (domain.BankTemplate, error)
	// Create makes bank b from t in one transaction, reassigning directive ids and enqueueing a first
	// build of every mental model; an existing bank is domain.ErrAlreadyExists. It is CreateNamed with
	// an empty name, kept for callers that need only the bank.
	Create(ctx context.Context, b port.BankRef, t domain.BankTemplate) (domain.Bank, error)
	// CreateNamed is Create with the bank's display name; it also returns the first-build operation of
	// every mental model, in template order (CreateBankFromTemplateResponse.mental_model_builds).
	CreateNamed(
		ctx context.Context, b port.BankRef, name string, t domain.BankTemplate,
	) (domain.Bank, []domain.Operation, error)
}

// RefreshQueuer enqueues mental-model refreshes; the mental-model engine (WP-12) owns the refresh payload
// and its scheduling rules, so WP-13 never builds a refresh job itself (decision 1303).
type RefreshQueuer interface {
	// QueueRefresh enqueues (or joins) a refresh of model id of bank b inside tx and returns its operation
	// id. The caller has already called tx.Seq(b).
	QueueRefresh(ctx context.Context, tx port.Tx, b port.BankRef, id port.MentalModelID) (port.OperationID, error)
}

// templateCodec implements TemplateCodec over port.Store.
type templateCodec struct {
	s       port.Store
	refresh RefreshQueuer
}

var _ TemplateCodec = (*templateCodec)(nil)

// NewTemplateCodec returns the TemplateCodec over s (Export lists the model specs with
// Reader.ListMentalModels); refresh enqueues the first build of each created model.
func NewTemplateCodec(s port.Store, refresh RefreshQueuer) TemplateCodec {
	return &templateCodec{s: s, refresh: refresh}
}

// Export reads the bank's own config and directives in one short transaction, then its model specs.
// Directive ids, bank refs and timestamps are dropped: Create reassigns them.
func (c *templateCodec) Export(ctx context.Context, b port.BankRef) (domain.BankTemplate, error) {
	var t domain.BankTemplate
	err := c.s.InTx(ctx, func(tx port.Tx) error {
		t = domain.BankTemplate{}
		bank, err := liveBank(ctx, tx, b)
		if err != nil {
			return err
		}
		dirs, err := tx.Directives().List(ctx, b, false)
		if err != nil {
			return err
		}
		t.Config = bank.Config
		for _, d := range dirs {
			t.Directives = append(t.Directives, portableDirective(d))
		}
		return nil
	})
	if err != nil {
		return domain.BankTemplate{}, err
	}
	models, _, err := c.s.Read().ListMentalModels(ctx, port.ModelQuery{Bank: b})
	if err != nil {
		return domain.BankTemplate{}, fmt.Errorf("export mental-model specs: %w", err)
	}
	for _, m := range models {
		t.MentalModels = append(t.MentalModels, m.Spec)
	}
	return t, nil
}

// portableDirective strips the stored identity of d, keeping what a template carries.
func portableDirective(d domain.Directive) domain.Directive {
	return domain.Directive{
		Name: d.Name, Rules: slices.Clone(d.Rules), Tags: domain.NormalizeTags(d.Tags), Active: d.Active,
	}
}

// Create is CreateNamed without a name, dropping the build operations.
func (c *templateCodec) Create(ctx context.Context, b port.BankRef, t domain.BankTemplate) (domain.Bank, error) {
	bank, _, err := c.CreateNamed(ctx, b, "", t)
	return bank, err
}

// CreateNamed creates the bank, its directives (new ids) and its mental models, and enqueues each model's
// first build, all in one transaction.
func (c *templateCodec) CreateNamed(
	ctx context.Context, b port.BankRef, name string, t domain.BankTemplate,
) (domain.Bank, []domain.Operation, error) {
	specs, err := templateSpecs(t.MentalModels)
	if err != nil {
		return domain.Bank{}, nil, err
	}
	if len(specs) > 0 && c.refresh == nil {
		return domain.Bank{}, nil, fmt.Errorf("%w: no refresh queuer wired for template builds",
			domain.ErrFailedPrecondition)
	}
	var (
		bank domain.Bank
		ops  []domain.Operation
	)
	err = c.s.InTx(ctx, func(tx port.Tx) error {
		bank, ops = domain.Bank{}, nil
		if err := tx.Banks().Create(ctx, domain.Bank{Ref: b, Name: name, Config: t.Config}); err != nil {
			return err
		}
		if _, err := tx.Seq(ctx, b); err != nil {
			return err
		}
		for _, d := range t.Directives {
			d = portableDirective(d)
			d.Bank = b
			if _, err := tx.Directives().Upsert(ctx, d); err != nil {
				return err
			}
		}
		var err error
		if ops, err = c.createModels(ctx, tx, b, specs); err != nil {
			return err
		}
		bank, err = tx.Banks().Get(ctx, b)
		return err
	})
	if err != nil {
		return domain.Bank{}, nil, err
	}
	return bank, ops, nil
}

// createModels creates each model in tx and enqueues its first build, returning the build operations.
func (c *templateCodec) createModels(
	ctx context.Context, tx port.Tx, b port.BankRef, specs []domain.MentalModelSpec,
) ([]domain.Operation, error) {
	ops := make([]domain.Operation, 0, len(specs))
	for _, spec := range specs {
		if err := tx.MentalModels().Create(ctx, domain.MentalModel{Spec: spec, Bank: b}); err != nil {
			return nil, err
		}
		id, err := c.refresh.QueueRefresh(ctx, tx, b, spec.ID)
		if err != nil {
			return nil, fmt.Errorf("queue first build of mental model %s: %w", spec.ID, err)
		}
		op, err := tx.Queue().Get(ctx, b, id)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, nil
}

// templateSpecs validates the template's model specs and normalizes their tags.
func templateSpecs(in []domain.MentalModelSpec) ([]domain.MentalModelSpec, error) {
	out := make([]domain.MentalModelSpec, 0, len(in))
	for _, s := range in {
		if s.ID == "" {
			return nil, fmt.Errorf("%w: template mental model without an id", domain.ErrInvalidArgument)
		}
		s.Tags = domain.NormalizeTags(s.Tags)
		out = append(out, s)
	}
	return out, nil
}

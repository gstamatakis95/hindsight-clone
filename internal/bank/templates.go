package bank

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"slices"
	"strings"
)

// ExportBankTemplate returns bank b's own config, directives and mental-model specs (no memories); an unknown or
// tombstoned bank is domain.ErrNotFound. It reads the config and directives in one short transaction, then the
// model specs. Directive ids, bank refs and timestamps are dropped: a create from the template reassigns them.
func (s *Service) ExportBankTemplate(ctx context.Context, b domain.BankRef) (domain.BankTemplate, error) {
	var t domain.BankTemplate
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
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
	models, _, err := s.d.DB.Read().ListMentalModels(ctx, store.ModelQuery{Bank: b})
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

// applyTemplate creates the template's directives (new ids) and mental models in tx, and enqueues each model's first
// build, returning the build operations. The bank row is new in tx, so the change seq it takes first is the bank's
// first.
func (s *Service) applyTemplate(
	ctx context.Context, tx store.Tx, b domain.BankRef, t domain.BankTemplate,
) ([]domain.Operation, error) {
	if _, err := tx.Seq(ctx, b); err != nil {
		return nil, err
	}
	for _, d := range t.Directives {
		d.Bank = b
		if _, err := tx.Directives().Create(ctx, d); err != nil {
			return nil, err
		}
	}
	return s.createModels(ctx, tx, b, t.MentalModels)
}

// createModels creates each model in tx and enqueues its first build, returning the build operations.
func (s *Service) createModels(
	ctx context.Context, tx store.Tx, b domain.BankRef, specs []domain.MentalModelSpec,
) ([]domain.Operation, error) {
	ops := make([]domain.Operation, 0, len(specs))
	for _, spec := range specs {
		if err := tx.MentalModels().Create(ctx, domain.MentalModel{Spec: spec, Bank: b}); err != nil {
			return nil, err
		}
		id, err := jobs.QueueModelRefresh(ctx, tx, b, spec.ID, s.d.Clock.Now())
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

// checkTemplate validates a template before any write and returns it normalized: directives need a name, unique
// within the template (they become the bank's directives, whose names are unique), and keep only what a template
// carries; mental models need an id and get normalized tags.
func checkTemplate(t domain.BankTemplate) (domain.BankTemplate, error) {
	out := domain.BankTemplate{Config: t.Config}
	names := make(map[string]bool, len(t.Directives))
	for i, d := range t.Directives {
		field := fmt.Sprintf("template.directives[%d].name", i)
		switch {
		case strings.TrimSpace(d.Name) == "":
			return domain.BankTemplate{}, domain.InvalidField(field, "a template directive needs a name")
		case names[d.Name]:
			return domain.BankTemplate{}, domain.InvalidField(field, "directive name %q is used twice", d.Name)
		}
		names[d.Name] = true
		out.Directives = append(out.Directives, portableDirective(d))
	}
	for _, m := range t.MentalModels {
		if m.ID == "" {
			return domain.BankTemplate{}, fmt.Errorf("%w: template mental model without an id", domain.ErrInvalidArgument)
		}
		m.Tags = domain.NormalizeTags(m.Tags)
		out.MentalModels = append(out.MentalModels, m)
	}
	return out, nil
}

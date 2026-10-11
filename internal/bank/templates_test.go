package bank

import (
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"testing"
)

// seedTemplateSource creates src with a config, two directives and two mental models.
func seedTemplateSource(t *testing.T, e env, src domain.BankRef) {
	t.Helper()
	cfg := domain.BankConfig{
		Retain: &domain.RetainConfig{Mission: "remember tea"}, Reflect: &domain.ReflectConfig{Mission: "be brief"},
	}
	e.tx(t, func(tx store.Tx) error {
		if err := tx.Banks().Create(e.ctx, domain.Bank{Ref: src, Name: "Source", Config: cfg}); err != nil {
			return err
		}
		for _, d := range []domain.Directive{
			{Bank: src, Name: "global", Rules: []string{"be polite"}, Active: true},
			{Bank: src, Name: "team", Rules: []string{"cite"}, Tags: []string{"team"}, Active: false},
		} {
			if _, err := tx.Directives().Upsert(e.ctx, d); err != nil {
				return err
			}
		}
		for _, s := range []domain.MentalModelSpec{
			{ID: "tea", Name: "Tea", SourceQuery: "what tea?"},
			{ID: "people", Name: "People", SourceQuery: "who is who?", Tags: []string{"Team"}},
		} {
			if err := tx.MentalModels().Create(e.ctx, domain.MentalModel{Bank: src, Spec: s}); err != nil {
				return err
			}
		}
		return nil
	})
}

// checkExport checks an export carries config, directives without identity and the model specs.
func checkExport(t *testing.T, tmpl domain.BankTemplate) {
	t.Helper()
	if tmpl.Config.GetRetain().Mission != "remember tea" || len(tmpl.Directives) != 2 || len(tmpl.MentalModels) != 2 {
		t.Fatalf("export = %+v", tmpl)
	}
	for _, d := range tmpl.Directives {
		if d.ID != "" || d.Bank != (domain.BankRef{}) {
			t.Errorf("exported directive keeps its identity: %+v", d)
		}
	}
}

// checkBuilds checks one pending refresh_mental_model build per model in bank b.
func checkBuilds(t *testing.T, ops []domain.Operation, b domain.BankRef, n int) {
	t.Helper()
	if len(ops) != n {
		t.Fatalf("builds = %d, want %d", len(ops), n)
	}
	for _, op := range ops {
		if op.Kind != domain.OperationKindRefreshMentalModel || op.Status != domain.OperationStatusPending ||
			op.Bank != b {
			t.Errorf("build op = %+v", op)
		}
	}
}

func TestTemplateRoundTrip(t *testing.T) {
	e := newEnv(t)
	s := e.svc(DeleteConfig{})
	src := domain.BankRef{Tenant: "t", Bank: "src"}
	dst := domain.BankRef{Tenant: "t", Bank: "dst"}
	seedTemplateSource(t, e, src)

	tmpl, err := s.ExportBankTemplate(e.ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	checkExport(t, tmpl)

	req := CreateRequest{Bank: domain.Bank{Ref: dst, Name: "Copy"}, Template: &tmpl}
	bank, ops, err := s.CreateBank(e.ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if bank.Name != "Copy" || bank.Config.GetReflect().Mission != "be brief" {
		t.Errorf("created bank = %+v", bank)
	}
	checkBuilds(t, ops, dst, 2)
	assertTemplateCopied(t, e, src, dst)

	if _, _, err := s.CreateBank(e.ctx, req); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("create over an existing bank: %v, want AlreadyExists", err)
	}
}

// assertTemplateCopied checks dst got src's directives with new ids and the models with normalized tags.
func assertTemplateCopied(t *testing.T, e env, src, dst domain.BankRef) {
	t.Helper()
	from, to := e.directives(t, src), e.directives(t, dst)
	if !slices.Equal(names(from), names(to)) {
		t.Fatalf("copied directives = %v, want %v", names(to), names(from))
	}
	for _, d := range to {
		if slices.ContainsFunc(from, func(x domain.Directive) bool { return x.ID == d.ID }) {
			t.Errorf("directive id %s reused", d.ID)
		}
	}
	e.tx(t, func(tx store.Tx) error {
		m, err := tx.MentalModels().Get(e.ctx, dst, "people")
		if err != nil {
			return err
		}
		if !slices.Equal(m.Spec.Tags, []string{"team"}) {
			t.Errorf("model tags = %v, want normalized [team]", m.Spec.Tags)
		}
		return nil
	})
}

func TestTemplateCreateRollsBackOnBadSpec(t *testing.T) {
	e := newEnv(t)
	s := e.svc(DeleteConfig{})
	b := domain.BankRef{Tenant: "t", Bank: "b"}
	create := func(specs ...domain.MentalModelSpec) error {
		_, _, err := s.CreateBank(e.ctx, CreateRequest{
			Bank: domain.Bank{Ref: b, Name: "x"}, Template: &domain.BankTemplate{MentalModels: specs},
		})
		return err
	}
	if err := create(domain.MentalModelSpec{}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("spec without id: %v", err)
	}
	if err := create(domain.MentalModelSpec{ID: "a"}, domain.MentalModelSpec{ID: "a"}); !errors.Is(err,
		domain.ErrAlreadyExists) {
		t.Fatalf("duplicate model ids: %v", err)
	}
	if _, err := s.GetBank(e.ctx, b); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a failed create left the bank behind: %v", err)
	}
}

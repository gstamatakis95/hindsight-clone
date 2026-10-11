package main

import (
	"context"
	"fmt"
	"memd/examples/internal/exampleutil"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "memd/gen/memory/v1"
)

func main() {
	exampleutil.Run(func(ctx context.Context, c *exampleutil.Client) error {
		suffix := exampleutil.UniqueID("")
		s := &scenario{root: c, acme: c.As("acme" + suffix), globex: c.As("globex" + suffix)}
		return exampleutil.Steps(ctx, s.tenantConfig, s.createFromConfig, s.createFromTemplate, s.isolation)
	})
}

type scenario struct {
	root, acme, globex *exampleutil.Client
	template           *pb.BankTemplate
}

// tenantConfig gives acme a skeptical assistant by default; globex keeps the server defaults.
func (s *scenario) tenantConfig(ctx context.Context) error {
	exampleutil.Section("tenant config")
	if _, err := s.acme.Config.UpdateTenantConfig(ctx, &pb.UpdateTenantConfigRequest{
		Config: &pb.BankConfig{Reflect: &pb.ReflectConfig{
			Disposition: &pb.Disposition{Skepticism: 5, Literalism: 3, Empathy: 2},
		}},
		UpdateMask: exampleutil.Paths("reflect.disposition"),
	}); err != nil {
		return fmt.Errorf("update tenant config: %w", err)
	}
	for _, t := range []*exampleutil.Client{s.acme, s.globex} {
		resp, err := t.Config.GetTenantConfig(ctx, &pb.GetTenantConfigRequest{})
		if err != nil {
			return fmt.Errorf("get tenant config: %w", err)
		}
		fmt.Printf("%s: own skepticism=%d, effective skepticism=%d\n", t.Tenant,
			resp.GetConfig().GetOwn().GetReflect().GetDisposition().GetSkepticism(),
			resp.GetConfig().GetEffective().GetReflect().GetDisposition().GetSkepticism())
	}
	return nil
}

// createFromConfig creates acme's support bank with its own mission and a standing directive, then exports it.
func (s *scenario) createFromConfig(ctx context.Context) error {
	exampleutil.Section("a bank with its own mission, exported as a template")
	if _, err := s.acme.Banks.CreateBank(ctx, &pb.CreateBankRequest{
		BankId: "support", Name: "Support desk",
		Initial: &pb.CreateBankRequest_Config{Config: &pb.BankConfig{
			Retain: &pb.RetainConfig{Mission: "Extract customer problems, product names and promised follow-ups."},
			Reflect: &pb.ReflectConfig{
				Mission:     "You help support agents prepare for a call.",
				Disposition: &pb.Disposition{Empathy: 5},
			},
		}},
	}); err != nil {
		return fmt.Errorf("create bank: %w", err)
	}
	if _, err := s.acme.Config.CreateDirective(ctx, &pb.CreateDirectiveRequest{
		BankId: "support",
		Directive: &pb.Directive{
			Name: "no-refund-promises", Rules: []string{"Never promise a refund; say a human will confirm."}, Active: true,
		},
	}); err != nil {
		return fmt.Errorf("create directive: %w", err)
	}
	exported, err := s.acme.Banks.ExportBankTemplate(ctx, &pb.ExportBankTemplateRequest{BankId: "support"})
	if err != nil {
		return fmt.Errorf("export template: %w", err)
	}
	s.template = exported.GetTemplate()
	fmt.Printf("template: mission=%q, %d directive(s)\n",
		s.template.GetConfig().GetReflect().GetMission(), len(s.template.GetDirectives()))
	return nil
}

// createFromTemplate stamps out the same bank for globex, which then tunes the mission for itself.
func (s *scenario) createFromTemplate(ctx context.Context) error {
	exampleutil.Section("the same template in another tenant")
	if _, err := s.globex.Banks.CreateBank(ctx, &pb.CreateBankRequest{
		BankId: "support", Name: "Globex support", Initial: &pb.CreateBankRequest_Template{Template: s.template},
	}); err != nil {
		return fmt.Errorf("create bank from template: %w", err)
	}
	if _, err := s.globex.Config.UpdateBankConfig(ctx, &pb.UpdateBankConfigRequest{
		BankId:     "support",
		Config:     &pb.BankConfig{Reflect: &pb.ReflectConfig{Mission: "You help support agents of a logistics company."}},
		UpdateMask: exampleutil.Paths("reflect.mission"),
	}); err != nil {
		return fmt.Errorf("update bank config: %w", err)
	}
	for _, t := range []*exampleutil.Client{s.acme, s.globex} {
		resp, err := t.Config.GetBankConfig(ctx, &pb.GetBankConfigRequest{BankId: "support"})
		if err != nil {
			return fmt.Errorf("get bank config: %w", err)
		}
		r := resp.GetConfig().GetEffective().GetReflect()
		fmt.Printf("%s/support: mission=%q empathy=%d skepticism=%d\n", t.Tenant, r.GetMission(),
			r.GetDisposition().GetEmpathy(), r.GetDisposition().GetSkepticism())
	}
	return nil
}

// isolation shows that one bank id holds different memories per tenant and a tenant cannot read another's banks.
func (s *scenario) isolation(ctx context.Context) error {
	exampleutil.Section("isolation")
	if _, err := s.acme.Memory.Retain(ctx, &pb.RetainRequest{BankId: "support", Items: []*pb.RetainItem{
		{DocumentId: "ticket-1", Content: "Customer Rivera reported that the export button crashes the app."},
	}}); err != nil {
		return fmt.Errorf("retain: %w", err)
	}
	for _, t := range []*exampleutil.Client{s.acme, s.globex} {
		stats, err := t.Banks.GetBankStats(ctx, &pb.GetBankStatsRequest{BankId: "support"})
		if err != nil {
			return fmt.Errorf("stats: %w", err)
		}
		fmt.Printf("%s/support: %d documents\n", t.Tenant, stats.GetStats().GetDocuments())
	}
	_, err := s.globex.Banks.GetBank(ctx, &pb.GetBankRequest{BankId: "acme-only"})
	fmt.Println("a bank the tenant does not have:", status.Code(err))
	if status.Code(err) != codes.NotFound {
		return fmt.Errorf("expected NOT_FOUND for an unknown bank, got %w", err)
	}
	_, err = s.root.As("").Banks.ListBanks(ctx, &pb.ListBanksRequest{})
	fmt.Println("a call without a tenant:", status.Code(err))
	if status.Code(err) != codes.InvalidArgument {
		return fmt.Errorf("expected INVALID_ARGUMENT without x-tenant-id, got %w", err)
	}
	return exampleutil.WaitIdle(ctx, s.acme, "support")
}

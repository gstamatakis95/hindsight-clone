package server_test

import (
	"context"
	"memd/internal/domain"
	"memd/internal/engine/bank"
	"memd/internal/engine/mentalmodel"
	"memd/internal/server"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	mockserver "memd/internal/mocks/server"

	pb "memd/gen/memory/v1"
)

// patchStore stands in for the bank, tenant and mental-model engines: it runs each patch the server
// builds against a stored value and keeps the result, the way the engines do inside their transaction.
type patchStore struct {
	// mu guards every field below.
	mu sync.Mutex
	// cfg is the stored bank overrides / tenant config.
	cfg domain.BankConfig
	// spec is the stored mental-model spec.
	spec domain.MentalModelSpec
	// gotCfg is the last patched config.
	gotCfg domain.BankConfig
	// gotSpec is the last patched spec.
	gotSpec domain.MentalModelSpec
	// created counts CreateBank and CreateNamed calls that reached the engine.
	created int
}

// wire installs engine stand-ins that run the patches.
func (p *patchStore) wire(d *server.Deps) {
	d.Banks = &mockserver.BankAdmin{
		CreateBankFunc: func(_ context.Context, b domain.Bank) (domain.Bank, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.created++
			return b, nil
		},
		UpdateBankConfigFunc: func(_ context.Context, b domain.BankRef, patch bank.ConfigPatch) (domain.Bank, error) {
			c, err := p.runConfig(patch)
			return domain.Bank{Ref: b, Config: c}, err
		},
	}
	d.Tenants = &mockserver.TenantAdmin{
		UpdateTenantConfigFunc: func(_ context.Context, _ string, patch bank.ConfigPatch) (domain.BankConfig, error) {
			return p.runConfig(patch)
		},
	}
	d.MentalModels = &mockserver.MentalModelAdmin{
		UpdateMentalModelFunc: func(
			_ context.Context, b domain.BankRef, _ domain.MentalModelID, patch mentalmodel.SpecPatch,
		) (domain.MentalModel, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			s, err := patch(p.spec)
			p.gotSpec = s
			return domain.MentalModel{Spec: s, Bank: b}, err
		},
	}
}

// runConfig applies patch to the stored config and records the result.
func (p *patchStore) runConfig(patch bank.ConfigPatch) (domain.BankConfig, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, err := patch(p.cfg)
	p.gotCfg = c
	return c, err
}

// invalidConfigs are configs every config write must reject with INVALID_ARGUMENT.
func invalidConfigs() map[string]*pb.BankConfig {
	return map[string]*pb.BankConfig{
		"embed binding": {Models: &pb.ModelBindings{Embed: &pb.ModelBinding{Model: "e"}}},
		"no model":      {Models: &pb.ModelBindings{Extract: &pb.ModelBinding{}}},
		"disposition 9": {Reflect: &pb.ReflectConfig{Disposition: &pb.Disposition{Skepticism: 9}}},
		"duplicate strategy names": {Observations: &pb.ObservationConfig{Strategies: []*pb.ConsolidationStrategy{
			{Name: "s"}, {Name: "s"},
		}}},
	}
}

// configWriters sends one whole config through CreateBank, UpdateBankConfig (no mask and a models
// mask), UpdateTenantConfig and CreateBankFromTemplate.
func configWriters() map[string]func(context.Context, *harness, *pb.BankConfig) *status.Status {
	whole := &fieldmaskpb.FieldMask{Paths: []string{"models", "reflect.disposition", "observations.strategies"}}
	return map[string]func(context.Context, *harness, *pb.BankConfig) *status.Status{
		"CreateBank": func(ctx context.Context, h *harness, c *pb.BankConfig) *status.Status {
			_, err := pb.NewBankServiceClient(h.conn).CreateBank(ctx, &pb.CreateBankRequest{BankId: "b", Config: c})
			return status.Convert(err)
		},
		"UpdateBankConfig": func(ctx context.Context, h *harness, c *pb.BankConfig) *status.Status {
			_, err := pb.NewBankServiceClient(h.conn).UpdateBankConfig(ctx, &pb.UpdateBankConfigRequest{
				BankId: "b", Config: c,
			})
			return status.Convert(err)
		},
		"UpdateBankConfig/masked": func(ctx context.Context, h *harness, c *pb.BankConfig) *status.Status {
			_, err := pb.NewBankServiceClient(h.conn).UpdateBankConfig(ctx, &pb.UpdateBankConfigRequest{
				BankId: "b", Config: c, UpdateMask: whole,
			})
			return status.Convert(err)
		},
		"UpdateTenantConfig": func(ctx context.Context, h *harness, c *pb.BankConfig) *status.Status {
			_, err := pb.NewTenantServiceClient(h.conn).UpdateTenantConfig(ctx, &pb.UpdateTenantConfigRequest{Config: c})
			return status.Convert(err)
		},
		"CreateBankFromTemplate": func(ctx context.Context, h *harness, c *pb.BankConfig) *status.Status {
			_, err := pb.NewBankServiceClient(h.conn).CreateBankFromTemplate(ctx, &pb.CreateBankFromTemplateRequest{
				BankId: "b", Template: &pb.BankTemplate{Config: c},
			})
			return status.Convert(err)
		},
	}
}

// TestConfigWritesValidated covers Accept 4: each invalid config is INVALID_ARGUMENT on every config
// write, and a create never reaches the engine; a valid config passes them all.
func TestConfigWritesValidated(t *testing.T) {
	for rpc, send := range configWriters() {
		for name, c := range invalidConfigs() {
			t.Run(rpc+"/"+name, func(t *testing.T) {
				p := &patchStore{cfg: domain.BankConfig{Models: &domain.ModelBindings{
					Extract: &domain.ModelBinding{Model: "s"},
				}}}
				h := newHarness(t, p.wire)
				st := send(tenantCtx(t), h, c)
				if st.Code() != codes.InvalidArgument {
					t.Fatalf("status = %v, want INVALID_ARGUMENT", st)
				}
				p.mu.Lock()
				created := p.created
				p.mu.Unlock()
				if _, called := h.calls.args("TemplateCodec.CreateNamed"); called || created != 0 {
					t.Fatal("invalid config reached the engine")
				}
			})
		}
		t.Run(rpc+"/valid", func(t *testing.T) {
			p := &patchStore{}
			h := newHarness(t, p.wire)
			valid := &pb.BankConfig{
				Models:  &pb.ModelBindings{Default: &pb.ModelBinding{Model: "m"}},
				Reflect: &pb.ReflectConfig{Disposition: &pb.Disposition{Skepticism: 5, Literalism: 1, Empathy: 3}},
				Observations: &pb.ObservationConfig{Strategies: []*pb.ConsolidationStrategy{{
					Name: "s", ConsolidateModel: &pb.ModelBinding{Model: "m"},
				}}},
			}
			if st := send(tenantCtx(t), h, valid); st.Code() != codes.OK {
				t.Fatalf("valid config: %v", st)
			}
		})
	}
}

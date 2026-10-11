package server_test

import (
	"context"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/engine/bank"
	"memd/internal/engine/mentalmodel"
	"memd/internal/server"
	"reflect"
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

// wire installs the real validator and engine stand-ins that run the patches.
func (p *patchStore) wire(d *server.Deps) {
	d.ValidateOverrides = config.ValidateOverrides
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

// results returns the last patched config and spec.
func (p *patchStore) results() (domain.BankConfig, domain.MentalModelSpec) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gotCfg, p.gotSpec
}

// sb builds a wire slot binding.
func sb(slot pb.ModelSlot, model string) *pb.SlotBinding {
	return &pb.SlotBinding{Slot: slot, Binding: &pb.ModelBinding{Model: model}}
}

// dsb builds a domain slot binding.
func dsb(slot domain.ModelSlot, model string) domain.SlotBinding {
	return domain.SlotBinding{Slot: slot, Binding: domain.ModelBinding{Model: model}}
}

// slotMap maps each bound slot to its model; nil models map to nil (inherit), empty to empty (clear).
func slotMap(models []domain.SlotBinding) map[domain.ModelSlot]string {
	if models == nil {
		return nil
	}
	out := map[domain.ModelSlot]string{}
	for _, m := range models {
		out[m.Slot] = m.Binding.Model
	}
	return out
}

// modelsWrite is one models write, sent through one of the three patch RPCs.
type modelsWrite struct {
	// models is the request's model list.
	models []*pb.SlotBinding
	// mask is the update mask (nil = implicit).
	mask []string
}

// configRPC sends a models write through UpdateBankConfig, UpdateTenantConfig or UpdateMentalModel
// (spec.overrides.models) and returns the patched model list.
type configRPC struct {
	// name labels the subtest.
	name string
	// call sends w and returns the patched model list.
	call func(t *testing.T, h *harness, p *patchStore, w modelsWrite) []domain.SlotBinding
}

// patchRPCs returns UpdateBankConfig, UpdateTenantConfig and UpdateMentalModel as configRPCs; the
// mental-model one prefixes mask paths with "overrides.".
func patchRPCs() []configRPC {
	cfgMask := func(w modelsWrite) *fieldmaskpb.FieldMask {
		if w.mask == nil {
			return nil
		}
		return &fieldmaskpb.FieldMask{Paths: w.mask}
	}
	return []configRPC{
		{name: "UpdateBankConfig", call: func(t *testing.T, h *harness, p *patchStore, w modelsWrite) []domain.SlotBinding {
			_, err := pb.NewBankServiceClient(h.conn).UpdateBankConfig(tenantCtx(t), &pb.UpdateBankConfigRequest{
				BankId: "b", Config: &pb.BankConfig{Models: w.models}, UpdateMask: cfgMask(w),
			})
			if err != nil {
				t.Fatal(err)
			}
			c, _ := p.results()
			return c.Models
		}},
		{name: "UpdateTenantConfig", call: func(t *testing.T, h *harness, p *patchStore, w modelsWrite) []domain.SlotBinding {
			_, err := pb.NewTenantServiceClient(h.conn).UpdateTenantConfig(tenantCtx(t),
				&pb.UpdateTenantConfigRequest{Config: &pb.BankConfig{Models: w.models}, UpdateMask: cfgMask(w)})
			if err != nil {
				t.Fatal(err)
			}
			c, _ := p.results()
			return c.Models
		}},
		{name: "UpdateMentalModel", call: func(t *testing.T, h *harness, p *patchStore, w modelsWrite) []domain.SlotBinding {
			var m *fieldmaskpb.FieldMask
			if w.mask != nil {
				m = &fieldmaskpb.FieldMask{}
				for _, path := range w.mask {
					m.Paths = append(m.Paths, "overrides."+path)
				}
			}
			_, err := pb.NewMentalModelServiceClient(h.conn).UpdateMentalModel(tenantCtx(t),
				&pb.UpdateMentalModelRequest{
					BankId: "b", MentalModelId: "m",
					Spec: &pb.MentalModelSpec{Overrides: &pb.BankConfig{Models: w.models}}, UpdateMask: m,
				})
			if err != nil {
				t.Fatal(err)
			}
			_, s := p.results()
			if s.Overrides == nil {
				return nil
			}
			return s.Overrides.Models
		}},
	}
}

// newPatchHarness starts a harness whose engines run patches over stored values: the bank and tenant
// config bind EXTRACT; with specOverrides the spec's overrides bind SYNTHESIZE (a slot mental-model
// overrides may use) and set reflect_mission, otherwise the spec has no overrides.
func newPatchHarness(t *testing.T, specOverrides bool) (*harness, *patchStore) {
	t.Helper()
	p := &patchStore{
		cfg:  domain.BankConfig{Models: []domain.SlotBinding{dsb(domain.ModelSlotExtract, "small")}},
		spec: domain.MentalModelSpec{ID: "m", Name: "keep"},
	}
	if specOverrides {
		p.spec.Overrides = &domain.BankConfig{
			ReflectMission: "keep", Models: []domain.SlotBinding{dsb(domain.ModelSlotSynthesize, "small")},
		}
	}
	return newHarness(t, p.wire), p
}

// TestConfigWriteModelsMergePerSlot covers Accept 1 and 3: with no mask, binding REFLECT keeps the stored
// binding of every other slot, for UpdateBankConfig, UpdateTenantConfig and UpdateMentalModel
// (spec.overrides.models, with stored overrides and with none).
func TestConfigWriteModelsMergePerSlot(t *testing.T) {
	write := modelsWrite{models: []*pb.SlotBinding{sb(pb.ModelSlot_MODEL_SLOT_REFLECT, "big")}}
	for _, rpc := range patchRPCs() {
		t.Run(rpc.name, func(t *testing.T) {
			h, p := newPatchHarness(t, true)
			got := slotMap(rpc.call(t, h, p, write))
			want := map[domain.ModelSlot]string{domain.ModelSlotExtract: "small", domain.ModelSlotReflect: "big"}
			if rpc.name == "UpdateMentalModel" {
				want = map[domain.ModelSlot]string{domain.ModelSlotSynthesize: "small", domain.ModelSlotReflect: "big"}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("models = %v, want %v", got, want)
			}
		})
	}
	t.Run("UpdateMentalModel/no stored overrides", func(t *testing.T) {
		h, p := newPatchHarness(t, false)
		got := slotMap(patchRPCs()[2].call(t, h, p, write))
		if want := map[domain.ModelSlot]string{domain.ModelSlotReflect: "big"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("models = %v, want %v", got, want)
		}
	})
	t.Run("UpdateMentalModel/other overrides kept", func(t *testing.T) {
		h, p := newPatchHarness(t, true)
		patchRPCs()[2].call(t, h, p, write)
		if _, s := p.results(); s.Overrides == nil || s.Overrides.ReflectMission != "keep" || s.Name != "keep" {
			t.Fatalf("patched spec lost untouched fields: %+v", s)
		}
	})
}

// TestConfigWriteModelsExplicitMask covers Accept 2 (and its mental-model twin): an explicit models path
// replaces the whole list, and an empty list under it clears it (a non-nil empty list).
func TestConfigWriteModelsExplicitMask(t *testing.T) {
	cases := []struct {
		name string
		w    modelsWrite
		want map[domain.ModelSlot]string
	}{
		{
			"replace",
			modelsWrite{models: []*pb.SlotBinding{sb(pb.ModelSlot_MODEL_SLOT_REFLECT, "big")}, mask: []string{"models"}},
			map[domain.ModelSlot]string{domain.ModelSlotReflect: "big"},
		},
		{"clear", modelsWrite{mask: []string{"models"}}, map[domain.ModelSlot]string{}},
	}
	for _, rpc := range patchRPCs() {
		for _, c := range cases {
			t.Run(rpc.name+"/"+c.name, func(t *testing.T) {
				h, p := newPatchHarness(t, true)
				got := slotMap(rpc.call(t, h, p, c.w))
				if !reflect.DeepEqual(got, c.want) {
					t.Fatalf("models = %v, want %v", got, c.want)
				}
			})
		}
	}
}

// invalidConfigs are the whole-config writes every config RPC must reject (Accept 4).
func invalidConfigs() map[string]*pb.BankConfig {
	return map[string]*pb.BankConfig{
		"EMBED binding": {Models: []*pb.SlotBinding{sb(pb.ModelSlot_MODEL_SLOT_EMBED, "e")}},
		"no model":      {Models: []*pb.SlotBinding{sb(pb.ModelSlot_MODEL_SLOT_EXTRACT, "")}},
		"duplicate slot": {Models: []*pb.SlotBinding{
			sb(pb.ModelSlot_MODEL_SLOT_REFLECT, "a"), sb(pb.ModelSlot_MODEL_SLOT_REFLECT, "b"),
		}},
		"disposition 9": {Disposition: &pb.Disposition{Skepticism: 9}},
		"strategy bound to REFLECT": {ConsolidationStrategies: []*pb.ConsolidationStrategy{{
			Name: "s", Models: []*pb.SlotBinding{sb(pb.ModelSlot_MODEL_SLOT_REFLECT, "m")},
		}}},
	}
}

// configWriters sends one whole config through CreateBank, UpdateBankConfig (no mask and a models
// mask), UpdateTenantConfig and CreateBankFromTemplate.
func configWriters() map[string]func(context.Context, *harness, *pb.BankConfig) *status.Status {
	whole := &fieldmaskpb.FieldMask{Paths: []string{
		"models", "disposition", "consolidation_strategies",
	}}
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
				p := &patchStore{cfg: domain.BankConfig{Models: []domain.SlotBinding{dsb(domain.ModelSlotExtract, "s")}}}
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
				Models:      []*pb.SlotBinding{sb(pb.ModelSlot_MODEL_SLOT_REFLECT, "m")},
				Disposition: &pb.Disposition{Skepticism: 5, Literalism: 1, Empathy: 3},
				ConsolidationStrategies: []*pb.ConsolidationStrategy{{
					Name: "s", Models: []*pb.SlotBinding{sb(pb.ModelSlot_MODEL_SLOT_CONSOLIDATE, "m")},
				}},
			}
			if st := send(tenantCtx(t), h, valid); st.Code() != codes.OK {
				t.Fatalf("valid config: %v", st)
			}
		})
	}
}

// TestUpdateMentalModelDuplicateSlot checks a request binding one override slot twice is rejected even
// though the per-slot merge would collapse the duplicate.
func TestUpdateMentalModelDuplicateSlot(t *testing.T) {
	h, _ := newPatchHarness(t, true)
	_, err := pb.NewMentalModelServiceClient(h.conn).UpdateMentalModel(tenantCtx(t), &pb.UpdateMentalModelRequest{
		BankId: "b", MentalModelId: "m", Spec: &pb.MentalModelSpec{Overrides: &pb.BankConfig{Models: []*pb.SlotBinding{
			sb(pb.ModelSlot_MODEL_SLOT_REFLECT, "a"), sb(pb.ModelSlot_MODEL_SLOT_REFLECT, "b"),
		}}},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want INVALID_ARGUMENT", err)
	}
}

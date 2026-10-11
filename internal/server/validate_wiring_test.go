package server_test

import (
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/server"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "memd/gen/memory/v1"
)

// TestRealValidatorAcceptsServerRPCNames wires the real config.ValidateOverrides (as cmd/memd does) and
// calls every RPC that validates overrides with one override that RPC may use. Each must pass: the rpc
// name the server hands the validator has to be one config knows. A forbidden override must still fail.
func TestRealValidatorAcceptsServerRPCNames(t *testing.T) {
	h := newHarness(t, func(d *server.Deps) { d.ValidateOverrides = config.ValidateOverrides })
	ctx := tenantCtx(t)
	reflectO := &pb.BankConfig{ReflectMission: "m"}
	retainO := &pb.BankConfig{RetainMission: "m"}
	rrf := int32(5)
	threshold := 0.5
	spec := &pb.MentalModelSpec{Id: "m", Overrides: reflectO}
	calls := map[string]func() error{
		"Retain": func() error {
			_, err := pb.NewMemoryServiceClient(h.conn).Retain(ctx, &pb.RetainRequest{
				BankId: "x", Overrides: retainO, Items: []*pb.RetainItem{{Content: "c", Overrides: retainO}},
			})
			return err
		},
		"Recall": func() error {
			_, err := pb.NewMemoryServiceClient(h.conn).Recall(ctx, &pb.RecallRequest{
				BankId: "x", Query: "q", Overrides: &pb.BankConfig{Recall: &pb.RecallConfig{RrfK: &rrf}},
			})
			return err
		},
		"Reflect": func() error {
			_, err := pb.NewMemoryServiceClient(h.conn).Reflect(ctx, &pb.ReflectRequest{
				BankId: "x", Query: "q", Overrides: reflectO,
			})
			return err
		},
		"UpdateMemory": func() error {
			_, err := pb.NewMemoryUnitServiceClient(h.conn).UpdateMemory(ctx, &pb.UpdateMemoryRequest{
				BankId: "x", MemoryId: "u", Overrides: &pb.BankConfig{
					Retain: &pb.RetainTuning{EntityMatchThreshold: &threshold},
				},
			})
			return err
		},
		"Consolidate": func() error {
			_, err := pb.NewObservationServiceClient(h.conn).Consolidate(ctx, &pb.ConsolidateRequest{
				BankId: "x", Overrides: &pb.BankConfig{ObservationsMission: "m"},
			})
			return err
		},
		"ReprocessDocument": func() error {
			_, err := pb.NewDocumentServiceClient(h.conn).ReprocessDocument(ctx, &pb.ReprocessDocumentRequest{
				BankId: "x", DocumentId: "d", Overrides: retainO,
			})
			return err
		},
		"PreviewPrompt": func() error {
			_, err := pb.NewBankServiceClient(h.conn).PreviewPrompt(ctx, &pb.PreviewPromptRequest{
				BankId: "x", Operation: pb.PromptOperation_PROMPT_OPERATION_REFLECT, Overrides: reflectO,
			})
			return err
		},
		"CreateMentalModel": func() error {
			_, err := pb.NewMentalModelServiceClient(h.conn).CreateMentalModel(ctx, &pb.CreateMentalModelRequest{
				BankId: "x", Spec: spec,
			})
			return err
		},
		"RefreshMentalModel": func() error {
			_, err := pb.NewMentalModelServiceClient(h.conn).RefreshMentalModel(ctx, &pb.RefreshMentalModelRequest{
				BankId: "x", MentalModelId: "m", Overrides: reflectO,
			})
			return err
		},
		"CreateBankFromTemplate": func() error {
			_, err := pb.NewBankServiceClient(h.conn).CreateBankFromTemplate(ctx,
				&pb.CreateBankFromTemplateRequest{
					BankId: "x", Template: &pb.BankTemplate{MentalModels: []*pb.MentalModelSpec{spec}},
				})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); err != nil {
			t.Errorf("%s with an allowed override: %v", name, err)
		}
	}
	_, err := pb.NewMemoryServiceClient(h.conn).Recall(ctx, &pb.RecallRequest{
		BankId: "x", Query: "q", Overrides: retainO,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("Recall with retain_mission: %v, want INVALID_ARGUMENT", err)
	}
	h.calls.reset()
	_, err = pb.NewMentalModelServiceClient(h.conn).UpdateMentalModel(ctx, &pb.UpdateMentalModelRequest{
		BankId: "x", MentalModelId: "m", Spec: &pb.MentalModelSpec{Overrides: reflectO},
	})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := h.calls.args("MentalModelAdmin.UpdateMentalModel")
	if _, err := args[2].(server.SpecPatch)(domain.MentalModelSpec{ID: "m"}); err != nil {
		t.Errorf("UpdateMentalModel patch with an allowed override: %v", err)
	}
}

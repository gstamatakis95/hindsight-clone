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

// wantOK fails the test when an RPC with an allowed override returned an error.
func wantOK(t *testing.T, rpc string, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("%s with an allowed override: %v", rpc, err)
	}
}

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
	memory := pb.NewMemoryServiceClient(h.conn)
	models := pb.NewMentalModelServiceClient(h.conn)
	banks := pb.NewBankServiceClient(h.conn)

	_, err := memory.Retain(ctx, &pb.RetainRequest{
		BankId: "x", Overrides: retainO, Items: []*pb.RetainItem{{Content: "c", Overrides: retainO}},
	})
	wantOK(t, "Retain", err)
	_, err = memory.Recall(ctx, &pb.RecallRequest{
		BankId: "x", Query: "q", Overrides: &pb.BankConfig{Recall: &pb.RecallConfig{RrfK: &rrf}},
	})
	wantOK(t, "Recall", err)
	_, err = memory.Reflect(ctx, &pb.ReflectRequest{BankId: "x", Query: "q", Overrides: reflectO})
	wantOK(t, "Reflect", err)
	_, err = pb.NewMemoryUnitServiceClient(h.conn).UpdateMemory(ctx, &pb.UpdateMemoryRequest{
		BankId: "x", MemoryId: "u",
		Overrides: &pb.BankConfig{Retain: &pb.RetainTuning{EntityMatchThreshold: &threshold}},
	})
	wantOK(t, "UpdateMemory", err)
	_, err = pb.NewObservationServiceClient(h.conn).Consolidate(ctx, &pb.ConsolidateRequest{
		BankId: "x", Overrides: &pb.BankConfig{ObservationsMission: "m"},
	})
	wantOK(t, "Consolidate", err)
	_, err = pb.NewDocumentServiceClient(h.conn).ReprocessDocument(ctx, &pb.ReprocessDocumentRequest{
		BankId: "x", DocumentId: "d", Overrides: retainO,
	})
	wantOK(t, "ReprocessDocument", err)
	_, err = banks.PreviewPrompt(ctx, &pb.PreviewPromptRequest{
		BankId: "x", Operation: pb.PromptOperation_PROMPT_OPERATION_REFLECT, Overrides: reflectO,
	})
	wantOK(t, "PreviewPrompt", err)
	_, err = models.CreateMentalModel(ctx, &pb.CreateMentalModelRequest{BankId: "x", Spec: spec})
	wantOK(t, "CreateMentalModel", err)
	_, err = models.RefreshMentalModel(ctx, &pb.RefreshMentalModelRequest{
		BankId: "x", MentalModelId: "m", Overrides: reflectO,
	})
	wantOK(t, "RefreshMentalModel", err)
	_, err = banks.CreateBankFromTemplate(ctx, &pb.CreateBankFromTemplateRequest{
		BankId: "x", Template: &pb.BankTemplate{MentalModels: []*pb.MentalModelSpec{spec}},
	})
	wantOK(t, "CreateBankFromTemplate", err)

	_, err = memory.Recall(ctx, &pb.RecallRequest{BankId: "x", Query: "q", Overrides: retainO})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("Recall with retain_mission: %v, want INVALID_ARGUMENT", err)
	}
	h.calls.reset()
	_, err = models.UpdateMentalModel(ctx, &pb.UpdateMentalModelRequest{
		BankId: "x", MentalModelId: "m", Spec: &pb.MentalModelSpec{Overrides: reflectO},
	})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := h.calls.args("MentalModelAdmin.UpdateMentalModel")
	_, err = args[2].(server.SpecPatch)(domain.MentalModelSpec{ID: "m"})
	wantOK(t, "UpdateMentalModel (patch)", err)
}

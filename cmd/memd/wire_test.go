package main

import (
	"testing"

	pb "memd/gen/memory/v1"
)

// TestWiredStackNoModelCallInsideTransaction runs the composition root's whole graph (every engine as
// wire.go builds it, over internal/fake and the emulated gateway) through retain, consolidation, recall,
// reflect, both refresh strategies and curation, and checks that no gateway call was ever made while a
// transaction was open (SPEC "Jobs": no model calls inside a transaction).
func TestWiredStackNoModelCallInsideTransaction(t *testing.T) {
	w := startWired(t)
	const bank = "b"
	mem := pb.NewMemoryServiceClient(w.conn)
	models := pb.NewMentalModelServiceClient(w.conn)
	ret, err := mem.Retain(w.ctx(t), &pb.RetainRequest{BankId: bank, Items: []*pb.RetainItem{{
		Content: "Alice moved to Lisbon in 2021. Alice works as a nurse.", DocumentId: "d1",
	}}})
	if err != nil || ret.GetDocuments()[0].GetMemoriesCreated() == 0 {
		t.Fatalf("retain: %v %v", ret, err)
	}
	w.idle(t, bank)
	obs, err := mem.ListMemories(w.ctx(t), &pb.ListMemoriesRequest{
		BankId: bank, FactTypes: []pb.FactType{pb.FactType_FACT_TYPE_OBSERVATION},
	})
	if err != nil || len(obs.GetMemories()) == 0 {
		t.Fatalf("observations: %v %v", obs, err)
	}
	rec, err := mem.Recall(w.ctx(t), &pb.RecallRequest{BankId: bank, Query: "Where does Alice live? Lisbon"})
	if err != nil || len(rec.GetResults()) == 0 {
		t.Fatalf("recall: %v %v", rec, err)
	}
	ref, err := mem.Reflect(w.ctx(t), &pb.ReflectRequest{BankId: bank, Query: "Where does Alice live?"})
	if err != nil || ref.GetText() == "" {
		t.Fatalf("reflect: %v %v", ref, err)
	}
	w.mentalModelBothStrategies(t, models, bank)
	facts, err := mem.ListMemories(w.ctx(t), &pb.ListMemoriesRequest{
		BankId: bank, FactTypes: []pb.FactType{pb.FactType_FACT_TYPE_WORLD, pb.FactType_FACT_TYPE_EXPERIENCE},
	})
	if err != nil || len(facts.GetMemories()) == 0 {
		t.Fatalf("facts: %v %v", facts, err)
	}
	fact := facts.GetMemories()[0].GetId()
	// Invalidating the fact deletes the observations citing it and refreshes the models citing those: no
	// debounce, so that refresh runs before the stack goes idle.
	debounce := int32(0)
	if _, err := pb.NewBankServiceClient(w.conn).UpdateBankConfig(w.ctx(t), &pb.UpdateBankConfigRequest{
		BankId: bank, Config: &pb.BankConfig{MentalModels: &pb.MentalModelConfig{RefreshDebounceSeconds: &debounce}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.InvalidateMemory(w.ctx(t), &pb.InvalidateMemoryRequest{
		BankId: bank, MemoryId: fact, Reason: "test",
	}); err != nil {
		t.Fatalf("invalidate memory: %v", err)
	}
	w.idle(t, bank)
	if _, err := mem.RestoreMemory(w.ctx(t), &pb.RestoreMemoryRequest{
		BankId: bank, MemoryId: fact, Reason: "test",
	}); err != nil {
		t.Fatalf("restore memory: %v", err)
	}
	w.idle(t, bank)
	w.guard.mu.Lock()
	defer w.guard.mu.Unlock()
	if len(w.guard.violations) > 0 {
		t.Fatalf("gateway calls inside a transaction: %v", w.guard.violations)
	}
}

// mentalModelBothStrategies builds a model agentically, then refreshes it single-shot.
func (w *wiredStack) mentalModelBothStrategies(t *testing.T, models pb.MentalModelServiceClient, bank string) {
	t.Helper()
	if _, err := models.CreateMentalModel(w.ctx(t), &pb.CreateMentalModelRequest{BankId: bank, Spec: &pb.MentalModelSpec{
		Id: "alice", Name: "Alice", SourceQuery: "Who is Alice?",
		Trigger: &pb.RefreshTrigger{Strategy: pb.RefreshStrategy_REFRESH_STRATEGY_AGENTIC},
	}}); err != nil {
		t.Fatal(err)
	}
	w.idle(t, bank)
	if _, err := models.RefreshMentalModel(w.ctx(t), &pb.RefreshMentalModelRequest{
		BankId: bank, MentalModelId: "alice", ForceFull: true, Strategy: pb.RefreshStrategy_REFRESH_STRATEGY_SINGLE_SHOT,
	}); err != nil {
		t.Fatal(err)
	}
	w.idle(t, bank)
	m, err := models.GetMentalModel(w.ctx(t), &pb.GetMentalModelRequest{BankId: bank, MentalModelId: "alice"})
	if err != nil || m.GetMentalModel().GetVersion() < 2 || m.GetMentalModel().GetContentMd() == "" {
		t.Fatalf("mental model after two refreshes: %v %v", m, err)
	}
}

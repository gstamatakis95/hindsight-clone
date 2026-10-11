package main

import (
	"context"
	"fmt"
	"memd/examples/internal/exampleutil"

	pb "memd/gen/memory/v1"
)

func main() {
	exampleutil.Run(func(ctx context.Context, c *exampleutil.Client) error {
		s := &scenario{c: c, bank: exampleutil.UniqueID("mental-models")}
		return exampleutil.Steps(ctx, s.setup, s.createModels, s.preview, s.newKnowledge, s.historyAndSearch)
	})
}

type scenario struct {
	c    *exampleutil.Client
	bank string
}

// setup refreshes a second after consolidation instead of the default 30 seconds, so the example is quick, and
// retains the first facts.
func (s *scenario) setup(ctx context.Context) error {
	debounce := int32(1)
	if _, err := s.c.Config.UpdateBankConfig(ctx, &pb.UpdateBankConfigRequest{
		BankId:     s.bank,
		Config:     &pb.BankConfig{MentalModels: &pb.MentalModelConfig{RefreshDebounceSeconds: &debounce}},
		UpdateMask: exampleutil.Paths("mental_models.refresh_debounce_seconds"),
	}); err != nil {
		return fmt.Errorf("update bank config: %w", err)
	}
	return s.retain(ctx, "atlas-kickoff", "Project Atlas migrates billing to the new ledger. "+
		"Marta owns the Atlas migration. The Atlas deadline is March.")
}

// retain stores one document and waits for the consolidation that follows, so the next step sees observations.
func (s *scenario) retain(ctx context.Context, doc, text string) error {
	if _, err := s.c.Memory.Retain(ctx, &pb.RetainRequest{
		BankId: s.bank, Items: []*pb.RetainItem{{DocumentId: doc, Content: text}},
	}); err != nil {
		return fmt.Errorf("retain %s: %w", doc, err)
	}
	return exampleutil.WaitIdle(ctx, s.c, s.bank, "refresh_mental_model")
}

// createModels makes two models over the same memories. The status page rewrites itself whenever consolidation
// changes something in scope (the reflect agent drafts it); the digest follows a cron schedule (UTC, 5 fields)
// and is drafted in one cheaper call.
func (s *scenario) createModels(ctx context.Context) error {
	exampleutil.Section("create a self-refreshing page and a cron digest")
	for _, spec := range []*pb.MentalModelSpec{{
		Id: "atlas-status", Name: "Atlas status", FolderPath: "projects/atlas",
		SourceQuery: "What is the status of project Atlas, who owns it and what are the risks?",
		Schedule:    &pb.RefreshSchedule{When: &pb.RefreshSchedule_AfterConsolidation{AfterConsolidation: true}},
		Strategy:    pb.RefreshStrategy_REFRESH_STRATEGY_AGENTIC,
	}, {
		Id: "atlas-weekly", Name: "Atlas weekly digest", FolderPath: "projects/atlas/reports",
		SourceQuery: "Summarize everything known about project Atlas this week",
		Schedule:    &pb.RefreshSchedule{When: &pb.RefreshSchedule_Cron{Cron: "0 9 * * 1"}},
		Strategy:    pb.RefreshStrategy_REFRESH_STRATEGY_SINGLE_SHOT,
	}} {
		created, err := s.c.MentalModels.CreateMentalModel(ctx, &pb.CreateMentalModelRequest{BankId: s.bank, Spec: spec})
		if err != nil {
			return fmt.Errorf("create %s: %w", spec.GetId(), err)
		}
		op, err := exampleutil.WaitOperation(ctx, s.c, s.bank, created.GetOperation().GetId())
		if err != nil {
			return err
		}
		fmt.Printf("%s: first build %s\n", spec.GetId(), op.GetStatus())
	}
	return nil
}

// preview runs the full refresh pipeline for each strategy without writing anything, to compare them before
// choosing one.
func (s *scenario) preview(ctx context.Context) error {
	exampleutil.Section("preview a refresh without writing it")
	for _, strategy := range []pb.RefreshStrategy{
		pb.RefreshStrategy_REFRESH_STRATEGY_AGENTIC, pb.RefreshStrategy_REFRESH_STRATEGY_SINGLE_SHOT,
	} {
		p, err := s.c.MentalModels.PreviewMentalModelRefresh(ctx, &pb.PreviewMentalModelRefreshRequest{
			BankId: s.bank, MentalModelId: "atlas-status", Strategy: strategy,
		})
		if err != nil {
			return fmt.Errorf("preview: %w", err)
		}
		fmt.Printf("%-30s facts=%d would_persist=%v\n",
			p.GetTrace().GetEffectiveStrategy(), p.GetFactsRetrieved(), p.GetWouldPersist())
	}
	return nil
}

// newKnowledge retains an update. Consolidation changes the scope, and the status page refreshes by itself.
func (s *scenario) newKnowledge(ctx context.Context) error {
	exampleutil.Section("new knowledge refreshes the status page")
	if err := s.retain(ctx, "atlas-update", "The Atlas migration slipped by two weeks because the ledger vendor "+
		"changed its API. Marta escalated the Atlas risk."); err != nil {
		return err
	}
	var model *pb.MentalModel
	err := exampleutil.Until(ctx, func() (bool, error) {
		got, err := s.c.MentalModels.GetMentalModel(ctx, &pb.GetMentalModelRequest{
			BankId: s.bank, MentalModelId: "atlas-status",
		})
		model = got.GetMentalModel()
		if err != nil {
			return false, fmt.Errorf("get mental model: %w", err)
		}
		return model.GetVersion() >= 2, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for the automatic refresh: %w", err)
	}
	fmt.Printf("atlas-status is at version %d: %s\n", model.GetVersion(), exampleutil.ShortText(model.GetContentMd(), 160))
	return nil
}

// historyAndSearch reads every earlier version, newest first (for audit or a manual rollback), searches the
// pages, and reads the cron digest back: it runs on the clock, so the example does not wait for it.
func (s *scenario) historyAndSearch(ctx context.Context) error {
	exampleutil.Section("history and search")
	hist, err := s.c.MentalModels.ListMentalModelHistory(ctx, &pb.ListMentalModelHistoryRequest{
		BankId: s.bank, MentalModelId: "atlas-status", Kind: pb.MentalModelHistoryEntry_KIND_VERSION,
	})
	if err != nil {
		return fmt.Errorf("history: %w", err)
	}
	for _, e := range hist.GetEntries() {
		fmt.Printf("  previous version: %s\n", exampleutil.ShortText(e.GetPreviousContent(), 100))
	}
	if len(hist.GetEntries()) == 0 {
		return fmt.Errorf("expected the refresh to leave a version in the history")
	}
	found, err := s.c.MentalModels.SearchMentalModels(ctx, &pb.SearchMentalModelsRequest{
		BankId: s.bank, Query: "Atlas risk", FolderPrefix: "projects/",
	})
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	for _, h := range found.GetHits() {
		fmt.Printf("  hit %-14s %s (score %.2f)\n", h.GetMentalModelId(), h.GetFolderPath(), h.GetScore())
	}
	if len(found.GetHits()) == 0 {
		return fmt.Errorf("expected search to find the Atlas pages")
	}
	list, err := s.c.MentalModels.ListMentalModels(ctx, &pb.ListMentalModelsRequest{
		BankId: s.bank, FolderPrefix: "projects/atlas/reports",
	})
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for _, m := range list.GetMentalModels() {
		fmt.Printf("  %s refreshes on cron %q\n", m.GetSpec().GetId(), m.GetSpec().GetSchedule().GetCron())
	}
	return nil
}

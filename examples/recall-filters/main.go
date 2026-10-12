package main

import (
	"context"
	"fmt"
	"memd/examples/internal/exampleutil"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "memd/gen/memory/v1"
)

func main() {
	exampleutil.Run(func(ctx context.Context, c *exampleutil.Client) error {
		s := &scenario{c: c, bank: exampleutil.UniqueID("recall-filters")}
		return exampleutil.Steps(ctx, s.retain, s.tagGroups, s.timeWindow, s.budgets, s.sources, s.arms)
	})
}

type scenario struct {
	c    *exampleutil.Client
	bank string
}

// day is noon UTC on a day of September 2026.
func day(d int) *timestamppb.Timestamp {
	return timestamppb.New(time.Date(2026, 9, d, 12, 0, 0, 0, time.UTC))
}

// retain stores four tagged documents from different weeks.
func (s *scenario) retain(ctx context.Context) error {
	item := func(id string, d int, content string, tags ...string) *pb.RetainItem {
		return &pb.RetainItem{DocumentId: id, Timestamp: day(d), Content: content, Tags: tags}
	}
	if _, err := s.c.Memory.Retain(ctx, &pb.RetainRequest{OrgId: s.c.Org, BankId: s.bank, Items: []*pb.RetainItem{
		item("eng-1", 1, "Engineering shipped the Atlas ledger importer.", "team:eng", "project:atlas"),
		item("sales-1", 10, "Sales signed Acme as the first Atlas customer.", "team:sales", "project:atlas"),
		item("eng-2", 20, "Engineering retired the legacy ledger cron runner.", "team:eng", "project:legacy"),
		item("ops-1", 25, "Operations upgraded the ledger database cluster.", "team:ops"),
	}}); err != nil {
		return fmt.Errorf("retain: %w", err)
	}
	return exampleutil.WaitIdle(ctx, s.c, s.bank)
}

// recall asks the same question with the settings of req and prints what came back.
func (s *scenario) recall(ctx context.Context, label string, req *pb.RecallRequest) (*pb.RecallResponse, error) {
	req.BankId, req.Query = s.bank, "What happened with the ledger?"
	resp, err := s.c.Memory.Recall(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("recall (%s): %w", label, err)
	}
	fmt.Printf("%s: %d results\n", label, len(resp.GetResults()))
	for _, m := range resp.GetResults() {
		fmt.Printf("    %-34s %v\n", exampleutil.Fact(m, 34), m.GetTags())
	}
	return resp, nil
}

// tagGroups asks for engineering memories that are not about the legacy project. Groups nest all_of, any_of and
// none_of; the top-level groups are AND-ed.
func (s *scenario) tagGroups(ctx context.Context) error {
	exampleutil.Section("tag groups")
	leaf := func(tag string) *pb.TagGroup {
		return &pb.TagGroup{Node: &pb.TagGroup_Leaf{Leaf: &pb.TagLeaf{Tags: []string{tag}}}}
	}
	resp, err := s.recall(ctx, "team:eng but not project:legacy", &pb.RecallRequest{Tags: &pb.TagFilter{
		Groups: []*pb.TagGroup{leaf("team:eng"), {Node: &pb.TagGroup_NoneOf{NoneOf: leaf("project:legacy")}}},
	}})
	if err != nil {
		return err
	}
	for _, m := range resp.GetResults() {
		if slices.Contains(m.GetTags(), "project:legacy") || !slices.Contains(m.GetTags(), "team:eng") {
			return fmt.Errorf("expected only team:eng memories without project:legacy, got %v", m.GetTags())
		}
	}
	return nil
}

// timeWindow gives the temporal arm and the time boost an explicit window: the last third of September.
func (s *scenario) timeWindow(ctx context.Context) error {
	exampleutil.Section("time window")
	resp, err := s.recall(ctx, "20-30 September", &pb.RecallRequest{
		TimeWindowStart: day(20), TimeWindowEnd: day(30), Trace: true,
	})
	if err != nil {
		return err
	}
	trace := resp.GetTrace()
	fmt.Println("  window used:", trace.GetTimeWindowStart().AsTime().Format(time.DateOnly),
		"to", trace.GetTimeWindowEnd().AsTime().Format(time.DateOnly))
	return nil
}

// budgets: the budget sets how many candidates each arm may return, max_tokens caps what is packed into the
// answer. A small pair keeps a prompt cheap, a large pair favours coverage.
func (s *scenario) budgets(ctx context.Context) error {
	exampleutil.Section("budgets")
	for _, tier := range []struct {
		budget pb.Budget
		tokens int32
	}{{pb.Budget_BUDGET_LOW, 24}, {pb.Budget_BUDGET_HIGH, 4096}} {
		label := fmt.Sprintf("%s, %d tokens", tier.budget, tier.tokens)
		resp, err := s.recall(ctx, label, &pb.RecallRequest{Budget: tier.budget, MaxTokens: tier.tokens, Trace: true})
		if err != nil {
			return err
		}
		t := resp.GetTrace()
		fmt.Printf("  packed %d of %d fused candidates in %d/%d tokens\n",
			t.GetPacked(), t.GetFused(), t.GetTokensUsed(), t.GetTokenBudget())
	}
	return nil
}

// sources asks for consolidated observations first, with the raw facts behind them attached, and for the chunks
// raw facts were read from.
func (s *scenario) sources(ctx context.Context) error {
	exampleutil.Section("prefer observations and attach sources")
	resp, err := s.recall(ctx, "prefer_observations", &pb.RecallRequest{
		PreferObservations: true,
		Include:            &pb.RecallInclude{SourceFacts: proto.Bool(true)},
	})
	if err != nil {
		return err
	}
	fmt.Printf("  %d source facts attached\n", len(resp.GetSourceFacts()))
	if len(resp.GetSourceFacts()) == 0 {
		return fmt.Errorf("expected source facts to be attached")
	}
	resp, err = s.recall(ctx, "raw facts with chunks", &pb.RecallRequest{
		FactTypes: []pb.FactType{pb.FactType_FACT_TYPE_WORLD},
		Include:   &pb.RecallInclude{Chunks: proto.Bool(true)},
	})
	if err != nil {
		return err
	}
	fmt.Printf("  %d chunks attached\n", len(resp.GetChunks()))
	if len(resp.GetChunks()) == 0 {
		return fmt.Errorf("expected chunks to be attached")
	}
	return nil
}

// arms lists the retrieval arms the server has and runs only two of them.
func (s *scenario) arms(ctx context.Context) error {
	exampleutil.Section("recall arms")
	arms, err := s.c.Memory.ListRecallArms(ctx, &pb.ListRecallArmsRequest{})
	if err != nil {
		return fmt.Errorf("list recall arms: %w", err)
	}
	for _, a := range arms.GetArms() {
		fmt.Printf("  %-9s %s\n", a.GetName(), exampleutil.ShortText(a.GetDescription(), 70))
	}
	resp, err := s.recall(ctx, "arms graph+vector", &pb.RecallRequest{Arms: []string{"graph", "vector"}, Trace: true})
	if err != nil {
		return err
	}
	var ran []string
	for _, a := range resp.GetTrace().GetArms() {
		ran = append(ran, a.GetArm())
	}
	slices.Sort(ran)
	fmt.Println("  arms that ran:", ran)
	if !slices.Equal(ran, []string{"graph", "vector"}) {
		return fmt.Errorf("expected only graph and vector to run, got %v", ran)
	}
	_, err = s.c.Memory.Recall(ctx, &pb.RecallRequest{
		OrgId: s.c.Org, BankId: s.bank, Query: "ledger", Arms: []string{"telepathy"},
	})
	fmt.Println("  an unknown arm:", status.Code(err))
	if status.Code(err) != codes.InvalidArgument {
		return fmt.Errorf("expected INVALID_ARGUMENT for an unknown arm, got %w", err)
	}
	return nil
}

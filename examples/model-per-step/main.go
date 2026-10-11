package main

import (
	"context"
	"flag"
	"fmt"
	"memd/examples/internal/exampleutil"

	pb "memd/gen/memory/v1"
)

var (
	cheap    = flag.String("cheap", "cheap-model", "gateway model for extract and consolidate")
	strong   = flag.String("strong", "strong-model", "gateway model for reflect and mental models")
	reranker = flag.String("reranker", "rerank-model", "gateway model for the cross-encoder")
	oneOff   = flag.String("one-off", "one-off-model", "gateway model used to override single requests")
)

func main() {
	exampleutil.Run(func(ctx context.Context, c *exampleutil.Client) error {
		s := &scenario{c: c, bank: exampleutil.UniqueID("model-per-step")}
		return exampleutil.Steps(ctx, s.bindModels, s.retain, s.consolidate, s.recall, s.reflect, s.mentalModel)
	})
}

type scenario struct {
	c    *exampleutil.Client
	bank string
}

// bindModels routes the bank: models.default covers every chat step, then the named slots take the strong model
// and the reranker. Each path in the mask replaces a binding whole.
func (s *scenario) bindModels(ctx context.Context) error {
	exampleutil.Section("bind models on the bank")
	cfg, err := s.c.Config.UpdateBankConfig(ctx, &pb.UpdateBankConfigRequest{
		BankId: s.bank,
		Config: &pb.BankConfig{Models: &pb.ModelBindings{
			Default:     exampleutil.Model(*cheap),
			Reflect:     exampleutil.Model(*strong),
			MentalModel: exampleutil.Model(*strong),
			Rerank:      exampleutil.Model(*reranker),
		}},
		UpdateMask: exampleutil.Paths("models.default", "models.reflect", "models.mental_model", "models.rerank"),
	})
	if err != nil {
		return fmt.Errorf("update bank config: %w", err)
	}
	m := cfg.GetConfig().GetEffective().GetModels()
	fmt.Printf("effective: default=%s reflect=%s mental_model=%s rerank=%s\n",
		m.GetDefault().GetModel(), m.GetReflect().GetModel(), m.GetMentalModel().GetModel(), m.GetRerank().GetModel())
	return nil
}

// retain stores two short documents, once with the bank's routing and once with a per-request extract model.
// Consolidation is skipped so the next step can run it by hand with its own model.
func (s *scenario) retain(ctx context.Context) error {
	exampleutil.Section("retain: bank routing, then a per-request override")
	for _, doc := range []struct{ id, model, text string }{
		{"notes-1", "", "Priya leads the billing team. The billing team ships on Thursdays."},
		{"notes-2", *oneOff, "Priya reports to Tomas. Tomas runs the platform group."},
	} {
		req := &pb.RetainRequest{
			BankId:            s.bank,
			Items:             []*pb.RetainItem{{DocumentId: doc.id, Content: doc.text}},
			SkipConsolidation: true,
		}
		want := *cheap
		if doc.model != "" {
			req.Overrides = &pb.RetainOverrides{ExtractModel: exampleutil.Model(doc.model)}
			want = doc.model
		}
		resp, err := s.c.Memory.Retain(ctx, req)
		if err != nil {
			return fmt.Errorf("retain %s: %w", doc.id, err)
		}
		exampleutil.PrintUsage(doc.id, resp.GetUsage())
		if got := exampleutil.UsedModel(resp.GetUsage(), pb.SlotUsage_MODEL_SLOT_EXTRACT); got != want {
			return fmt.Errorf("%s was extracted by %q, want %q", doc.id, got, want)
		}
	}
	return nil
}

// consolidate runs a manual consolidation with its own consolidate model; the operation reports what it used.
func (s *scenario) consolidate(ctx context.Context) error {
	exampleutil.Section("consolidate with a one-off model")
	cons, err := s.c.Observations.Consolidate(ctx, &pb.ConsolidateRequest{
		BankId:    s.bank,
		Overrides: &pb.ConsolidateOverrides{ConsolidateModel: exampleutil.Model(*oneOff)},
	})
	if err != nil {
		return fmt.Errorf("consolidate: %w", err)
	}
	op, err := exampleutil.WaitOperation(ctx, s.c, s.bank, cons.GetOperation().GetId())
	if err != nil {
		return err
	}
	exampleutil.PrintUsage("consolidate operation", op.GetUsage())
	if got := exampleutil.UsedModel(op.GetUsage(), pb.SlotUsage_MODEL_SLOT_CONSOLIDATE); got != *oneOff {
		return fmt.Errorf("consolidate used %q, want %q", got, *oneOff)
	}
	return nil
}

// recall uses the bank's reranker; the trace shows how many candidates the cross-encoder scored.
func (s *scenario) recall(ctx context.Context) error {
	exampleutil.Section("recall with the reranker")
	rec, err := s.c.Memory.Recall(ctx, &pb.RecallRequest{BankId: s.bank, Query: "Who does Priya report to?", Trace: true})
	if err != nil {
		return fmt.Errorf("recall: %w", err)
	}
	fmt.Printf("%d results, %d candidates sent to the cross-encoder\n",
		len(rec.GetResults()), rec.GetTrace().GetReranked())
	if rec.GetTrace().GetReranked() == 0 {
		return fmt.Errorf("expected the bank's reranker to run")
	}
	return nil
}

// reflect runs twice: on the bank's strong model, then on a one-off model for this call only.
func (s *scenario) reflect(ctx context.Context) error {
	exampleutil.Section("reflect: bank model, then a per-request model")
	for _, override := range []string{"", *oneOff} {
		req := &pb.ReflectRequest{BankId: s.bank, Query: "Who is in Priya's chain of command?"}
		want := *strong
		if override != "" {
			req.Overrides = &pb.ReflectOverrides{ReflectModel: exampleutil.Model(override)}
			want = override
		}
		resp, err := s.c.Memory.Reflect(ctx, req)
		if err != nil {
			return fmt.Errorf("reflect: %w", err)
		}
		exampleutil.PrintUsage("reflect", resp.GetUsage())
		if got := exampleutil.UsedModel(resp.GetUsage(), pb.SlotUsage_MODEL_SLOT_REFLECT); got != want {
			return fmt.Errorf("reflect used %q, want %q", got, want)
		}
	}
	return nil
}

// mentalModel builds a single-shot page, which uses the MENTAL_MODEL slot: one recall plus one strong-model call.
func (s *scenario) mentalModel(ctx context.Context) error {
	exampleutil.Section("mental model build on the strong model")
	created, err := s.c.MentalModels.CreateMentalModel(ctx, &pb.CreateMentalModelRequest{
		BankId: s.bank,
		Spec: &pb.MentalModelSpec{
			Id: "org-chart", Name: "Org chart", SourceQuery: "Who reports to whom?",
			Strategy: pb.RefreshStrategy_REFRESH_STRATEGY_SINGLE_SHOT,
		},
	})
	if err != nil {
		return fmt.Errorf("create mental model: %w", err)
	}
	built, err := exampleutil.WaitOperation(ctx, s.c, s.bank, created.GetOperation().GetId())
	if err != nil {
		return err
	}
	exampleutil.PrintUsage("mental model build", built.GetUsage())
	if got := exampleutil.UsedModel(built.GetUsage(), pb.SlotUsage_MODEL_SLOT_MENTAL_MODEL); got != *strong {
		return fmt.Errorf("the mental model was built by %q, want %q", got, *strong)
	}
	return nil
}

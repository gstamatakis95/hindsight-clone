package main

import (
	"context"
	"fmt"
	"memd/examples/internal/exampleutil"
	"strings"

	pb "memd/gen/memory/v1"
)

func main() {
	exampleutil.Run(func(ctx context.Context, c *exampleutil.Client) error {
		s := &scenario{c: c, bank: exampleutil.UniqueID("curation")}
		return exampleutil.Steps(ctx, s.retain, s.correct, s.invalidateAndRestore, s.documentFixes, s.rebuildObservations)
	})
}

type scenario struct {
	c    *exampleutil.Client
	bank string
}

// retain stores a profile that contains one wrong fact and waits for its observations.
func (s *scenario) retain(ctx context.Context) error {
	if _, err := s.c.Memory.Retain(ctx, &pb.RetainRequest{OrgId: s.c.Org, BankId: s.bank, Items: []*pb.RetainItem{{
		DocumentId: "profile-dana",
		Content:    "Dana lives in Lisbon. Dana is vegetarian. Dana works on motion planning.",
		Tags:       []string{"draft"},
	}}}); err != nil {
		return fmt.Errorf("retain: %w", err)
	}
	return exampleutil.WaitIdle(ctx, s.c, s.bank)
}

// correct fixes a fact. UpdateMemory applies only the fields named in update_mask, records the edit in the
// memory's history and re-derives its embedding and links in the background.
func (s *scenario) correct(ctx context.Context) error {
	exampleutil.Section("correct a wrong fact")
	wrong, err := s.find(ctx, "Lisbon", pb.MemoryState_MEMORY_STATE_VALID)
	if err != nil {
		return err
	}
	updated, err := s.c.Memory.UpdateMemory(ctx, &pb.UpdateMemoryRequest{
		OrgId: s.c.Org, BankId: s.bank, MemoryId: wrong.GetId(),
		Memory:     &pb.MemoryEdit{Text: "Dana lives in Berlin."},
		UpdateMask: exampleutil.Paths("text"),
		Reason:     "Dana moved; the old profile was out of date",
	})
	if err != nil {
		return fmt.Errorf("update memory: %w", err)
	}
	fmt.Println("now reads:", updated.GetMemory().GetText())
	return s.wait(ctx, "rederive", updated.GetOperation())
}

// invalidateAndRestore retires a fact: it disappears from recall and consolidation but is kept, so it can come back.
func (s *scenario) invalidateAndRestore(ctx context.Context) error {
	exampleutil.Section("invalidate and restore")
	veg, err := s.find(ctx, "vegetarian", pb.MemoryState_MEMORY_STATE_VALID)
	if err != nil {
		return err
	}
	if _, err := s.c.Memory.InvalidateMemory(ctx, &pb.InvalidateMemoryRequest{
		OrgId: s.c.Org, BankId: s.bank, MemoryId: veg.GetId(), Reason: "unverified",
	}); err != nil {
		return fmt.Errorf("invalidate: %w", err)
	}
	resp, err := s.c.Memory.Recall(ctx, &pb.RecallRequest{OrgId: s.c.Org, BankId: s.bank, Query: "What does Dana eat?"})
	if err != nil {
		return fmt.Errorf("recall: %w", err)
	}
	for _, m := range resp.GetResults() {
		if strings.Contains(m.GetText(), "vegetarian") {
			return fmt.Errorf("invalidated fact still recalled: %s", m.GetText())
		}
	}
	fmt.Printf("recall returns %d memories, none about vegetarian\n", len(resp.GetResults()))
	if _, err := s.find(ctx, "vegetarian", pb.MemoryState_MEMORY_STATE_INVALIDATED); err != nil {
		return err
	}
	restored, err := s.c.Memory.RestoreMemory(ctx, &pb.RestoreMemoryRequest{
		OrgId: s.c.Org, BankId: s.bank, MemoryId: veg.GetId(), Reason: "confirmed by Dana",
	})
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	return s.wait(ctx, "restore", restored.GetOperation())
}

// documentFixes gives every memory of a document new tags, then re-extracts the document from its original text.
func (s *scenario) documentFixes(ctx context.Context) error {
	exampleutil.Section("retag and reprocess a document")
	retag, err := s.c.Documents.RetagDocument(ctx, &pb.RetagDocumentRequest{
		OrgId: s.c.Org, BankId: s.bank, DocumentId: "profile-dana", Tags: &pb.TagSet{Tags: []string{"people", "verified"}},
	})
	if err != nil {
		return fmt.Errorf("retag: %w", err)
	}
	if err := s.wait(ctx, "retag", retag.GetOperation()); err != nil {
		return err
	}
	reprocess, err := s.c.Documents.ReprocessDocument(ctx, &pb.ReprocessDocumentRequest{
		OrgId: s.c.Org, BankId: s.bank, DocumentId: "profile-dana",
	})
	if err != nil {
		return fmt.Errorf("reprocess: %w", err)
	}
	if err := s.wait(ctx, "reprocess", reprocess.GetOperation()); err != nil {
		return err
	}
	return exampleutil.WaitIdle(ctx, s.c, s.bank)
}

// rebuildObservations clears the derived observations and lets consolidation rebuild them: observations are
// never edited directly.
func (s *scenario) rebuildObservations(ctx context.Context) error {
	exampleutil.Section("clear and rebuild observations")
	cleared, err := s.c.Observations.ClearObservations(ctx, &pb.ClearObservationsRequest{
		OrgId: s.c.Org, BankId: s.bank, Target: &pb.ClearObservationsRequest_All{All: true}, Reconsolidate: true,
	})
	if err != nil {
		return fmt.Errorf("clear observations: %w", err)
	}
	fmt.Printf("deleted %d observations\n", cleared.GetDeleted())
	if cleared.GetDeleted() == 0 {
		return fmt.Errorf("expected observations to clear")
	}
	return s.wait(ctx, "reconsolidate", cleared.GetOperation())
}

// find returns the first memory of the bank in the given state whose text contains sub.
func (s *scenario) find(ctx context.Context, sub string, state pb.MemoryState) (*pb.Memory, error) {
	resp, err := s.c.Memory.ListMemories(ctx, &pb.ListMemoriesRequest{
		OrgId: s.c.Org, BankId: s.bank, TextContains: sub, State: state,
		FactTypes: []pb.FactType{pb.FactType_FACT_TYPE_WORLD, pb.FactType_FACT_TYPE_EXPERIENCE},
	})
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	if len(resp.GetMemories()) == 0 {
		return nil, fmt.Errorf("no %s memory contains %q", state, sub)
	}
	return resp.GetMemories()[0], nil
}

// wait follows the background operation a curation call returned and prints how it ended.
func (s *scenario) wait(ctx context.Context, what string, op *pb.Operation) error {
	if op == nil {
		fmt.Printf("%s: nothing to do\n", what)
		return nil
	}
	done, err := exampleutil.WaitOperation(ctx, s.c, s.bank, op.GetId())
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s %s\n", what, op.GetKind(), done.GetStatus())
	return nil
}

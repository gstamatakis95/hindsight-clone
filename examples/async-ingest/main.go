package main

import (
	"context"
	"fmt"
	"memd/examples/internal/exampleutil"

	pb "memd/gen/memory/v1"
)

// refreshKind is the operation kind of a mental-model refresh, as GetBankStats names it.
const refreshKind = "refresh_mental_model"

func main() {
	exampleutil.Run(func(ctx context.Context, c *exampleutil.Client) error {
		s := &scenario{c: c, bank: exampleutil.UniqueID("async-ingest")}
		return exampleutil.Steps(ctx, s.setup, s.enqueueBatch, s.pollOperations, s.followEvents, s.cancelAndRetry)
	})
}

type scenario struct {
	c      *exampleutil.Client
	bank   string
	batch  string // id of the retain_batch operation
	cursor int64  // next_after_seq of the last ListEvents
}

// setup adds a mental model that refreshes after consolidation, with a long debounce: its refresh waits as a
// PENDING operation, which gives cancelAndRetry something to cancel that no worker can race.
func (s *scenario) setup(ctx context.Context) error {
	debounce := int32(3600)
	if _, err := s.c.Config.UpdateBankConfig(ctx, &pb.UpdateBankConfigRequest{
		BankId:     s.bank,
		Config:     &pb.BankConfig{MentalModels: &pb.MentalModelConfig{RefreshDebounceSeconds: &debounce}},
		UpdateMask: exampleutil.Paths("mental_models.refresh_debounce_seconds"),
	}); err != nil {
		return fmt.Errorf("update bank config: %w", err)
	}
	created, err := s.c.MentalModels.CreateMentalModel(ctx, &pb.CreateMentalModelRequest{
		BankId: s.bank,
		Spec: &pb.MentalModelSpec{
			Id: "ingest-digest", Name: "Digest", SourceQuery: "Summarize each ticket and customer request",
			Schedule: &pb.RefreshSchedule{When: &pb.RefreshSchedule_AfterConsolidation{AfterConsolidation: true}},
		},
	})
	if err != nil {
		return fmt.Errorf("create mental model: %w", err)
	}
	_, err = exampleutil.WaitOperation(ctx, s.c, s.bank, created.GetOperation().GetId())
	return err
}

// enqueueBatch retains five documents with run_async: the call returns at once with the operations it enqueued.
func (s *scenario) enqueueBatch(ctx context.Context) error {
	exampleutil.Section("enqueue a batch")
	var items []*pb.RetainItem
	for i, text := range []string{
		"Ticket 101: the invoice export is slow for large accounts.",
		"Ticket 102: a customer asked for single sign-on.",
		"Ticket 103: dark mode flickers on the settings page.",
		"Ticket 104: the API returns 500 when a webhook URL is empty.",
		"Ticket 105: a customer wants to export audit logs weekly.",
	} {
		items = append(items, &pb.RetainItem{DocumentId: fmt.Sprintf("ticket-%d", 101+i), Content: text})
	}
	resp, err := s.c.Memory.Retain(ctx, &pb.RetainRequest{BankId: s.bank, Items: items, RunAsync: true})
	if err != nil {
		return fmt.Errorf("retain: %w", err)
	}
	batch := resp.GetBatchOperation()
	s.batch = batch.GetId()
	fmt.Printf("batch operation %s (%s), %d document operations\n", s.batch, batch.GetKind(), len(resp.GetDocuments()))
	return nil
}

// pollOperations waits for the parent, whose status aggregates the per-document children, and lists the rest.
func (s *scenario) pollOperations(ctx context.Context) error {
	exampleutil.Section("poll the operations")
	done, err := exampleutil.WaitOperation(ctx, s.c, s.bank, s.batch)
	if err != nil {
		return err
	}
	fmt.Println("batch finished:", done.GetStatus())
	list, err := s.c.Operations.ListOperations(ctx, &pb.ListOperationsRequest{BankId: s.bank, ExcludeParents: true})
	if err != nil {
		return fmt.Errorf("list operations: %w", err)
	}
	for _, op := range list.GetOperations() {
		fmt.Printf("  %-36s %s\n", op.GetKind(), op.GetStatus())
	}
	return exampleutil.WaitIdle(ctx, s.c, s.bank, refreshKind)
}

// followEvents reads the lifecycle events. A consumer remembers next_after_seq, so a later poll returns only
// what is new.
func (s *scenario) followEvents(ctx context.Context) error {
	exampleutil.Section("follow the event feed")
	if err := s.printEvents(ctx); err != nil {
		return err
	}
	if _, err := s.c.Memory.Retain(ctx, &pb.RetainRequest{BankId: s.bank, Items: []*pb.RetainItem{
		{DocumentId: "ticket-106", Content: "Ticket 106: the mobile app logs users out after an update."},
	}}); err != nil {
		return fmt.Errorf("retain: %w", err)
	}
	fmt.Println("after one more retain:")
	if err := s.printEvents(ctx); err != nil {
		return err
	}
	return exampleutil.WaitIdle(ctx, s.c, s.bank, refreshKind)
}

func (s *scenario) printEvents(ctx context.Context) error {
	resp, err := s.c.Operations.ListEvents(ctx, &pb.ListEventsRequest{BankId: s.bank, AfterSeq: s.cursor})
	if err != nil {
		return fmt.Errorf("list events: %w", err)
	}
	for _, e := range resp.GetEvents() {
		fmt.Printf("  seq %-3d %-36s %s\n", e.GetSeq(), e.GetType(), e.GetSubjectId())
	}
	s.cursor = resp.GetNextAfterSeq()
	return nil
}

// cancelAndRetry cancels the parked digest refresh, then puts it back in the queue to run at once.
func (s *scenario) cancelAndRetry(ctx context.Context) error {
	exampleutil.Section("cancel and retry")
	pending, err := s.c.Operations.ListOperations(ctx, &pb.ListOperationsRequest{
		BankId: s.bank, Kind: pb.OperationKind_OPERATION_KIND_REFRESH_MENTAL_MODEL,
		Status: pb.OperationStatus_OPERATION_STATUS_PENDING,
	})
	if err != nil {
		return fmt.Errorf("list operations: %w", err)
	}
	if len(pending.GetOperations()) != 1 {
		return fmt.Errorf("expected one parked refresh, got %d", len(pending.GetOperations()))
	}
	id := pending.GetOperations()[0].GetId()
	cancelled, err := s.c.Operations.CancelOperation(ctx, &pb.CancelOperationRequest{BankId: s.bank, OperationId: id})
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	fmt.Println("cancel:", cancelled.GetOperation().GetStatus())
	retried, err := s.c.Operations.RetryOperation(ctx, &pb.RetryOperationRequest{BankId: s.bank, OperationId: id})
	if err != nil {
		return fmt.Errorf("retry: %w", err)
	}
	fmt.Println("retry:", retried.GetOperation().GetStatus())
	final, err := exampleutil.WaitOperation(ctx, s.c, s.bank, id)
	if err != nil {
		return err
	}
	fmt.Println("finished:", final.GetStatus())
	return exampleutil.Expect(final.GetStatus() == pb.OperationStatus_OPERATION_STATUS_COMPLETED,
		"the retried refresh to complete")
}

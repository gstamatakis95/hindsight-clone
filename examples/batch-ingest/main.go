package main

import (
	"context"
	"fmt"
	"memd/examples/internal/exampleutil"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "memd/gen/memory/v1"
)

func main() {
	exampleutil.Run(func(ctx context.Context, c *exampleutil.Client) error {
		s := &scenario{c: c, bank: exampleutil.UniqueID("batch-ingest")}
		return exampleutil.Steps(ctx, s.ingest, s.redeliver, s.outOfOrder)
	})
}

type scenario struct {
	c    *exampleutil.Client
	bank string
}

// item is one source row as a retain item: the row id is the document id and the row version the revision, so a
// redelivered row is recognised and an old version never overwrites a newer one.
func item(id string, version int64, text string) *pb.RetainItem {
	return &pb.RetainItem{DocumentId: id, Revision: &version, Content: text}
}

// batch is what the producer read from its source: three rows, an old version of one of them delivered after the
// new one, and a row without an id (a producer bug).
func batch() []*pb.RetainItem {
	return []*pb.RetainItem{
		item("ticket-201", 1, "Ticket 201: the invoice export is slow for large accounts."),
		item("ticket-202", 1, "Ticket 202: a customer asked for single sign-on."),
		item("ticket-203", 2, "Ticket 203: dark mode flickers on the settings page and the billing page."),
		item("ticket-203", 1, "Ticket 203: dark mode flickers on the settings page."),
		{Content: "Ticket 204: no id, so it can never be deduplicated."},
	}
}

// ingest sends the batch. Every item gets its own status: the invalid one is rejected without failing the call,
// and the old version of ticket-203 loses to the newer one in the same call.
func (s *scenario) ingest(ctx context.Context) error {
	exampleutil.Section("ingest a batch")
	resp, err := s.send(ctx, batch())
	if err != nil {
		return err
	}
	for _, r := range resp.GetResults() {
		if r.GetStatus() != pb.RetainBatchResult_STATUS_ACCEPTED {
			continue
		}
		if _, err := exampleutil.WaitOperation(ctx, s.c, s.bank, r.GetOperation().GetId()); err != nil {
			return err
		}
	}
	return expectStatuses(resp, "ACCEPTED", "ACCEPTED", "ACCEPTED", "STALE", "REJECTED")
}

// redeliver sends the same batch again, as a queue does after a consumer crash: nothing is written and no model
// is called, because every document already holds this content.
func (s *scenario) redeliver(ctx context.Context) error {
	exampleutil.Section("redeliver the same batch")
	resp, err := s.send(ctx, batch())
	if err != nil {
		return err
	}
	return expectStatuses(resp, "UNCHANGED", "UNCHANGED", "UNCHANGED", "STALE", "REJECTED")
}

// outOfOrder delivers version 1 of ticket-203 again, after version 2 was stored: it is stale and changes nothing.
func (s *scenario) outOfOrder(ctx context.Context) error {
	exampleutil.Section("an old version arrives late")
	resp, err := s.send(ctx, []*pb.RetainItem{
		item("ticket-203", 1, "Ticket 203: dark mode flickers on the settings page."),
	})
	if err != nil {
		return err
	}
	doc, err := s.c.Documents.GetDocument(ctx, &pb.GetDocumentRequest{
		OrgId: s.c.Org, BankId: s.bank, DocumentId: "ticket-203",
	})
	if err != nil {
		return fmt.Errorf("get document: %w", err)
	}
	fmt.Println("ticket-203 stored revision:", doc.GetDocument().GetRevision())
	if err := expectStatuses(resp, "STALE"); err != nil {
		return err
	}
	return exampleutil.Expect(doc.GetDocument().GetRevision() == 2, "the stored revision to stay 2")
}

// send calls RetainBatch and prints one line per item. While the bank's or the org's retain backlog is over the
// server's limit, the whole call is rejected with RESOURCE_EXHAUSTED before anything is written, and RetryInfo says
// how long to wait: a producer pauses that long and sends the same batch again.
func (s *scenario) send(ctx context.Context, items []*pb.RetainItem) (*pb.RetainBatchResponse, error) {
	for {
		resp, err := s.c.Memory.RetainBatch(ctx, &pb.RetainBatchRequest{OrgId: s.c.Org, BankId: s.bank, Items: items})
		if wait, ok := backlogDelay(err); ok {
			fmt.Println("backlog is full; retrying in", wait)
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("waiting out the backlog: %w", ctx.Err())
			case <-time.After(wait):
			}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("retain batch: %w", err)
		}
		for _, r := range resp.GetResults() {
			fmt.Printf("  %-12s %-18s %s\n", r.GetDocumentId(), r.GetStatus(), describe(r))
		}
		return resp, nil
	}
}

// backlogDelay returns the RetryInfo delay of a backlog rejection.
func backlogDelay(err error) (time.Duration, bool) {
	st, ok := status.FromError(err)
	if err == nil || !ok || st.Code() != codes.ResourceExhausted {
		return 0, false
	}
	for _, d := range st.Details() {
		if r, ok := d.(*errdetails.RetryInfo); ok {
			return r.GetRetryDelay().AsDuration(), true
		}
	}
	return 0, false
}

// describe is the operation of an accepted item or the error of a rejected one.
func describe(r *pb.RetainBatchResult) string {
	switch {
	case r.GetOperation() != nil:
		return "operation " + r.GetOperation().GetId()
	case r.GetError() != nil:
		return fmt.Sprintf("%s %v", r.GetError().GetCode(), r.GetError().GetFields())
	}
	return ""
}

// expectStatuses checks the per-item statuses (without the STATUS_ prefix).
func expectStatuses(resp *pb.RetainBatchResponse, want ...string) error {
	results := resp.GetResults()
	if err := exampleutil.Expect(len(results) == len(want), "one result per item"); err != nil {
		return err
	}
	for i, w := range want {
		if got := results[i].GetStatus().String(); got != "STATUS_"+w {
			return exampleutil.Expect(false, "item %d to be %s, got %s", i, w, got)
		}
	}
	return nil
}

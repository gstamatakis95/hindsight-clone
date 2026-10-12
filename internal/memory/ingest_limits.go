package memory

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"time"
)

// IngestLimits are the operator limits of Retain and RetainBatch (the ingest section of the server config
// file). A zero field takes its default.
type IngestLimits struct {
	// MaxBatchItems caps the items of one RetainBatch call (default 500).
	MaxBatchItems int
	// MaxRetainItems caps the items of one Retain call (default 100).
	MaxRetainItems int
	// MaxDocumentBytes caps one item's content: a larger Retain item fails the call with INVALID_ARGUMENT, a larger
	// RetainBatch item is REJECTED (default 2 MiB). It keeps every stored text within one GetDocument response.
	MaxDocumentBytes int
	// MaxRequestBytes caps the summed content of one RetainBatch call (default 16 MiB). The gRPC server's
	// receive limit is set from the same setting.
	MaxRequestBytes int
	// MaxPendingPerBank is the bank's pending retain_document backlog over which RetainBatch is rejected
	// (default 5000).
	MaxPendingPerBank int
	// MaxPendingPerOrg is the org's pending retain_document backlog, across its banks, over which RetainBatch
	// is rejected (default 50000).
	MaxPendingPerOrg int
	// RetryAfter is the retry delay a backlog rejection carries (default 10s).
	RetryAfter time.Duration
}

// Defaults of IngestLimits.
const (
	// DefaultMaxBatchItems is IngestLimits.MaxBatchItems' default.
	DefaultMaxBatchItems = 500
	// DefaultMaxRetainItems is IngestLimits.MaxRetainItems' default.
	DefaultMaxRetainItems = 100
	// DefaultMaxDocumentBytes is IngestLimits.MaxDocumentBytes' default.
	DefaultMaxDocumentBytes = 2 << 20
	// DefaultMaxRequestBytes is IngestLimits.MaxRequestBytes' default.
	DefaultMaxRequestBytes = 16 << 20
	// DefaultMaxPendingPerBank is IngestLimits.MaxPendingPerBank's default.
	DefaultMaxPendingPerBank = 5000
	// DefaultMaxPendingPerOrg is IngestLimits.MaxPendingPerOrg's default.
	DefaultMaxPendingPerOrg = 50000
	// DefaultIngestRetryAfter is IngestLimits.RetryAfter's default.
	DefaultIngestRetryAfter = 10 * time.Second
)

// withDefaults fills the zero fields.
func (l IngestLimits) withDefaults() IngestLimits {
	fill := func(v *int, def int) {
		if *v <= 0 {
			*v = def
		}
	}
	fill(&l.MaxBatchItems, DefaultMaxBatchItems)
	fill(&l.MaxRetainItems, DefaultMaxRetainItems)
	fill(&l.MaxDocumentBytes, DefaultMaxDocumentBytes)
	fill(&l.MaxRequestBytes, DefaultMaxRequestBytes)
	fill(&l.MaxPendingPerBank, DefaultMaxPendingPerBank)
	fill(&l.MaxPendingPerOrg, DefaultMaxPendingPerOrg)
	if l.RetryAfter <= 0 {
		l.RetryAfter = DefaultIngestRetryAfter
	}
	return l
}

// checkBacklog rejects a RetainBatch call while the bank's or the org's pending retain_document backlog is over
// its limit: domain.ErrIngestBacklog carrying the retry delay. Each count stops one past its limit.
func (i *ingestor) checkBacklog(ctx context.Context, b domain.BankRef) error {
	l := i.d.Limits
	var bank, org int
	err := i.d.Store.InTx(ctx, func(tx store.Tx) error {
		var err error
		kind := domain.OperationKindRetainDocument
		if bank, err = tx.Queue().CountPending(ctx, b, kind, l.MaxPendingPerBank+1); err != nil {
			return err
		}
		org, err = tx.Queue().CountPendingInOrg(ctx, b.OrgRef(), kind, l.MaxPendingPerOrg+1)
		return err
	})
	switch {
	case err != nil:
		return fmt.Errorf("retain batch: backlog: %w", err)
	case bank > l.MaxPendingPerBank:
		return domain.WithRetryDelay(fmt.Errorf("%w: bank %s has more than %d pending retains",
			domain.ErrIngestBacklog, b, l.MaxPendingPerBank), l.RetryAfter)
	case org > l.MaxPendingPerOrg:
		return domain.WithRetryDelay(fmt.Errorf("%w: org %s has more than %d pending retains",
			domain.ErrIngestBacklog, b.OrgRef(), l.MaxPendingPerOrg), l.RetryAfter)
	}
	return nil
}

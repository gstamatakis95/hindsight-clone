package ingestapi

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/memory"
	"testing"
	"time"
)

// TestDecideSettlement is the status-to-settlement table of the package comment: the error class decides before
// the status, a backlog rejection honours its RetryInfo, and a retryable failure is dead-lettered at the maximum
// attempts.
func TestDecideSettlement(t *testing.T) {
	cfg := Config{}.withDefaults() // 10 attempts, 1s doubling to 5m
	wrap := func(class error) error { return fmt.Errorf("%w: detail", class) }
	backlog := domain.WithRetryDelay(wrap(domain.ErrIngestBacklog), 7*time.Second)
	cases := []struct {
		name    string
		st      memory.BatchStatus
		err     error
		attempt int
		orgGone bool
		want    decision
	}{
		{"accepted", memory.BatchAccepted, nil, 1, false, decision{act: actAck}},
		{"unchanged", memory.BatchUnchanged, nil, 1, false, decision{act: actAck}},
		{"stale", memory.BatchStale, nil, 1, false, decision{act: actAck}},
		{"superseded", memory.BatchSuperseded, nil, 1, false, decision{act: actAck}},
		{
			"an item without an outcome is retried, never acked", memory.BatchUnspecified, nil, 1, false,
			decision{act: actNack, reason: "UNAVAILABLE", delay: time.Second},
		},
		{
			"rejected item", memory.BatchRejected, wrap(domain.ErrInvalidArgument), 1, false,
			decision{act: actDeadLetter, reason: "INVALID_ARGUMENT"},
		},
		{
			"rejected too many scopes", memory.BatchRejected, wrap(domain.ErrTooManyScopes), 9, false,
			decision{act: actDeadLetter, reason: "TOO_MANY_SCOPES"},
		},
		{
			"failed item", memory.BatchFailed, wrap(domain.ErrUnavailable), 1, false,
			decision{act: actNack, reason: "UNAVAILABLE", delay: time.Second},
		},
		{
			"failed item, third delivery", memory.BatchFailed, wrap(domain.ErrUnavailable), 3, false,
			decision{act: actNack, reason: "UNAVAILABLE", delay: 4 * time.Second},
		},
		{
			"failed item, backoff capped", memory.BatchFailed, wrap(domain.ErrUnavailable), 9, false,
			decision{act: actNack, reason: "UNAVAILABLE", delay: 256 * time.Second},
		},
		{
			"failed item at max attempts", memory.BatchFailed, wrap(domain.ErrUnavailable), 10, false,
			decision{act: actDeadLetter, reason: reasonMaxAttempts},
		},
		{
			"unknown attempt never reaches the maximum", memory.BatchFailed, wrap(domain.ErrUnavailable), 0, false,
			decision{act: actNack, reason: "UNAVAILABLE", delay: time.Second},
		},
		{
			"call unavailable", memory.BatchUnspecified, wrap(domain.ErrUnavailable), 2, false,
			decision{act: actNack, reason: "UNAVAILABLE", delay: 2 * time.Second},
		},
		{
			"call conflict", memory.BatchUnspecified, wrap(domain.ErrConflict), 1, false,
			decision{act: actNack, reason: "CONFLICT", delay: time.Second},
		},
		{
			"call cancelled", memory.BatchUnspecified, context.Canceled, 1, false,
			decision{act: actNack, reason: "CANCELLED", delay: time.Second},
		},
		{
			"call invalid", memory.BatchUnspecified, wrap(domain.ErrInvalidArgument), 1, false,
			decision{act: actDeadLetter, reason: "INVALID_ARGUMENT"},
		},
		{
			"backlog honours RetryInfo and pauses", memory.BatchUnspecified, backlog, 20, false,
			decision{act: actNack, reason: "INGEST_BACKLOG", delay: 7 * time.Second, pause: 7 * time.Second},
		},
		{
			"org deleting, rejected item, is dropped", memory.BatchRejected, wrap(domain.ErrOrgDeleting), 1, false,
			decision{act: actDrop, reason: "ORG_DELETING"},
		},
		{
			"org not found with a receipt is dropped", memory.BatchUnspecified, wrap(domain.ErrOrgNotFound), 1, true,
			decision{act: actDrop, reason: reasonOrgDeleted},
		},
		{
			"org not found without a receipt is retried", memory.BatchUnspecified, wrap(domain.ErrOrgNotFound), 1,
			false,
			decision{act: actNack, reason: "ORG_NOT_FOUND", delay: time.Second},
		},
		{
			"org not found as a rejected item is retried", memory.BatchRejected, wrap(domain.ErrOrgNotFound), 2,
			false,
			decision{act: actNack, reason: "ORG_NOT_FOUND", delay: 2 * time.Second},
		},
		{
			"org not found without a receipt, at max attempts", memory.BatchUnspecified, wrap(domain.ErrOrgNotFound),
			10, false,
			decision{act: actDeadLetter, reason: reasonMaxAttempts},
		},
		{
			"bank deleting, rejected item, is retried", memory.BatchRejected, wrap(domain.ErrBankDeleting), 1, false,
			decision{act: actNack, reason: "BANK_DELETING", delay: time.Second},
		},
		{
			"bank deleting call is retried", memory.BatchUnspecified, wrap(domain.ErrBankDeleting), 1, false,
			decision{act: actNack, reason: "BANK_DELETING", delay: time.Second},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decide(tc.st, tc.err, tc.attempt, tc.orgGone, cfg)
			got.detail = "" // the human text is checked by the consumer tests
			if got != tc.want {
				t.Fatalf("decide = %+v, want %+v", got, tc.want)
			}
		})
	}
}

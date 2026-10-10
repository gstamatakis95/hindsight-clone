package bank

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// BankDeleter deletes a bank in two phases: an api-side tombstone and a batched delete_bank job.
type BankDeleter interface { //nolint:revive // stutter: name fixed by the spec
	// Tombstone is the api side of DeleteBank: in one transaction it sets deleting_at, cancels the bank's
	// pending jobs and enqueues delete_bank, returning that operation. From then on writes fail with
	// domain.ErrBankDeleting and reads with domain.ErrNotFound. Tombstoning an already deleting bank
	// returns the pending delete_bank operation.
	Tombstone(ctx context.Context, b port.BankRef) (domain.Operation, error)
	// Run is the delete_bank job: it removes one budget of rows in committed batches and reports
	// done=false when work is left (the handler re-enqueues). The last run deletes the blob prefix
	// {tenant}/{bank}/ and then the bank row.
	Run(ctx context.Context, j port.Job) (done bool, err error)
}

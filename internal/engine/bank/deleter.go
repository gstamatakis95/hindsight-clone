package bank

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// BankDeleter deletes a bank in two phases: an api-side tombstone and a batched delete_bank job.
type BankDeleter interface { //nolint:revive // stutter: name fixed by the spec
	// Tombstone is the api side of DeleteBank: in one transaction it sets deleting_at, cancels the bank's
	// pending jobs (Queue.CancelBank spares delete_bank) and enqueues delete_bank with
	// DedupePendingOrRunning (Enqueue allows delete_bank on a tombstoned bank), returning that operation.
	// From then on writes fail with domain.ErrBankDeleting (Tx.Seq and Queue.Enqueue refuse the bank) and
	// reads with domain.ErrNotFound. Tombstoning an already deleting bank returns its delete_bank operation.
	Tombstone(ctx context.Context, b port.BankRef) (domain.Operation, error)
	// Run is the delete_bank job: it removes rows with BankRepo.DeleteBatch in committed batches and
	// reports done=false when its budget runs out (the handler re-enqueues). Once nothing is left it
	// deletes the blob prefix {tenant}/{bank}/, then BankRepo.Delete removes the bank row with its
	// remaining events and operations, so a later write creates a clean bank.
	Run(ctx context.Context, j port.Job) (done bool, err error)
}

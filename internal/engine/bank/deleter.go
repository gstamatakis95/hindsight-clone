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
	// reports done=false when its budget runs out (the handler re-enqueues with DedupePending). It
	// re-checks the tombstone (deleting_at) and that the job is still alive right before
	// Blob.DeletePrefix. Once nothing is left it deletes the blob prefix {tenant}/{bank}/, then
	// BankRepo.Delete removes the bank row with its remaining events and operations, so a later write
	// creates a clean bank. A run that finds the bank gone (or no longer tombstoned) is done and deletes
	// no blobs.
	Run(ctx context.Context, j port.Job) (done bool, err error)
}

// JobFence tells a running job whether it still holds its operation; port.Claimer satisfies it.
type JobFence interface {
	// Alive reports whether the job should keep running.
	Alive(ctx context.Context, id port.OperationID) (bool, error)
}

// DeleterConfig bounds one delete_bank run.
type DeleterConfig struct {
	// BatchSize is the row limit of one DeleteBatch transaction; 0 = DefaultDeleteBatchSize.
	BatchSize int
	// MaxBatches is how many batches one run commits before it yields (done=false); 0 =
	// DefaultDeleteMaxBatches.
	MaxBatches int
}

// Delete budget defaults.
const (
	// DefaultDeleteBatchSize is the default rows per DeleteBatch transaction.
	DefaultDeleteBatchSize = 1000
	// DefaultDeleteMaxBatches is the default batches per delete_bank run.
	DefaultDeleteMaxBatches = 100
)

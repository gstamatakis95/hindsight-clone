package bank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// DeleteBankSerialKey is the delete_bank serial key (decision 1301).
const DeleteBankSerialKey = "bank"

// bankDeleter implements BankDeleter over port.Store and port.Blob.
type bankDeleter struct {
	s     port.Store
	blob  port.Blob
	fence JobFence
	clock port.Clock
	cfg   DeleterConfig
}

var _ BankDeleter = (*bankDeleter)(nil)

// NewBankDeleter returns the BankDeleter. fence (port.Claimer in production) is checked right before the
// blob prefix is deleted; clock stamps deleting_at.
func NewBankDeleter(s port.Store, blob port.Blob, fence JobFence, clock port.Clock, cfg DeleterConfig) BankDeleter {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultDeleteBatchSize
	}
	if cfg.MaxBatches <= 0 {
		cfg.MaxBatches = DefaultDeleteMaxBatches
	}
	return &bankDeleter{s: s, blob: blob, fence: fence, clock: clock, cfg: cfg}
}

// Tombstone marks the bank deleting, cancels its pending jobs and enqueues (or joins) delete_bank, in one
// transaction. It takes no change seq: Tx.Seq refuses a tombstoned bank, and nothing here is memory
// content. MarkDeleting runs first, so the bank row is locked before the queue touches it.
func (d *bankDeleter) Tombstone(ctx context.Context, b port.BankRef) (domain.Operation, error) {
	now := d.clock.Now()
	var op domain.Operation
	err := d.s.InTx(ctx, func(tx port.Tx) error {
		op = domain.Operation{}
		if _, err := tx.Banks().Get(ctx, b); err != nil {
			return err
		}
		if err := tx.Banks().MarkDeleting(ctx, b, now); err != nil {
			return err
		}
		if _, err := tx.Queue().CancelBank(ctx, b); err != nil {
			return err
		}
		id, _, err := tx.Queue().Enqueue(ctx, port.Job{
			Bank: b, Kind: domain.OperationKindDeleteBank, SerialKey: DeleteBankSerialKey,
			Payload: json.RawMessage(`{}`), Dedupe: port.DedupePendingOrRunning,
		})
		if err != nil {
			return err
		}
		op, err = tx.Queue().Get(ctx, b, id)
		return err
	})
	if err != nil {
		return domain.Operation{}, err
	}
	return op, nil
}

// bankGone reports a DeleteBatch or Delete error that means this delete has nothing left to do: the bank
// row is gone (an earlier run finished) or it is no longer tombstoned (a fresh bank took its place).
func bankGone(err error) bool {
	return errors.Is(err, domain.ErrNotFound) ||
		(errors.Is(err, domain.ErrFailedPrecondition) && !errors.Is(err, domain.ErrBankDeleting))
}

// Run deletes up to MaxBatches batches, each in its own transaction, and finishes the bank once a batch
// finds nothing left.
func (d *bankDeleter) Run(ctx context.Context, j port.Job) (bool, error) {
	for range d.cfg.MaxBatches {
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("%w: delete_bank %s: %w", domain.ErrCancelled, j.ID, err)
		}
		var n int
		err := d.s.InTx(ctx, func(tx port.Tx) error {
			var err error
			n, err = tx.Banks().DeleteBatch(ctx, j.Bank, d.cfg.BatchSize)
			return err
		})
		if bankGone(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if n == 0 {
			return d.finish(ctx, j)
		}
	}
	return false, nil
}

// finish deletes the blob prefix and then the bank row. Right before the prefix delete it re-checks that
// the job still holds its operation and that the bank is still tombstoned, so a stale duplicate run never
// deletes the blobs of a fresh bank that a write created after an earlier run finished.
func (d *bankDeleter) finish(ctx context.Context, j port.Job) (bool, error) {
	alive, err := d.fence.Alive(ctx, j.ID)
	if err != nil {
		return false, fmt.Errorf("delete_bank %s: alive check: %w", j.ID, err)
	}
	if !alive {
		return false, fmt.Errorf("%w: delete_bank %s no longer holds its operation", domain.ErrCancelled, j.ID)
	}
	deleting, err := d.stillDeleting(ctx, j.Bank)
	if err != nil || !deleting {
		return err == nil, err
	}
	if _, err := d.blob.DeletePrefix(ctx, domain.BankBlobPrefix(j.Bank)); err != nil {
		return false, err
	}
	err = d.s.InTx(ctx, func(tx port.Tx) error { return tx.Banks().Delete(ctx, j.Bank) })
	if err != nil && !bankGone(err) {
		return false, err
	}
	return true, nil
}

// stillDeleting reports whether bank b still exists and is tombstoned.
func (d *bankDeleter) stillDeleting(ctx context.Context, b port.BankRef) (bool, error) {
	var deleting bool
	err := d.s.InTx(ctx, func(tx port.Tx) error {
		bank, err := tx.Banks().Get(ctx, b)
		if err != nil {
			return err
		}
		deleting = bank.Deleting()
		return nil
	})
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return deleting, nil
}

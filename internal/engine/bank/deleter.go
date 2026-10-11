package bank

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/blob"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"time"
)

// BankDeleter deletes a bank in two phases: an api-side tombstone and a batched delete_bank job.
type BankDeleter interface { //nolint:revive // stutter: name fixed by the spec
	// Tombstone is the api side of DeleteBank: in one transaction it sets deleting_at, cancels the bank's
	// pending jobs (Queue.CancelBank spares delete_bank) and enqueues delete_bank with
	// DedupePendingOrRunning (Enqueue allows delete_bank on a tombstoned bank), returning that operation.
	// From then on writes fail with domain.ErrBankDeleting (Tx.Seq and Queue.Enqueue refuse the bank) and
	// reads with domain.ErrNotFound. Tombstoning an already deleting bank returns its delete_bank operation
	// (or a new one when the previous delete_bank failed or was cancelled: re-issuing DeleteBank resumes a
	// half-deleted bank). The returned operation can reach COMPLETED, or vanish with the bank row, while a
	// continuation still runs; the completion signal for clients is GetBank returning NOT_FOUND.
	Tombstone(ctx context.Context, b domain.BankRef) (domain.Operation, error)
	// Run is the delete_bank job: it removes rows with BankRepo.DeleteBatch in committed batches and
	// reports done=false when its budget runs out (the handler re-enqueues with DedupePending). It
	// re-checks the tombstone (deleting_at) and that the job is still alive before deleting blobs, and
	// again between blob pages. While another job of the bank is still
	// PROCESSING (a handler winding down after the tombstone), it deletes neither blobs nor the bank row:
	// it enqueues its own continuation DeleteBlockedDelay later and reports done=false, both before the
	// blob delete and in the transaction of the final BankRepo.Delete, so no job outlives its bank and
	// writes into a fresh one. Once nothing is left it deletes the blobs under {tenant}/{bank}/, then
	// BankRepo.Delete removes the bank row with its remaining events and operations, so a later write
	// creates a clean bank. A run that finds the bank gone (or no longer tombstoned) is done and deletes
	// no blobs.
	// The job's liveness comes from run.Alive.
	Run(ctx context.Context, run *jobs.Run) (done bool, err error)
}

// DeleterConfig bounds one delete_bank run.
type DeleterConfig struct {
	// BatchSize is the row limit of one DeleteBatch transaction; 0 = DefaultDeleteBatchSize.
	BatchSize int
	// MaxBatches is how many batches one run commits before it yields (done=false); 0 =
	// DefaultDeleteMaxBatches.
	MaxBatches int
	// BlobPageSize is the blob keys per listed page (deleted with a fence check between pages); 0 =
	// DefaultDeleteBlobPageSize.
	BlobPageSize int
}

// Delete budget defaults.
const (
	// DefaultDeleteBatchSize is the default rows per DeleteBatch transaction.
	DefaultDeleteBatchSize = 1000
	// DefaultDeleteMaxBatches is the default batches per delete_bank run.
	DefaultDeleteMaxBatches = 100
	// DefaultDeleteBlobPageSize is the default keys per listed blob page.
	DefaultDeleteBlobPageSize = 1000
	// DeleteBlockedDelay is how long a delete_bank continuation waits when another job of the bank is still
	// processing.
	DeleteBlockedDelay = 5 * time.Second
)

// bankDeleter implements BankDeleter over store.DB and blob.Store.
type bankDeleter struct {
	s     store.DB
	blob  blob.Store
	clock clock.Clock
	cfg   DeleterConfig
}

var _ BankDeleter = (*bankDeleter)(nil)

// NewBankDeleter returns the BankDeleter; clock stamps deleting_at.
func NewBankDeleter(s store.DB, blob blob.Store, clock clock.Clock, cfg DeleterConfig) BankDeleter {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultDeleteBatchSize
	}
	if cfg.MaxBatches <= 0 {
		cfg.MaxBatches = DefaultDeleteMaxBatches
	}
	if cfg.BlobPageSize <= 0 {
		cfg.BlobPageSize = DefaultDeleteBlobPageSize
	}
	return &bankDeleter{s: s, blob: blob, clock: clock, cfg: cfg}
}

// Tombstone marks the bank deleting, cancels its pending jobs and enqueues (or joins) delete_bank, in one
// transaction. It takes no change seq: Tx.Seq refuses a tombstoned bank, and nothing here is memory
// content. MarkDeleting runs first, so the bank row is locked before the queue touches it.
func (d *bankDeleter) Tombstone(ctx context.Context, b domain.BankRef) (domain.Operation, error) {
	now := d.clock.Now()
	var op domain.Operation
	err := d.s.InTx(ctx, func(tx store.Tx) error {
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
		id, _, err := jobs.EnqueueDeleteBank(ctx, tx, b)
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
func (d *bankDeleter) Run(ctx context.Context, run *jobs.Run) (bool, error) {
	j := run.Job
	for range d.cfg.MaxBatches {
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("%w: delete_bank %s: %w", domain.ErrCancelled, j.ID, err)
		}
		var n int
		err := d.s.InTx(ctx, func(tx store.Tx) error {
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
			return d.finish(ctx, run)
		}
	}
	return false, nil
}

// finish deletes the blobs and then the bank row. Before the blobs it checks the fence (the job still
// holds its operation and the bank is still tombstoned, so a stale duplicate run never deletes the blobs of
// a fresh bank) and that no other job of the bank is still processing; the processing check repeats in the
// transaction that deletes the bank row.
func (d *bankDeleter) finish(ctx context.Context, run *jobs.Run) (bool, error) {
	j := run.Job
	ok, err := d.fenced(ctx, run)
	if err != nil || !ok {
		return err == nil, err
	}
	var blocked bool
	err = d.s.InTx(ctx, func(tx store.Tx) error {
		var err error
		blocked, err = d.blockIfBusy(ctx, tx, j.Bank)
		return err
	})
	if err != nil || blocked {
		return false, err
	}
	if ok, err = d.deleteBlobs(ctx, run); err != nil || !ok {
		return err == nil, err
	}
	err = d.s.InTx(ctx, func(tx store.Tx) error {
		var err error
		if blocked, err = d.blockIfBusy(ctx, tx, j.Bank); err != nil || blocked {
			return err
		}
		return tx.Banks().Delete(ctx, j.Bank)
	})
	if err != nil && !bankGone(err) {
		return false, err
	}
	return !blocked, nil
}

// fenced reports whether the delete may go on: the job is alive (else domain.ErrCancelled) and the bank
// still exists and is tombstoned (else false: nothing left to do).
func (d *bankDeleter) fenced(ctx context.Context, run *jobs.Run) (bool, error) {
	j := run.Job
	alive, err := run.Alive(ctx)
	if err != nil {
		return false, fmt.Errorf("delete_bank %s: alive check: %w", j.ID, err)
	}
	if !alive {
		return false, fmt.Errorf("%w: delete_bank %s no longer holds its operation", domain.ErrCancelled, j.ID)
	}
	return d.stillDeleting(ctx, j.Bank)
}

// blockIfBusy reports whether a job of b other than delete_bank is still processing; if so it enqueues the
// delete's continuation DeleteBlockedDelay from now in tx (the handler's own DedupePending re-enqueue then
// joins it).
func (d *bankDeleter) blockIfBusy(ctx context.Context, tx store.Tx, b domain.BankRef) (bool, error) {
	busy, err := otherJobsProcessing(ctx, tx, b)
	if err != nil || !busy {
		return false, err
	}
	_, _, err = jobs.EnqueueDeleteBankContinuation(ctx, tx, b, d.clock.Now().Add(DeleteBlockedDelay))
	return true, err
}

// otherJobsProcessing reports whether b has a PROCESSING operation of a kind other than delete_bank.
func otherJobsProcessing(ctx context.Context, tx store.Tx, b domain.BankRef) (bool, error) {
	f := store.OperationFilter{Status: domain.OperationStatusProcessing, PageSize: 100}
	for {
		ops, next, err := tx.Queue().List(ctx, b, f)
		if err != nil {
			return false, err
		}
		for _, op := range ops {
			if op.Kind != domain.OperationKindDeleteBank {
				return true, nil
			}
		}
		if next == "" {
			return false, nil
		}
		f.PageToken = next
	}
}

// deleteBlobs deletes the bank's blobs page by page: it lists one page under the bank prefix, deletes each listed key
// (DeletePrefix of the full key: every listed key lies under the bank prefix being emptied, so nothing outside it is
// touched) and checks the fence before the next page. ok=false means a fence check found the bank gone or no longer
// tombstoned, so the delete stops there with nothing left to do.
func (d *bankDeleter) deleteBlobs(ctx context.Context, run *jobs.Run) (bool, error) {
	prefix := blob.BankPrefix(run.Job.Bank)
	after := ""
	for page := 0; ; page++ {
		if page > 0 {
			if ok, err := d.fenced(ctx, run); err != nil || !ok {
				return false, err
			}
		}
		keys, err := d.blob.List(ctx, prefix, after, d.cfg.BlobPageSize)
		if err != nil {
			return false, fmt.Errorf("list blobs under %s: %w", prefix, err)
		}
		if len(keys) == 0 {
			return true, nil
		}
		for _, k := range keys {
			if _, err := d.blob.DeletePrefix(ctx, k); err != nil {
				return false, fmt.Errorf("delete blob %s: %w", k, err)
			}
		}
		after = keys[len(keys)-1]
	}
}

// stillDeleting reports whether bank b still exists and is tombstoned.
func (d *bankDeleter) stillDeleting(ctx context.Context, b domain.BankRef) (bool, error) {
	var deleting bool
	err := d.s.InTx(ctx, func(tx store.Tx) error {
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

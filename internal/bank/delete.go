package bank

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/blob"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"time"
)

// DeleteConfig bounds one delete_bank run.
type DeleteConfig struct {
	// BatchSize is the row limit of one DeleteBatch transaction; 0 = DefaultDeleteBatchSize.
	BatchSize int
	// MaxBatches is how many batches one run commits before it yields and re-enqueues itself; 0 =
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

// withDefaults fills zero fields.
func (c DeleteConfig) withDefaults() DeleteConfig {
	if c.BatchSize <= 0 {
		c.BatchSize = DefaultDeleteBatchSize
	}
	if c.MaxBatches <= 0 {
		c.MaxBatches = DefaultDeleteMaxBatches
	}
	if c.BlobPageSize <= 0 {
		c.BlobPageSize = DefaultDeleteBlobPageSize
	}
	return c
}

// DeleteBank is the api side of the delete: in one transaction it sets deleting_at, cancels the bank's pending
// jobs (Queue.CancelBank spares delete_bank) and enqueues (or joins) delete_bank with DedupePendingOrRunning,
// returning that operation; an unknown bank is domain.ErrNotFound. It takes no change seq: Tx.Seq refuses a
// tombstoned bank, and nothing here is memory content. MarkDeleting runs first, so the bank row is locked before
// the queue touches it.
func (s *Service) DeleteBank(ctx context.Context, b domain.BankRef) (domain.Operation, error) {
	now := s.d.Clock.Now()
	var op domain.Operation
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
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

// RunDeleteJob handles delete_bank: it runs one bounded delete and re-enqueues the job (jobs.Requeue,
// DedupePending) while work is left. delete_bank retries without an attempt cap: a transient failure on the
// kind's last attempt enqueues a continuation after jobs.Backoff before the error fails this row, so a
// half-deleted bank is never abandoned.
func (s *Service) RunDeleteJob(ctx context.Context, run *jobs.Run) error {
	j := run.Job
	done, err := s.runDelete(ctx, run)
	if err != nil {
		err = fmt.Errorf("delete_bank: %w", err)
		if jobs.IsPermanent(err) || ctx.Err() != nil {
			return err
		}
		if limit := j.Kind.MaxAttempts(); limit > 0 && j.Attempt+1 >= limit {
			at := s.d.Clock.Now().Add(jobs.Backoff(j.Attempt))
			if eerr := jobs.Requeue(ctx, s.d.DB, j, jobs.SerialKeyDeleteBank, at); eerr != nil {
				return errors.Join(err, eerr)
			}
		}
		return err
	}
	if done {
		return nil
	}
	return jobs.Requeue(ctx, s.d.DB, j, jobs.SerialKeyDeleteBank, time.Time{})
}

// ResumeStalledDeletes re-enqueues delete_bank for every tombstoned bank whose delete stopped and returns how many
// it inserted: ReclaimStale and the shutdown release charge attempts and fail delete_bank at its cap with no
// continuation, which would leave the bank tombstoned forever. jobs.EnqueueDeleteBank joins a delete_bank another
// tick inserted meanwhile; a bank whose delete just finished is skipped. Each bank gets one short transaction with
// no change seq (Seq refuses a tombstoned bank); an error on one bank doesn't stop the rest.
func (s *Service) ResumeStalledDeletes(ctx context.Context) (int, error) {
	banks, err := s.d.Maintenance.StalledBankDeletes(ctx)
	if err != nil {
		return 0, fmt.Errorf("list stalled bank deletes: %w", err)
	}
	inserted := 0
	var errs []error
	for _, b := range banks {
		var joined bool
		err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
			var err error
			_, joined, err = jobs.EnqueueDeleteBank(ctx, tx, b)
			return err
		})
		switch {
		case errors.Is(err, domain.ErrBankDeleting) || errors.Is(err, domain.ErrNotFound):
		case err != nil:
			errs = append(errs, fmt.Errorf("bank %s: %w", b, err))
		case !joined:
			inserted++
		}
	}
	return inserted, errors.Join(errs...)
}

// bankGone reports a DeleteBatch or Delete error that means this delete has nothing left to do: the bank row is
// gone (an earlier run finished) or it is no longer tombstoned (a fresh bank took its place).
func bankGone(err error) bool {
	return errors.Is(err, domain.ErrNotFound) ||
		(errors.Is(err, domain.ErrFailedPrecondition) && !errors.Is(err, domain.ErrBankDeleting))
}

// runDelete removes rows with BankRepo.DeleteBatch, up to MaxBatches committed batches, and finishes the bank once
// a batch finds nothing left; done=false means work is left (the budget ran out, or a processing job blocked the
// finish). It re-checks the tombstone (deleting_at) and that the job is still alive before deleting blobs, and
// again between blob pages. While another job of the bank is still PROCESSING it deletes neither blobs nor the
// bank row: it enqueues its own continuation DeleteBlockedDelay later, both before the blob delete and in the
// transaction of the final BankRepo.Delete, so no job outlives its bank and writes into a fresh one. A run that
// finds the bank gone (or no longer tombstoned) is done and deletes no blobs.
func (s *Service) runDelete(ctx context.Context, run *jobs.Run) (bool, error) {
	j := run.Job
	for range s.d.Delete.MaxBatches {
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("%w: delete_bank %s: %w", domain.ErrCancelled, j.ID, err)
		}
		var n int
		err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
			var err error
			n, err = tx.Banks().DeleteBatch(ctx, j.Bank, s.d.Delete.BatchSize)
			return err
		})
		if bankGone(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if n == 0 {
			return s.finish(ctx, run)
		}
	}
	return false, nil
}

// finish deletes the blobs and then the bank row. Before the blobs it checks the fence (the job still holds its
// operation and the bank is still tombstoned, so a stale duplicate run never deletes the blobs of a fresh bank)
// and that no other job of the bank is still processing; the processing check repeats in the transaction that
// deletes the bank row.
func (s *Service) finish(ctx context.Context, run *jobs.Run) (bool, error) {
	j := run.Job
	ok, err := s.fenced(ctx, run)
	if err != nil || !ok {
		return err == nil, err
	}
	var blocked bool
	err = s.d.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		blocked, err = s.blockIfBusy(ctx, tx, j.Bank)
		return err
	})
	if err != nil || blocked {
		return false, err
	}
	if ok, err = s.deleteBlobs(ctx, run); err != nil || !ok {
		return err == nil, err
	}
	err = s.d.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		if blocked, err = s.blockIfBusy(ctx, tx, j.Bank); err != nil || blocked {
			return err
		}
		return tx.Banks().Delete(ctx, j.Bank)
	})
	if err != nil && !bankGone(err) {
		return false, err
	}
	return !blocked, nil
}

// fenced reports whether the delete may go on: the job is alive (else domain.ErrCancelled) and the bank still
// exists and is tombstoned (else false: nothing left to do).
func (s *Service) fenced(ctx context.Context, run *jobs.Run) (bool, error) {
	j := run.Job
	alive, err := run.Alive(ctx)
	if err != nil {
		return false, fmt.Errorf("delete_bank %s: alive check: %w", j.ID, err)
	}
	if !alive {
		return false, fmt.Errorf("%w: delete_bank %s no longer holds its operation", domain.ErrCancelled, j.ID)
	}
	return s.stillDeleting(ctx, j.Bank)
}

// blockIfBusy reports whether a job of b other than delete_bank is still processing; if so it enqueues the
// delete's continuation DeleteBlockedDelay from now in tx (the job's own DedupePending re-enqueue then joins it).
func (s *Service) blockIfBusy(ctx context.Context, tx store.Tx, b domain.BankRef) (bool, error) {
	busy, err := otherJobsProcessing(ctx, tx, b)
	if err != nil || !busy {
		return false, err
	}
	_, _, err = jobs.EnqueueDeleteBankContinuation(ctx, tx, b, s.d.Clock.Now().Add(DeleteBlockedDelay))
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

// deleteBlobs deletes the bank's blobs page by page: it lists one page under the bank prefix and deletes each listed
// key (DeletePrefix of the full key: every listed key lies under the bank prefix being emptied, so nothing outside it
// is touched), checking the fence before every page after the first. blob.Store.List returns a short page only when
// nothing is left, so a short page is the last one and no List is spent learning that. ok=false means a fence check
// found the bank gone or no longer tombstoned, so the delete stops there with nothing left to do.
func (s *Service) deleteBlobs(ctx context.Context, run *jobs.Run) (bool, error) {
	prefix := blob.BankPrefix(run.Job.Bank)
	size := s.d.Delete.BlobPageSize
	after := ""
	for page := 0; ; page++ {
		if page > 0 {
			if ok, err := s.fenced(ctx, run); err != nil || !ok {
				return false, err
			}
		}
		keys, err := s.d.Blob.List(ctx, prefix, after, size)
		if err != nil {
			return false, fmt.Errorf("list blobs under %s: %w", prefix, err)
		}
		for _, k := range keys {
			if _, err := s.d.Blob.DeletePrefix(ctx, k); err != nil {
				return false, fmt.Errorf("delete blob %s: %w", k, err)
			}
		}
		if len(keys) < size {
			return true, nil
		}
		after = keys[len(keys)-1]
	}
}

// stillDeleting reports whether bank b still exists and is tombstoned.
func (s *Service) stillDeleting(ctx context.Context, b domain.BankRef) (bool, error) {
	var deleting bool
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
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

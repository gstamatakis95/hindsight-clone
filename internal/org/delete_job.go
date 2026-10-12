package org

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/blob"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"time"
)

// next says when a delete_org run continues: done, or a continuation due at At (zero = now).
type next struct {
	done bool
	at   time.Time
}

// finished is the next of a run with nothing left to do.
var finished = next{done: true}

// RunDeleteJob handles delete_org: it runs one bounded pass of the org delete and re-enqueues the job (jobs.Requeue,
// DedupePending) while work is left. Like delete_bank it retries without an attempt cap: a transient failure on the
// kind's last attempt enqueues a continuation after jobs.Backoff before the error fails this row, so a half-deleted
// org is never abandoned (and the tick's resume_org_deletes step is the backstop).
func (s *Service) RunDeleteJob(ctx context.Context, run *jobs.Run) error {
	j := run.Job
	n, err := s.runDelete(ctx, run)
	if errors.Is(err, domain.ErrOrgNotFound) { // a duplicate run finished the org meanwhile
		return nil
	}
	if err != nil {
		err = fmt.Errorf("delete_org: %w", err)
		if jobs.IsPermanent(err) || ctx.Err() != nil {
			return err
		}
		if limit := j.Kind.MaxAttempts(); limit > 0 && j.Attempt+1 >= limit {
			at := s.d.Clock.Now().Add(jobs.Backoff(j.Attempt))
			if eerr := jobs.Requeue(ctx, s.d.DB, j, jobs.SerialKeyDeleteOrg, at); eerr != nil {
				return errors.Join(err, eerr)
			}
		}
		return err
	}
	if n.done {
		return nil
	}
	return jobs.Requeue(ctx, s.d.DB, j, jobs.SerialKeyDeleteOrg, n.at)
}

// runDelete runs one pass of the delete of the org run's job names:
//  1. fan out: enqueue delete_bank for every tombstoned bank without one, and wait (BankWait) while any bank row
//     of the org is left;
//  2. sweep, fence, blobs and verify (verify), up to VerifyAttempts rounds.
//
// It is idempotent: every step re-reads the org from Postgres, so a duplicate or resumed run picks up where the last
// one stopped. An org that is gone, or no longer tombstoned, is done.
func (s *Service) runDelete(ctx context.Context, run *jobs.Run) (next, error) {
	o := run.Job.Bank.OrgRef()
	rc, ok, err := s.loadReceipt(ctx, run)
	if err != nil || !ok {
		return finished, err
	}
	if err := s.report(ctx, run, "banks"); err != nil {
		return next{}, err
	}
	left, err := s.fanOut(ctx, o)
	if err != nil {
		return next{}, err
	}
	if left > 0 {
		return next{at: s.d.Clock.Now().Add(s.d.Delete.BankWait)}, nil
	}
	var leftover error
	for range s.d.Delete.VerifyAttempts {
		n, err := s.verify(ctx, run, rc)
		var lo *leftoverError
		if !errors.As(err, &lo) {
			return n, err
		}
		leftover = err
	}
	return next{}, leftover
}

// loadReceipt reads the run's org and its receipt: ok=false when the org is gone or not tombstoned (nothing to do).
func (s *Service) loadReceipt(ctx context.Context, run *jobs.Run) (domain.DeletionReceipt, bool, error) {
	var p jobs.DeleteOrgPayload
	if err := json.Unmarshal(run.Job.Payload, &p); err != nil {
		return domain.DeletionReceipt{}, false, fmt.Errorf("%w: delete_org payload: %w", jobs.ErrPermanent, err)
	}
	if p.ReceiptID == "" {
		return domain.DeletionReceipt{}, false, fmt.Errorf("%w: delete_org payload has no receipt id", jobs.ErrPermanent)
	}
	o := run.Job.Bank.OrgRef()
	var (
		rc       domain.DeletionReceipt
		deleting bool
	)
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		org, err := tx.Orgs().Get(ctx, o)
		if err != nil {
			return err
		}
		if deleting = org.Deleting(); !deleting {
			return nil
		}
		rc, err = tx.Receipts().Get(ctx, o.Tenant, p.ReceiptID)
		return err
	})
	if errors.Is(err, domain.ErrOrgNotFound) {
		return domain.DeletionReceipt{}, false, nil
	}
	return rc, deleting && err == nil, err
}

// report records the run's stage; it returns domain.ErrCancelled once the run no longer holds its operation.
func (s *Service) report(ctx context.Context, run *jobs.Run, stage string) error {
	return run.Report(ctx, domain.Progress{Stage: stage, RecordedAt: s.d.Clock.Now()})
}

// fanOut enqueues delete_bank for every tombstoned bank of o that has none pending or processing, FanoutBatch
// banks per transaction, and returns how many bank rows of o are left. The delete_bank jobs do the per-bank work
// (batches, blob pages, fencing, resume); the maintenance tick's resume_bank_deletes step is their backstop.
func (s *Service) fanOut(ctx context.Context, o domain.OrgRef) (int, error) {
	for {
		var banks []domain.BankRef
		err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
			var err error
			if banks, err = tx.Orgs().BanksAwaitingDelete(ctx, o, s.d.Delete.FanoutBatch); err != nil {
				return err
			}
			for _, b := range banks {
				if _, _, err := jobs.EnqueueDeleteBank(ctx, tx, b); err != nil && !errors.Is(err, domain.ErrNotFound) {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return 0, fmt.Errorf("fan out bank deletes: %w", err)
		}
		if len(banks) < s.d.Delete.FanoutBatch {
			break
		}
	}
	var left int
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		left, err = tx.Orgs().CountBanks(ctx, o)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("count banks: %w", err)
	}
	return left, nil
}

// leftoverError is a failed verification: rows or blobs of the org were found after the sweep.
type leftoverError struct {
	v domain.ReceiptVerification
}

// Error reports the counts.
func (e *leftoverError) Error() string {
	return fmt.Sprintf("verification found %d rows in %d tables and %d blobs of the org left", e.v.RowsRemaining,
		e.v.TablesChecked, e.v.BlobsRemaining)
}

// verify runs one round of steps 3 to 7: sweep the org's rows, check the fence, wait out the blob grace, delete
// and list the blobs, then finish in one transaction that deletes the org's operations, events and row and counts
// the org's rows in every table carrying org_id. Any blob or row left is a *leftoverError, after the receipt is
// stamped FAILED_VERIFICATION in its own transaction; the caller sweeps again.
func (s *Service) verify(ctx context.Context, run *jobs.Run, rc domain.DeletionReceipt) (next, error) {
	o := run.Job.Bank.OrgRef()
	if err := s.report(ctx, run, "sweep"); err != nil {
		return next{}, err
	}
	if n, err := s.sweep(ctx, o); err != nil || !n.done {
		return n, err
	}
	if n, err := s.fence(ctx, run); err != nil || !n.done {
		return n, err
	}
	if due := rc.RequestedAt.Add(s.d.Delete.BlobGrace); s.d.Clock.Now().Before(due) {
		return next{at: due}, nil
	}
	if err := s.report(ctx, run, "blobs"); err != nil {
		return next{}, err
	}
	if n, err := s.deleteBlobs(ctx, run); err != nil || !n.done {
		return n, err
	}
	blobsLeft, err := s.d.Blob.List(ctx, blob.OrgPrefix(o), "", 1)
	if err != nil {
		return next{}, fmt.Errorf("list blobs under %s: %w", blob.OrgPrefix(o), err)
	}
	if len(blobsLeft) > 0 {
		return next{}, s.failVerification(ctx, rc, domain.ReceiptVerification{BlobsRemaining: 1})
	}
	if err := s.report(ctx, run, "verify"); err != nil {
		return next{}, err
	}
	return s.finish(ctx, run, rc)
}

// sweep deletes the org's rows whatever their bank (stragglers, and rows a crash left behind a deleted bank) with
// OrgRepo.DeleteBatch, up to MaxBatches batches; done=false means work is left (re-enqueue now).
func (s *Service) sweep(ctx context.Context, o domain.OrgRef) (next, error) {
	for range s.d.Delete.MaxBatches {
		if err := ctx.Err(); err != nil {
			return next{}, fmt.Errorf("%w: delete_org %s: %w", domain.ErrCancelled, o, err)
		}
		var n int
		err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
			var err error
			n, err = tx.Orgs().DeleteBatch(ctx, o, s.d.Delete.SweepBatch)
			return err
		})
		if err != nil {
			return next{}, fmt.Errorf("sweep org rows: %w", err)
		}
		if n == 0 {
			return finished, nil
		}
	}
	return next{}, nil
}

// fence reports whether the irreversible steps may go on (done=true): the run still holds its operation (else
// domain.ErrCancelled), and no other operation of the org is processing (else a continuation BlockedDelay later).
func (s *Service) fence(ctx context.Context, run *jobs.Run) (next, error) {
	alive, err := run.Alive(ctx)
	if err != nil {
		return next{}, fmt.Errorf("alive check: %w", err)
	}
	if !alive {
		return next{}, fmt.Errorf("%w: delete_org %s no longer holds its operation", domain.ErrCancelled, run.Job.ID)
	}
	var busy bool
	err = s.d.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		busy, err = tx.Queue().ProcessingInOrg(ctx, run.Job.Bank.OrgRef(), run.Job.ID)
		return err
	})
	if err != nil {
		return next{}, fmt.Errorf("processing check: %w", err)
	}
	if busy {
		return next{at: s.d.Clock.Now().Add(BlockedDelay)}, nil
	}
	return finished, nil
}

// deleteBlobs deletes the org's blobs page by page: it lists one page under blob.OrgPrefix and deletes each listed
// key (DeletePrefix of the full key, so nothing outside the prefix is touched), checking the fence before every page
// after the first. A short page is the last one.
func (s *Service) deleteBlobs(ctx context.Context, run *jobs.Run) (next, error) {
	prefix := blob.OrgPrefix(run.Job.Bank.OrgRef())
	size := s.d.Delete.BlobPageSize
	after := ""
	for page := 0; ; page++ {
		if page > 0 {
			if n, err := s.fence(ctx, run); err != nil || !n.done {
				return n, err
			}
		}
		keys, err := s.d.Blob.List(ctx, prefix, after, size)
		if err != nil {
			return next{}, fmt.Errorf("list blobs under %s: %w", prefix, err)
		}
		for _, k := range keys {
			if _, err := s.d.Blob.DeletePrefix(ctx, k); err != nil {
				return next{}, fmt.Errorf("delete blob %s: %w", k, err)
			}
		}
		if len(keys) < size {
			return finished, nil
		}
		after = keys[len(keys)-1]
	}
}

// finish deletes the org in one transaction: it locks the org row, re-checks that nothing else of the org is
// processing, deletes the org's operations (its own row included, so the runner's Complete finds none, which it
// treats as done), events and row, counts the org's rows in every table carrying org_id and, when none is left,
// stamps the receipt COMPLETED. Any row left rolls the whole transaction back and is a *leftoverError.
func (s *Service) finish(ctx context.Context, run *jobs.Run, rc domain.DeletionReceipt) (next, error) {
	o := run.Job.Bank.OrgRef()
	var (
		n    next
		done domain.DeletionReceipt
	)
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		n, done = finished, domain.DeletionReceipt{}
		org, err := tx.Orgs().LockForDelete(ctx, o)
		if err != nil || !org.Deleting() {
			return err
		}
		if busy, err := tx.Queue().ProcessingInOrg(ctx, o, run.Job.ID); err != nil || busy {
			n = next{at: s.d.Clock.Now().Add(BlockedDelay)}
			return err
		}
		if err := tx.Orgs().Delete(ctx, o); err != nil {
			return err
		}
		tables, rows, err := tx.Orgs().CountRows(ctx, o)
		if err != nil {
			return err
		}
		v := domain.ReceiptVerification{TablesChecked: tables, RowsRemaining: rows, VerifiedAt: s.d.Clock.Now()}
		if rows > 0 {
			return &leftoverError{v: v}
		}
		done = completed(rc, v, s.d.ServerVersion)
		return tx.Receipts().Update(ctx, done)
	})
	var lo *leftoverError
	switch {
	case errors.As(err, &lo):
		return next{}, s.failVerification(ctx, rc, lo.v)
	case errors.Is(err, domain.ErrOrgNotFound):
		return finished, nil
	case err != nil:
		return next{}, fmt.Errorf("finish: %w", err)
	}
	if done.ID != "" {
		run.SetResult(map[string]any{"receipt_id": done.ID, "tables_checked": done.Verification.TablesChecked})
		s.d.Logger.InfoContext(ctx, "org delete completed", "receipt", done.ID)
	}
	return n, nil
}

// completed is rc stamped COMPLETED with verification v at v.VerifiedAt.
func completed(rc domain.DeletionReceipt, v domain.ReceiptVerification, version string) domain.DeletionReceipt {
	at := v.VerifiedAt
	rc.Status, rc.CompletedAt, rc.Verification, rc.ServerVersion = domain.ReceiptStatusCompleted, &at, &v, version
	return rc
}

// failVerification stamps rc FAILED_VERIFICATION with v in its own transaction (the finish transaction rolled back)
// and returns the *leftoverError the caller loops on.
func (s *Service) failVerification(ctx context.Context, rc domain.DeletionReceipt, v domain.ReceiptVerification) error {
	if v.VerifiedAt.IsZero() {
		v.VerifiedAt = s.d.Clock.Now()
	}
	rc.Status, rc.CompletedAt, rc.Verification, rc.ServerVersion =
		domain.ReceiptStatusFailedVerification, nil, &v, s.d.ServerVersion
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error { return tx.Receipts().Update(ctx, rc) })
	if err != nil {
		return fmt.Errorf("record failed verification: %w", err)
	}
	return &leftoverError{v: v}
}

package fake

import (
	"context"
	"encoding/json"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"time"
)

// Claim takes up to n due PENDING jobs of kinds, oldest first, following the spec's claim SQL: a job is
// skipped while its (bank, serial key) has a PROCESSING row or an older due PENDING row (any kind; an
// empty key never blocks), and jobs of tombstoned banks are skipped except delete_bank. Claimed rows
// become PROCESSING under worker with a fresh heartbeat.
func (s *Store) Claim(ctx context.Context, worker string, kinds []port.JobKind, n int) ([]port.Job, error) {
	var out []port.Job
	err := s.write(ctx, func(t *tx) error {
		out = nil
		for _, row := range t.st.claimable(kinds, t.now) {
			if len(out) >= n {
				break
			}
			if row.op.SerialKey != "" && t.st.keyBusy(row, t.now) {
				continue
			}
			row.op.Status, row.op.WorkerID, row.op.UpdatedAt = domain.OperationStatusProcessing, worker, t.now
			row.op.HeartbeatAt = ptrCopy(&t.now)
			t.st.ops[row.op.ID] = row
			out = append(out, jobOut(row))
		}
		return nil
	})
	return out, err
}

// claimable returns the due PENDING rows of kinds whose bank admits a claim, oldest first.
func (s *state) claimable(kinds []port.JobKind, now time.Time) []opRow {
	var rows []opRow
	for _, row := range s.ops {
		if row.op.Status != domain.OperationStatusPending || row.runAt.After(now) || !slices.Contains(kinds, row.op.Kind) {
			continue
		}
		bank, ok := s.banks[row.op.Bank]
		if !ok || (bank.bank.Deleting() && row.op.Kind != domain.OperationKindDeleteBank) {
			continue
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(x, y opRow) int {
		if olderOp(x, y) {
			return -1
		}
		return 1
	})
	return rows
}

// keyBusy reports whether o's (bank, serial key) has a PROCESSING row or an older due PENDING row.
func (s *state) keyBusy(o opRow, now time.Time) bool {
	for _, p := range s.ops {
		if p.op.ID == o.op.ID || p.op.Bank != o.op.Bank || p.op.SerialKey != o.op.SerialKey {
			continue
		}
		if p.op.Status == domain.OperationStatusProcessing {
			return true
		}
		if p.op.Status == domain.OperationStatusPending && !p.runAt.After(now) && olderOp(p, o) {
			return true
		}
	}
	return false
}

// Heartbeat refreshes heartbeat_at on the ids still PROCESSING under worker (cancel-requested ones
// included) and returns a *port.LostJobsError naming the ids whose cancel was requested or that worker no
// longer holds.
func (s *Store) Heartbeat(ctx context.Context, worker string, ids []port.OperationID) error {
	var lost []port.OperationID
	err := s.write(ctx, func(t *tx) error {
		lost = nil
		for _, id := range ids {
			row, ok := t.st.ops[id]
			if !ok || row.op.Status != domain.OperationStatusProcessing || row.op.WorkerID != worker {
				lost = append(lost, id)
				continue
			}
			row.op.HeartbeatAt = ptrCopy(&t.now)
			t.st.ops[id] = row
			if row.cancelRequested != nil {
				lost = append(lost, id)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(lost) > 0 {
		return &port.LostJobsError{IDs: lost}
	}
	return nil
}

// processing runs fn on a PROCESSING row: unknown is domain.ErrNotFound, any other status
// domain.ErrFailedPrecondition (and nothing changes).
func (s *Store) processing(ctx context.Context, id port.OperationID, fn func(t *tx, row opRow) error) error {
	return s.write(ctx, func(t *tx) error {
		row, ok := t.st.ops[id]
		if !ok {
			return notFound("operation %s", id)
		}
		if row.op.Status != domain.OperationStatusProcessing {
			return precondition("operation %s is %s, not processing", id, row.op.Status)
		}
		return fn(t, row)
	})
}

// Progress records the job's latest checkpoint (PROCESSING rows only).
func (s *Store) Progress(ctx context.Context, id port.OperationID, p domain.Progress) error {
	return s.processing(ctx, id, func(t *tx, row opRow) error {
		row.op.Progress, row.op.UpdatedAt = &p, t.now
		t.st.ops[id] = row
		return nil
	})
}

// Alive reports whether the job is PROCESSING without a cancel request and its bank is live (a
// delete_bank job stays alive on its tombstoned bank). Unknown ids are (false, nil). It reads the latest
// committed snapshot without the store lock, so it is safe inside an InTx callback.
func (s *Store) Alive(ctx context.Context, id port.OperationID) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, ctxErr(err)
	}
	st := s.snapshot()
	row, ok := st.ops[id]
	if !ok || row.op.Status != domain.OperationStatusProcessing || row.cancelRequested != nil {
		return false, nil
	}
	bank, ok := st.banks[row.op.Bank]
	return ok && (!bank.bank.Deleting() || row.op.Kind == domain.OperationKindDeleteBank), nil
}

// Complete marks the job COMPLETED with its result and usage; a cancel-requested job becomes CANCELLED
// instead and Complete returns domain.ErrCancelled.
func (s *Store) Complete(ctx context.Context, id port.OperationID, result json.RawMessage, usage []port.Usage) error {
	return s.processing(ctx, id, func(t *tx, row opRow) error {
		if row.cancelRequested != nil {
			t.st.ops[id] = finish(row, domain.OperationStatusCancelled, t.now)
			return t.commitWith(domain.ErrCancelled)
		}
		row = finish(row, domain.OperationStatusCompleted, t.now)
		row.op.Result, row.op.Usage = cloneRaw(result), slices.Clone(usage)
		t.st.ops[id] = row
		return nil
	})
}

// Fail records cause. With retryAt the job returns to PENDING at *retryAt with attempt+1; without, it
// becomes FAILED and an OPERATION_FAILED event is appended under a fresh bank change seq (no event when
// the bank is tombstoned or gone). A cancel-requested job becomes CANCELLED instead and Fail returns
// domain.ErrCancelled.
func (s *Store) Fail(ctx context.Context, id port.OperationID, cause error, retryAt *time.Time) error {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	return s.processing(ctx, id, func(t *tx, row opRow) error {
		row.op.Error = msg
		switch {
		case row.cancelRequested != nil:
			t.st.ops[id] = finish(row, domain.OperationStatusCancelled, t.now)
			return t.commitWith(domain.ErrCancelled)
		case retryAt != nil:
			row.op.Status, row.op.Attempts, row.op.UpdatedAt = domain.OperationStatusPending, row.op.Attempts+1, t.now
			row.runAt, row.op.WorkerID, row.op.HeartbeatAt = *retryAt, "", nil
			t.st.ops[id] = row
		default:
			t.failForGood(row)
		}
		return nil
	})
}

// failForGood moves row to FAILED and appends its OPERATION_FAILED event when the bank is live.
func (t *tx) failForGood(row opRow) {
	t.st.ops[row.op.ID] = finish(row, domain.OperationStatusFailed, t.now)
	seq, err := t.seq(row.op.Bank)
	if err != nil {
		return // tombstoned or gone bank: FAILED without the event
	}
	t.st.appendEvent(domain.LifecycleEvent{
		Seq: seq, Type: domain.EventTypeOperationFailed, Bank: row.op.Bank, At: t.now, OperationID: row.op.ID,
	}, t.now)
}

// ReclaimStale handles PROCESSING jobs whose heartbeat is older than ttl: cancel-requested ones become
// CANCELLED; below their kind's max attempts they return to PENDING with attempt+1 (run_at unchanged); at
// attempt+1 >= max they become FAILED with one OPERATION_FAILED event (none on a tombstoned or gone bank).
// It returns how many rows it changed.
func (s *Store) ReclaimStale(ctx context.Context, ttl time.Duration) (int, error) {
	n := 0
	err := s.write(ctx, func(t *tx) error {
		n = 0
		cutoff := t.now.Add(-ttl)
		for _, row := range t.st.ops {
			if row.op.Status != domain.OperationStatusProcessing || row.op.HeartbeatAt == nil ||
				!row.op.HeartbeatAt.Before(cutoff) {
				continue
			}
			n++
			maxAttempts := row.op.Kind.MaxAttempts()
			switch {
			case row.cancelRequested != nil:
				t.st.ops[row.op.ID] = finish(row, domain.OperationStatusCancelled, t.now)
			case maxAttempts > 0 && row.op.Attempts+1 >= maxAttempts:
				row.op.Error = "reclaimed after max attempts: worker heartbeat lost"
				t.failForGood(row)
			default:
				row.op.Status, row.op.Attempts, row.op.UpdatedAt = domain.OperationStatusPending, row.op.Attempts+1, t.now
				row.op.WorkerID, row.op.HeartbeatAt = "", nil
				t.st.ops[row.op.ID] = row
			}
		}
		return nil
	})
	return n, err
}

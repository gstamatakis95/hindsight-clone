package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// claimTries bounds Claim's retries after a lost race on the unique processing index.
	claimTries = 5
	// processingKeyIndex is the unique index that allows one processing job per serial key.
	processingKeyIndex = "operations_processing_serial_key_uniq"
)

// workTx runs fn in one short READ COMMITTED transaction on the pool and commits when fn returns nil.
func (s *DB) workTx(ctx context.Context, fn func(t *tx) error) error {
	pgTx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return mapErr("begin", err)
	}
	defer func() { _ = pgTx.Rollback(context.WithoutCancel(ctx)) }() // no-op after a commit
	if err := fn(newTx(pgTx)); err != nil {
		return err
	}
	if err := pgTx.Commit(ctx); err != nil {
		return mapErr("commit", err)
	}
	return nil
}

// Claim takes up to n due jobs of kinds with the spec's claim SQL and marks them PROCESSING under worker.
// A lost race on the unique processing index (or a deadlock between two claimers) aborts the transaction;
// Claim then retries in a new one, up to claimTries times, and returns an empty batch rather than an error.
func (s *DB) Claim(ctx context.Context, worker string, kinds []domain.OperationKind, n int) ([]store.Job, error) {
	if n <= 0 || len(kinds) == 0 {
		return []store.Job{}, nil
	}
	kindNames := make([]string, len(kinds))
	for i, k := range kinds {
		kindNames[i] = string(k)
	}
	limit := int32(min(n, 10_000)) //nolint:gosec // bounded above
	for range claimTries {
		jobs, err := s.claimOnce(ctx, worker, kindNames, limit)
		if err == nil {
			return jobs, nil
		}
		if !lostClaimRace(err) {
			return nil, err
		}
	}
	return []store.Job{}, nil
}

// claimOnce runs one claim transaction and returns the claimed jobs oldest first.
func (s *DB) claimOnce(ctx context.Context, worker string, kinds []string, limit int32) ([]store.Job, error) {
	var jobs []store.Job
	err := s.workTx(ctx, func(t *tx) error {
		rows, err := t.q.ClaimSelect(ctx, db.ClaimSelectParams{Column1: kinds, Limit: limit})
		if err != nil {
			return mapErr("claim", err)
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(rows))
		jobs = make([]store.Job, 0, len(rows))
		for _, row := range rows {
			j, err := jobOf(row)
			if err != nil {
				// A row that can't be decoded would be claimed (and fail) forever: fail it for good instead.
				// No OPERATION_FAILED event: that needs the bank row lock, which must not follow op row locks.
				msg := fmt.Sprintf("undecodable operation row: %v", err)
				if err := t.q.ClaimFailUndecodable(ctx, db.ClaimFailUndecodableParams{Error: &msg, ID: row.ID}); err != nil {
					return mapErr("claim: fail undecodable row", err)
				}
				continue
			}
			ids, jobs = append(ids, row.ID), append(jobs, j)
		}
		if len(ids) == 0 {
			return nil
		}
		if err := t.q.ClaimMark(ctx, db.ClaimMarkParams{WorkerID: &worker, Ids: ids}); err != nil {
			return mapErr("claim: mark processing", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if jobs == nil {
		jobs = []store.Job{}
	}
	return jobs, nil
}

// lostClaimRace reports whether a claim failed because a concurrent claim won: a unique violation of the
// processing serial-key index, or a deadlock between two claimers waiting on that index.
func lostClaimRace(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == codeDeadlockDetected ||
		(pgErr.Code == codeUniqueViolation && pgErr.ConstraintName == processingKeyIndex)
}

// Heartbeat refreshes the jobs still PROCESSING under worker and reports, as a *store.LostJobsError, every
// id that worker no longer holds or whose cancel was requested.
func (s *DB) Heartbeat(ctx context.Context, worker string, ids []domain.OperationID) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := db.New(s.pool).ClaimHeartbeat(ctx, db.ClaimHeartbeatParams{Ids: parseIDs(ids), WorkerID: &worker})
	if err != nil {
		return mapErr("heartbeat", err)
	}
	held := make(map[domain.OperationID]bool, len(rows))
	for _, row := range rows {
		held[idOf[domain.OperationID](row.ID)] = !row.CancelRequested
	}
	var lost []domain.OperationID
	for _, id := range ids {
		if !held[normalID(id)] && !slices.Contains(lost, id) {
			lost = append(lost, id)
		}
	}
	if len(lost) > 0 {
		return &store.LostJobsError{IDs: lost}
	}
	return nil
}

// normalID returns id in canonical UUID form when it parses, so it matches the ids the database returns.
func normalID(id domain.OperationID) domain.OperationID {
	u, err := uuid.Parse(string(id))
	if err != nil {
		return id
	}
	return idOf[domain.OperationID](u)
}

// Progress records a PROCESSING job's latest checkpoint.
func (s *DB) Progress(ctx context.Context, id domain.OperationID, p domain.Progress) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}
	raw, err := marshal("progress", p)
	if err != nil {
		return err
	}
	q := db.New(s.pool)
	n, err := q.ClaimProgress(ctx, db.ClaimProgressParams{Progress: raw, ID: uid})
	if err != nil {
		return mapErr(fmt.Sprintf("progress %s", id), err)
	}
	if n == 0 {
		return notProcessing(ctx, q, uid, "progress")
	}
	return nil
}

// notProcessing explains why a fenced update matched nothing: domain.ErrNotFound for an unknown id,
// domain.ErrFailedPrecondition for a job that is not PROCESSING.
func notProcessing(ctx context.Context, q *db.Queries, id uuid.UUID, what string) error {
	status, err := q.QueueStatus(ctx, id)
	if err != nil {
		return mapErr(fmt.Sprintf("%s %s", what, id), err)
	}
	return fmt.Errorf("%w: %s %s: status %s", domain.ErrFailedPrecondition, what, id, status)
}

// Alive reports whether the job is PROCESSING without a cancel request in a live bank (a delete_bank job
// stays alive in its tombstoned bank). An unknown id is (false, nil).
func (s *DB) Alive(ctx context.Context, id domain.OperationID) (bool, error) {
	uid, err := uuid.Parse(string(id))
	if err != nil {
		return false, nil //nolint:nilerr // a malformed id names no job, which is not alive
	}
	alive, err := db.New(s.pool).ClaimAlive(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, mapErr(fmt.Sprintf("alive %s", id), err)
	}
	return alive, nil
}

// Complete marks a PROCESSING job COMPLETED with its result and usage; a cancel-requested job becomes
// CANCELLED instead and Complete returns domain.ErrCancelled.
func (s *DB) Complete(ctx context.Context, id domain.OperationID, result json.RawMessage, usage []domain.Usage) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}
	if usage == nil {
		usage = []domain.Usage{}
	}
	rawUsage, err := marshal("usage", usage)
	if err != nil {
		return err
	}
	if len(result) == 0 {
		result = nil
	}
	q := db.New(s.pool)
	status, err := q.ClaimComplete(ctx, db.ClaimCompleteParams{Result: result, Usage: rawUsage, ID: uid})
	if errors.Is(err, pgx.ErrNoRows) {
		return notProcessing(ctx, q, uid, "complete")
	}
	if err != nil {
		return mapErr(fmt.Sprintf("complete %s", id), err)
	}
	return cancelledErr(id, status)
}

// cancelledErr returns domain.ErrCancelled when a terminal write ended the job CANCELLED.
func cancelledErr(id domain.OperationID, status string) error {
	if status == string(domain.OperationStatusCancelled) {
		return fmt.Errorf("%w: operation %s", domain.ErrCancelled, id)
	}
	return nil
}

// Fail records cause on a PROCESSING job. With retryAt it returns the job to PENDING at *retryAt with
// attempt+1; without, it ends FAILED with one OPERATION_FAILED event (none on a tombstoned or gone bank). A
// cancel-requested job becomes CANCELLED instead and Fail returns domain.ErrCancelled.
func (s *DB) Fail(ctx context.Context, id domain.OperationID, cause error, retryAt *time.Time) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}
	var errText *string
	if cause != nil {
		errText = new(string)
		*errText = cause.Error()
	}
	if retryAt == nil {
		status, err := s.finishFailed(ctx, uid, errText, nil)
		if err != nil {
			return err
		}
		return cancelledErr(id, status)
	}
	q := db.New(s.pool)
	status, err := q.ClaimRetryLater(ctx, db.ClaimRetryLaterParams{RunAt: *retryAt, Error: errText, ID: uid})
	if errors.Is(err, pgx.ErrNoRows) {
		return notProcessing(ctx, q, uid, "fail")
	}
	if err != nil {
		return mapErr(fmt.Sprintf("fail %s", id), err)
	}
	return cancelledErr(id, status)
}

// staleGuard restricts finishFailed to a job that is still stale at the attempt ReclaimStale saw.
type staleGuard struct {
	secs     float64
	attempts int32
}

// finishFailed ends a PROCESSING job FAILED (CANCELLED if its cancel was requested) in its own transaction
// and returns the new status. Lock order: it reads the job unlocked, bumps the bank's change seq (bank row
// first), then runs the fenced UPDATE; a FAILED job gets one OPERATION_FAILED event at that seq. A
// tombstoned or gone bank takes no seq and gets no event. A fenced update that matches nothing rolls the
// transaction back and returns domain.ErrNotFound or domain.ErrFailedPrecondition.
func (s *DB) finishFailed(ctx context.Context, id uuid.UUID, errText *string, guard *staleGuard) (string, error) {
	var status string
	err := s.workTx(ctx, func(t *tx) error {
		peek, err := t.q.ClaimPeek(ctx, id)
		if err != nil {
			return mapErr(fmt.Sprintf("fail %s", id), err)
		}
		if peek.Status != string(domain.OperationStatusProcessing) {
			return fmt.Errorf("%w: fail %s: status %s", domain.ErrFailedPrecondition, id, peek.Status)
		}
		bank := domain.NewBankRef(peek.TenantID, peek.OrgID, peek.BankID)
		var seq int64
		if !peek.CancelRequested {
			if seq, err = liveSeq(ctx, t, bank); err != nil {
				return err
			}
		}
		arg := db.ClaimFinishFailedParams{Error: errText, ID: id}
		if guard != nil {
			arg.StaleSecs.Float64, arg.StaleSecs.Valid, arg.Attempts = guard.secs, true, guard.attempts
		}
		if status, err = t.q.ClaimFinishFailed(ctx, arg); errors.Is(err, pgx.ErrNoRows) {
			return notProcessing(ctx, t.q, id, "fail")
		} else if err != nil {
			return mapErr(fmt.Sprintf("fail %s", id), err)
		}
		if status != string(domain.OperationStatusFailed) || seq == 0 {
			return nil
		}
		err = t.q.ClaimAppendFailedEvent(ctx, db.ClaimAppendFailedEventParams{
			TenantID: bank.Tenant, OrgID: bank.Org, BankID: bank.Bank, Seq: seq, OperationID: uuid.NullUUID{UUID: id, Valid: true},
		})
		return mapErr(fmt.Sprintf("fail %s: event", id), err)
	})
	return status, err
}

// liveSeq bumps b's change seq through the transaction; a tombstoned or gone bank returns 0 and no error.
func liveSeq(ctx context.Context, t *tx, b domain.BankRef) (int64, error) {
	seq, err := t.Seq(ctx, b)
	if errors.Is(err, domain.ErrBankDeleting) || errors.Is(err, domain.ErrNotFound) {
		return 0, nil
	}
	return seq, err
}

// ReclaimStale handles PROCESSING jobs whose heartbeat is older than ttl (database time): below their kind's
// max attempts they return to PENDING with attempt+1; cancel-requested ones become CANCELLED; the rest
// become FAILED, each in its own transaction with one OPERATION_FAILED event. One job's failure never
// stops the sweep; its error is returned with the count after the other jobs are handled.
func (s *DB) ReclaimStale(ctx context.Context, ttl time.Duration) (int, error) {
	secs := ttl.Seconds()
	q := db.New(s.pool)
	stale, err := q.ClaimStale(ctx, secs)
	if err != nil {
		return 0, mapErr("reclaim: scan", err)
	}
	var requeue, cancel []uuid.UUID
	var fail []db.ClaimStaleRow
	for _, row := range stale {
		maxAttempts := domain.OperationKind(row.Kind).MaxAttempts()
		switch {
		case row.CancelRequested:
			cancel = append(cancel, row.ID)
		case maxAttempts == 0 || int(row.Attempts)+1 < maxAttempts:
			requeue = append(requeue, row.ID)
		default:
			fail = append(fail, row)
		}
	}
	total := 0
	var errs []error
	if len(requeue) > 0 {
		n, err := q.ClaimRequeueStale(ctx, db.ClaimRequeueStaleParams{Ids: requeue, StaleSecs: secs})
		total += int(n)
		errs = append(errs, mapErr("reclaim: requeue", err))
	}
	if len(cancel) > 0 {
		n, err := q.ClaimCancelStale(ctx, db.ClaimCancelStaleParams{Ids: cancel, StaleSecs: secs})
		total += int(n)
		errs = append(errs, mapErr("reclaim: cancel", err))
	}
	for _, row := range fail {
		msg := fmt.Sprintf("worker heartbeat lost on attempt %d of %d", row.Attempts+1,
			domain.OperationKind(row.Kind).MaxAttempts())
		_, err := s.finishFailed(ctx, row.ID, &msg, &staleGuard{secs: secs, attempts: row.Attempts})
		switch {
		case err == nil:
			total++
		case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrFailedPrecondition):
			// Finished, reclaimed or heartbeated since the scan: nothing to do.
		default:
			errs = append(errs, err)
		}
	}
	return total, errors.Join(errs...)
}

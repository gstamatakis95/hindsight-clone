package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// queue is store.Queue over one transaction.
type queue struct {
	q  *db.Queries
	tx *tx
}

var _ store.Queue = (*queue)(nil)

// Enqueue inserts j, or joins (and locks FOR UPDATE) a joinable row under its effective dedupe policy. It
// takes FOR KEY SHARE on the bank row first and the dedupe advisory lock in its own statement before the
// lookup. A deadlock with another enqueuer surfaces as domain.ErrConflict.
func (r *queue) Enqueue(ctx context.Context, j store.Job) (domain.OperationID, bool, error) {
	b := j.Bank
	deletingAt, err := r.q.QueueLockBank(ctx, db.QueueLockBankParams{TenantID: b.Tenant, ID: b.Bank})
	if err != nil {
		return "", false, mapErr(fmt.Sprintf("enqueue: bank %s/%s", b.Tenant, b.Bank), err)
	}
	if deletingAt != nil && j.Kind != domain.OperationKindDeleteBank {
		return "", false, fmt.Errorf("%w: enqueue %s on %s/%s", domain.ErrBankDeleting, j.Kind, b.Tenant, b.Bank)
	}
	policy := effectiveDedupe(j)
	if policy != store.DedupeNone {
		id, found, err := r.findJoinable(ctx, j, policy)
		if err != nil || found {
			return id, found, err
		}
	}
	id, err := r.insert(ctx, j, policy != store.DedupeNone)
	return id, false, err
}

// effectiveDedupe is the policy Enqueue applies to j: refresh_mental_model always dedupes as Pending, a job
// with overrides never dedupes, and a job without a serial key never dedupes.
func effectiveDedupe(j store.Job) store.DedupePolicy {
	switch {
	case j.SerialKey == "":
		return store.DedupeNone
	case j.Kind == domain.OperationKindRefreshMentalModel:
		return store.DedupePending
	case j.Overrides != nil:
		return store.DedupeNone
	default:
		return j.Dedupe
	}
}

// findJoinable takes the (tenant, bank, kind, serial key) advisory lock, then looks for a joinable row under
// policy and locks it FOR UPDATE.
func (r *queue) findJoinable(
	ctx context.Context, j store.Job, policy store.DedupePolicy,
) (domain.OperationID, bool, error) {
	b := j.Bank
	if err := r.q.QueueDedupeLock(ctx, db.QueueDedupeLockParams{
		TenantID: b.Tenant, BankID: b.Bank, Kind: string(j.Kind), SerialKey: j.SerialKey,
	}); err != nil {
		return "", false, mapErr("enqueue: dedupe lock", err)
	}
	id, err := r.q.QueueFindJoinable(ctx, db.QueueFindJoinableParams{
		TenantID: b.Tenant, BankID: b.Bank, Kind: string(j.Kind), SerialKey: j.SerialKey,
		IncludeRunning: policy == store.DedupePendingOrRunning,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, mapErr("enqueue: dedupe lookup", err)
	}
	return idOf[domain.OperationID](id), true, nil
}

// insert stores j as a new pending row with a fresh UUIDv7 id (or j.ID when set).
func (r *queue) insert(ctx context.Context, j store.Job, joinable bool) (domain.OperationID, error) {
	id, err := newOpID(j.ID)
	if err != nil {
		return "", err
	}
	parent, err := nullID(j.ParentID)
	if err != nil {
		return "", err
	}
	overrides, err := marshalPtr("overrides", j.Overrides)
	if err != nil {
		return "", err
	}
	payload := j.Payload
	if len(payload) == 0 || string(payload) == "null" {
		payload = json.RawMessage(`{}`)
	}
	err = r.q.QueueInsert(ctx, db.QueueInsertParams{
		ID: id, TenantID: j.Bank.Tenant, BankID: j.Bank.Bank, Kind: string(j.Kind),
		SerialKey: textPtr(j.SerialKey), Payload: payload, Overrides: overrides, Joinable: joinable,
		ParentID: parent, Attempts: int32(j.Attempt), RunAt: orNow(j.RunAt), //nolint:gosec // attempts are small
	})
	if err != nil {
		return "", mapErr("enqueue: insert", err)
	}
	return idOf[domain.OperationID](id), nil
}

// newOpID returns the id of a new operation: id itself when set, else a UUIDv7, so ids of one process
// sort by creation and break created_at ties in insertion order.
func newOpID(id domain.OperationID) (uuid.UUID, error) {
	if id != "" {
		return parseNewID(id)
	}
	u, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: new operation id: %w", domain.ErrUnavailable, err)
	}
	return u, nil
}

// Upgrade shallow-merges payload into a PENDING job (force_full OR-ed) and replaces its overrides when o is
// non-nil, under the row lock.
func (r *queue) Upgrade(
	ctx context.Context, id domain.OperationID, payload json.RawMessage, o *domain.BankConfig,
) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}
	row, err := r.q.QueueLockOp(ctx, uid)
	if err != nil {
		return mapErr(fmt.Sprintf("upgrade %s", id), err)
	}
	if row.Status != string(domain.OperationStatusPending) {
		return fmt.Errorf("%w: upgrade %s: status %s", domain.ErrFailedPrecondition, id, row.Status)
	}
	merged, err := mergePayload(row.Payload, payload)
	if err != nil {
		return err
	}
	overrides := row.Overrides
	if o != nil {
		if overrides, err = marshal("overrides", o); err != nil {
			return err
		}
	}
	err = r.q.QueueSetPayload(ctx, db.QueueSetPayloadParams{Payload: merged, Overrides: overrides, ID: uid})
	return mapErr(fmt.Sprintf("upgrade %s", id), err)
}

// forceFullKey is the payload flag Upgrade OR-s instead of replacing.
const forceFullKey = "force_full"

// mergePayload shallow-merges patch into base: present keys replace, absent keys stay, force_full is OR-ed.
// A nil or empty patch keeps base.
func mergePayload(base, patch json.RawMessage) (json.RawMessage, error) {
	if len(patch) == 0 || string(patch) == "null" {
		return base, nil
	}
	out := map[string]json.RawMessage{}
	if err := unmarshal("queued payload", base, &out); err != nil {
		return nil, err
	}
	var add map[string]json.RawMessage
	if err := json.Unmarshal(patch, &add); err != nil {
		return nil, fmt.Errorf("%w: upgrade payload is not a JSON object: %w", domain.ErrInvalidArgument, err)
	}
	for k, v := range add {
		if k == forceFullKey && jsonTrue(out[k]) {
			continue
		}
		out[k] = v
	}
	return marshal("payload", out)
}

// jsonTrue reports whether raw is the JSON literal true.
func jsonTrue(raw json.RawMessage) bool {
	var v bool
	return json.Unmarshal(raw, &v) == nil && v
}

// Reschedule sets a PENDING job's run_at.
func (r *queue) Reschedule(ctx context.Context, id domain.OperationID, at time.Time) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}
	n, err := r.q.QueueReschedule(ctx, db.QueueRescheduleParams{RunAt: at, ID: uid})
	if err != nil {
		return mapErr(fmt.Sprintf("reschedule %s", id), err)
	}
	if n == 0 {
		return r.notPending(ctx, uid, "reschedule")
	}
	return nil
}

// notPending explains why a pending-only update matched nothing: domain.ErrNotFound for an unknown id,
// domain.ErrFailedPrecondition otherwise.
func (r *queue) notPending(ctx context.Context, id uuid.UUID, what string) error {
	status, err := r.q.QueueStatus(ctx, id)
	if err != nil {
		return mapErr(fmt.Sprintf("%s %s", what, id), err)
	}
	return fmt.Errorf("%w: %s %s: status %s", domain.ErrFailedPrecondition, what, id, status)
}

// CancelBank cancels every PENDING job of b except its delete_bank job.
func (r *queue) CancelBank(ctx context.Context, b domain.BankRef) (int, error) {
	n, err := r.q.QueueCancelBank(ctx, db.QueueCancelBankParams{TenantID: b.Tenant, BankID: b.Bank})
	if err != nil {
		return 0, mapErr(fmt.Sprintf("cancel bank %s/%s", b.Tenant, b.Bank), err)
	}
	return int(n), nil
}

// Get returns one operation of b.
func (r *queue) Get(ctx context.Context, b domain.BankRef, id domain.OperationID) (domain.Operation, error) {
	uid, err := parseID(id)
	if err != nil {
		return domain.Operation{}, err
	}
	row, err := r.q.QueueGet(ctx, db.QueueGetParams{TenantID: b.Tenant, BankID: b.Bank, ID: uid})
	if err != nil {
		return domain.Operation{}, mapErr(fmt.Sprintf("operation %s", id), err)
	}
	return operationOf(row)
}

// List pages b's operations newest first; the token is the last row's (created_at, id). A page size of 0 or less
// is domain.ListPage's default and a larger one is capped at its maximum.
func (r *queue) List(
	ctx context.Context, b domain.BankRef, f store.OperationFilter,
) ([]domain.Operation, string, error) {
	size := f.PageSize
	if size <= 0 {
		size = domain.ListPage.Default
	}
	size = min(size, domain.ListPage.Max)
	arg := db.QueueListParams{
		TenantID: b.Tenant, BankID: b.Bank, Status: textPtr(f.Status), Kind: textPtr(f.Kind),
		ExcludeParents: f.ExcludeParents, Lim: int32(size + 1), //nolint:gosec // size <= domain.ListPage.Max
	}
	if f.PageToken != "" {
		at, id, err := decodeOpsToken(f.PageToken)
		if err != nil {
			return nil, "", err
		}
		arg.AfterCreatedAt, arg.AfterID = &at, id
	}
	rows, err := r.q.QueueList(ctx, arg)
	if err != nil {
		return nil, "", mapErr("list operations", err)
	}
	next := ""
	if len(rows) > size {
		rows = rows[:size]
		last := rows[size-1]
		next = encodeOpsToken(last.CreatedAt, last.ID)
	}
	ops := make([]domain.Operation, 0, len(rows))
	for _, row := range rows {
		op, err := operationOf(row)
		if err != nil {
			return nil, "", err
		}
		ops = append(ops, op)
	}
	return ops, next, nil
}

// Cancel cancels a PENDING operation at once, or records the cancel request of a PROCESSING one.
func (r *queue) Cancel(ctx context.Context, b domain.BankRef, id domain.OperationID) (domain.Operation, error) {
	uid, err := parseID(id)
	if err != nil {
		return domain.Operation{}, err
	}
	row, err := r.q.QueueCancel(ctx, db.QueueCancelParams{TenantID: b.Tenant, BankID: b.Bank, ID: uid})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Operation{}, r.wrongStatus(ctx, b, uid, "cancel", domain.ErrOperationNotCancellable)
	}
	if err != nil {
		return domain.Operation{}, mapErr(fmt.Sprintf("cancel %s", id), err)
	}
	return operationOf(row)
}

// Retry re-queues a FAILED or CANCELLED operation as PENDING, due now, at attempt 0.
func (r *queue) Retry(ctx context.Context, b domain.BankRef, id domain.OperationID) (domain.Operation, error) {
	uid, err := parseID(id)
	if err != nil {
		return domain.Operation{}, err
	}
	if err := r.lockForRetry(ctx, b, uid); err != nil {
		return domain.Operation{}, err
	}
	row, err := r.q.QueueRetry(ctx, db.QueueRetryParams{TenantID: b.Tenant, BankID: b.Bank, ID: uid})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Operation{}, r.wrongStatus(ctx, b, uid, "retry", domain.ErrOperationNotRetryable)
	}
	if err != nil {
		return domain.Operation{}, mapErr(fmt.Sprintf("retry %s", id), err)
	}
	return operationOf(row)
}

// lockForRetry takes the locks Enqueue takes before a row becomes PENDING (and possibly joinable) again:
// FOR KEY SHARE on the bank row, refusing a tombstoned bank (domain.ErrBankDeleting) except for its
// delete_bank job, then the row's dedupe advisory lock, so a concurrent Enqueue can't insert a duplicate.
func (r *queue) lockForRetry(ctx context.Context, b domain.BankRef, id uuid.UUID) error {
	row, err := r.q.QueueGet(ctx, db.QueueGetParams{TenantID: b.Tenant, BankID: b.Bank, ID: id})
	if err != nil {
		return mapErr(fmt.Sprintf("retry %s", id), err)
	}
	deletingAt, err := r.q.QueueLockBank(ctx, db.QueueLockBankParams{TenantID: b.Tenant, ID: b.Bank})
	if err != nil {
		return mapErr(fmt.Sprintf("retry: bank %s/%s", b.Tenant, b.Bank), err)
	}
	if deletingAt != nil && row.Kind != string(domain.OperationKindDeleteBank) {
		return fmt.Errorf("%w: retry %s on %s/%s", domain.ErrBankDeleting, id, b.Tenant, b.Bank)
	}
	if row.SerialKey == nil {
		return nil
	}
	err = r.q.QueueDedupeLock(ctx, db.QueueDedupeLockParams{
		TenantID: b.Tenant, BankID: b.Bank, Kind: row.Kind, SerialKey: *row.SerialKey,
	})
	return mapErr("retry: dedupe lock", err)
}

// wrongStatus explains why a bank-scoped status update (cancel or retry) matched nothing: domain.ErrNotFound
// when b has no such operation, else class (domain.ErrOperationNotCancellable or ErrOperationNotRetryable).
func (r *queue) wrongStatus(ctx context.Context, b domain.BankRef, id uuid.UUID, what string, class error) error {
	row, err := r.q.QueueGet(ctx, db.QueueGetParams{TenantID: b.Tenant, BankID: b.Bank, ID: id})
	if err != nil {
		return mapErr(fmt.Sprintf("%s %s", what, id), err)
	}
	return fmt.Errorf("%w: %s %s: status %s", class, what, id, row.Status)
}

// Prune deletes terminal operations finished before before.
func (r *queue) Prune(ctx context.Context, before time.Time) (int, error) {
	n, err := r.q.QueuePrune(ctx, before)
	if err != nil {
		return 0, mapErr("prune operations", err)
	}
	return int(n), nil
}

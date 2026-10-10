package fake

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"strconv"
	"time"
)

// List page sizes.
const (
	defaultPageSize = 100
	maxPageSize     = 1000
)

// queue is the Queue (operations table) of one transaction.
type queue struct{ t *tx }

var _ port.Queue = queue{}

// Enqueue inserts j or joins a joinable row. See port.Queue: rows enqueued with DedupeNone or with
// overrides, and cancel-requested rows, are never joined; refresh_mental_model always dedupes as Pending
// and is always joinable. A tombstoned bank is domain.ErrBankDeleting (except for delete_bank); an unknown
// bank is domain.ErrNotFound. The transaction lock serializes concurrent enqueuers.
func (q queue) Enqueue(_ context.Context, j port.Job) (port.OperationID, bool, error) {
	st := q.t.st
	bank, ok := st.banks[j.Bank]
	if !ok {
		return "", false, notFound("bank %s/%s", j.Bank.Tenant, j.Bank.Bank)
	}
	if bank.bank.Deleting() && j.Kind != domain.OperationKindDeleteBank {
		return "", false, fmt.Errorf("%w: bank %s/%s", domain.ErrBankDeleting, j.Bank.Tenant, j.Bank.Bank)
	}
	if len(j.Payload) > 0 && !json.Valid(j.Payload) {
		return "", false, fmt.Errorf("%w: job payload is not valid JSON", domain.ErrInvalidArgument)
	}
	dedupe := j.Dedupe
	isRefresh := j.Kind == domain.OperationKindRefreshMentalModel
	if isRefresh && dedupe == port.DedupeNone {
		dedupe = port.DedupePending
	}
	joinable := isRefresh || (dedupe != port.DedupeNone && j.Overrides == nil)
	if joinable {
		if id, ok := st.joinableRow(j, dedupe); ok {
			return id, true, nil
		}
	}
	return q.insert(j, joinable), false, nil
}

// joinableRow returns the oldest joinable row j can join under dedupe.
func (s *state) joinableRow(j port.Job, dedupe port.DedupePolicy) (port.OperationID, bool) {
	var found *opRow
	for _, row := range s.ops {
		op := row.op
		if !row.joinable || row.cancelRequested != nil || op.Bank != j.Bank || op.Kind != j.Kind ||
			op.SerialKey != j.SerialKey {
			continue
		}
		running := dedupe == port.DedupePendingOrRunning && op.Status == domain.OperationStatusProcessing
		if op.Status != domain.OperationStatusPending && !running {
			continue
		}
		if found == nil || olderOp(row, *found) {
			found = &row
		}
	}
	if found == nil {
		return "", false
	}
	return found.op.ID, true
}

// olderOp orders operations by (created_at, insertion order).
func olderOp(x, y opRow) bool {
	return cmp.Or(x.op.CreatedAt.Compare(y.op.CreatedAt), cmp.Compare(x.order, y.order)) < 0
}

// insert stores a new PENDING row for j and returns its id.
func (q queue) insert(j port.Job, joinable bool) port.OperationID {
	st := q.t.st
	id := port.OperationID(newUUID())
	runAt := j.RunAt
	if runAt.IsZero() {
		runAt = q.t.now
	}
	payload := j.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	row := opRow{
		op: domain.Operation{
			ID: id, Bank: j.Bank, Kind: j.Kind, Status: domain.OperationStatusPending, SerialKey: j.SerialKey,
			ParentID: j.ParentID, CreatedAt: q.t.now, UpdatedAt: q.t.now,
		},
		payload: compactJSON(payload), overrides: jsonOf(j.Overrides), runAt: runAt, joinable: joinable,
		order: st.next(),
	}
	st.ops[id] = row
	return id
}

// compactJSON returns a compacted copy of valid JSON (a plain copy if it is not valid).
func compactJSON(raw json.RawMessage) json.RawMessage {
	out, err := json.Marshal(raw)
	if err != nil {
		return slices.Clone(raw)
	}
	return out
}

// Upgrade merges a joiner's payload into a PENDING row (present keys replace, force_full is OR-ed) and
// replaces its overrides when o is non-nil. Not PENDING is domain.ErrFailedPrecondition; unknown is
// domain.ErrNotFound. A payload that is not a JSON object is domain.ErrInvalidArgument.
func (q queue) Upgrade(_ context.Context, id port.OperationID, payload json.RawMessage, o *domain.BankConfig) error {
	row, ok := q.t.st.ops[id]
	if !ok {
		return notFound("operation %s", id)
	}
	if row.op.Status != domain.OperationStatusPending {
		return precondition("operation %s is %s, not pending", id, row.op.Status)
	}
	merged, err := mergePayload(row.payload, payload)
	if err != nil {
		return err
	}
	row.payload = merged
	if o != nil {
		row.overrides = jsonOf(o)
	}
	row.op.UpdatedAt = q.t.now
	q.t.st.ops[id] = row
	return nil
}

// mergePayload shallow-merges add into base: keys in add replace, except force_full, which is OR-ed.
func mergePayload(base, add json.RawMessage) (json.RawMessage, error) {
	if len(add) == 0 {
		return base, nil
	}
	var b, a map[string]json.RawMessage
	if err := json.Unmarshal(base, &b); err != nil || b == nil {
		b = map[string]json.RawMessage{}
	}
	if err := json.Unmarshal(add, &a); err != nil {
		return nil, fmt.Errorf("%w: upgrade payload is not a JSON object: %w", domain.ErrInvalidArgument, err)
	}
	for k, v := range a {
		if k == "force_full" && string(b[k]) == "true" {
			continue
		}
		b[k] = v
	}
	out, err := json.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("fake queue: merge payload: %w", err)
	}
	return out, nil
}

// Reschedule sets run_at of a PENDING row; any other status is domain.ErrFailedPrecondition.
func (q queue) Reschedule(_ context.Context, id port.OperationID, at time.Time) error {
	row, ok := q.t.st.ops[id]
	if !ok {
		return notFound("operation %s", id)
	}
	if row.op.Status != domain.OperationStatusPending {
		return precondition("operation %s is %s, not pending", id, row.op.Status)
	}
	row.runAt, row.op.UpdatedAt = at, q.t.now
	q.t.st.ops[id] = row
	return nil
}

// CancelBank cancels every PENDING job of b except delete_bank and returns how many.
func (q queue) CancelBank(_ context.Context, b port.BankRef) (int, error) {
	n := 0
	for id, row := range q.t.st.ops {
		if row.op.Bank == b && row.op.Status == domain.OperationStatusPending &&
			row.op.Kind != domain.OperationKindDeleteBank {
			q.t.st.ops[id] = finish(row, domain.OperationStatusCancelled, q.t.now)
			n++
		}
	}
	return n, nil
}

// finish moves row to a terminal status at now and releases its worker.
func finish(row opRow, status domain.OperationStatus, now time.Time) opRow {
	row.op.Status, row.op.UpdatedAt = status, now
	row.finishedAt = ptrCopy(&now)
	row.op.WorkerID, row.op.HeartbeatAt = "", nil
	return row
}

// Get returns one operation of b; unknown (or another bank's) is domain.ErrNotFound.
func (q queue) Get(_ context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error) {
	row, ok := q.t.st.ops[id]
	if !ok || row.op.Bank != b {
		return domain.Operation{}, notFound("operation %s", id)
	}
	return opOut(row), nil
}

// List pages b's operations newest first; the page token is an offset.
func (q queue) List(_ context.Context, b port.BankRef, f port.OperationFilter) ([]domain.Operation, string, error) {
	var rows []opRow
	for _, row := range q.t.st.ops {
		if opMatches(row.op, b, f) {
			rows = append(rows, row)
		}
	}
	slices.SortFunc(rows, func(x, y opRow) int {
		if olderOp(x, y) {
			return 1
		}
		return -1
	})
	return page(rows, f)
}

// opMatches reports whether op belongs to b and passes f's status, kind and parent filters.
func opMatches(op domain.Operation, b port.BankRef, f port.OperationFilter) bool {
	return op.Bank == b && (f.Status == "" || op.Status == f.Status) && (f.Kind == "" || op.Kind == f.Kind) &&
		(!f.ExcludeParents || op.Kind != domain.OperationKindRetainBatch)
}

// page cuts one page out of sorted rows; the token is the offset of the next page ("" = last page).
func page(rows []opRow, f port.OperationFilter) ([]domain.Operation, string, error) {
	offset := 0
	if f.PageToken != "" {
		n, err := strconv.Atoi(f.PageToken)
		if err != nil || n < 0 {
			return nil, "", fmt.Errorf("%w: bad page token %q", domain.ErrInvalidArgument, f.PageToken)
		}
		offset = min(n, len(rows))
	}
	size := f.PageSize
	if size <= 0 {
		size = defaultPageSize
	}
	end := min(offset+min(size, maxPageSize), len(rows))
	out := make([]domain.Operation, 0, end-offset)
	for _, row := range rows[offset:end] {
		out = append(out, opOut(row))
	}
	next := ""
	if end < len(rows) {
		next = strconv.Itoa(end)
	}
	return out, next, nil
}

// Cancel cancels a PENDING row at once, or records the cancel request on a PROCESSING row (which stays
// PROCESSING until its handler's Complete or Fail, or ReclaimStale). Other statuses are
// domain.ErrFailedPrecondition.
func (q queue) Cancel(_ context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error) {
	row, ok := q.t.st.ops[id]
	if !ok || row.op.Bank != b {
		return domain.Operation{}, notFound("operation %s", id)
	}
	switch row.op.Status {
	case domain.OperationStatusPending:
		row = finish(row, domain.OperationStatusCancelled, q.t.now)
	case domain.OperationStatusProcessing:
		if row.cancelRequested == nil {
			row.cancelRequested = ptrCopy(&q.t.now)
		}
		row.op.UpdatedAt = q.t.now
	default:
		return domain.Operation{}, precondition("operation %s is %s", id, row.op.Status)
	}
	q.t.st.ops[id] = row
	return opOut(row), nil
}

// Retry resets a FAILED or CANCELLED row to PENDING, due now, at attempt 0 with no error. Other statuses
// are domain.ErrFailedPrecondition.
func (q queue) Retry(_ context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error) {
	row, ok := q.t.st.ops[id]
	if !ok || row.op.Bank != b {
		return domain.Operation{}, notFound("operation %s", id)
	}
	if row.op.Status != domain.OperationStatusFailed && row.op.Status != domain.OperationStatusCancelled {
		return domain.Operation{}, precondition("operation %s is %s", id, row.op.Status)
	}
	row.op.Status, row.op.Attempts, row.op.Error, row.op.UpdatedAt = domain.OperationStatusPending, 0, "", q.t.now
	row.runAt, row.finishedAt, row.cancelRequested = q.t.now, nil, nil
	row.op.WorkerID, row.op.HeartbeatAt = "", nil
	q.t.st.ops[id] = row
	return opOut(row), nil
}

// Prune deletes terminal operations finished before before and returns how many.
func (q queue) Prune(_ context.Context, before time.Time) (int, error) {
	n := deleteWhere(q.t.st.ops, len(q.t.st.ops), func(_ port.OperationID, row opRow) bool {
		return row.op.Status.Terminal() && row.finishedAt != nil && row.finishedAt.Before(before)
	})
	return n, nil
}

// opOut returns a caller-owned Operation view of a row.
func opOut(row opRow) domain.Operation {
	op := row.op
	op.Result, op.Usage = cloneRaw(op.Result), slices.Clone(op.Usage)
	op.EffectiveOverrides = fromJSON[domain.BankConfig](row.overrides)
	op.Progress, op.HeartbeatAt = ptrCopy(op.Progress), ptrCopy(op.HeartbeatAt)
	if op.Status == domain.OperationStatusPending {
		op.NextRunAt = ptrCopy(&row.runAt)
	}
	return op
}

// jobOut returns the Job view of a claimed row.
func jobOut(row opRow) port.Job {
	return port.Job{
		ID: row.op.ID, Bank: row.op.Bank, Kind: row.op.Kind, SerialKey: row.op.SerialKey,
		Payload: cloneRaw(row.payload), Overrides: fromJSON[domain.BankConfig](row.overrides),
		ParentID: row.op.ParentID, RunAt: row.runAt, Attempt: row.op.Attempts,
	}
}

// cloneRaw copies a raw JSON message (nil stays nil).
func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	return slices.Clone(raw)
}

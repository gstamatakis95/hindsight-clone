package postgres

import (
	"context"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"
	"time"
)

// eventRepo is store.EventRepo over one transaction.
type eventRepo struct {
	q  *db.Queries
	tx *tx
}

var _ store.EventRepo = (*eventRepo)(nil)

// Append stores e; e.Seq is the caller's Tx.Seq. A zero e.At takes the database's now().
func (r *eventRepo) Append(ctx context.Context, e domain.LifecycleEvent) error {
	op, err := nullID(e.OperationID)
	if err != nil {
		return err
	}
	err = r.q.EventAppend(ctx, db.EventAppendParams{
		TenantID: e.Bank.Tenant, OrgID: e.Bank.Org, BankID: e.Bank.Bank,
		Seq: e.Seq, Type: string(e.Type), At: orNow(e.CreatedAt),
		OperationID: op, SubjectID: e.SubjectID,
	})
	return mapErr("append event", err)
}

// List returns the bank's events with seq > afterSeq in seq order, keeping only types (empty = all). The
// limit is soft (limit <= 0 = no limit): the page ends at the seq of the limit-th matching event, so a seq
// group is never split. lastSeq is the page's end, or with fewer matches the highest seq after afterSeq,
// filtered-out events included; it is never below afterSeq. One statement computes both, so a group that
// commits meanwhile is either fully in the page or above lastSeq.
func (r *eventRepo) List(
	ctx context.Context, b domain.BankRef, afterSeq int64, limit int, types []domain.EventType,
) (events []domain.LifecycleEvent, lastSeq int64, err error) {
	ts := make([]string, len(types))
	for i, t := range types {
		ts[i] = string(t)
	}
	rows, err := r.q.EventList(ctx, db.EventListParams{
		TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, AfterSeq: afterSeq, Lim: toInt32Sat(max(limit, 0)), Types: ts,
	})
	if err != nil {
		return nil, 0, mapErr("list events", err)
	}
	lastSeq = afterSeq
	events = make([]domain.LifecycleEvent, 0, len(rows))
	for _, row := range rows {
		lastSeq = max(lastSeq, row.BoundSeq)
		if row.EventSeq == nil {
			continue // the bound-only row: no event matched
		}
		events = append(events, eventOf(b, row))
	}
	return events, lastSeq, nil
}

// eventOf builds a lifecycle event from an EventList row that carries one.
func eventOf(b domain.BankRef, row db.EventListRow) domain.LifecycleEvent {
	e := domain.LifecycleEvent{
		Seq: *row.EventSeq, Type: domain.EventType(textOf[string](row.EventType)), Bank: b,
		OperationID: nullIDOf[domain.OperationID](row.EventOperationID), SubjectID: textOf[string](row.EventSubjectID),
	}
	if row.EventAt != nil {
		e.CreatedAt = *row.EventAt
	}
	return e
}

// Prune deletes events whose At is before before, across banks, and returns how many.
func (r *eventRepo) Prune(ctx context.Context, before time.Time) (int, error) {
	n, err := r.q.EventPrune(ctx, before)
	if err != nil {
		return 0, mapErr("prune events", err)
	}
	return int(n), nil
}

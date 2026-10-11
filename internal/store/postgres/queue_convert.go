package postgres

import (
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"

	"github.com/google/uuid"
)

// operationOf builds a domain.Operation from its row. NextRunAt is set only on pending rows.
func operationOf(r db.Operation) (domain.Operation, error) {
	op := domain.Operation{
		ID: idOf[domain.OperationID](r.ID), Bank: domain.BankRef{Tenant: r.TenantID, Bank: r.BankID},
		Kind: domain.OperationKind(r.Kind), Status: domain.OperationStatus(r.Status),
		SerialKey: textOf[string](r.SerialKey), Error: textOf[string](r.Error), Result: r.Result,
		ParentID: nullIDOf[domain.OperationID](r.ParentID), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		Attempts: int(r.Attempts), HeartbeatAt: r.HeartbeatAt, WorkerID: textOf[string](r.WorkerID),
	}
	if op.Status == domain.OperationStatusPending {
		runAt := r.RunAt
		op.NextRunAt = &runAt
	}
	if string(op.Result) == "null" {
		op.Result = nil
	}
	var err error
	if op.EffectiveOverrides, err = unmarshalPtr[domain.BankConfig]("overrides", r.Overrides); err != nil {
		return domain.Operation{}, err
	}
	if op.Progress, err = unmarshalPtr[domain.Progress]("progress", r.Progress); err != nil {
		return domain.Operation{}, err
	}
	if err := unmarshal("usage", r.Usage, &op.Usage); err != nil {
		return domain.Operation{}, err
	}
	return op, nil
}

// jobOf builds the store.Job a worker receives from a claimed row.
func jobOf(r db.Operation) (store.Job, error) {
	j := store.Job{
		ID: idOf[domain.OperationID](r.ID), Bank: domain.BankRef{Tenant: r.TenantID, Bank: r.BankID},
		Kind: domain.OperationKind(r.Kind), SerialKey: textOf[string](r.SerialKey), Payload: r.Payload,
		ParentID: nullIDOf[domain.OperationID](r.ParentID), RunAt: r.RunAt, Attempt: int(r.Attempts),
	}
	if r.Joinable {
		j.Dedupe = store.DedupePending
	}
	var err error
	if j.Overrides, err = unmarshalPtr[domain.BankConfig]("overrides", r.Overrides); err != nil {
		return store.Job{}, err
	}
	return j, nil
}

// opsCursor is the keyset position of an operations page (domain.EncodePageToken): the last row's created_at
// in Unix microseconds and its id.
type opsCursor struct {
	At int64     `json:"at"`
	ID uuid.UUID `json:"id"`
}

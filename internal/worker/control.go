package worker

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// OperationControl implements cancel and retry on top of the operations table.
type OperationControl interface {
	// Cancel marks a pending or processing operation CANCELLED (a running handler stops at its next
	// checkpoint); a terminal one is domain.ErrFailedPrecondition.
	Cancel(ctx context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error)
	// Retry re-queues a FAILED or CANCELLED operation as PENDING; any other status is
	// domain.ErrFailedPrecondition.
	Retry(ctx context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error)
}

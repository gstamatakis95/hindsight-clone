package grpcapi

import (
	"context"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"

	pb "memd/gen/memory/v1"
)

// OperationHandler serves memory.v1.OperationService.
type OperationHandler struct {
	pb.UnimplementedOperationServiceServer
	// Operations serves GetOperation, ListOperations and ListEvents.
	Operations OperationReader
	// Control serves CancelOperation and RetryOperation.
	Control jobs.OperationControl
}

var _ pb.OperationServiceServer = (*OperationHandler)(nil)

// OperationReader reads a bank's operations and lifecycle events; an unknown or deleting bank is
// domain.ErrNotFound.
type OperationReader interface {
	// GetOperation returns one operation of b; unknown = domain.ErrNotFound.
	GetOperation(ctx context.Context, b domain.BankRef, id domain.OperationID) (domain.Operation, error)
	// ListOperations pages the bank's operations, newest first, and returns the next page token.
	ListOperations(ctx context.Context, b domain.BankRef, f store.OperationFilter) ([]domain.Operation, string, error)
	// ListEvents returns events with seq > afterSeq (types empty = all) and the last seq scanned. A limit of 0 or
	// less is 100 and any limit is capped at 1000.
	ListEvents(
		ctx context.Context, b domain.BankRef, afterSeq int64, limit int, types []domain.EventType,
	) ([]domain.LifecycleEvent, int64, error)
}

// GetOperation returns one operation of the bank.
func (h *OperationHandler) GetOperation(
	ctx context.Context, req *pb.GetOperationRequest,
) (*pb.GetOperationResponse, error) {
	b, id, err := operationRef(ctx, req.GetBankId(), req.GetOperationId())
	if err != nil {
		return nil, toStatus(err)
	}
	op, err := h.Operations.GetOperation(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetOperationResponse{Operation: operationToProto(op)}, nil
}

// ListOperations pages the bank's operations, newest first.
func (h *OperationHandler) ListOperations(
	ctx context.Context, req *pb.ListOperationsRequest,
) (*pb.ListOperationsResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	f, err := listOperationsFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	ops, next, err := h.Operations.ListOperations(ctx, b, f)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ListOperationsResponse{Operations: operationsToProto(ops), NextPageToken: next}, nil
}

// CancelOperation cancels a pending or processing operation.
func (h *OperationHandler) CancelOperation(
	ctx context.Context, req *pb.CancelOperationRequest,
) (*pb.CancelOperationResponse, error) {
	b, id, err := operationRef(ctx, req.GetBankId(), req.GetOperationId())
	if err != nil {
		return nil, toStatus(err)
	}
	op, err := h.Control.Cancel(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.CancelOperationResponse{Operation: operationToProto(op)}, nil
}

// RetryOperation re-queues a failed or cancelled operation.
func (h *OperationHandler) RetryOperation(
	ctx context.Context, req *pb.RetryOperationRequest,
) (*pb.RetryOperationResponse, error) {
	b, id, err := operationRef(ctx, req.GetBankId(), req.GetOperationId())
	if err != nil {
		return nil, toStatus(err)
	}
	op, err := h.Control.Retry(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.RetryOperationResponse{Operation: operationToProto(op)}, nil
}

// ListEvents returns the bank's lifecycle events after a sequence number.
func (h *OperationHandler) ListEvents(ctx context.Context, req *pb.ListEventsRequest) (*pb.ListEventsResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	types, err := eventTypesFromProto(req.GetTypes())
	if err != nil {
		return nil, toStatus(err)
	}
	events, last, err := h.Operations.ListEvents(ctx, b, req.GetAfterSeq(), int(req.GetLimit()), types)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ListEventsResponse{Events: eventsToProto(events), LastSeq: last}, nil
}

// operationRef builds the bank ref of a request and checks its required operation id.
func operationRef(ctx context.Context, bankID, opID string) (domain.BankRef, domain.OperationID, error) {
	b, err := bankRef(ctx, bankID)
	if err != nil {
		return domain.BankRef{}, "", err
	}
	if err := requireID("operation_id", opID); err != nil {
		return domain.BankRef{}, "", err
	}
	return b, domain.OperationID(opID), nil
}

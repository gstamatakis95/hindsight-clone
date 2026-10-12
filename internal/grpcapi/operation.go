package grpcapi

import (
	"context"
	"memd/internal/domain"
	"memd/internal/operation"

	pb "memd/gen/memory/v1"
)

// OperationHandler serves memory.v1.OperationService.
type OperationHandler struct {
	pb.UnimplementedOperationServiceServer
	// Operations serves every OperationService RPC.
	Operations *operation.Service
}

var _ pb.OperationServiceServer = (*OperationHandler)(nil)

// GetOperation returns one operation of the bank.
func (h *OperationHandler) GetOperation(
	ctx context.Context, req *pb.GetOperationRequest,
) (*pb.GetOperationResponse, error) {
	b, id, err := operationRef(ctx, req, req.GetOperationId())
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
	b, err := scopeRef(ctx, req)
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
	b, id, err := operationRef(ctx, req, req.GetOperationId())
	if err != nil {
		return nil, toStatus(err)
	}
	op, err := h.Operations.CancelOperation(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.CancelOperationResponse{Operation: operationToProto(op)}, nil
}

// RetryOperation re-queues a failed or cancelled operation.
func (h *OperationHandler) RetryOperation(
	ctx context.Context, req *pb.RetryOperationRequest,
) (*pb.RetryOperationResponse, error) {
	b, id, err := operationRef(ctx, req, req.GetOperationId())
	if err != nil {
		return nil, toStatus(err)
	}
	op, err := h.Operations.RetryOperation(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.RetryOperationResponse{Operation: operationToProto(op)}, nil
}

// ListEvents returns the bank's lifecycle events after a sequence number.
func (h *OperationHandler) ListEvents(ctx context.Context, req *pb.ListEventsRequest) (*pb.ListEventsResponse, error) {
	b, err := bankRef(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	types, err := eventTypesFromProto(req.GetTypes())
	if err != nil {
		return nil, toStatus(err)
	}
	events, last, err := h.Operations.ListEvents(ctx, b, req.GetAfterSeq(), int(req.GetPageSize()), types)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ListEventsResponse{Events: eventsToProto(events), NextAfterSeq: last}, nil
}

// scopeRef builds the bank ref of an OperationService request, or the org scope (domain.OrgRef.Scope) when its
// bank_id is empty: org-scoped operations (delete_org) belong to no bank.
func scopeRef(ctx context.Context, r bankScoped) (domain.BankRef, error) {
	if r.GetBankId() == "" {
		o, err := orgRef(ctx, r)
		return o.Scope(), err
	}
	return bankRef(ctx, r)
}

// operationRef builds the bank ref (or org scope, see scopeRef) of a request and checks its required operation id.
func operationRef(ctx context.Context, r bankScoped, opID string) (domain.BankRef, domain.OperationID, error) {
	b, err := scopeRef(ctx, r)
	if err != nil {
		return domain.BankRef{}, "", err
	}
	if err := requireID("operation_id", opID); err != nil {
		return domain.BankRef{}, "", err
	}
	return b, domain.OperationID(opID), nil
}

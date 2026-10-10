package server

import (
	"context"
	"memd/internal/port"

	pb "memd/gen/memory/v1"
)

// operationServer implements memoryv1.OperationServiceServer over its engines.
type operationServer struct {
	pb.UnimplementedOperationServiceServer
	// d holds the engines and consumer interfaces.
	d Deps
}

var _ pb.OperationServiceServer = (*operationServer)(nil)

// newOperationServer returns the OperationService server over d.
func newOperationServer(d Deps) *operationServer { return &operationServer{d: d} }

// GetOperation returns one operation of the bank.
func (s *operationServer) GetOperation(
	ctx context.Context, req *pb.GetOperationRequest,
) (*pb.GetOperationResponse, error) {
	if s.d.Operations == nil {
		return nil, notWired("Deps.Operations")
	}
	b, id, err := operationRef(ctx, req.GetBankId(), req.GetOperationId())
	if err != nil {
		return nil, toStatus(err)
	}
	op, err := s.d.Operations.GetOperation(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetOperationResponse{Operation: operationToProto(op)}, nil
}

// ListOperations pages the bank's operations, newest first.
func (s *operationServer) ListOperations(
	ctx context.Context, req *pb.ListOperationsRequest,
) (*pb.ListOperationsResponse, error) {
	if s.d.Operations == nil {
		return nil, notWired("Deps.Operations")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	f, err := listOperationsFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	ops, next, err := s.d.Operations.ListOperations(ctx, b, f)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ListOperationsResponse{Operations: operationsToProto(ops), NextPageToken: next}, nil
}

// CancelOperation cancels a pending or processing operation.
func (s *operationServer) CancelOperation(
	ctx context.Context, req *pb.CancelOperationRequest,
) (*pb.CancelOperationResponse, error) {
	if s.d.Control == nil {
		return nil, notWired("Deps.Control")
	}
	b, id, err := operationRef(ctx, req.GetBankId(), req.GetOperationId())
	if err != nil {
		return nil, toStatus(err)
	}
	op, err := s.d.Control.Cancel(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.CancelOperationResponse{Operation: operationToProto(op)}, nil
}

// RetryOperation re-queues a failed or cancelled operation.
func (s *operationServer) RetryOperation(
	ctx context.Context, req *pb.RetryOperationRequest,
) (*pb.RetryOperationResponse, error) {
	if s.d.Control == nil {
		return nil, notWired("Deps.Control")
	}
	b, id, err := operationRef(ctx, req.GetBankId(), req.GetOperationId())
	if err != nil {
		return nil, toStatus(err)
	}
	op, err := s.d.Control.Retry(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.RetryOperationResponse{Operation: operationToProto(op)}, nil
}

// ListEvents returns the bank's lifecycle events after a sequence number.
func (s *operationServer) ListEvents(ctx context.Context, req *pb.ListEventsRequest) (*pb.ListEventsResponse, error) {
	if s.d.Operations == nil {
		return nil, notWired("Deps.Operations")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	types, err := eventTypesFromProto(req.GetTypes())
	if err != nil {
		return nil, toStatus(err)
	}
	events, last, err := s.d.Operations.ListEvents(ctx, b, req.GetAfterSeq(), int(req.GetLimit()), types)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ListEventsResponse{Events: eventsToProto(events), LastSeq: last}, nil
}

// operationRef builds the bank ref of a request and checks its required operation id.
func operationRef(ctx context.Context, bankID, opID string) (port.BankRef, port.OperationID, error) {
	b, err := bankRef(ctx, bankID)
	if err != nil {
		return port.BankRef{}, "", err
	}
	if err := requireID("operation_id", opID); err != nil {
		return port.BankRef{}, "", err
	}
	return b, port.OperationID(opID), nil
}

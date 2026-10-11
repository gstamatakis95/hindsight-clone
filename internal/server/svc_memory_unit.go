package server

import (
	"context"
	"memd/internal/domain"

	pb "memd/gen/memory/v1"
)

// memoryUnitServer implements memoryv1.MemoryUnitServiceServer over its engines.
type memoryUnitServer struct {
	pb.UnimplementedMemoryUnitServiceServer
	// d holds the dependencies.
	d Deps
}

var _ pb.MemoryUnitServiceServer = (*memoryUnitServer)(nil)

// newMemoryUnitServer returns the MemoryUnitService server over d.
func newMemoryUnitServer(d Deps) *memoryUnitServer { return &memoryUnitServer{d: d} }

// unitRef resolves the bank and a required unit id field of a request.
func unitRef(ctx context.Context, bankID, field, id string) (domain.BankRef, domain.UnitID, error) {
	b, err := bankRef(ctx, bankID)
	if err != nil {
		return domain.BankRef{}, "", err
	}
	if err := requireID(field, id); err != nil {
		return domain.BankRef{}, "", err
	}
	return b, domain.UnitID(id), nil
}

// ListMemories lists live and archived units with filters and keyset pagination.
func (s *memoryUnitServer) ListMemories(
	ctx context.Context, req *pb.ListMemoriesRequest,
) (*pb.ListMemoriesResponse, error) {
	if s.d.Lister == nil {
		return nil, notWired("Deps.Lister")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := listMemoriesRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	page, err := s.d.Lister.List(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return listMemoriesPageToProto(page), nil
}

// GetMemory returns one unit, live or invalidated.
func (s *memoryUnitServer) GetMemory(ctx context.Context, req *pb.GetMemoryRequest) (*pb.GetMemoryResponse, error) {
	if s.d.Lister == nil {
		return nil, notWired("Deps.Lister")
	}
	b, id, err := unitRef(ctx, req.GetBankId(), "memory_id", req.GetMemoryId())
	if err != nil {
		return nil, toStatus(err)
	}
	u, err := s.d.Lister.Get(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetMemoryResponse{Memory: unitToProto(u)}, nil
}

// GetObservationHistory returns every version of an observation, in the order the lister returns them.
func (s *memoryUnitServer) GetObservationHistory(
	ctx context.Context, req *pb.GetObservationHistoryRequest,
) (*pb.GetObservationHistoryResponse, error) {
	if s.d.Lister == nil {
		return nil, notWired("Deps.Lister")
	}
	b, id, err := unitRef(ctx, req.GetBankId(), "observation_id", req.GetObservationId())
	if err != nil {
		return nil, toStatus(err)
	}
	versions, err := s.d.Lister.ObservationHistory(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return observationHistoryToProto(versions), nil
}

// UpdateMemory edits, invalidates or restores a raw fact.
func (s *memoryUnitServer) UpdateMemory(
	ctx context.Context, req *pb.UpdateMemoryRequest,
) (*pb.UpdateMemoryResponse, error) {
	if s.d.Curator == nil {
		return nil, notWired("Deps.Curator")
	}
	b, _, err := unitRef(ctx, req.GetBankId(), "memory_id", req.GetMemoryId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := updateMemoryRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	if err := s.d.validate("UpdateMemory", in.Overrides); err != nil {
		return nil, toStatus(err)
	}
	u, op, err := s.d.Curator.Update(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return updateResultToProto(updateResult{Unit: u, OperationID: op}), nil
}

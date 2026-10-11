package grpcapi

import (
	"context"
	"memd/internal/domain"
	"memd/internal/engine/curate"
	"memd/internal/memory"

	pb "memd/gen/memory/v1"
)

// MemoryUnitHandler serves memory.v1.MemoryUnitService.
type MemoryUnitHandler struct {
	pb.UnimplementedMemoryUnitServiceServer
	// Memory serves ListMemories, GetMemory and UpdateMemory.
	Memory *memory.Service
	// Lister serves GetObservationHistory.
	Lister curate.MemoryLister
}

var _ pb.MemoryUnitServiceServer = (*MemoryUnitHandler)(nil)

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
func (h *MemoryUnitHandler) ListMemories(
	ctx context.Context, req *pb.ListMemoriesRequest,
) (*pb.ListMemoriesResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := listMemoriesRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	page, err := h.Memory.ListMemories(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return listMemoriesPageToProto(page), nil
}

// GetMemory returns one unit, live or invalidated.
func (h *MemoryUnitHandler) GetMemory(ctx context.Context, req *pb.GetMemoryRequest) (*pb.GetMemoryResponse, error) {
	b, id, err := unitRef(ctx, req.GetBankId(), "memory_id", req.GetMemoryId())
	if err != nil {
		return nil, toStatus(err)
	}
	u, err := h.Memory.GetMemory(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetMemoryResponse{Memory: unitToProto(u)}, nil
}

// GetObservationHistory returns every version of an observation, in the order the lister returns them.
func (h *MemoryUnitHandler) GetObservationHistory(
	ctx context.Context, req *pb.GetObservationHistoryRequest,
) (*pb.GetObservationHistoryResponse, error) {
	b, id, err := unitRef(ctx, req.GetBankId(), "observation_id", req.GetObservationId())
	if err != nil {
		return nil, toStatus(err)
	}
	versions, err := h.Lister.ObservationHistory(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return observationHistoryToProto(versions), nil
}

// UpdateMemory edits, invalidates or restores a raw fact.
func (h *MemoryUnitHandler) UpdateMemory(
	ctx context.Context, req *pb.UpdateMemoryRequest,
) (*pb.UpdateMemoryResponse, error) {
	b, _, err := unitRef(ctx, req.GetBankId(), "memory_id", req.GetMemoryId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := updateMemoryRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	if err := validate(in.Overrides); err != nil {
		return nil, toStatus(err)
	}
	u, op, err := h.Memory.UpdateMemory(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return updateResultToProto(u, op), nil
}

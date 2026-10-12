package grpcapi

import (
	"context"
	"memd/internal/domain"
	"memd/internal/memory"

	pb "memd/gen/memory/v1"
)

// MemoryHandler serves memory.v1.MemoryService.
type MemoryHandler struct {
	pb.UnimplementedMemoryServiceServer
	// Memory serves every MemoryService RPC.
	Memory *memory.Service
}

var _ pb.MemoryServiceServer = (*MemoryHandler)(nil)

// Retain stores content and extracts memories from it.
func (h *MemoryHandler) Retain(ctx context.Context, req *pb.RetainRequest) (*pb.RetainResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := retainRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	if err := validate(in.Overrides); err != nil {
		return nil, toStatus(err)
	}
	for i := range in.Items {
		if err := validate(in.Items[i].Overrides); err != nil {
			return nil, toStatus(err)
		}
	}
	out, err := h.Memory.Retain(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return retainResultToProto(out), nil
}

// Recall retrieves memories for a query.
func (h *MemoryHandler) Recall(ctx context.Context, req *pb.RecallRequest) (*pb.RecallResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := recallRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	if err := validate(in.Overrides); err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Memory.Recall(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return recallResultToProto(out), nil
}

// ListRecallArms lists the registered recall arms with their descriptions.
func (h *MemoryHandler) ListRecallArms(
	context.Context, *pb.ListRecallArmsRequest,
) (*pb.ListRecallArmsResponse, error) {
	infos := h.Memory.ListRecallArms()
	out := &pb.ListRecallArmsResponse{Arms: make([]*pb.RecallArm, len(infos))}
	for i, a := range infos {
		out.Arms[i] = &pb.RecallArm{Name: a.Name, Description: a.Description}
	}
	return out, nil
}

// Reflect answers a query with the agentic reflect loop.
func (h *MemoryHandler) Reflect(ctx context.Context, req *pb.ReflectRequest) (*pb.ReflectResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := reflectRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	if err := validate(in.Overrides); err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Memory.Reflect(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	resp := reflectResultToProto(out)
	if resp.BasedOn == nil && domain.ValueOr(in.Include.BasedOn, true) {
		resp.BasedOn = &pb.BasedOn{} // based_on was asked for: an answer citing nothing still has one
	}
	return resp, nil
}

// unitRef resolves the bank and a required memory id field of a request.
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

// ListMemories lists live and invalidated memories with filters and keyset pagination.
func (h *MemoryHandler) ListMemories(
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

// GetMemory returns one memory, live or invalidated.
func (h *MemoryHandler) GetMemory(ctx context.Context, req *pb.GetMemoryRequest) (*pb.GetMemoryResponse, error) {
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

// UpdateMemory edits a live raw fact.
func (h *MemoryHandler) UpdateMemory(
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
	return &pb.UpdateMemoryResponse{Memory: unitToProto(u), Operation: operationPtrToProto(op)}, nil
}

// InvalidateMemory archives a live raw fact.
func (h *MemoryHandler) InvalidateMemory(
	ctx context.Context, req *pb.InvalidateMemoryRequest,
) (*pb.InvalidateMemoryResponse, error) {
	b, id, err := unitRef(ctx, req.GetBankId(), "memory_id", req.GetMemoryId())
	if err != nil {
		return nil, toStatus(err)
	}
	u, err := h.Memory.InvalidateMemory(ctx, b, id, req.GetReason())
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.InvalidateMemoryResponse{Memory: unitToProto(u)}, nil
}

// RestoreMemory brings an invalidated raw fact back.
func (h *MemoryHandler) RestoreMemory(
	ctx context.Context, req *pb.RestoreMemoryRequest,
) (*pb.RestoreMemoryResponse, error) {
	b, _, err := unitRef(ctx, req.GetBankId(), "memory_id", req.GetMemoryId())
	if err != nil {
		return nil, toStatus(err)
	}
	in := restoreMemoryRequestFromProto(req)
	in.Bank = b
	if err := validate(in.Overrides); err != nil {
		return nil, toStatus(err)
	}
	u, op, err := h.Memory.RestoreMemory(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.RestoreMemoryResponse{Memory: unitToProto(u), Operation: operationPtrToProto(op)}, nil
}

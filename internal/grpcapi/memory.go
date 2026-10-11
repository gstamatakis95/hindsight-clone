package grpcapi

import (
	"context"
	"memd/internal/memory"

	pb "memd/gen/memory/v1"
)

// MemoryHandler serves memory.v1.MemoryService.
type MemoryHandler struct {
	pb.UnimplementedMemoryServiceServer
	// Memory serves Retain, Recall, Reflect and ListRecallArms.
	Memory *memory.Service
}

var _ pb.MemoryServiceServer = (*MemoryHandler)(nil)

// Retain stores content and extracts memory units from it.
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
	if resp.BasedOn == nil && (in.Include.Facts == nil || *in.Include.Facts) {
		resp.BasedOn = &pb.BasedOn{} // facts were asked for: an answer citing nothing still has based_on
	}
	return resp, nil
}

package grpcapi

import (
	"context"
	"memd/internal/engine/retain"
	"memd/internal/recall"

	reflecteng "memd/internal/reflect"

	pb "memd/gen/memory/v1"
)

// MemoryHandler serves memory.v1.MemoryService.
type MemoryHandler struct {
	pb.UnimplementedMemoryServiceServer
	// Ingestor serves Retain.
	Ingestor retain.Ingestor
	// Engine serves Recall.
	Engine *recall.Engine
	// Agent serves Reflect.
	Agent *reflecteng.Agent
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
	out, err := h.Ingestor.Ingest(ctx, in)
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
	out, err := h.Engine.Recall(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return recallResultToProto(out), nil
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
	out, err := h.Agent.Reflect(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	resp := reflectResultToProto(out)
	if resp.BasedOn == nil && (in.Include.Facts == nil || *in.Include.Facts) {
		resp.BasedOn = &pb.BasedOn{} // facts were asked for: an answer citing nothing still has based_on
	}
	return resp, nil
}

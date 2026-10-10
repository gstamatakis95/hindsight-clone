package server

import (
	"context"
	"memd/internal/engine/recall"
	"memd/internal/engine/retain"

	reflecteng "memd/internal/engine/reflect"

	pb "memd/gen/memory/v1"
)

// memoryServer implements memoryv1.MemoryServiceServer over its engines.
type memoryServer struct {
	pb.UnimplementedMemoryServiceServer
	// d holds the dependencies.
	d Deps
}

var _ pb.MemoryServiceServer = (*memoryServer)(nil)

// newMemoryServer returns the MemoryService server over d.
func newMemoryServer(d Deps) *memoryServer { return &memoryServer{d: d} }

// NewMemoryServer returns the MemoryService server over the retain, recall and reflect engines.
//
//nolint:ireturn // signature fixed by docs/SPEC.md (internal/server).
func NewMemoryServer(in retain.Ingestor, rc recall.Recaller, rf reflecteng.Reflector) pb.MemoryServiceServer {
	return newMemoryServer(Deps{Ingestor: in, Recaller: rc, Reflector: rf})
}

// Retain stores content and extracts memory units from it.
func (s *memoryServer) Retain(ctx context.Context, req *pb.RetainRequest) (*pb.RetainResponse, error) {
	if s.d.Ingestor == nil {
		return nil, notWired("Deps.Ingestor")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := retainRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	if err := s.d.validate("Retain", in.Overrides); err != nil {
		return nil, toStatus(err)
	}
	for i := range in.Items {
		if err := s.d.validate("Retain", in.Items[i].Overrides); err != nil {
			return nil, toStatus(err)
		}
	}
	out, err := s.d.Ingestor.Ingest(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	// TODO(WP-08): copy out.Usage once IngestResult carries it.
	return retainResultToProto(retainResult{IngestResult: out}), nil
}

// Recall retrieves memories for a query.
func (s *memoryServer) Recall(ctx context.Context, req *pb.RecallRequest) (*pb.RecallResponse, error) {
	if s.d.Recaller == nil {
		return nil, notWired("Deps.Recaller")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := recallRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	if err := s.d.validate("Recall", in.Overrides); err != nil {
		return nil, toStatus(err)
	}
	out, err := s.d.Recaller.Recall(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return recallResultToProto(out), nil
}

// Reflect answers a query with the agentic reflect loop.
func (s *memoryServer) Reflect(ctx context.Context, req *pb.ReflectRequest) (*pb.ReflectResponse, error) {
	if s.d.Reflector == nil {
		return nil, notWired("Deps.Reflector")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := reflectRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	if err := s.d.validate("Reflect", in.Overrides); err != nil {
		return nil, toStatus(err)
	}
	out, err := s.d.Reflector.Reflect(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	resp := reflectResultToProto(out)
	if resp.BasedOn == nil && (in.Include.Facts == nil || *in.Include.Facts) {
		resp.BasedOn = &pb.BasedOn{} // facts were asked for: an answer citing nothing still has based_on
	}
	return resp, nil
}

package server

import (
	"context"
	"memd/internal/domain"

	pb "memd/gen/memory/v1"
)

// observationServer implements memoryv1.ObservationServiceServer over its engines.
type observationServer struct {
	pb.UnimplementedObservationServiceServer
	// d holds the engines and consumer interfaces.
	d Deps
}

var _ pb.ObservationServiceServer = (*observationServer)(nil)

// newObservationServer returns the ObservationService server over d.
func newObservationServer(d Deps) *observationServer { return &observationServer{d: d} }

// Consolidate queues a manual consolidation over the given scopes (all when empty).
func (s *observationServer) Consolidate(
	ctx context.Context, req *pb.ConsolidateRequest,
) (*pb.ConsolidateResponse, error) {
	if s.d.Observations == nil {
		return nil, notWired("Deps.Observations")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	overrides, err := configFromProto(req.GetOverrides())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := s.d.validate("Consolidate", overrides); err != nil {
		return nil, toStatus(err)
	}
	op, err := s.d.Observations.Consolidate(ctx, b, tagSetsFromProto(req.GetScopes()), overrides)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ConsolidateResponse{Operation: operationToProto(op)}, nil
}

// ListScopes returns one page of the bank's observation scopes with their counts.
func (s *observationServer) ListScopes(ctx context.Context, req *pb.ListScopesRequest) (*pb.ListScopesResponse, error) {
	if s.d.Observations == nil {
		return nil, notWired("Deps.Observations")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := s.d.Observations.ListScopes(ctx, b, int(req.GetPageSize()), req.GetPageToken())
	if err != nil {
		return nil, toStatus(err)
	}
	return scopePageToProto(out), nil
}

// ClearObservations deletes the observations of the given scopes (all when empty).
func (s *observationServer) ClearObservations(
	ctx context.Context, req *pb.ClearObservationsRequest,
) (*pb.ClearObservationsResponse, error) {
	if s.d.Observations == nil {
		return nil, notWired("Deps.Observations")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := s.d.Observations.ClearObservations(ctx, b, tagSetsFromProto(req.GetScopes()), req.GetReconsolidate())
	if err != nil {
		return nil, toStatus(err)
	}
	return clearResultToObservationsProto(out), nil
}

// ClearMemoryObservations deletes the observations citing one memory.
func (s *observationServer) ClearMemoryObservations(
	ctx context.Context, req *pb.ClearMemoryObservationsRequest,
) (*pb.ClearMemoryObservationsResponse, error) {
	if s.d.Observations == nil {
		return nil, notWired("Deps.Observations")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := requireID("memory_id", req.GetMemoryId()); err != nil {
		return nil, toStatus(err)
	}
	out, err := s.d.Observations.ClearMemoryObservations(ctx, b, domain.UnitID(req.GetMemoryId()), req.GetReconsolidate())
	if err != nil {
		return nil, toStatus(err)
	}
	return clearResultToMemoryProto(out), nil
}

// PreviewConsolidationStrategies evaluates draft strategies against the bank's existing scopes.
func (s *observationServer) PreviewConsolidationStrategies(
	ctx context.Context, req *pb.PreviewConsolidationStrategiesRequest,
) (*pb.PreviewConsolidationStrategiesResponse, error) {
	if s.d.Strategies == nil {
		return nil, notWired("Deps.Strategies")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	draft, err := strategiesFromProto(req.GetStrategies())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := s.d.Strategies.Preview(ctx, b, draft)
	if err != nil {
		return nil, toStatus(err)
	}
	return strategyPreviewToProto(out), nil
}

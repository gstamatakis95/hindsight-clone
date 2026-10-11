package grpcapi

import (
	"context"
	"memd/internal/domain"
	"memd/internal/observation"

	pb "memd/gen/memory/v1"
)

// ObservationHandler serves memory.v1.ObservationService.
type ObservationHandler struct {
	pb.UnimplementedObservationServiceServer
	// Observations serves every ObservationService RPC.
	Observations *observation.Service
}

var _ pb.ObservationServiceServer = (*ObservationHandler)(nil)

// Consolidate queues a manual consolidation over the given scopes (all when empty).
func (h *ObservationHandler) Consolidate(
	ctx context.Context, req *pb.ConsolidateRequest,
) (*pb.ConsolidateResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	var d decoder
	overrides := d.consolidateOverrides(req.GetOverrides())
	err = d.err
	if err != nil {
		return nil, toStatus(err)
	}
	if err := validate(overrides); err != nil {
		return nil, toStatus(err)
	}
	op, err := h.Observations.Consolidate(ctx, b, tagSetsFromProto(req.GetScopes()), overrides)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ConsolidateResponse{Operation: operationToProto(op)}, nil
}

// ListScopes returns one page of the bank's observation scopes with their counts.
func (h *ObservationHandler) ListScopes(
	ctx context.Context, req *pb.ListScopesRequest,
) (*pb.ListScopesResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Observations.ListObservationScopes(ctx, b, int(req.GetPageSize()), req.GetPageToken())
	if err != nil {
		return nil, toStatus(err)
	}
	return scopePageToProto(out), nil
}

// ClearObservations deletes the observations of the given scopes (all when empty).
func (h *ObservationHandler) ClearObservations(
	ctx context.Context, req *pb.ClearObservationsRequest,
) (*pb.ClearObservationsResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in := observation.ClearRequest{
		Bank: b, Scopes: tagSetsFromProto(req.GetScopes()), Reconsolidate: req.GetReconsolidate(),
	}
	in.All = len(in.Scopes) == 0 // an empty scope list clears every scope
	out, err := h.Observations.ClearObservations(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return clearResultToObservationsProto(out), nil
}

// ClearMemoryObservations deletes the observations citing one memory.
func (h *ObservationHandler) ClearMemoryObservations(
	ctx context.Context, req *pb.ClearMemoryObservationsRequest,
) (*pb.ClearMemoryObservationsResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := requireID("memory_id", req.GetMemoryId()); err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Observations.ClearObservations(ctx, observation.ClearRequest{
		Bank: b, MemoryID: domain.UnitID(req.GetMemoryId()), Reconsolidate: req.GetReconsolidate(),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return clearResultToMemoryProto(out), nil
}

// PreviewConsolidationStrategies evaluates draft strategies against the bank's existing scopes.
func (h *ObservationHandler) PreviewConsolidationStrategies(
	ctx context.Context, req *pb.PreviewConsolidationStrategiesRequest,
) (*pb.PreviewConsolidationStrategiesResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	draft, err := strategiesFromProto(req.GetStrategies())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Observations.PreviewConsolidationStrategies(ctx, b, draft)
	if err != nil {
		return nil, toStatus(err)
	}
	return strategyPreviewToProto(out), nil
}

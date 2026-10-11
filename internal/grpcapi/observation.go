package grpcapi

import (
	"context"
	"memd/internal/domain"
	"memd/internal/engine/consolidate"
	"memd/internal/engine/curate"

	pb "memd/gen/memory/v1"
)

// ObservationHandler serves memory.v1.ObservationService.
type ObservationHandler struct {
	pb.UnimplementedObservationServiceServer
	// Observations serves Consolidate, ListScopes, ClearObservations and ClearMemoryObservations.
	Observations curate.ObservationAdmin
	// Strategies serves PreviewConsolidationStrategies.
	Strategies consolidate.StrategyPreviewer
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
	out, err := h.Observations.ListScopes(ctx, b, int(req.GetPageSize()), req.GetPageToken())
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
	out, err := h.Observations.ClearObservations(ctx, b, tagSetsFromProto(req.GetScopes()), req.GetReconsolidate())
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
	out, err := h.Observations.ClearMemoryObservations(ctx, b, domain.UnitID(req.GetMemoryId()), req.GetReconsolidate())
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
	out, err := h.Strategies.Preview(ctx, b, draft)
	if err != nil {
		return nil, toStatus(err)
	}
	return strategyPreviewToProto(out), nil
}

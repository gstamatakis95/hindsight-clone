package grpcapi

import (
	"context"
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

// ListObservationScopes returns one page of the bank's observation scopes with their counts.
func (h *ObservationHandler) ListObservationScopes(
	ctx context.Context, req *pb.ListObservationScopesRequest,
) (*pb.ListObservationScopesResponse, error) {
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

// ListObservationHistory returns one page of an observation's versions, newest first.
func (h *ObservationHandler) ListObservationHistory(
	ctx context.Context, req *pb.ListObservationHistoryRequest,
) (*pb.ListObservationHistoryResponse, error) {
	b, id, err := unitRef(ctx, req.GetBankId(), "observation_id", req.GetObservationId())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Observations.ListObservationHistory(ctx, b, id, int(req.GetPageSize()), req.GetPageToken())
	if err != nil {
		return nil, toStatus(err)
	}
	return observationHistoryToProto(out), nil
}

// ClearObservations deletes the observations of exact scopes, of one memory or of the whole bank; the request
// must name exactly one target.
func (h *ObservationHandler) ClearObservations(
	ctx context.Context, req *pb.ClearObservationsRequest,
) (*pb.ClearObservationsResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := clearRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	out, err := h.Observations.ClearObservations(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ClearObservationsResponse{Deleted: out.Deleted, Operation: operationPtrToProto(out.Operation)}, nil
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

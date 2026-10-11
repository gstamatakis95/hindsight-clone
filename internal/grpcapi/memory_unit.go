package grpcapi

import (
	"context"
	"memd/internal/observation"

	pb "memd/gen/memory/v1"
)

// MemoryUnitHandler serves memory.v1.MemoryUnitService.
type MemoryUnitHandler struct {
	pb.UnimplementedMemoryUnitServiceServer
	// Observations serves GetObservationHistory.
	Observations *observation.Service
}

var _ pb.MemoryUnitServiceServer = (*MemoryUnitHandler)(nil)

// GetObservationHistory returns every version of an observation, in the order the store returns them.
func (h *MemoryUnitHandler) GetObservationHistory(
	ctx context.Context, req *pb.GetObservationHistoryRequest,
) (*pb.GetObservationHistoryResponse, error) {
	b, id, err := unitRef(ctx, req.GetBankId(), "observation_id", req.GetObservationId())
	if err != nil {
		return nil, toStatus(err)
	}
	versions, err := h.Observations.ListObservationHistory(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return observationHistoryToProto(versions), nil
}

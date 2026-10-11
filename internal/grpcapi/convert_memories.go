package grpcapi

import (
	"memd/internal/domain"
	"memd/internal/memory"

	pb "memd/gen/memory/v1"
)

// Converters for MemoryUnitService.

// listMemoriesRequestFromProto converts a ListMemoriesRequest; Bank carries only bank_id (the handler sets
// the tenant).
func listMemoriesRequestFromProto(r *pb.ListMemoriesRequest) (memory.ListRequest, error) {
	var d decoder
	out := memory.ListRequest{
		Bank:       domain.BankRef{Bank: r.GetBankId()},
		Types:      fromEnums[domain.FactType](&d, r.GetTypes()),
		Q:          r.GetQ(),
		DocumentID: domain.DocumentID(r.GetDocumentId()),
		EntityID:   domain.EntityID(r.GetEntityId()),
		State:      fromEnum[domain.MemoryState](&d, r.GetState()),
		Start:      d.tsPtr(r.GetStart()),
		End:        d.tsPtr(r.GetEnd()),
		TimeField:  fromEnum[domain.TimeField](&d, r.GetTimeField()),
		Tags:       d.tagFilter(r.GetTags()),
		PageSize:   int(r.GetPageSize()),
		PageToken:  r.GetPageToken(),
	}
	return out, d.err
}

// listMemoriesPageToProto converts a memory page to a ListMemoriesResponse.
func listMemoriesPageToProto(p memory.Page) *pb.ListMemoriesResponse {
	return &pb.ListMemoriesResponse{Items: unitsToProto(p.Items), NextPageToken: p.NextPageToken}
}

// observationVersionToProto converts an ObservationVersion.
func observationVersionToProto(v domain.ObservationVersion) *pb.ObservationVersion {
	return &pb.ObservationVersion{
		Version: v.Version, Text: v.Text, SourceIds: strs[string](v.SourceIDs), At: tsToProto(v.At),
	}
}

// observationHistoryToProto converts an observation's versions, in the order given.
func observationHistoryToProto(in []domain.ObservationVersion) *pb.GetObservationHistoryResponse {
	out := &pb.GetObservationHistoryResponse{}
	if in != nil {
		out.Versions = make([]*pb.ObservationVersion, len(in))
		for i := range in {
			out.Versions[i] = observationVersionToProto(in[i])
		}
	}
	return out
}

// entityListFromProto converts an EntityList keeping presence: nil = nil (unchanged), a set list is a
// non-nil slice even when empty (clear).
func entityListFromProto(l *pb.EntityList) []string {
	if l == nil {
		return nil
	}
	return append([]string{}, l.GetNames()...)
}

// updateMemoryRequestFromProto converts an UpdateMemoryRequest; Bank carries only bank_id (the handler
// sets the tenant).
func updateMemoryRequestFromProto(r *pb.UpdateMemoryRequest) (memory.UpdateRequest, error) {
	var d decoder
	out := memory.UpdateRequest{
		Bank:            domain.BankRef{Bank: r.GetBankId()},
		ID:              domain.UnitID(r.GetMemoryId()),
		Text:            optCopy(r.Text),
		Context:         optCopy(r.Context),
		OccurredStart:   d.tsPtr(r.GetOccurredStart()),
		OccurredEnd:     d.tsPtr(r.GetOccurredEnd()),
		ClearOccurred:   r.GetClearOccurred(),
		FactType:        fromEnum[domain.FactType](&d, r.GetFactType()),
		Entities:        entityListFromProto(r.GetEntities()),
		ResolveEntities: optCopy(r.ResolveEntities),
		State:           fromEnum[domain.MemoryState](&d, r.GetState()),
		Reason:          r.GetReason(),
		Overrides:       rederiveOverrides(r.GetOverrides()),
	}
	return out, d.err
}

// updateResultToProto converts what memory.Service.UpdateMemory returns (the unit after the change and the
// rederive_memory operation, nil for invalidate) to an UpdateMemoryResponse.
func updateResultToProto(u domain.Unit, op *domain.Operation) *pb.UpdateMemoryResponse {
	return &pb.UpdateMemoryResponse{Memory: unitToProto(u), Operation: operationPtrToProto(op)}
}

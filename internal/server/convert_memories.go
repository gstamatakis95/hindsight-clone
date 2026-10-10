package server

import (
	"memd/internal/domain"
	"memd/internal/engine/curate"
	"memd/internal/port"

	pb "memd/gen/memory/v1"
)

// Converters for memories.proto (MemoryUnitService).

// listMemoriesRequestFromProto converts a ListMemoriesRequest; Bank carries only bank_id (the handler sets
// the tenant).
func listMemoriesRequestFromProto(r *pb.ListMemoriesRequest) (curate.ListMemoriesRequest, error) {
	var d decoder
	out := curate.ListMemoriesRequest{
		Bank:       port.BankRef{Bank: r.GetBankId()},
		Types:      fromEnums[domain.FactType](&d, r.GetTypes()),
		Q:          r.GetQ(),
		DocumentID: port.DocumentID(r.GetDocumentId()),
		EntityID:   port.EntityID(r.GetEntityId()),
		State:      fromEnum[domain.MemoryState](&d, r.GetState()),
		Start:      d.tsPtr(r.GetStart()),
		End:        d.tsPtr(r.GetEnd()),
		TimeField:  fromEnum[domain.TimeField](&d, r.GetTimeField()),
		Tags:       r.GetTags(),
		TagsMatch:  fromEnum[domain.TagsMatch](&d, r.GetTagsMatch()),
		PageSize:   int(r.GetPageSize()),
		PageToken:  r.GetPageToken(),
	}
	return out, d.err
}

// listMemoriesRequestToProto converts a ListMemoriesRequest back to proto (bank_id = Bank.Bank).
func listMemoriesRequestToProto(r curate.ListMemoriesRequest) *pb.ListMemoriesRequest {
	return &pb.ListMemoriesRequest{
		BankId:     r.Bank.Bank,
		Types:      toEnums[pb.FactType](r.Types),
		Q:          r.Q,
		DocumentId: string(r.DocumentID),
		EntityId:   string(r.EntityID),
		State:      toEnum[pb.MemoryState](r.State),
		Start:      tsPtrToProto(r.Start),
		End:        tsPtrToProto(r.End),
		TimeField:  toEnum[pb.TimeField](r.TimeField),
		Tags:       r.Tags,
		TagsMatch:  toEnum[pb.TagsMatch](r.TagsMatch),
		PageSize:   i32(r.PageSize),
		PageToken:  r.PageToken,
	}
}

// listMemoriesPageFromProto converts a ListMemoriesResponse; items are plain units (scores dropped).
func listMemoriesPageFromProto(r *pb.ListMemoriesResponse) (curate.ListMemoriesPage, error) {
	var d decoder
	out := curate.ListMemoriesPage{Items: d.units(r.GetItems()), NextPageToken: r.GetNextPageToken()}
	return out, d.err
}

// listMemoriesPageToProto converts a ListMemoriesPage to a ListMemoriesResponse.
func listMemoriesPageToProto(p curate.ListMemoriesPage) *pb.ListMemoriesResponse {
	return &pb.ListMemoriesResponse{Items: unitsToProto(p.Items), NextPageToken: p.NextPageToken}
}

// observationVersion converts an ObservationVersion.
func (d *decoder) observationVersion(v *pb.ObservationVersion) domain.ObservationVersion {
	return domain.ObservationVersion{
		Version: v.GetVersion(), Text: v.GetText(), SourceIDs: strs[domain.UnitID](v.GetSourceIds()),
		At: d.ts(v.GetAt()),
	}
}

// observationVersionToProto converts an ObservationVersion.
func observationVersionToProto(v domain.ObservationVersion) *pb.ObservationVersion {
	return &pb.ObservationVersion{
		Version: v.Version, Text: v.Text, SourceIds: strs[string](v.SourceIDs), At: tsToProto(v.At),
	}
}

// observationHistoryFromProto converts a GetObservationHistoryResponse.
func observationHistoryFromProto(r *pb.GetObservationHistoryResponse) ([]domain.ObservationVersion, error) {
	var d decoder
	var out []domain.ObservationVersion
	if r.GetVersions() != nil {
		out = make([]domain.ObservationVersion, len(r.GetVersions()))
		for i, v := range r.GetVersions() {
			out[i] = d.observationVersion(v)
		}
	}
	return out, d.err
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

// entityListToProto converts entity names keeping presence: nil = nil, non-nil (even empty) = a set list.
func entityListToProto(names []string) *pb.EntityList {
	if names == nil {
		return nil
	}
	return &pb.EntityList{Names: names}
}

// updateMemoryRequestFromProto converts an UpdateMemoryRequest; Bank carries only bank_id (the handler
// sets the tenant).
func updateMemoryRequestFromProto(r *pb.UpdateMemoryRequest) (curate.UpdateMemoryRequest, error) {
	var d decoder
	out := curate.UpdateMemoryRequest{
		Bank:            port.BankRef{Bank: r.GetBankId()},
		ID:              port.UnitID(r.GetMemoryId()),
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
		Overrides:       d.configPtr(r.GetOverrides()),
	}
	return out, d.err
}

// updateMemoryRequestToProto converts an UpdateMemoryRequest back to proto (bank_id = Bank.Bank).
func updateMemoryRequestToProto(r curate.UpdateMemoryRequest) *pb.UpdateMemoryRequest {
	return &pb.UpdateMemoryRequest{
		BankId:          r.Bank.Bank,
		MemoryId:        string(r.ID),
		Text:            optCopy(r.Text),
		Context:         optCopy(r.Context),
		OccurredStart:   tsPtrToProto(r.OccurredStart),
		OccurredEnd:     tsPtrToProto(r.OccurredEnd),
		ClearOccurred:   r.ClearOccurred,
		FactType:        toEnum[pb.FactType](r.FactType),
		Entities:        entityListToProto(r.Entities),
		ResolveEntities: optCopy(r.ResolveEntities),
		State:           toEnum[pb.MemoryState](r.State),
		Reason:          r.Reason,
		Overrides:       configToProto(r.Overrides),
	}
}

// updateResult is the server-side counterpart of UpdateMemoryResponse: what Curator.Update returns.
type updateResult struct {
	// Unit is the unit after the change.
	Unit domain.Unit
	// OperationID is the rederive_memory operation; empty for invalidate.
	OperationID port.OperationID
}

// updateResultFromProto converts an UpdateMemoryResponse; the memory is a plain unit (scores dropped).
func updateResultFromProto(r *pb.UpdateMemoryResponse) (updateResult, error) {
	var d decoder
	out := updateResult{Unit: d.unit(r.GetMemory()), OperationID: port.OperationID(r.GetOperationId())}
	return out, d.err
}

// updateResultToProto converts a curation result to an UpdateMemoryResponse.
func updateResultToProto(r updateResult) *pb.UpdateMemoryResponse {
	return &pb.UpdateMemoryResponse{Memory: unitToProto(r.Unit), OperationId: string(r.OperationID)}
}

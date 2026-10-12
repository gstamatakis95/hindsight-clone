package grpcapi

import (
	"bytes"
	"encoding/json"
	"memd/internal/domain"
	"memd/internal/store"

	pb "memd/gen/memory/v1"
)

// operationToProto converts an Operation. The bank's tenant, SerialKey and WorkerID are not on the wire; the
// kind-specific result goes out as debug_summary, its compact JSON text.
func operationToProto(o domain.Operation) *pb.Operation {
	return &pb.Operation{
		Id:                 string(o.ID),
		OrgId:              o.Bank.Org,
		BankId:             o.Bank.Bank,
		Kind:               toEnum[pb.OperationKind](o.Kind),
		Status:             toEnum[pb.OperationStatus](o.Status),
		Error:              o.Error,
		DebugSummary:       debugSummary(o.Result),
		Usage:              usagesToProto(o.Usage),
		EffectiveOverrides: configToProto(o.EffectiveOverrides),
		ParentId:           string(o.ParentID),
		Progress:           progressToProto(o.Progress),
		CreatedAt:          tsToProto(o.CreatedAt),
		UpdatedAt:          tsToProto(o.UpdatedAt),
		NextRunAt:          tsPtrToProto(o.NextRunAt),
		Attempts:           i32(o.Attempts),
		HeartbeatAt:        tsPtrToProto(o.HeartbeatAt),
	}
}

// debugSummary returns a result JSON as compact text; empty or invalid JSON = "".
func debugSummary(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return ""
	}
	return b.String()
}

// operationPtrToProto converts an optional Operation; nil = nil.
func operationPtrToProto(o *domain.Operation) *pb.Operation {
	if o == nil {
		return nil
	}
	return operationToProto(*o)
}

// operationsToProto converts operations; nil stays nil.
func operationsToProto(in []domain.Operation) []*pb.Operation {
	if in == nil {
		return nil
	}
	out := make([]*pb.Operation, len(in))
	for i := range in {
		out[i] = operationToProto(in[i])
	}
	return out
}

// progressToProto converts a Progress; nil = nil.
func progressToProto(p *domain.Progress) *pb.Progress {
	if p == nil {
		return nil
	}
	return &pb.Progress{Stage: p.Stage, RecordedAt: tsToProto(p.RecordedAt), Processed: p.Processed, Total: p.Total}
}

// listOperationsFromProto converts a ListOperationsRequest to an OperationFilter (the bank is separate).
func listOperationsFromProto(r *pb.ListOperationsRequest) (store.OperationFilter, error) {
	var d decoder
	out := store.OperationFilter{
		Status:         fromEnum[domain.OperationStatus](&d, r.GetStatus()),
		Kind:           fromEnum[domain.OperationKind](&d, r.GetKind()),
		ExcludeParents: r.GetExcludeParents(),
		PageSize:       int(r.GetPageSize()),
		PageToken:      r.GetPageToken(),
	}
	return out, d.err
}

// eventToProto converts a LifecycleEvent; the bank's tenant is not on the wire.
func eventToProto(e domain.LifecycleEvent) *pb.LifecycleEvent {
	return &pb.LifecycleEvent{
		Seq:         e.Seq,
		Type:        toEnum[pb.EventType](e.Type),
		OrgId:       e.Bank.Org,
		BankId:      e.Bank.Bank,
		CreatedAt:   tsToProto(e.CreatedAt),
		OperationId: string(e.OperationID),
		SubjectId:   e.SubjectID,
	}
}

// eventsToProto converts lifecycle events; nil stays nil.
func eventsToProto(in []domain.LifecycleEvent) []*pb.LifecycleEvent {
	if in == nil {
		return nil
	}
	out := make([]*pb.LifecycleEvent, len(in))
	for i := range in {
		out[i] = eventToProto(in[i])
	}
	return out
}

// eventTypesFromProto converts a ListEventsRequest's type filter; nil stays nil.
func eventTypesFromProto(in []pb.EventType) ([]domain.EventType, error) {
	var d decoder
	out := fromEnums[domain.EventType](&d, in)
	return out, d.err
}

package server

import (
	"memd/internal/domain"
	"memd/internal/port"

	pb "memd/gen/memory/v1"
)

// operationToProto converts an Operation. Bank, SerialKey and WorkerID are not on the wire.
func operationToProto(o domain.Operation) *pb.Operation {
	return &pb.Operation{
		Id:                 string(o.ID),
		Kind:               toEnum[pb.OperationKind](o.Kind),
		Status:             toEnum[pb.OperationStatus](o.Status),
		Error:              o.Error,
		Result:             jsonToStruct(o.Result),
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

// operation converts an Operation; nil = the zero operation.
func (d *decoder) operation(m *pb.Operation) domain.Operation {
	return domain.Operation{
		ID:                 port.OperationID(m.GetId()),
		Kind:               fromEnum[domain.OperationKind](d, m.GetKind()),
		Status:             fromEnum[domain.OperationStatus](d, m.GetStatus()),
		Error:              m.GetError(),
		Result:             d.jsonFromStruct(m.GetResult()),
		Usage:              d.usages(m.GetUsage()),
		EffectiveOverrides: d.configPtr(m.GetEffectiveOverrides()),
		ParentID:           port.OperationID(m.GetParentId()),
		Progress:           d.progress(m.GetProgress()),
		CreatedAt:          d.ts(m.GetCreatedAt()),
		UpdatedAt:          d.ts(m.GetUpdatedAt()),
		NextRunAt:          d.tsPtr(m.GetNextRunAt()),
		Attempts:           int(m.GetAttempts()),
		HeartbeatAt:        d.tsPtr(m.GetHeartbeatAt()),
	}
}

// operationPtr converts an optional Operation; nil = nil.
func (d *decoder) operationPtr(m *pb.Operation) *domain.Operation {
	if m == nil {
		return nil
	}
	o := d.operation(m)
	return &o
}

// operations converts repeated Operations; nil stays nil.
func (d *decoder) operations(in []*pb.Operation) []domain.Operation {
	if in == nil {
		return nil
	}
	out := make([]domain.Operation, len(in))
	for i, m := range in {
		out[i] = d.operation(m)
	}
	return out
}

// progressToProto converts a Progress; nil = nil.
func progressToProto(p *domain.Progress) *pb.Progress {
	if p == nil {
		return nil
	}
	return &pb.Progress{Stage: p.Stage, At: tsToProto(p.At), Processed: p.Processed, Total: p.Total}
}

// progress converts a Progress; nil = nil.
func (d *decoder) progress(m *pb.Progress) *domain.Progress {
	if m == nil {
		return nil
	}
	return &domain.Progress{Stage: m.GetStage(), At: d.ts(m.GetAt()), Processed: m.GetProcessed(), Total: m.GetTotal()}
}

// listOperationsFromProto converts a ListOperationsRequest to an OperationFilter (the bank is separate).
func listOperationsFromProto(r *pb.ListOperationsRequest) (port.OperationFilter, error) {
	var d decoder
	out := port.OperationFilter{
		Status:         fromEnum[domain.OperationStatus](&d, r.GetStatus()),
		Kind:           fromEnum[port.JobKind](&d, r.GetKind()),
		ExcludeParents: r.GetExcludeParents(),
		PageSize:       int(r.GetPageSize()),
		PageToken:      r.GetPageToken(),
	}
	return out, d.err
}

// listOperationsRequestToProto converts an OperationFilter back to a ListOperationsRequest without a bank.
func listOperationsRequestToProto(f port.OperationFilter) *pb.ListOperationsRequest {
	return &pb.ListOperationsRequest{
		Status:         toEnum[pb.OperationStatus](f.Status),
		Kind:           toEnum[pb.OperationKind](f.Kind),
		ExcludeParents: f.ExcludeParents,
		PageSize:       i32(f.PageSize),
		PageToken:      f.PageToken,
	}
}

// eventToProto converts a LifecycleEvent; the bank's tenant is not on the wire.
func eventToProto(e domain.LifecycleEvent) *pb.LifecycleEvent {
	return &pb.LifecycleEvent{
		Seq:         e.Seq,
		Type:        toEnum[pb.EventType](e.Type),
		BankId:      e.Bank.Bank,
		At:          tsToProto(e.At),
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

// event converts a LifecycleEvent; the bank carries only its id.
func (d *decoder) event(m *pb.LifecycleEvent) domain.LifecycleEvent {
	return domain.LifecycleEvent{
		Seq:         m.GetSeq(),
		Type:        fromEnum[domain.EventType](d, m.GetType()),
		Bank:        port.BankRef{Bank: m.GetBankId()},
		At:          d.ts(m.GetAt()),
		OperationID: port.OperationID(m.GetOperationId()),
		SubjectID:   m.GetSubjectId(),
	}
}

// eventTypesFromProto converts a ListEventsRequest's type filter; nil stays nil.
func eventTypesFromProto(in []pb.EventType) ([]domain.EventType, error) {
	var d decoder
	out := fromEnums[domain.EventType](&d, in)
	return out, d.err
}

package server

import (
	"memd/internal/domain"
	"memd/internal/engine/mentalmodel"

	pb "memd/gen/memory/v1"
)

// normModel fills the spec of a MentalModel: the domain spec (and its trigger) is a value, so an unset
// spec converts back as an empty one.
func normModel(m *pb.MentalModel) {
	if m.Spec == nil {
		m.Spec = &pb.MentalModelSpec{Trigger: &pb.RefreshTrigger{}}
	}
}

// Round trips for mental_model.proto (MentalModelSpec and RefreshTrigger live in roundtrip_common_test.go).
func init() {
	addRT(func(m *pb.MentalModel) (*pb.MentalModel, error) {
		var d decoder
		return mentalModelToProto(d.mentalModel(m), domain.DetailFull), d.err
	}, normModel)
	addRT(func(m *pb.DeltaOp) (*pb.DeltaOp, error) {
		var d decoder
		return deltaOpToProto(d.deltaOp(m)), d.err
	}, nil)
	addRT(func(m *pb.RefreshTrace) (*pb.RefreshTrace, error) {
		var d decoder
		return refreshTraceToProto(d.refreshTrace(m)), d.err
	}, nil)
	addRT(func(m *pb.ResolvedScope) (*pb.ResolvedScope, error) {
		var d decoder
		return resolvedScopeToProto(d.resolvedScope(m)), d.err
	}, nil)
	addRT(func(m *pb.DryRunRefreshResponse) (*pb.DryRunRefreshResponse, error) {
		o, err := dryRunFromProto(m)
		return dryRunToProto(o), err
	}, func(m *pb.DryRunRefreshResponse) {
		// trace and resolved_scope are values in mentalmodel.RefreshOutcome: unset converts back as empty.
		if m.Trace == nil {
			m.Trace = &pb.RefreshTrace{}
		}
		if m.ResolvedScope == nil {
			m.ResolvedScope = &pb.ResolvedScope{}
		}
	})
	addRT(func(m *pb.MentalModelHistoryEntry) (*pb.MentalModelHistoryEntry, error) {
		var d decoder
		return historyEntryToProto(d.historyEntry(m)), d.err
	}, nil)
	addRT(func(m *pb.GetMentalModelHistoryResponse) (*pb.GetMentalModelHistoryResponse, error) {
		p, err := historyPageFromProto(m)
		return historyPageToProto(p), err
	}, nil)
	addRT(func(m *pb.GetMentalModelHistoryRequest) (*pb.GetMentalModelHistoryRequest, error) {
		r, err := historyRequestFromProto(m)
		return historyRequestToProto(r), err
	}, nil)
	addRT(func(m *pb.ListMentalModelsRequest) (*pb.ListMentalModelsRequest, error) {
		r, err := listMentalModelsFromProto(m)
		return listMentalModelsToProto(r), err
	}, nil)
	addRT(func(m *pb.ListMentalModelsResponse) (*pb.ListMentalModelsResponse, error) {
		var d decoder
		p := mentalmodel.ModelPage{Items: d.mentalModels(m.GetMentalModels()), NextPageToken: m.GetNextPageToken()}
		return mentalModelPageToProto(p, domain.DetailFull), d.err
	}, func(m *pb.ListMentalModelsResponse) {
		for _, x := range m.MentalModels {
			normModel(x)
		}
	})
	addRT(func(m *pb.RefreshMentalModelRequest) (*pb.RefreshMentalModelRequest, error) {
		r, err := refreshRequestFromProto(m)
		return refreshRequestToProto(r), err
	}, nil)
	addRT(func(m *pb.SearchPagesRequest) (*pb.SearchPagesRequest, error) {
		return searchPagesToProto(searchPagesFromProto(m)), nil
	}, nil)
	addRT(func(m *pb.PageHit) (*pb.PageHit, error) { return pageHitToProto(pageHitFromProto(m)), nil }, nil)
	addRT(func(m *pb.SearchPagesResponse) (*pb.SearchPagesResponse, error) {
		return &pb.SearchPagesResponse{Hits: pageHitsToProto(pageHitsFromProto(m.GetHits()))}, nil
	}, nil)

	const ids = "ids only; decoded inline by the handler, covered by TestEveryRPCReachable"
	addExempt[*pb.CreateMentalModelRequest]("envelope around MentalModelSpec plus bank_id")
	addExempt[*pb.CreateMentalModelResponse]("envelope around MentalModel and Operation")
	addExempt[*pb.GetMentalModelRequest]("ids plus detail; decoded inline, covered by TestMentalModelDetail")
	addExempt[*pb.GetMentalModelResponse]("envelope around MentalModel")
	addExempt[*pb.UpdateMentalModelRequest]("decoded to a SpecPatch func by specPatch; no value counterpart")
	addExempt[*pb.UpdateMentalModelResponse]("envelope around MentalModel")
	addExempt[*pb.RefreshMentalModelResponse]("envelope around Operation plus joined_queued")
	addExempt[*pb.DryRunRefreshRequest]("ids plus strategy; decoded inline into mentalmodel.RefreshOptions")
	addExempt[*pb.ClearMentalModelRequest](ids)
	addExempt[*pb.ClearMentalModelResponse]("envelope around MentalModel")
	addExempt[*pb.DeleteMentalModelRequest](ids)
	addExempt[*pb.DeleteMentalModelResponse]("empty")
	addExempt[*pb.DeleteFolderRequest](ids)
	addExempt[*pb.DeleteFolderResponse]("envelope around a count")
}

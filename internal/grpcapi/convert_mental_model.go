package grpcapi

import (
	"memd/internal/domain"
	"memd/internal/mentalmodel"
	"memd/internal/recall"

	pb "memd/gen/memory/v1"
)

// View defaults per RPC (MentalModelService): GetMentalModel returns everything, ListMentalModels only the
// basic view.
const (
	// getViewDefault is GetMentalModel's view when the request leaves it unspecified.
	getViewDefault = domain.MentalModelViewFull
	// listViewDefault is ListMentalModels' view when the request leaves it unspecified.
	listViewDefault = domain.MentalModelViewBasic
)

// viewOr returns view, or def when view is unspecified.
func viewOr(view, def domain.MentalModelView) domain.MentalModelView {
	if view == domain.MentalModelViewUnspecified {
		return def
	}
	return view
}

// mentalModelToProto converts a MentalModel trimmed to view (memory.v1.MentalModelView): BASIC is the spec and
// status fields, CONTENT adds content_md and structured_output_json, FULL (or unspecified) adds
// source_memory_ids and last_trace. Callers resolve the RPC's default view first.
func mentalModelToProto(m domain.MentalModel, view domain.MentalModelView) *pb.MentalModel {
	out := &pb.MentalModel{
		Spec:                specToProto(m.Spec),
		Version:             m.Version,
		Stale:               m.Stale,
		LastRefreshedAt:     tsPtrToProto(m.LastRefreshedAt),
		LastSeqSeen:         m.LastSeqSeen,
		LastRefreshFailedAt: tsPtrToProto(m.LastRefreshFailedAt),
		AutoRefreshPaused:   m.AutoRefreshPaused,
		CreatedAt:           tsToProto(m.CreatedAt),
	}
	if view == domain.MentalModelViewBasic {
		return out
	}
	out.ContentMd = m.ContentMD
	out.StructuredOutputJson = string(m.StructuredOutputJSON)
	if view != domain.MentalModelViewContent {
		out.SourceMemoryIds = strs[string](m.SourceIDs)
		out.LastTrace = refreshTracePtrToProto(m.LastTrace)
	}
	return out
}

// mentalModelsToProto converts mental models trimmed to view; nil stays nil.
func mentalModelsToProto(in []domain.MentalModel, view domain.MentalModelView) []*pb.MentalModel {
	if in == nil {
		return nil
	}
	out := make([]*pb.MentalModel, len(in))
	for i := range in {
		out[i] = mentalModelToProto(in[i], view)
	}
	return out
}

// deltaOpToProto converts a DeltaOp.
func deltaOpToProto(o domain.DeltaOp) *pb.DeltaOp {
	return &pb.DeltaOp{
		Kind:         toEnum[pb.DeltaOp_Kind](o.Kind),
		SectionId:    o.SectionID,
		BlockId:      o.BlockID,
		AfterBlockId: o.AfterBlockID,
		Heading:      o.Heading,
		BlocksMd:     o.BlocksMD,
		Applied:      o.Applied,
		SkipReason:   o.SkipReason,
	}
}

// deltaOpsToProto converts delta ops; nil stays nil.
func deltaOpsToProto(in []domain.DeltaOp) []*pb.DeltaOp {
	if in == nil {
		return nil
	}
	out := make([]*pb.DeltaOp, len(in))
	for i := range in {
		out[i] = deltaOpToProto(in[i])
	}
	return out
}

// refreshTraceToProto converts a RefreshTrace.
func refreshTraceToProto(t domain.RefreshTrace) *pb.RefreshTrace {
	return &pb.RefreshTrace{
		EffectiveMode:      toEnum[pb.RefreshMode](t.EffectiveMode),
		ModeFallbackReason: t.ModeFallbackReason,
		Outcome:            toEnum[pb.RefreshOutcome](t.Outcome),
		ToolCalls:          toolCallsToProto(t.ToolCalls),
		DeltaOperations:    deltaOpsToProto(t.DeltaOperations),
		Usage:              usagesToProto(t.Usage),
		RecordedAt:         tsToProto(t.RecordedAt),
		DurationMs:         t.DurationMS,
		Warnings:           t.Warnings,
		EffectiveStrategy:  toEnum[pb.RefreshStrategy](t.EffectiveStrategy),
	}
}

// refreshTracePtrToProto converts an optional RefreshTrace; nil = nil.
func refreshTracePtrToProto(t *domain.RefreshTrace) *pb.RefreshTrace {
	if t == nil {
		return nil
	}
	return refreshTraceToProto(*t)
}

// resolvedScopeToProto converts a ResolvedScope.
func resolvedScopeToProto(s mentalmodel.ResolvedScope) *pb.ResolvedScope {
	return &pb.ResolvedScope{
		Tags:      tagFilterToProto(s.Tags),
		FactTypes: toEnums[pb.FactType](s.FactTypes),
	}
}

// previewToProto converts a preview's RefreshOutcome to its response.
func previewToProto(o mentalmodel.RefreshOutcome) *pb.PreviewMentalModelRefreshResponse {
	return &pb.PreviewMentalModelRefreshResponse{
		RequestedMode:   toEnum[pb.RefreshMode](o.RequestedMode),
		Trace:           refreshTraceToProto(o.Trace),
		ResolvedScope:   resolvedScopeToProto(o.ResolvedScope),
		WindowAfterSeq:  o.WindowAfterSeq,
		WindowBeforeSeq: o.WindowBeforeSeq,
		FactsRetrieved:  i32(o.FactsRetrieved),
		FactsUsed:       i32(o.FactsUsed),
		Diff:            o.Diff,
		WouldPersist:    o.WouldPersist,
	}
}

// historyEntryToProto converts a MentalModelHistoryEntry; Version is not on the wire.
func historyEntryToProto(e domain.MentalModelHistoryEntry) *pb.MentalModelHistoryEntry {
	return &pb.MentalModelHistoryEntry{
		Kind:            toEnum[pb.MentalModelHistoryEntry_Kind](e.Kind),
		PreviousContent: e.PreviousContent,
		CreatedAt:       tsToProto(e.CreatedAt),
		Outcome:         toEnum[pb.RefreshOutcome](e.Outcome),
		FailureReason:   e.FailureReason,
		ErrorMessage:    e.ErrorMessage,
	}
}

// historyPageToProto converts a HistoryPage to its response.
func historyPageToProto(p mentalmodel.HistoryPage) *pb.ListMentalModelHistoryResponse {
	var entries []*pb.MentalModelHistoryEntry
	if p.Items != nil {
		entries = make([]*pb.MentalModelHistoryEntry, len(p.Items))
		for i := range p.Items {
			entries[i] = historyEntryToProto(p.Items[i])
		}
	}
	return &pb.ListMentalModelHistoryResponse{Entries: entries, NextPageToken: p.NextPageToken}
}

// historyRequestFromProto converts a ListMentalModelHistoryRequest; Bank.Bank carries bank_id (the handler
// sets the full ref).
func historyRequestFromProto(r *pb.ListMentalModelHistoryRequest) (mentalmodel.HistoryRequest, error) {
	var d decoder
	out := mentalmodel.HistoryRequest{
		Bank:      domain.BankRef{Bank: r.GetBankId()},
		ID:        domain.MentalModelID(r.GetMentalModelId()),
		Kind:      fromEnum[domain.HistoryKind](&d, r.GetKind()),
		PageSize:  int(r.GetPageSize()),
		PageToken: r.GetPageToken(),
	}
	return out, d.err
}

// listMentalModelsFromProto converts a ListMentalModelsRequest; View stays as sent (the handler applies
// the default) and Bank.Bank carries bank_id.
func listMentalModelsFromProto(r *pb.ListMentalModelsRequest) (mentalmodel.ListRequest, error) {
	var d decoder
	out := mentalmodel.ListRequest{
		Bank:         domain.BankRef{Bank: r.GetBankId()},
		FolderPrefix: r.GetFolderPrefix(),
		View:         fromEnum[domain.MentalModelView](&d, r.GetView()),
		Tags:         d.tagFilter(r.GetTags()),
		PageSize:     int(r.GetPageSize()),
		PageToken:    r.GetPageToken(),
	}
	return out, d.err
}

// mentalModelPageToProto converts a ModelPage trimmed to view.
func mentalModelPageToProto(p mentalmodel.ModelPage, view domain.MentalModelView) *pb.ListMentalModelsResponse {
	return &pb.ListMentalModelsResponse{
		MentalModels:  mentalModelsToProto(p.Items, view),
		NextPageToken: p.NextPageToken,
	}
}

// refreshRequestFromProto converts a RefreshMentalModelRequest; Bank.Bank carries bank_id.
func refreshRequestFromProto(r *pb.RefreshMentalModelRequest) (mentalmodel.RefreshRequest, error) {
	var d decoder
	out := mentalmodel.RefreshRequest{
		Bank:      domain.BankRef{Bank: r.GetBankId()},
		ID:        domain.MentalModelID(r.GetMentalModelId()),
		ForceFull: r.GetForceFull(),
		Overrides: d.refreshOverrides(r.GetOverrides()),
		Strategy:  fromEnum[domain.RefreshStrategy](&d, r.GetStrategy()),
	}
	return out, d.err
}

// searchFromProto converts a SearchMentalModelsRequest; Bank.Bank carries bank_id.
func searchFromProto(r *pb.SearchMentalModelsRequest) recall.SearchMentalModelsRequest {
	return recall.SearchMentalModelsRequest{
		Bank:         domain.BankRef{Bank: r.GetBankId()},
		Query:        r.GetQuery(),
		FolderPrefix: r.GetFolderPrefix(),
		Limit:        int(r.GetMaxResults()),
	}
}

// hitToProto converts a MentalModelHit.
func hitToProto(h domain.MentalModelHit) *pb.MentalModelHit {
	return &pb.MentalModelHit{
		MentalModelId: string(h.MentalModelID),
		FolderPath:    h.FolderPath,
		Name:          h.Name,
		Snippet:       h.Snippet,
		Score:         h.Score,
	}
}

// hitsToProto converts search hits; nil stays nil.
func hitsToProto(in []domain.MentalModelHit) []*pb.MentalModelHit {
	if in == nil {
		return nil
	}
	out := make([]*pb.MentalModelHit, len(in))
	for i := range in {
		out[i] = hitToProto(in[i])
	}
	return out
}

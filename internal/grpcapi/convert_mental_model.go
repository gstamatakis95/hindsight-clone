package grpcapi

import (
	"memd/internal/domain"
	"memd/internal/engine/mentalmodel"

	pb "memd/gen/memory/v1"
)

// Detail defaults per RPC (MentalModelService): GetMentalModel returns everything, ListMentalModels only
// metadata.
const (
	// getDetailDefault is GetMentalModel's detail when the request leaves it unspecified.
	getDetailDefault = domain.DetailFull
	// listDetailDefault is ListMentalModels' detail when the request leaves it unspecified.
	listDetailDefault = domain.DetailMetadata
)

// detailOr returns detail, or def when detail is unspecified.
func detailOr(detail, def domain.Detail) domain.Detail {
	if detail == domain.DetailUnspecified {
		return def
	}
	return detail
}

// mentalModelToProto converts a MentalModel trimmed to detail (memory.v1.Detail): METADATA is the spec and
// status fields, CONTENT adds content_md and structured_output_json, FULL (or unspecified) adds source_ids
// and last_trace. Callers resolve the RPC's default detail first.
func mentalModelToProto(m domain.MentalModel, detail domain.Detail) *pb.MentalModel {
	out := &pb.MentalModel{
		Spec:                specToProto(m.Spec),
		BankId:              m.Bank.Bank,
		Version:             m.Version,
		IsStale:             m.IsStale,
		LastRefreshedAt:     tsPtrToProto(m.LastRefreshedAt),
		LastSeqSeen:         m.LastSeqSeen,
		LastRefreshFailedAt: tsPtrToProto(m.LastRefreshFailedAt),
		AutoRefreshPaused:   m.AutoRefreshPaused,
		CreatedAt:           tsToProto(m.CreatedAt),
	}
	if detail == domain.DetailMetadata {
		return out
	}
	out.ContentMd = m.ContentMD
	out.StructuredOutputJson = string(m.StructuredOutputJSON)
	if detail != domain.DetailContent {
		out.SourceIds = strs[string](m.SourceIDs)
		out.LastTrace = refreshTracePtrToProto(m.LastTrace)
	}
	return out
}

// mentalModelsToProto converts mental models trimmed to detail; nil stays nil.
func mentalModelsToProto(in []domain.MentalModel, detail domain.Detail) []*pb.MentalModel {
	if in == nil {
		return nil
	}
	out := make([]*pb.MentalModel, len(in))
	for i := range in {
		out[i] = mentalModelToProto(in[i], detail)
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
		Tags:      s.Tags,
		TagsMatch: toEnum[pb.TagsMatch](s.TagsMatch),
		TagGroups: tagGroupsToProto(s.TagGroups),
		FactTypes: toEnums[pb.FactType](s.FactTypes),
	}
}

// dryRunToProto converts a dry run's RefreshOutcome to its response.
func dryRunToProto(o mentalmodel.RefreshOutcome) *pb.DryRunRefreshResponse {
	return &pb.DryRunRefreshResponse{
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
		Kind:            toEnum[pb.HistoryKind](e.Kind),
		PreviousContent: e.PreviousContent,
		ChangedAt:       tsToProto(e.ChangedAt),
		Outcome:         toEnum[pb.RefreshOutcome](e.Outcome),
		FailureReason:   e.FailureReason,
		ErrorMessage:    e.ErrorMessage,
	}
}

// historyPageToProto converts a HistoryPage to its response.
func historyPageToProto(p mentalmodel.HistoryPage) *pb.GetMentalModelHistoryResponse {
	var entries []*pb.MentalModelHistoryEntry
	if p.Items != nil {
		entries = make([]*pb.MentalModelHistoryEntry, len(p.Items))
		for i := range p.Items {
			entries[i] = historyEntryToProto(p.Items[i])
		}
	}
	return &pb.GetMentalModelHistoryResponse{Entries: entries, NextPageToken: p.NextPageToken}
}

// historyRequestFromProto converts a GetMentalModelHistoryRequest; Bank.Bank carries bank_id (the handler
// sets the full ref).
func historyRequestFromProto(r *pb.GetMentalModelHistoryRequest) (mentalmodel.HistoryRequest, error) {
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

// listMentalModelsFromProto converts a ListMentalModelsRequest; Detail stays as sent (the handler applies
// the default) and Bank.Bank carries bank_id.
func listMentalModelsFromProto(r *pb.ListMentalModelsRequest) (mentalmodel.ListRequest, error) {
	var d decoder
	out := mentalmodel.ListRequest{
		Bank:         domain.BankRef{Bank: r.GetBankId()},
		FolderPrefix: r.GetFolderPrefix(),
		Detail:       fromEnum[domain.Detail](&d, r.GetDetail()),
		Tags:         r.GetTags(),
		TagsMatch:    fromEnum[domain.TagsMatch](&d, r.GetTagsMatch()),
		PageSize:     int(r.GetPageSize()),
		PageToken:    r.GetPageToken(),
	}
	return out, d.err
}

// mentalModelPageToProto converts a MentalModelPage trimmed to detail.
func mentalModelPageToProto(p mentalmodel.ModelPage, detail domain.Detail) *pb.ListMentalModelsResponse {
	return &pb.ListMentalModelsResponse{
		MentalModels:  mentalModelsToProto(p.Items, detail),
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

// searchPagesFromProto converts a SearchPagesRequest; Bank.Bank carries bank_id.
func searchPagesFromProto(r *pb.SearchPagesRequest) mentalmodel.SearchPagesRequest {
	return mentalmodel.SearchPagesRequest{
		Bank:         domain.BankRef{Bank: r.GetBankId()},
		Query:        r.GetQuery(),
		FolderPrefix: r.GetFolderPrefix(),
		Limit:        int(r.GetLimit()),
	}
}

// pageHitToProto converts a PageHit.
func pageHitToProto(h domain.PageHit) *pb.PageHit {
	return &pb.PageHit{
		MentalModelId: string(h.MentalModelID),
		FolderPath:    h.FolderPath,
		Name:          h.Name,
		Snippet:       h.Snippet,
		Score:         h.Score,
	}
}

// pageHitsToProto converts page hits; nil stays nil.
func pageHitsToProto(in []domain.PageHit) []*pb.PageHit {
	if in == nil {
		return nil
	}
	out := make([]*pb.PageHit, len(in))
	for i := range in {
		out[i] = pageHitToProto(in[i])
	}
	return out
}

package server

import (
	"memd/internal/domain"
	"memd/internal/engine/mentalmodel"

	pb "memd/gen/memory/v1"
)

// Detail defaults per RPC (mental_model.proto): GetMentalModel returns everything, ListMentalModels only
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

// mentalModel converts a MentalModel. Domain fields with no proto twin (Embedding, UpdatedAt) stay zero.
func (d *decoder) mentalModel(m *pb.MentalModel) domain.MentalModel {
	return domain.MentalModel{
		Spec:                 d.spec(m.GetSpec()),
		Bank:                 domain.BankRef{Bank: m.GetBankId()},
		ContentMD:            m.GetContentMd(),
		StructuredOutputJSON: rawFromString(m.GetStructuredOutputJson()),
		Version:              m.GetVersion(),
		IsStale:              m.GetIsStale(),
		LastRefreshedAt:      d.tsPtr(m.GetLastRefreshedAt()),
		LastSeqSeen:          m.GetLastSeqSeen(),
		LastRefreshFailedAt:  d.tsPtr(m.GetLastRefreshFailedAt()),
		AutoRefreshPaused:    m.GetAutoRefreshPaused(),
		SourceIDs:            strs[domain.UnitID](m.GetSourceIds()),
		LastTrace:            d.refreshTracePtr(m.GetLastTrace()),
		CreatedAt:            d.ts(m.GetCreatedAt()),
	}
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

// mentalModels converts repeated MentalModels; nil stays nil.
func (d *decoder) mentalModels(in []*pb.MentalModel) []domain.MentalModel {
	if in == nil {
		return nil
	}
	out := make([]domain.MentalModel, len(in))
	for i, m := range in {
		out[i] = d.mentalModel(m)
	}
	return out
}

// deltaOp converts a DeltaOp.
func (d *decoder) deltaOp(o *pb.DeltaOp) domain.DeltaOp {
	return domain.DeltaOp{
		Kind:         fromEnum[domain.DeltaOpKind](d, o.GetKind()),
		SectionID:    o.GetSectionId(),
		BlockID:      o.GetBlockId(),
		AfterBlockID: o.GetAfterBlockId(),
		Heading:      o.GetHeading(),
		BlocksMD:     o.GetBlocksMd(),
		Applied:      o.GetApplied(),
		SkipReason:   o.GetSkipReason(),
	}
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

// deltaOps converts repeated DeltaOps; nil stays nil.
func (d *decoder) deltaOps(in []*pb.DeltaOp) []domain.DeltaOp {
	if in == nil {
		return nil
	}
	out := make([]domain.DeltaOp, len(in))
	for i, o := range in {
		out[i] = d.deltaOp(o)
	}
	return out
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

// refreshTrace converts a RefreshTrace; nil = the zero trace.
func (d *decoder) refreshTrace(t *pb.RefreshTrace) domain.RefreshTrace {
	return domain.RefreshTrace{
		EffectiveMode:      fromEnum[domain.RefreshMode](d, t.GetEffectiveMode()),
		ModeFallbackReason: t.GetModeFallbackReason(),
		Outcome:            fromEnum[domain.RefreshOutcome](d, t.GetOutcome()),
		ToolCalls:          toolCallsFromProto(t.GetToolCalls()),
		DeltaOperations:    d.deltaOps(t.GetDeltaOperations()),
		Usage:              d.usages(t.GetUsage()),
		RecordedAt:         d.ts(t.GetRecordedAt()),
		DurationMS:         t.GetDurationMs(),
		Warnings:           t.GetWarnings(),
		EffectiveStrategy:  fromEnum[domain.RefreshStrategy](d, t.GetEffectiveStrategy()),
	}
}

// refreshTracePtr converts an optional RefreshTrace; nil = nil.
func (d *decoder) refreshTracePtr(t *pb.RefreshTrace) *domain.RefreshTrace {
	if t == nil {
		return nil
	}
	out := d.refreshTrace(t)
	return &out
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

// resolvedScope converts a ResolvedScope; nil = the zero scope.
func (d *decoder) resolvedScope(s *pb.ResolvedScope) mentalmodel.ResolvedScope {
	return mentalmodel.ResolvedScope{
		Tags:      s.GetTags(),
		TagsMatch: fromEnum[domain.TagsMatch](d, s.GetTagsMatch()),
		TagGroups: d.tagGroups(s.GetTagGroups()),
		FactTypes: fromEnums[domain.FactType](d, s.GetFactTypes()),
	}
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

// dryRunFromProto converts a DryRunRefreshResponse to the RefreshOutcome it reports. ContentMD and
// VersionWritten are not on the wire.
func dryRunFromProto(r *pb.DryRunRefreshResponse) (mentalmodel.RefreshOutcome, error) {
	var d decoder
	out := mentalmodel.RefreshOutcome{
		Trace:           d.refreshTrace(r.GetTrace()),
		RequestedMode:   fromEnum[domain.RefreshMode](&d, r.GetRequestedMode()),
		ResolvedScope:   d.resolvedScope(r.GetResolvedScope()),
		Diff:            r.GetDiff(),
		WindowAfterSeq:  r.GetWindowAfterSeq(),
		WindowBeforeSeq: r.GetWindowBeforeSeq(),
		FactsRetrieved:  int(r.GetFactsRetrieved()),
		FactsUsed:       int(r.GetFactsUsed()),
		WouldPersist:    r.GetWouldPersist(),
	}
	return out, d.err
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

// historyEntry converts a MentalModelHistoryEntry; the domain Version has no proto twin and stays 0.
func (d *decoder) historyEntry(e *pb.MentalModelHistoryEntry) domain.MentalModelHistoryEntry {
	return domain.MentalModelHistoryEntry{
		Kind:            fromEnum[domain.HistoryKind](d, e.GetKind()),
		PreviousContent: e.GetPreviousContent(),
		ChangedAt:       d.ts(e.GetChangedAt()),
		Outcome:         fromEnum[domain.RefreshOutcome](d, e.GetOutcome()),
		FailureReason:   e.GetFailureReason(),
		ErrorMessage:    e.GetErrorMessage(),
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

// historyPageFromProto converts a GetMentalModelHistoryResponse to a HistoryPage.
func historyPageFromProto(r *pb.GetMentalModelHistoryResponse) (mentalmodel.HistoryPage, error) {
	var d decoder
	var items []domain.MentalModelHistoryEntry
	if r.GetEntries() != nil {
		items = make([]domain.MentalModelHistoryEntry, len(r.GetEntries()))
		for i, e := range r.GetEntries() {
			items[i] = d.historyEntry(e)
		}
	}
	return mentalmodel.HistoryPage{Items: items, NextPageToken: r.GetNextPageToken()}, d.err
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

// historyRequestToProto converts a HistoryRequest back to its proto (round-trip tests).
func historyRequestToProto(r mentalmodel.HistoryRequest) *pb.GetMentalModelHistoryRequest {
	return &pb.GetMentalModelHistoryRequest{
		BankId:        r.Bank.Bank,
		MentalModelId: string(r.ID),
		Kind:          toEnum[pb.HistoryKind](r.Kind),
		PageSize:      i32(r.PageSize),
		PageToken:     r.PageToken,
	}
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

// listMentalModelsToProto converts a ListMentalModelsRequest back to its proto (round-trip tests).
func listMentalModelsToProto(r mentalmodel.ListRequest) *pb.ListMentalModelsRequest {
	return &pb.ListMentalModelsRequest{
		BankId:       r.Bank.Bank,
		FolderPrefix: r.FolderPrefix,
		Detail:       toEnum[pb.Detail](r.Detail),
		Tags:         r.Tags,
		TagsMatch:    toEnum[pb.TagsMatch](r.TagsMatch),
		PageSize:     i32(r.PageSize),
		PageToken:    r.PageToken,
	}
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

// refreshRequestToProto converts a RefreshRequest back to its proto (round-trip tests).
func refreshRequestToProto(r mentalmodel.RefreshRequest) *pb.RefreshMentalModelRequest {
	return &pb.RefreshMentalModelRequest{
		BankId:        r.Bank.Bank,
		MentalModelId: string(r.ID),
		ForceFull:     r.ForceFull,
		Overrides:     refreshOverridesToProto(r.Overrides),
		Strategy:      toEnum[pb.RefreshStrategy](r.Strategy),
	}
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

// searchPagesToProto converts a SearchPagesRequest back to its proto (round-trip tests).
func searchPagesToProto(r mentalmodel.SearchPagesRequest) *pb.SearchPagesRequest {
	return &pb.SearchPagesRequest{
		BankId:       r.Bank.Bank,
		Query:        r.Query,
		FolderPrefix: r.FolderPrefix,
		Limit:        i32(r.Limit),
	}
}

// pageHitFromProto converts a PageHit.
func pageHitFromProto(h *pb.PageHit) domain.PageHit {
	return domain.PageHit{
		MentalModelID: domain.MentalModelID(h.GetMentalModelId()),
		FolderPath:    h.GetFolderPath(),
		Name:          h.GetName(),
		Snippet:       h.GetSnippet(),
		Score:         h.GetScore(),
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

// pageHitsFromProto converts repeated PageHits; nil stays nil.
func pageHitsFromProto(in []*pb.PageHit) []domain.PageHit {
	if in == nil {
		return nil
	}
	out := make([]domain.PageHit, len(in))
	for i, h := range in {
		out[i] = pageHitFromProto(h)
	}
	return out
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

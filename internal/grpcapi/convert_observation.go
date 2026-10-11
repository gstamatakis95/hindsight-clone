package grpcapi

import (
	"memd/internal/domain"
	"memd/internal/observation"

	pb "memd/gen/memory/v1"
)

// observationScopeToProto converts a ScopeInfo; its scope is always set.
func observationScopeToProto(s domain.ScopeInfo) *pb.ObservationScope {
	return &pb.ObservationScope{
		Scope:                  &pb.TagSet{Tags: s.Scope.Tags},
		Observations:           s.Observations,
		UnconsolidatedMemories: s.UnconsolidatedMemories,
	}
}

// scopePageToProto converts a ScopePage to a ListObservationScopesResponse.
func scopePageToProto(p observation.ScopePage) *pb.ListObservationScopesResponse {
	var scopes []*pb.ObservationScope
	if p.Items != nil {
		scopes = make([]*pb.ObservationScope, len(p.Items))
		for i := range p.Items {
			scopes[i] = observationScopeToProto(p.Items[i])
		}
	}
	return &pb.ListObservationScopesResponse{Scopes: scopes, NextPageToken: p.NextPageToken}
}

// observationVersionToProto converts an ObservationVersion.
func observationVersionToProto(v domain.ObservationVersion) *pb.ObservationVersion {
	return &pb.ObservationVersion{
		Version: v.Version, Text: v.Text, SourceFactIds: strs[string](v.SourceFactIDs), CreatedAt: tsToProto(v.CreatedAt),
	}
}

// observationHistoryToProto converts a HistoryPage to a ListObservationHistoryResponse, in the order given.
func observationHistoryToProto(p observation.HistoryPage) *pb.ListObservationHistoryResponse {
	out := &pb.ListObservationHistoryResponse{NextPageToken: p.NextPageToken}
	if p.Items != nil {
		out.Versions = make([]*pb.ObservationVersion, len(p.Items))
		for i := range p.Items {
			out.Versions[i] = observationVersionToProto(p.Items[i])
		}
	}
	return out
}

// clearRequestFromProto converts a ClearObservationsRequest's target and reconsolidate flag; the caller sets
// Bank. No target, an empty scope list, an empty memory_id or all = false is an ErrInvalidArgument naming the
// field, so a destructive clear never falls back to a default target.
func clearRequestFromProto(r *pb.ClearObservationsRequest) (observation.ClearRequest, error) {
	out := observation.ClearRequest{Reconsolidate: r.GetReconsolidate()}
	switch t := r.GetTarget().(type) {
	case *pb.ClearObservationsRequest_Scopes:
		if len(t.Scopes.GetTagSets()) == 0 {
			return out, domain.InvalidField("scopes.tag_sets", "scopes must name at least one scope")
		}
		out.Scopes = tagSetsFromProto(t.Scopes.GetTagSets())
	case *pb.ClearObservationsRequest_MemoryId:
		if err := requireID("memory_id", t.MemoryId); err != nil {
			return out, err
		}
		out.MemoryID = domain.UnitID(t.MemoryId)
	case *pb.ClearObservationsRequest_All:
		if !t.All {
			return out, domain.InvalidField("all", "all must be true to clear every observation")
		}
		out.All = true
	default:
		return out, domain.InvalidField("target", "set one of scopes, memory_id and all")
	}
	return out, nil
}

// scopeKeysToProto encodes scope keys as length-prefixed tag strings; the bank is not on the wire.
func scopeKeysToProto(in []domain.ScopeKey) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i := range in {
		out[i] = domain.EncodeScopeTags(in[i].Tags)
	}
	return out
}

// strategyMatchToProto converts a StrategyMatch.
func strategyMatchToProto(m observation.StrategyMatch) *pb.StrategyMatch {
	return &pb.StrategyMatch{
		Strategy:       m.Strategy,
		MatchedScopes:  scopeKeysToProto(m.MatchedScopes),
		ShadowedScopes: scopeKeysToProto(m.ShadowedScopes),
	}
}

// strategyPreviewToProto converts a StrategyPreview to a PreviewConsolidationStrategiesResponse.
func strategyPreviewToProto(p observation.StrategyPreview) *pb.PreviewConsolidationStrategiesResponse {
	var matches []*pb.StrategyMatch
	if p.Matches != nil {
		matches = make([]*pb.StrategyMatch, len(p.Matches))
		for i := range p.Matches {
			matches[i] = strategyMatchToProto(p.Matches[i])
		}
	}
	return &pb.PreviewConsolidationStrategiesResponse{Matches: matches, Complete: p.Complete}
}

// strategiesFromProto converts a PreviewConsolidationStrategiesRequest's draft strategies.
func strategiesFromProto(in []*pb.ConsolidationStrategy) ([]domain.ConsolidationStrategy, error) {
	var d decoder
	out := d.strategies(in)
	return out, d.err
}

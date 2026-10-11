package grpcapi

import (
	"memd/internal/domain"
	"memd/internal/engine/consolidate"
	"memd/internal/engine/curate"

	pb "memd/gen/memory/v1"
)

// scopeInfoToProto converts a ScopeInfo; its scope is always set.
func scopeInfoToProto(s domain.ScopeInfo) *pb.ScopeInfo {
	return &pb.ScopeInfo{
		Scope:               &pb.TagSet{Tags: s.Scope.Tags},
		Observations:        s.Observations,
		UnconsolidatedUnits: s.UnconsolidatedUnits,
	}
}

// scopePageToProto converts a ScopePage to a ListScopesResponse.
func scopePageToProto(p curate.ScopePage) *pb.ListScopesResponse {
	var scopes []*pb.ScopeInfo
	if p.Items != nil {
		scopes = make([]*pb.ScopeInfo, len(p.Items))
		for i := range p.Items {
			scopes[i] = scopeInfoToProto(p.Items[i])
		}
	}
	return &pb.ListScopesResponse{Scopes: scopes, NextPageToken: p.NextPageToken}
}

// clearResultToObservationsProto converts a ClearResult to a ClearObservationsResponse.
func clearResultToObservationsProto(r curate.ClearResult) *pb.ClearObservationsResponse {
	return &pb.ClearObservationsResponse{Deleted: r.Deleted, Operation: operationPtrToProto(r.Operation)}
}

// clearResultToMemoryProto converts a ClearResult to a ClearMemoryObservationsResponse.
func clearResultToMemoryProto(r curate.ClearResult) *pb.ClearMemoryObservationsResponse {
	return &pb.ClearMemoryObservationsResponse{Deleted: r.Deleted, Operation: operationPtrToProto(r.Operation)}
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
func strategyMatchToProto(m consolidate.StrategyMatch) *pb.StrategyMatch {
	return &pb.StrategyMatch{
		Strategy:       m.Strategy,
		MatchedScopes:  scopeKeysToProto(m.MatchedScopes),
		ShadowedScopes: scopeKeysToProto(m.ShadowedScopes),
	}
}

// strategyPreviewToProto converts a StrategyPreview to a PreviewConsolidationStrategiesResponse.
func strategyPreviewToProto(p consolidate.StrategyPreview) *pb.PreviewConsolidationStrategiesResponse {
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

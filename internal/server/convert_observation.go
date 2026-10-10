package server

import (
	"memd/internal/domain"
	"memd/internal/engine/consolidate"
	"memd/internal/port"

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

// scopeInfoFromProto converts a ScopeInfo; a nil scope is the empty (shared) set.
func scopeInfoFromProto(m *pb.ScopeInfo) domain.ScopeInfo {
	return domain.ScopeInfo{
		Scope:               domain.TagSet{Tags: m.GetScope().GetTags()},
		Observations:        m.GetObservations(),
		UnconsolidatedUnits: m.GetUnconsolidatedUnits(),
	}
}

// scopePageToProto converts a ScopePage to a ListScopesResponse.
func scopePageToProto(p ScopePage) *pb.ListScopesResponse {
	var scopes []*pb.ScopeInfo
	if p.Items != nil {
		scopes = make([]*pb.ScopeInfo, len(p.Items))
		for i := range p.Items {
			scopes[i] = scopeInfoToProto(p.Items[i])
		}
	}
	return &pb.ListScopesResponse{Scopes: scopes, NextPageToken: p.NextPageToken}
}

// scopePageFromProto converts a ListScopesResponse to a ScopePage.
func scopePageFromProto(m *pb.ListScopesResponse) ScopePage {
	var scopes []domain.ScopeInfo
	if m.GetScopes() != nil {
		scopes = make([]domain.ScopeInfo, len(m.GetScopes()))
		for i, s := range m.GetScopes() {
			scopes[i] = scopeInfoFromProto(s)
		}
	}
	return ScopePage{Items: scopes, NextPageToken: m.GetNextPageToken()}
}

// clearResultToObservationsProto converts a ClearResult to a ClearObservationsResponse.
func clearResultToObservationsProto(r ClearResult) *pb.ClearObservationsResponse {
	return &pb.ClearObservationsResponse{Deleted: r.Deleted, Operation: operationPtrToProto(r.Operation)}
}

// clearResultToMemoryProto converts a ClearResult to a ClearMemoryObservationsResponse.
func clearResultToMemoryProto(r ClearResult) *pb.ClearMemoryObservationsResponse {
	return &pb.ClearMemoryObservationsResponse{Deleted: r.Deleted, Operation: operationPtrToProto(r.Operation)}
}

// clearResult converts the fields of either Clear*Response to a ClearResult.
func (d *decoder) clearResult(deleted int64, op *pb.Operation) ClearResult {
	return ClearResult{Deleted: deleted, Operation: d.operationPtr(op)}
}

// scopeKeysToProto encodes scope keys as length-prefixed tag strings; the bank is not on the wire.
func scopeKeysToProto(in []port.ScopeKey) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i := range in {
		out[i] = domain.EncodeScopeTags(in[i].Tags)
	}
	return out
}

// scopeKeys decodes length-prefixed tag strings structurally (no normalization); a malformed one fails the
// decoder with INVALID_ARGUMENT.
func (d *decoder) scopeKeys(in []string) []port.ScopeKey {
	if in == nil {
		return nil
	}
	out := make([]port.ScopeKey, len(in))
	for i, s := range in {
		tags, err := domain.DecodeScopeTags(s)
		if err != nil {
			d.fail(err)
		}
		out[i] = port.ScopeKey{Tags: tags}
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

// strategyMatch converts a StrategyMatch.
func (d *decoder) strategyMatch(m *pb.StrategyMatch) consolidate.StrategyMatch {
	return consolidate.StrategyMatch{
		Strategy:       m.GetStrategy(),
		MatchedScopes:  d.scopeKeys(m.GetMatchedScopes()),
		ShadowedScopes: d.scopeKeys(m.GetShadowedScopes()),
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

// strategyPreview converts a PreviewConsolidationStrategiesResponse to a StrategyPreview.
func (d *decoder) strategyPreview(m *pb.PreviewConsolidationStrategiesResponse) consolidate.StrategyPreview {
	var matches []consolidate.StrategyMatch
	if m.GetMatches() != nil {
		matches = make([]consolidate.StrategyMatch, len(m.GetMatches()))
		for i, x := range m.GetMatches() {
			matches[i] = d.strategyMatch(x)
		}
	}
	return consolidate.StrategyPreview{Matches: matches, Complete: m.GetComplete()}
}

// strategiesFromProto converts a PreviewConsolidationStrategiesRequest's draft strategies.
func strategiesFromProto(in []*pb.ConsolidationStrategy) ([]domain.ConsolidationStrategy, error) {
	var d decoder
	out := d.strategies(in)
	return out, d.err
}

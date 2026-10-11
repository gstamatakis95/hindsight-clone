package grpcapi

import (
	"memd/internal/domain"
	"memd/internal/recall"
	reflecteng "memd/internal/reflect"

	pb "memd/gen/memory/v1"
)

// ---------- tags and scopes ----------

// tagSetsFromProto converts repeated TagSets; a nil element becomes an empty set.
func tagSetsFromProto(in []*pb.TagSet) []domain.TagSet {
	if in == nil {
		return nil
	}
	out := make([]domain.TagSet, len(in))
	for i, s := range in {
		out[i] = domain.TagSet{Tags: s.GetTags()}
	}
	return out
}

// tagSetsToProto converts tag sets to repeated TagSets.
func tagSetsToProto(in []domain.TagSet) []*pb.TagSet {
	if in == nil {
		return nil
	}
	out := make([]*pb.TagSet, len(in))
	for i := range in {
		out[i] = &pb.TagSet{Tags: in[i].Tags}
	}
	return out
}

// scopesFromProto converts ObservationScopeModes; nil = nil.
func (d *decoder) scopes(s *pb.ObservationScopeModes) *domain.ObservationScopeModes {
	if s == nil {
		return nil
	}
	return &domain.ObservationScopeModes{
		Mode:            fromEnum[domain.ScopeMode](d, s.GetMode()),
		Custom:          tagSetsFromProto(s.GetCustom()),
		MaxCombinations: optInt(s.MaxCombinations),
	}
}

// scopesToProto converts ObservationScopeModes; nil = nil.
func scopesToProto(s *domain.ObservationScopeModes) *pb.ObservationScopeModes {
	if s == nil {
		return nil
	}
	return &pb.ObservationScopeModes{
		Mode:            toEnum[pb.ObservationScopeModes_Mode](s.Mode),
		Custom:          tagSetsToProto(s.Custom),
		MaxCombinations: optInt32(s.MaxCombinations),
	}
}

// tagGroup converts a TagGroup oneof; an unset oneof becomes a group with an empty Kind.
func (d *decoder) tagGroup(g *pb.TagGroup) domain.TagGroup {
	switch n := g.GetNode().(type) {
	case *pb.TagGroup_Leaf:
		return domain.TagGroup{Kind: domain.TagGroupLeaf, Leaf: d.tagLeaf(n.Leaf)}
	case *pb.TagGroup_AllOf:
		return domain.TagGroup{Kind: domain.TagGroupAllOf, Groups: d.tagGroups(n.AllOf.GetGroups())}
	case *pb.TagGroup_AnyOf:
		return domain.TagGroup{Kind: domain.TagGroupAnyOf, Groups: d.tagGroups(n.AnyOf.GetGroups())}
	case *pb.TagGroup_NoneOf:
		var not *domain.TagGroup
		if n.NoneOf != nil {
			v := d.tagGroup(n.NoneOf)
			not = &v
		}
		return domain.TagGroup{Kind: domain.TagGroupNoneOf, Not: not}
	default:
		return domain.TagGroup{}
	}
}

// tagLeaf converts a TagLeaf; nil = nil.
func (d *decoder) tagLeaf(l *pb.TagLeaf) *domain.TagLeaf {
	if l == nil {
		return nil
	}
	return &domain.TagLeaf{
		Tags:    l.GetTags(),
		Match:   fromEnum[domain.TagsMatch](d, l.GetMatch()),
		Resolve: fromEnum[domain.TagResolve](d, l.GetResolve()),
	}
}

// tagGroups converts repeated TagGroups; nil stays nil.
func (d *decoder) tagGroups(in []*pb.TagGroup) []domain.TagGroup {
	if in == nil {
		return nil
	}
	out := make([]domain.TagGroup, len(in))
	for i, g := range in {
		out[i] = d.tagGroup(g)
	}
	return out
}

// tagGroupToProto converts a TagGroup; an unknown Kind becomes an unset oneof.
func tagGroupToProto(g domain.TagGroup) *pb.TagGroup {
	switch g.Kind {
	case domain.TagGroupLeaf:
		return &pb.TagGroup{Node: &pb.TagGroup_Leaf{Leaf: tagLeafToProto(g.Leaf)}}
	case domain.TagGroupAllOf:
		return &pb.TagGroup{Node: &pb.TagGroup_AllOf{AllOf: &pb.TagGroupList{Groups: tagGroupsToProto(g.Groups)}}}
	case domain.TagGroupAnyOf:
		return &pb.TagGroup{Node: &pb.TagGroup_AnyOf{AnyOf: &pb.TagGroupList{Groups: tagGroupsToProto(g.Groups)}}}
	case domain.TagGroupNoneOf:
		var not *pb.TagGroup
		if g.Not != nil {
			not = tagGroupToProto(*g.Not)
		}
		return &pb.TagGroup{Node: &pb.TagGroup_NoneOf{NoneOf: not}}
	default:
		return &pb.TagGroup{}
	}
}

// tagLeafToProto converts a TagLeaf; nil = nil.
func tagLeafToProto(l *domain.TagLeaf) *pb.TagLeaf {
	if l == nil {
		return nil
	}
	return &pb.TagLeaf{
		Tags:    l.Tags,
		Match:   toEnum[pb.TagsMatch](l.Match),
		Resolve: toEnum[pb.TagLeaf_Resolve](l.Resolve),
	}
}

// tagGroupsToProto converts tag groups; nil stays nil.
func tagGroupsToProto(in []domain.TagGroup) []*pb.TagGroup {
	if in == nil {
		return nil
	}
	out := make([]*pb.TagGroup, len(in))
	for i := range in {
		out[i] = tagGroupToProto(in[i])
	}
	return out
}

// tagFilter converts a TagFilter, the one tag-filter message of every site; nil = the zero filter (no filter).
// Validation (both forms set, flat-only sites) is the receiving service's.
func (d *decoder) tagFilter(f *pb.TagFilter) domain.TagFilter {
	if f == nil {
		return domain.TagFilter{}
	}
	return domain.TagFilter{
		Tags: f.GetTags(), Match: fromEnum[domain.TagsMatch](d, f.GetMatch()), Groups: d.tagGroups(f.GetGroups()),
	}
}

// tagFilterToProto converts a TagFilter; the zero filter = nil.
func tagFilterToProto(f domain.TagFilter) *pb.TagFilter {
	if len(f.Tags) == 0 && f.Match == domain.TagsMatchUnspecified && len(f.Groups) == 0 {
		return nil
	}
	return &pb.TagFilter{Tags: f.Tags, Match: toEnum[pb.TagsMatch](f.Match), Groups: tagGroupsToProto(f.Groups)}
}

// ---------- scores and memories ----------

// minScores converts MinScores; nil = no floors.
func minScoresFromProto(m *pb.MinScores) domain.MinScores {
	if m == nil {
		return domain.MinScores{}
	}
	return domain.MinScores{Arms: m.GetArms(), Reranker: optCopy(m.Reranker), Final: optCopy(m.Final)}
}

// scoresToProto converts Scores.
func scoresToProto(s recall.Scores) *pb.Scores {
	return &pb.Scores{Final: s.Final, Reranker: optCopy(s.Reranker), Arms: s.Arms}
}

// unitToProto converts a domain unit to a Memory without scores.
func unitToProto(u domain.Unit) *pb.Memory {
	return &pb.Memory{
		Id:            string(u.ID),
		FactType:      toEnum[pb.FactType](u.Type),
		Text:          u.Text,
		Context:       u.Context,
		Metadata:      u.Metadata,
		Tags:          u.Tags,
		Entities:      u.Entities,
		OccurredStart: tsPtrToProto(u.OccurredStart),
		OccurredEnd:   tsPtrToProto(u.OccurredEnd),
		MentionedAt:   tsPtrToProto(u.MentionedAt),
		DocumentId:    string(u.DocumentID),
		ChunkId:       string(u.ChunkID),
		ProofCount:    i32(u.ProofCount),
		SourceFactIds: strs[string](u.SourceIDs),
		State:         toEnum[pb.MemoryState](u.State),
		EditedAt:      tsPtrToProto(u.EditedAt),
	}
}

// unitsToProto converts units to Memories without scores.
func unitsToProto(in []domain.Unit) []*pb.Memory {
	out := make([]*pb.Memory, len(in))
	for i := range in {
		out[i] = unitToProto(in[i])
	}
	return out
}

// memoryToProto converts a recall result to a scored Memory.
func memoryToProto(m recall.Memory) *pb.Memory {
	out := unitToProto(m.Unit)
	out.Scores = scoresToProto(m.Scores)
	out.Stale = m.Stale
	return out
}

// chunkToProto converts a Chunk.
func chunkToProto(c recall.Chunk) *pb.Chunk {
	return &pb.Chunk{Id: string(c.ID), Text: c.Text, ChunkIndex: i32(c.Index), Truncated: c.Truncated}
}

// ---------- directives, usage, entities ----------

// directiveFromProto converts a Directive (the bank is set by the caller).
func directiveFromProto(x *pb.Directive) domain.Directive {
	return domain.Directive{
		ID: domain.DirectiveID(x.GetId()), Name: x.GetName(), Rules: x.GetRules(), Tags: x.GetTags(),
		Active: x.GetActive(),
	}
}

// directiveToProto converts a Directive.
func directiveToProto(x domain.Directive) *pb.Directive {
	return &pb.Directive{Id: string(x.ID), Name: x.Name, Rules: x.Rules, Tags: x.Tags, Active: x.Active}
}

// directivesFromProto converts repeated Directives; nil stays nil.
func directivesFromProto(in []*pb.Directive) []domain.Directive {
	if in == nil {
		return nil
	}
	out := make([]domain.Directive, len(in))
	for i, x := range in {
		out[i] = directiveFromProto(x)
	}
	return out
}

// directivesToProto converts directives.
func directivesToProto(in []domain.Directive) []*pb.Directive {
	if in == nil {
		return nil
	}
	out := make([]*pb.Directive, len(in))
	for i := range in {
		out[i] = directiveToProto(in[i])
	}
	return out
}

// usageToProto converts a Usage.
func usageToProto(u domain.Usage) *pb.SlotUsage {
	return &pb.SlotUsage{
		Slot: toEnum[pb.SlotUsage_ModelSlot](u.Slot), Model: u.Model, InputTokens: u.InputTokens,
		OutputTokens: u.OutputTokens,
	}
}

// usagesToProto converts usages; nil stays nil.
func usagesToProto(in []domain.Usage) []*pb.SlotUsage {
	if in == nil {
		return nil
	}
	out := make([]*pb.SlotUsage, len(in))
	for i := range in {
		out[i] = usageToProto(in[i])
	}
	return out
}

// entityHintsFromProto converts repeated EntityHints; nil stays nil.
func entityHintsFromProto(in []*pb.EntityHint) []domain.EntityHint {
	if in == nil {
		return nil
	}
	out := make([]domain.EntityHint, len(in))
	for i, h := range in {
		out[i] = domain.EntityHint{Text: h.GetText(), Type: h.GetType()}
	}
	return out
}

// ---------- traces ----------

// armTraceToProto converts an ArmTrace.
func armTraceToProto(a recall.ArmTrace) *pb.ArmTrace {
	return &pb.ArmTrace{
		Arm: a.Arm, Candidates: i32(a.Candidates), DurationMs: a.DurationMS, TopIds: strs[string](a.TopIDs),
	}
}

// recallTraceToProto converts a RecallTrace; nil = nil.
func recallTraceToProto(t *recall.Trace) *pb.RecallTrace {
	if t == nil {
		return nil
	}
	out := &pb.RecallTrace{
		Fused: i32(t.Fused), Reranked: i32(t.Reranked), Packed: i32(t.Packed), TokenBudget: i32(t.TokenBudget),
		TokensUsed: i32(t.TokensUsed), DurationMs: t.DurationMS,
	}
	if t.Arms != nil {
		out.Arms = make([]*pb.ArmTrace, len(t.Arms))
		for i := range t.Arms {
			out.Arms[i] = armTraceToProto(t.Arms[i])
		}
	}
	if t.Window != nil {
		out.TimeWindowStart = tsToProto(t.Window.Start)
		out.TimeWindowEnd = tsToProto(t.Window.End)
	}
	return out
}

// toolCallToProto converts a ToolCallTrace.
func toolCallToProto(c domain.ToolCallTrace) *pb.ToolCallTrace {
	return &pb.ToolCallTrace{
		Iteration: i32(c.Iteration), Tool: c.Tool, ArgumentsJson: c.ArgumentsJSON, OutputJson: c.OutputJSON,
		Error: c.Error, DurationMs: c.DurationMS,
	}
}

// toolCallsToProto converts tool-call traces; nil stays nil.
func toolCallsToProto(in []domain.ToolCallTrace) []*pb.ToolCallTrace {
	if in == nil {
		return nil
	}
	out := make([]*pb.ToolCallTrace, len(in))
	for i := range in {
		out[i] = toolCallToProto(in[i])
	}
	return out
}

// reflectTraceToProto converts a ReflectTrace; nil = nil.
func reflectTraceToProto(t *reflecteng.Trace) *pb.ReflectTrace {
	if t == nil {
		return nil
	}
	return &pb.ReflectTrace{
		ToolCalls: toolCallsToProto(t.ToolCalls), Iterations: i32(t.Iterations), StopReason: string(t.StopReason),
		DirectiveIds: strs[string](t.DirectiveIDs), DurationMs: t.DurationMS,
	}
}

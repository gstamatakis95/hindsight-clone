package server

import (
	"memd/internal/domain"
	"memd/internal/engine/recall"
	reflecteng "memd/internal/engine/reflect"

	pb "memd/gen/memory/v1"
)

// ---------- tags and scopes ----------

// tagSetFromProto converts a TagSet; nil = nil.
func tagSetFromProto(s *pb.TagSet) *domain.TagSet {
	if s == nil {
		return nil
	}
	return &domain.TagSet{Tags: s.GetTags()}
}

// tagSetToProto converts a TagSet; nil = nil.
func tagSetToProto(s *domain.TagSet) *pb.TagSet {
	if s == nil {
		return nil
	}
	return &pb.TagSet{Tags: s.Tags}
}

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

// scopesFromProto converts ObservationScopes; nil = nil.
func (d *decoder) scopes(s *pb.ObservationScopes) *domain.ObservationScopes {
	if s == nil {
		return nil
	}
	return &domain.ObservationScopes{
		Mode:            fromEnum[domain.ScopeMode](d, s.GetMode()),
		Custom:          tagSetsFromProto(s.GetCustom()),
		MaxCombinations: optInt(s.MaxCombinations),
	}
}

// scopesToProto converts ObservationScopes; nil = nil.
func scopesToProto(s *domain.ObservationScopes) *pb.ObservationScopes {
	if s == nil {
		return nil
	}
	return &pb.ObservationScopes{
		Mode:            toEnum[pb.ObservationScopes_Mode](s.Mode),
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

// tagFilter builds a request's TagFilter from its flat tags, match mode and tag groups.
func (d *decoder) tagFilter(tags []string, match pb.TagsMatch, groups []*pb.TagGroup) domain.TagFilter {
	return domain.TagFilter{Tags: tags, Match: fromEnum[domain.TagsMatch](d, match), Groups: d.tagGroups(groups)}
}

// ---------- scores and memories ----------

// minScores converts MinScores; nil = no floors.
func minScoresFromProto(m *pb.MinScores) domain.MinScores {
	if m == nil {
		return domain.MinScores{}
	}
	return domain.MinScores{
		Semantic: optCopy(m.Semantic), Keyword: optCopy(m.Keyword),
		Reranker: optCopy(m.Reranker), Final: optCopy(m.Final),
	}
}

// minScoresToProto converts MinScores; no floors = nil.
func minScoresToProto(m domain.MinScores) *pb.MinScores {
	if m == (domain.MinScores{}) {
		return nil
	}
	return &pb.MinScores{
		Semantic: optCopy(m.Semantic), Keyword: optCopy(m.Keyword),
		Reranker: optCopy(m.Reranker), Final: optCopy(m.Final),
	}
}

// scoresFromProto converts Scores; nil = zero scores.
func scoresFromProto(s *pb.Scores) recall.Scores {
	if s == nil {
		return recall.Scores{}
	}
	return recall.Scores{
		Final: s.GetFinal(), Reranker: optCopy(s.Reranker),
		Semantic: optCopy(s.Semantic), Keyword: optCopy(s.Keyword),
	}
}

// scoresToProto converts Scores.
func scoresToProto(s recall.Scores) *pb.Scores {
	return &pb.Scores{
		Final: s.Final, Reranker: optCopy(s.Reranker),
		Semantic: optCopy(s.Semantic), Keyword: optCopy(s.Keyword),
	}
}

// unit converts a Memory to a domain unit (scores and stale are dropped; see memory).
func (d *decoder) unit(m *pb.Memory) domain.Unit {
	return domain.Unit{
		ID:            domain.UnitID(m.GetId()),
		Type:          fromEnum[domain.FactType](d, m.GetType()),
		Text:          m.GetText(),
		Context:       m.GetContext(),
		Metadata:      m.GetMetadata(),
		Tags:          m.GetTags(),
		Entities:      m.GetEntities(),
		OccurredStart: d.tsPtr(m.GetOccurredStart()),
		OccurredEnd:   d.tsPtr(m.GetOccurredEnd()),
		MentionedAt:   d.tsPtr(m.GetMentionedAt()),
		DocumentID:    domain.DocumentID(m.GetDocumentId()),
		ChunkID:       domain.ChunkID(m.GetChunkId()),
		ProofCount:    int(m.GetProofCount()),
		SourceIDs:     strs[domain.UnitID](m.GetSourceFactIds()),
		State:         fromEnum[domain.MemoryState](d, m.GetState()),
		EditedAt:      d.tsPtr(m.GetEditedAt()),
	}
}

// unitToProto converts a domain unit to a Memory without scores.
func unitToProto(u domain.Unit) *pb.Memory {
	return &pb.Memory{
		Id:            string(u.ID),
		Type:          toEnum[pb.FactType](u.Type),
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

// unitsFromProto converts Memories to units; nil stays nil.
func (d *decoder) units(in []*pb.Memory) []domain.Unit {
	if in == nil {
		return nil
	}
	out := make([]domain.Unit, len(in))
	for i, m := range in {
		out[i] = d.unit(m)
	}
	return out
}

// memory converts a scored Memory (a recall result).
func (d *decoder) memory(m *pb.Memory) recall.Memory {
	return recall.Memory{Unit: d.unit(m), Scores: scoresFromProto(m.GetScores()), Stale: m.GetStale()}
}

// memoryToProto converts a recall result to a scored Memory.
func memoryToProto(m recall.Memory) *pb.Memory {
	out := unitToProto(m.Unit)
	out.Scores = scoresToProto(m.Scores)
	out.Stale = m.Stale
	return out
}

// chunkFromProto converts a Chunk.
func chunkFromProto(c *pb.Chunk) recall.Chunk {
	return recall.Chunk{
		ID: domain.ChunkID(c.GetId()), Text: c.GetText(), Index: int(c.GetChunkIndex()), Truncated: c.GetTruncated(),
	}
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

// usage converts a SlotUsage.
func (d *decoder) usage(u *pb.SlotUsage) domain.Usage {
	return domain.Usage{
		Slot: fromEnum[domain.ModelSlot](d, u.GetSlot()), Model: u.GetModel(),
		InputTokens: u.GetInputTokens(), OutputTokens: u.GetOutputTokens(),
	}
}

// usages converts repeated SlotUsage; nil stays nil.
func (d *decoder) usages(in []*pb.SlotUsage) []domain.Usage {
	if in == nil {
		return nil
	}
	out := make([]domain.Usage, len(in))
	for i, u := range in {
		out[i] = d.usage(u)
	}
	return out
}

// usageToProto converts a Usage.
func usageToProto(u domain.Usage) *pb.SlotUsage {
	return &pb.SlotUsage{
		Slot: toEnum[pb.ModelSlot](u.Slot), Model: u.Model, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
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

// entityHintsToProto converts entity hints; nil stays nil.
func entityHintsToProto(in []domain.EntityHint) []*pb.EntityHint {
	if in == nil {
		return nil
	}
	out := make([]*pb.EntityHint, len(in))
	for i, h := range in {
		out[i] = &pb.EntityHint{Text: h.Text, Type: h.Type}
	}
	return out
}

// ---------- traces ----------

// armTraceFromProto converts an ArmTrace.
func armTraceFromProto(a *pb.ArmTrace) recall.ArmTrace {
	return recall.ArmTrace{
		Arm: a.GetArm(), Candidates: int(a.GetCandidates()), DurationMS: a.GetDurationMs(),
		TopIDs: strs[domain.UnitID](a.GetTopIds()),
	}
}

// armTraceToProto converts an ArmTrace.
func armTraceToProto(a recall.ArmTrace) *pb.ArmTrace {
	return &pb.ArmTrace{
		Arm: a.Arm, Candidates: i32(a.Candidates), DurationMs: a.DurationMS, TopIds: strs[string](a.TopIDs),
	}
}

// recallTrace converts a RecallTrace; nil = nil.
func (d *decoder) recallTrace(t *pb.RecallTrace) *recall.RecallTrace {
	if t == nil {
		return nil
	}
	var arms []recall.ArmTrace
	if t.GetArms() != nil {
		arms = make([]recall.ArmTrace, len(t.GetArms()))
		for i, a := range t.GetArms() {
			arms[i] = armTraceFromProto(a)
		}
	}
	var window *domain.TimeWindow
	if t.GetTimeWindowStart() != nil || t.GetTimeWindowEnd() != nil {
		window = &domain.TimeWindow{Start: d.ts(t.GetTimeWindowStart()), End: d.ts(t.GetTimeWindowEnd())}
	}
	return &recall.RecallTrace{
		Arms: arms, Fused: int(t.GetFused()), Reranked: int(t.GetReranked()), Packed: int(t.GetPacked()),
		TokenBudget: int(t.GetTokenBudget()), TokensUsed: int(t.GetTokensUsed()), Window: window,
		DurationMS: t.GetDurationMs(),
	}
}

// recallTraceToProto converts a RecallTrace; nil = nil.
func recallTraceToProto(t *recall.RecallTrace) *pb.RecallTrace {
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

// toolCallFromProto converts a ToolCallTrace.
func toolCallFromProto(c *pb.ToolCallTrace) domain.ToolCallTrace {
	return domain.ToolCallTrace{
		Iteration: int(c.GetIteration()), Tool: c.GetTool(), ArgumentsJSON: c.GetArgumentsJson(),
		OutputJSON: c.GetOutputJson(), Error: c.GetError(), DurationMS: c.GetDurationMs(),
	}
}

// toolCallToProto converts a ToolCallTrace.
func toolCallToProto(c domain.ToolCallTrace) *pb.ToolCallTrace {
	return &pb.ToolCallTrace{
		Iteration: i32(c.Iteration), Tool: c.Tool, ArgumentsJson: c.ArgumentsJSON, OutputJson: c.OutputJSON,
		Error: c.Error, DurationMs: c.DurationMS,
	}
}

// toolCallsFromProto converts repeated ToolCallTraces; nil stays nil.
func toolCallsFromProto(in []*pb.ToolCallTrace) []domain.ToolCallTrace {
	if in == nil {
		return nil
	}
	out := make([]domain.ToolCallTrace, len(in))
	for i, c := range in {
		out[i] = toolCallFromProto(c)
	}
	return out
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

// reflectTraceFromProto converts a ReflectTrace; nil = nil.
func reflectTraceFromProto(t *pb.ReflectTrace) *reflecteng.ReflectTrace {
	if t == nil {
		return nil
	}
	return &reflecteng.ReflectTrace{
		ToolCalls: toolCallsFromProto(t.GetToolCalls()), Iterations: int(t.GetIterations()),
		StopReason: reflecteng.StopReason(t.GetStopReason()), DirectiveIDs: strs[domain.DirectiveID](t.GetDirectiveIds()),
		DurationMS: t.GetDurationMs(),
	}
}

// reflectTraceToProto converts a ReflectTrace; nil = nil.
func reflectTraceToProto(t *reflecteng.ReflectTrace) *pb.ReflectTrace {
	if t == nil {
		return nil
	}
	return &pb.ReflectTrace{
		ToolCalls: toolCallsToProto(t.ToolCalls), Iterations: i32(t.Iterations), StopReason: string(t.StopReason),
		DirectiveIds: strs[string](t.DirectiveIDs), DurationMs: t.DurationMS,
	}
}

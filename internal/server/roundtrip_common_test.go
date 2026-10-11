package server

import (
	pb "memd/gen/memory/v1"
)

// Round trips for common.proto, models.proto, config.proto and the mental-model spec.
func init() {
	addRT(func(m *pb.TagSet) (*pb.TagSet, error) { return tagSetToProto(tagSetFromProto(m)), nil }, nil)
	addRT(func(m *pb.ObservationScopes) (*pb.ObservationScopes, error) {
		var d decoder
		return scopesToProto(d.scopes(m)), d.err
	}, nil)
	addRT(func(m *pb.TagGroup) (*pb.TagGroup, error) {
		var d decoder
		return tagGroupToProto(d.tagGroup(m)), d.err
	}, nil)
	addRT(func(m *pb.TagGroupList) (*pb.TagGroupList, error) {
		var d decoder
		return &pb.TagGroupList{Groups: tagGroupsToProto(d.tagGroups(m.GetGroups()))}, d.err
	}, nil)
	addRT(func(m *pb.TagLeaf) (*pb.TagLeaf, error) {
		var d decoder
		return tagLeafToProto(d.tagLeaf(m)), d.err
	}, nil)
	addRT(func(m *pb.MinScores) (*pb.MinScores, error) { return minScoresToProto(minScoresFromProto(m)), nil }, nil)
	addRT(func(m *pb.Scores) (*pb.Scores, error) { return scoresToProto(scoresFromProto(m)), nil }, nil)
	addRT(func(m *pb.Memory) (*pb.Memory, error) {
		var d decoder
		return memoryToProto(d.memory(m)), d.err
	}, func(m *pb.Memory) {
		if m.Scores == nil {
			m.Scores = &pb.Scores{}
		}
	})
	addRT(func(m *pb.Chunk) (*pb.Chunk, error) { return chunkToProto(chunkFromProto(m)), nil }, nil)
	addRT(func(m *pb.Directive) (*pb.Directive, error) { return directiveToProto(directiveFromProto(m)), nil }, nil)
	addRT(func(m *pb.SlotUsage) (*pb.SlotUsage, error) {
		var d decoder
		return usageToProto(d.usage(m)), d.err
	}, nil)
	addRT(func(m *pb.ArmTrace) (*pb.ArmTrace, error) { return armTraceToProto(armTraceFromProto(m)), nil }, nil)
	addRT(func(m *pb.RecallTrace) (*pb.RecallTrace, error) {
		var d decoder
		return recallTraceToProto(d.recallTrace(m)), d.err
	}, nil)
	addRT(func(m *pb.ToolCallTrace) (*pb.ToolCallTrace, error) {
		return toolCallToProto(toolCallFromProto(m)), nil
	}, nil)
	addRT(func(m *pb.ReflectTrace) (*pb.ReflectTrace, error) {
		return reflectTraceToProto(reflectTraceFromProto(m)), nil
	}, nil)
	addRT(func(m *pb.ModelBinding) (*pb.ModelBinding, error) {
		return bindingPtrToProto(bindingPtrFromProto(m)), nil
	}, nil)
	addRT(func(m *pb.ModelBindings) (*pb.ModelBindings, error) {
		return modelBindingsToProto(modelBindingsFromProto(m)), nil
	}, nil)
	overrideRoundTrips()
	addRT(func(m *pb.BankConfig) (*pb.BankConfig, error) {
		var d decoder
		return configValueToProto(d.config(m)), d.err
	}, nil)
	addRT(func(m *pb.Disposition) (*pb.Disposition, error) {
		return dispositionToProto(dispositionFromProto(m)), nil
	}, nil)
	addRT(func(m *pb.EntityLabel) (*pb.EntityLabel, error) {
		var d decoder
		return labelToProto(d.label(m)), d.err
	}, nil)
	addRT(func(m *pb.LabelValue) (*pb.LabelValue, error) {
		var d decoder
		l := d.label(&pb.EntityLabel{Values: []*pb.LabelValue{m}})
		return labelToProto(l).GetValues()[0], d.err
	}, nil)
	addRT(func(m *pb.ScopePattern) (*pb.ScopePattern, error) {
		var d decoder
		s := d.strategy(&pb.ConsolidationStrategy{Scopes: []*pb.ScopePattern{m}})
		return strategyToProto(s).GetScopes()[0], d.err
	}, nil)
	addRT(func(m *pb.ConsolidationStrategy) (*pb.ConsolidationStrategy, error) {
		var d decoder
		return strategyToProto(d.strategy(m)), d.err
	}, nil)
	addRT(func(m *pb.RetainConfig) (*pb.RetainConfig, error) {
		var d decoder
		return retainConfigToProto(d.retainConfig(m)), d.err
	}, nil)
	addRT(func(m *pb.ObservationConfig) (*pb.ObservationConfig, error) {
		var d decoder
		return observationConfigToProto(d.observationConfig(m)), d.err
	}, nil)
	addRT(func(m *pb.RecallConfig) (*pb.RecallConfig, error) {
		var d decoder
		return recallConfigToProto(d.recallConfig(m)), d.err
	}, nil)
	addRT(func(m *pb.ReflectConfig) (*pb.ReflectConfig, error) {
		var d decoder
		return reflectConfigToProto(d.reflectConfig(m)), d.err
	}, nil)
	addRT(func(m *pb.MentalModelConfig) (*pb.MentalModelConfig, error) {
		var d decoder
		return mentalModelConfigToProto(d.mentalModelConfig(m)), d.err
	}, nil)
	addRT(func(m *pb.MentalModelSpec) (*pb.MentalModelSpec, error) {
		var d decoder
		return specToProto(d.spec(m)), d.err
	}, nil)
	addRT(func(m *pb.RefreshTrigger) (*pb.RefreshTrigger, error) {
		var d decoder
		return triggerToProto(d.trigger(m)), d.err
	}, nil)
}

// overrideRoundTrips registers the per-RPC override messages: each converts to its config level and back.
func overrideRoundTrips() {
	addRT(func(m *pb.RetainOverrides) (*pb.RetainOverrides, error) {
		var d decoder
		return retainOverridesToProto(d.retainOverrides(m)), d.err
	}, nil)
	addRT(func(m *pb.RecallOverrides) (*pb.RecallOverrides, error) {
		var d decoder
		return recallOverridesToProto(d.recallOverrides(m)), d.err
	}, nil)
	addRT(func(m *pb.ReflectOverrides) (*pb.ReflectOverrides, error) {
		var d decoder
		return reflectOverridesToProto(d.reflectOverrides(m)), d.err
	}, nil)
	addRT(func(m *pb.ConsolidateOverrides) (*pb.ConsolidateOverrides, error) {
		var d decoder
		return consolidateOverridesToProto(d.consolidateOverrides(m)), d.err
	}, nil)
	addRT(func(m *pb.RederiveOverrides) (*pb.RederiveOverrides, error) {
		return rederiveOverridesToProto(rederiveOverrides(m)), nil
	}, nil)
	addRT(func(m *pb.RefreshOverrides) (*pb.RefreshOverrides, error) {
		var d decoder
		return refreshOverridesToProto(d.refreshOverrides(m)), d.err
	}, nil)
}

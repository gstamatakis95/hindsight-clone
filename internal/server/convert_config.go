package server

import (
	"memd/internal/domain"

	pb "memd/gen/memory/v1"
)

// configFromProto converts a request's BankConfig overrides; nil = nil (no overrides).
func configFromProto(c *pb.BankConfig) (*domain.BankConfig, error) {
	var d decoder
	out := d.configPtr(c)
	return out, d.err
}

// configPtr converts a BankConfig; nil = nil.
func (d *decoder) configPtr(c *pb.BankConfig) *domain.BankConfig {
	if c == nil {
		return nil
	}
	v := d.config(c)
	return &v
}

// config converts a BankConfig; nil = the zero config.
func (d *decoder) config(c *pb.BankConfig) domain.BankConfig {
	if c == nil {
		return domain.BankConfig{}
	}
	return domain.BankConfig{
		RetainMission:                     c.GetRetainMission(),
		ObservationsMission:               c.GetObservationsMission(),
		ReflectMission:                    c.GetReflectMission(),
		Disposition:                       dispositionFromProto(c.GetDisposition()),
		ExtractionMode:                    fromEnum[domain.ExtractionMode](d, c.GetExtractionMode()),
		CustomExtractionPrompt:            c.GetCustomExtractionPrompt(),
		EntityLabels:                      d.labels(c.GetEntityLabels()),
		AutoConsolidation:                 optCopy(c.AutoConsolidation),
		DedupThreshold:                    optCopy(c.DedupThreshold),
		MaxObservationsPerScope:           optInt(c.MaxObservationsPerScope),
		ConsolidationSourceFactsMaxTokens: optInt(c.ConsolidationSourceFactsMaxTokens),
		ConsolidationStrategies:           d.strategies(c.GetConsolidationStrategies()),
		Recall:                            d.recallConfig(c.GetRecall()),
		Models:                            d.slotBindings(c.GetModels()),
		Retain:                            retainTuningFromProto(c.GetRetain()),
		Reflect:                           d.reflectConfig(c.GetReflect()),
		MentalModels:                      d.mentalModelConfig(c.GetMentalModels()),
		DefaultObservationScopes:          d.scopes(c.GetDefaultObservationScopes()),
		EnableObservations:                optCopy(c.EnableObservations),
		ConsolidationLLMBatchSize:         optInt(c.ConsolidationLlmBatchSize),
		ConsolidationSourceTokensPerObs:   optInt(c.ConsolidationSourceFactsMaxTokensPerObservation),
		EntitiesAllowFreeForm:             optCopy(c.EntitiesAllowFreeForm),
		ConsolidationMaxUnitsPerRun:       optInt(c.ConsolidationMaxUnitsPerRun),
	}
}

// configToProto converts a BankConfig pointer; nil = nil.
func configToProto(c *domain.BankConfig) *pb.BankConfig {
	if c == nil {
		return nil
	}
	return configValueToProto(*c)
}

// configValueToProto converts a BankConfig; the zero config is an empty (non-nil) message.
func configValueToProto(c domain.BankConfig) *pb.BankConfig {
	return &pb.BankConfig{
		RetainMission:                     c.RetainMission,
		ObservationsMission:               c.ObservationsMission,
		ReflectMission:                    c.ReflectMission,
		Disposition:                       dispositionToProto(c.Disposition),
		ExtractionMode:                    toEnum[pb.ExtractionMode](c.ExtractionMode),
		CustomExtractionPrompt:            c.CustomExtractionPrompt,
		EntityLabels:                      labelsToProto(c.EntityLabels),
		AutoConsolidation:                 optCopy(c.AutoConsolidation),
		DedupThreshold:                    optCopy(c.DedupThreshold),
		MaxObservationsPerScope:           optInt32(c.MaxObservationsPerScope),
		ConsolidationSourceFactsMaxTokens: optInt32(c.ConsolidationSourceFactsMaxTokens),
		ConsolidationStrategies:           strategiesToProto(c.ConsolidationStrategies),
		Recall:                            recallConfigToProto(c.Recall),
		Models:                            slotBindingsToProto(c.Models),
		Retain:                            retainTuningToProto(c.Retain),
		Reflect:                           reflectConfigToProto(c.Reflect),
		MentalModels:                      mentalModelConfigToProto(c.MentalModels),
		DefaultObservationScopes:          scopesToProto(c.DefaultObservationScopes),
		EnableObservations:                optCopy(c.EnableObservations),
		ConsolidationLlmBatchSize:         optInt32(c.ConsolidationLLMBatchSize),
		ConsolidationSourceFactsMaxTokensPerObservation: optInt32(c.ConsolidationSourceTokensPerObs),
		EntitiesAllowFreeForm:       optCopy(c.EntitiesAllowFreeForm),
		ConsolidationMaxUnitsPerRun: optInt32(c.ConsolidationMaxUnitsPerRun),
	}
}

// dispositionFromProto converts a Disposition; nil = nil.
func dispositionFromProto(x *pb.Disposition) *domain.Disposition {
	if x == nil {
		return nil
	}
	return &domain.Disposition{
		Skepticism: int(x.GetSkepticism()), Literalism: int(x.GetLiteralism()), Empathy: int(x.GetEmpathy()),
	}
}

// dispositionToProto converts a Disposition; nil = nil.
func dispositionToProto(x *domain.Disposition) *pb.Disposition {
	if x == nil {
		return nil
	}
	return &pb.Disposition{Skepticism: i32(x.Skepticism), Literalism: i32(x.Literalism), Empathy: i32(x.Empathy)}
}

// label converts an EntityLabel, recursing into map fields.
func (d *decoder) label(l *pb.EntityLabel) domain.EntityLabel {
	var fields map[string]domain.EntityLabel
	if l.GetFields() != nil {
		fields = make(map[string]domain.EntityLabel, len(l.GetFields()))
		for k, f := range l.GetFields() {
			fields[k] = d.label(f)
		}
	}
	var values []domain.LabelValue
	if l.GetValues() != nil {
		values = make([]domain.LabelValue, len(l.GetValues()))
		for i, v := range l.GetValues() {
			values[i] = domain.LabelValue{Value: v.GetValue(), Description: v.GetDescription()}
		}
	}
	return domain.EntityLabel{
		Key: l.GetKey(), Description: l.GetDescription(), Type: fromEnum[domain.LabelType](d, l.GetType()),
		Values: values, Fields: fields, Optional: optCopy(l.Optional), WriteToTags: l.GetWriteToTags(),
	}
}

// labels converts repeated EntityLabels; nil stays nil.
func (d *decoder) labels(in []*pb.EntityLabel) []domain.EntityLabel {
	if in == nil {
		return nil
	}
	out := make([]domain.EntityLabel, len(in))
	for i, l := range in {
		out[i] = d.label(l)
	}
	return out
}

// labelToProto converts an EntityLabel, recursing into map fields.
func labelToProto(l domain.EntityLabel) *pb.EntityLabel {
	var fields map[string]*pb.EntityLabel
	if l.Fields != nil {
		fields = make(map[string]*pb.EntityLabel, len(l.Fields))
		for k, f := range l.Fields {
			fields[k] = labelToProto(f)
		}
	}
	var values []*pb.LabelValue
	if l.Values != nil {
		values = make([]*pb.LabelValue, len(l.Values))
		for i, v := range l.Values {
			values[i] = &pb.LabelValue{Value: v.Value, Description: v.Description}
		}
	}
	return &pb.EntityLabel{
		Key: l.Key, Description: l.Description, Type: toEnum[pb.EntityLabel_Type](l.Type),
		Values: values, Fields: fields, Optional: optCopy(l.Optional), WriteToTags: l.WriteToTags,
	}
}

// labelsToProto converts entity labels; nil stays nil.
func labelsToProto(in []domain.EntityLabel) []*pb.EntityLabel {
	if in == nil {
		return nil
	}
	out := make([]*pb.EntityLabel, len(in))
	for i := range in {
		out[i] = labelToProto(in[i])
	}
	return out
}

// strategy converts a ConsolidationStrategy.
func (d *decoder) strategy(s *pb.ConsolidationStrategy) domain.ConsolidationStrategy {
	var pats []domain.ScopePattern
	if s.GetScopes() != nil {
		pats = make([]domain.ScopePattern, len(s.GetScopes()))
		for i, p := range s.GetScopes() {
			pats[i] = domain.ScopePattern{Tags: p.GetTags(), TagsMatch: fromEnum[domain.TagsMatch](d, p.GetTagsMatch())}
		}
	}
	return domain.ConsolidationStrategy{
		Name: s.GetName(), Scopes: pats, ObservationsMission: s.GetObservationsMission(),
		MaxObservationsPerScope:            optInt(s.MaxObservationsPerScope),
		SourceFactsMaxTokens:               optInt(s.SourceFactsMaxTokens),
		SourceFactsMaxTokensPerObservation: optInt(s.SourceFactsMaxTokensPerObservation),
		Models:                             d.slotBindings(s.GetModels()),
	}
}

// strategies converts repeated ConsolidationStrategies; nil stays nil.
func (d *decoder) strategies(in []*pb.ConsolidationStrategy) []domain.ConsolidationStrategy {
	if in == nil {
		return nil
	}
	out := make([]domain.ConsolidationStrategy, len(in))
	for i, s := range in {
		out[i] = d.strategy(s)
	}
	return out
}

// strategyToProto converts a ConsolidationStrategy.
func strategyToProto(s domain.ConsolidationStrategy) *pb.ConsolidationStrategy {
	var pats []*pb.ScopePattern
	if s.Scopes != nil {
		pats = make([]*pb.ScopePattern, len(s.Scopes))
		for i, p := range s.Scopes {
			pats[i] = &pb.ScopePattern{Tags: p.Tags, TagsMatch: toEnum[pb.TagsMatch](p.TagsMatch)}
		}
	}
	return &pb.ConsolidationStrategy{
		Name: s.Name, Scopes: pats, ObservationsMission: s.ObservationsMission,
		MaxObservationsPerScope:            optInt32(s.MaxObservationsPerScope),
		SourceFactsMaxTokens:               optInt32(s.SourceFactsMaxTokens),
		SourceFactsMaxTokensPerObservation: optInt32(s.SourceFactsMaxTokensPerObservation),
		Models:                             slotBindingsToProto(s.Models),
	}
}

// strategiesToProto converts consolidation strategies; nil stays nil.
func strategiesToProto(in []domain.ConsolidationStrategy) []*pb.ConsolidationStrategy {
	if in == nil {
		return nil
	}
	out := make([]*pb.ConsolidationStrategy, len(in))
	for i := range in {
		out[i] = strategyToProto(in[i])
	}
	return out
}

// retainTuningFromProto converts RetainTuning; nil = nil.
func retainTuningFromProto(t *pb.RetainTuning) *domain.RetainTuning {
	if t == nil {
		return nil
	}
	return &domain.RetainTuning{
		ChunkMaxTokens:            optInt(t.ChunkMaxTokens),
		ChunkOverlapTokens:        optInt(t.ChunkOverlapTokens),
		EntityMatchThreshold:      optCopy(t.EntityMatchThreshold),
		TemporalLinkWindowHours:   optInt(t.TemporalLinkWindowHours),
		SemanticLinkK:             optInt(t.SemanticLinkK),
		SemanticLinkMinSimilarity: optCopy(t.SemanticLinkMinSimilarity),
		ExtractCausalLinks:        optCopy(t.ExtractCausalLinks),
		StructuredChunkMaxTokens:  optInt(t.StructuredChunkMaxTokens),
		TemporalLinkCap:           optInt(t.TemporalLinkCap),
	}
}

// retainTuningToProto converts RetainTuning; nil = nil.
func retainTuningToProto(t *domain.RetainTuning) *pb.RetainTuning {
	if t == nil {
		return nil
	}
	return &pb.RetainTuning{
		ChunkMaxTokens:            optInt32(t.ChunkMaxTokens),
		ChunkOverlapTokens:        optInt32(t.ChunkOverlapTokens),
		EntityMatchThreshold:      optCopy(t.EntityMatchThreshold),
		TemporalLinkWindowHours:   optInt32(t.TemporalLinkWindowHours),
		SemanticLinkK:             optInt32(t.SemanticLinkK),
		SemanticLinkMinSimilarity: optCopy(t.SemanticLinkMinSimilarity),
		ExtractCausalLinks:        optCopy(t.ExtractCausalLinks),
		StructuredChunkMaxTokens:  optInt32(t.StructuredChunkMaxTokens),
		TemporalLinkCap:           optInt32(t.TemporalLinkCap),
	}
}

// recallConfig converts a RecallConfig; nil = nil.
func (d *decoder) recallConfig(r *pb.RecallConfig) *domain.RecallConfig {
	if r == nil {
		return nil
	}
	return &domain.RecallConfig{
		BudgetFunction:        fromEnum[domain.BudgetFunction](d, r.GetBudgetFunction()),
		BudgetLow:             optInt(r.BudgetLow),
		BudgetMid:             optInt(r.BudgetMid),
		BudgetHigh:            optInt(r.BudgetHigh),
		StrategyBoosts:        r.GetStrategyBoosts(),
		EnabledStrategies:     r.GetEnabledStrategies(),
		RerankerMaxCandidates: optInt(r.RerankerMaxCandidates),
		Rerank:                optCopy(r.Rerank),
		RRFK:                  optInt(r.RrfK),
		RecencyAlpha:          optCopy(r.RecencyAlpha),
		TemporalAlpha:         optCopy(r.TemporalAlpha),
		ProofAlpha:            optCopy(r.ProofAlpha),
		RecencyHorizonDays:    optInt(r.RecencyHorizonDays),
		GraphMaxHops:          optInt(r.GraphMaxHops),
		TemporalBuckets:       optInt(r.TemporalBuckets),
		FuzzyTagMinSimilarity: optCopy(r.FuzzyTagMinSimilarity),
		BudgetMin:             optInt(r.BudgetMin),
		BudgetMax:             optInt(r.BudgetMax),
	}
}

// recallConfigToProto converts a RecallConfig; nil = nil.
func recallConfigToProto(r *domain.RecallConfig) *pb.RecallConfig {
	if r == nil {
		return nil
	}
	return &pb.RecallConfig{
		BudgetFunction:        toEnum[pb.BudgetFunction](r.BudgetFunction),
		BudgetLow:             optInt32(r.BudgetLow),
		BudgetMid:             optInt32(r.BudgetMid),
		BudgetHigh:            optInt32(r.BudgetHigh),
		StrategyBoosts:        r.StrategyBoosts,
		EnabledStrategies:     r.EnabledStrategies,
		RerankerMaxCandidates: optInt32(r.RerankerMaxCandidates),
		Rerank:                optCopy(r.Rerank),
		RrfK:                  optInt32(r.RRFK),
		RecencyAlpha:          optCopy(r.RecencyAlpha),
		TemporalAlpha:         optCopy(r.TemporalAlpha),
		ProofAlpha:            optCopy(r.ProofAlpha),
		RecencyHorizonDays:    optInt32(r.RecencyHorizonDays),
		GraphMaxHops:          optInt32(r.GraphMaxHops),
		TemporalBuckets:       optInt32(r.TemporalBuckets),
		FuzzyTagMinSimilarity: optCopy(r.FuzzyTagMinSimilarity),
		BudgetMin:             optInt32(r.BudgetMin),
		BudgetMax:             optInt32(r.BudgetMax),
	}
}

// reflectConfig converts a ReflectConfig; nil = nil.
func (d *decoder) reflectConfig(r *pb.ReflectConfig) *domain.ReflectConfig {
	if r == nil {
		return nil
	}
	return &domain.ReflectConfig{
		MaxIterations:                     optInt(r.MaxIterations),
		EnabledTools:                      r.GetEnabledTools(),
		RequireEvidence:                   optCopy(r.RequireEvidence),
		RecallBudget:                      fromEnum[domain.Budget](d, r.GetRecallBudget()),
		RecallMaxTokens:                   optInt(r.RecallMaxTokens),
		SearchObservationsMaxTokens:       optInt(r.SearchObservationsMaxTokens),
		SearchObservationsIncludeEntities: optCopy(r.SearchObservationsIncludeEntities),
		MaxAnswerTokens:                   optInt(r.MaxAnswerTokens),
		MaxTotalTokens:                    optCopy(r.MaxTotalTokens),
	}
}

// reflectConfigToProto converts a ReflectConfig; nil = nil.
func reflectConfigToProto(r *domain.ReflectConfig) *pb.ReflectConfig {
	if r == nil {
		return nil
	}
	return &pb.ReflectConfig{
		MaxIterations:                     optInt32(r.MaxIterations),
		EnabledTools:                      r.EnabledTools,
		RequireEvidence:                   optCopy(r.RequireEvidence),
		RecallBudget:                      toEnum[pb.Budget](r.RecallBudget),
		RecallMaxTokens:                   optInt32(r.RecallMaxTokens),
		SearchObservationsMaxTokens:       optInt32(r.SearchObservationsMaxTokens),
		SearchObservationsIncludeEntities: optCopy(r.SearchObservationsIncludeEntities),
		MaxAnswerTokens:                   optInt32(r.MaxAnswerTokens),
		MaxTotalTokens:                    optCopy(r.MaxTotalTokens),
	}
}

// mentalModelConfig converts a MentalModelConfig; nil = nil.
func (d *decoder) mentalModelConfig(m *pb.MentalModelConfig) *domain.MentalModelConfig {
	if m == nil {
		return nil
	}
	return &domain.MentalModelConfig{
		DefaultMode:               fromEnum[domain.RefreshMode](d, m.GetDefaultMode()),
		MaxDeltaRatio:             optCopy(m.MaxDeltaRatio),
		RefreshDebounceSeconds:    optInt(m.RefreshDebounceSeconds),
		MaxContentTokens:          optInt(m.MaxContentTokens),
		MinRefreshIntervalSeconds: optInt(m.MinRefreshIntervalSeconds),
		HistoryMaxEntries:         optInt(m.HistoryMaxEntries),
		DefaultStrategy:           fromEnum[domain.RefreshStrategy](d, m.GetDefaultStrategy()),
	}
}

// mentalModelConfigToProto converts a MentalModelConfig; nil = nil.
func mentalModelConfigToProto(m *domain.MentalModelConfig) *pb.MentalModelConfig {
	if m == nil {
		return nil
	}
	return &pb.MentalModelConfig{
		DefaultMode:               toEnum[pb.RefreshMode](m.DefaultMode),
		MaxDeltaRatio:             optCopy(m.MaxDeltaRatio),
		RefreshDebounceSeconds:    optInt32(m.RefreshDebounceSeconds),
		MaxContentTokens:          optInt32(m.MaxContentTokens),
		MinRefreshIntervalSeconds: optInt32(m.MinRefreshIntervalSeconds),
		HistoryMaxEntries:         optInt32(m.HistoryMaxEntries),
		DefaultStrategy:           toEnum[pb.RefreshStrategy](m.DefaultStrategy),
	}
}

// modelBinding converts a ModelBinding; nil = the zero binding.
func modelBindingFromProto(b *pb.ModelBinding) domain.ModelBinding {
	if b == nil {
		return domain.ModelBinding{}
	}
	return domain.ModelBinding{
		Model: b.GetModel(), Temperature: optCopy(b.Temperature), TopP: optCopy(b.TopP),
		MaxOutputTokens: optInt(b.MaxOutputTokens), ReasoningEffort: b.GetReasoningEffort(),
		TimeoutMS: optInt(b.TimeoutMs), MaxRetries: optInt(b.MaxRetries), Extra: structFromProto(b.GetExtra()),
	}
}

// modelBindingToProto converts a ModelBinding.
func modelBindingToProto(b domain.ModelBinding) *pb.ModelBinding {
	return &pb.ModelBinding{
		Model: b.Model, Temperature: optCopy(b.Temperature), TopP: optCopy(b.TopP),
		MaxOutputTokens: optInt32(b.MaxOutputTokens), ReasoningEffort: b.ReasoningEffort,
		TimeoutMs: optInt32(b.TimeoutMS), MaxRetries: optInt32(b.MaxRetries), Extra: structToProto(b.Extra),
	}
}

// slotBindings converts repeated SlotBindings; nil stays nil.
func (d *decoder) slotBindings(in []*pb.SlotBinding) []domain.SlotBinding {
	if in == nil {
		return nil
	}
	out := make([]domain.SlotBinding, len(in))
	for i, sb := range in {
		out[i] = domain.SlotBinding{
			Slot: fromEnum[domain.ModelSlot](d, sb.GetSlot()), Binding: modelBindingFromProto(sb.GetBinding()),
		}
	}
	return out
}

// slotBindingsToProto converts slot bindings; nil stays nil.
func slotBindingsToProto(in []domain.SlotBinding) []*pb.SlotBinding {
	if in == nil {
		return nil
	}
	out := make([]*pb.SlotBinding, len(in))
	for i := range in {
		out[i] = &pb.SlotBinding{Slot: toEnum[pb.ModelSlot](in[i].Slot), Binding: modelBindingToProto(in[i].Binding)}
	}
	return out
}

package grpcapi

import (
	"memd/internal/domain"

	pb "memd/gen/memory/v1"
)

// BankConfigFromProto converts a whole BankConfig level, such as the defaults block of the server config file; an
// unknown enum value is domain.ErrInvalidArgument.
func BankConfigFromProto(c *pb.BankConfig) (domain.BankConfig, error) {
	var d decoder
	out := d.config(c)
	return out, d.err
}

// config converts a BankConfig; nil = the zero config.
func (d *decoder) config(c *pb.BankConfig) domain.BankConfig {
	if c == nil {
		return domain.BankConfig{}
	}
	return domain.BankConfig{
		Retain:       d.retainConfig(c.GetRetain()),
		Observations: d.observationConfig(c.GetObservations()),
		Recall:       d.recallConfig(c.GetRecall()),
		Reflect:      d.reflectConfig(c.GetReflect()),
		MentalModels: d.mentalModelConfig(c.GetMentalModels()),
		Models:       modelBindingsFromProto(c.GetModels()),
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
		Retain:       retainConfigToProto(c.Retain),
		Observations: observationConfigToProto(c.Observations),
		Recall:       recallConfigToProto(c.Recall),
		Reflect:      reflectConfigToProto(c.Reflect),
		MentalModels: mentalModelConfigToProto(c.MentalModels),
		Models:       modelBindingsToProto(c.Models),
	}
}

// retainConfig converts a RetainConfig; nil = nil.
func (d *decoder) retainConfig(t *pb.RetainConfig) *domain.RetainConfig {
	if t == nil {
		return nil
	}
	return &domain.RetainConfig{
		Mission:                   t.GetMission(),
		ExtractionMode:            fromEnum[domain.ExtractionMode](d, t.GetExtractionMode()),
		CustomExtractionPrompt:    t.GetCustomExtractionPrompt(),
		EntityLabels:              d.labels(t.GetEntityLabels()),
		EntitiesAllowFreeForm:     optCopy(t.EntitiesAllowFreeForm),
		DefaultObservationScopes:  d.scopes(t.GetDefaultObservationScopes()),
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

// retainConfigToProto converts a RetainConfig; nil = nil.
func retainConfigToProto(t *domain.RetainConfig) *pb.RetainConfig {
	if t == nil {
		return nil
	}
	return &pb.RetainConfig{
		Mission:                   t.Mission,
		ExtractionMode:            toEnum[pb.ExtractionMode](t.ExtractionMode),
		CustomExtractionPrompt:    t.CustomExtractionPrompt,
		EntityLabels:              labelsToProto(t.EntityLabels),
		EntitiesAllowFreeForm:     optCopy(t.EntitiesAllowFreeForm),
		DefaultObservationScopes:  scopesToProto(t.DefaultObservationScopes),
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

// observationConfig converts an ObservationConfig; nil = nil.
func (d *decoder) observationConfig(o *pb.ObservationConfig) *domain.ObservationConfig {
	if o == nil {
		return nil
	}
	return &domain.ObservationConfig{
		Enabled:                            optCopy(o.Enabled),
		AutoConsolidation:                  optCopy(o.AutoConsolidation),
		Mission:                            o.GetMission(),
		DedupThreshold:                     optCopy(o.DedupThreshold),
		MaxObservationsPerScope:            optInt(o.MaxObservationsPerScope),
		SourceFactsMaxTokens:               optInt(o.SourceFactsMaxTokens),
		SourceFactsMaxTokensPerObservation: optInt(o.SourceFactsMaxTokensPerObservation),
		LLMBatchSize:                       optInt(o.LlmBatchSize),
		MaxUnitsPerRun:                     optInt(o.MaxUnitsPerRun),
		Strategies:                         d.strategies(o.GetStrategies()),
		ReconcileGraceSeconds:              optInt(o.ReconcileGraceSeconds),
	}
}

// observationConfigToProto converts an ObservationConfig; nil = nil.
func observationConfigToProto(o *domain.ObservationConfig) *pb.ObservationConfig {
	if o == nil {
		return nil
	}
	return &pb.ObservationConfig{
		Enabled:                            optCopy(o.Enabled),
		AutoConsolidation:                  optCopy(o.AutoConsolidation),
		Mission:                            o.Mission,
		DedupThreshold:                     optCopy(o.DedupThreshold),
		MaxObservationsPerScope:            optInt32(o.MaxObservationsPerScope),
		SourceFactsMaxTokens:               optInt32(o.SourceFactsMaxTokens),
		SourceFactsMaxTokensPerObservation: optInt32(o.SourceFactsMaxTokensPerObservation),
		LlmBatchSize:                       optInt32(o.LLMBatchSize),
		MaxUnitsPerRun:                     optInt32(o.MaxUnitsPerRun),
		Strategies:                         strategiesToProto(o.Strategies),
		ReconcileGraceSeconds:              optInt32(o.ReconcileGraceSeconds),
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
	var pats []domain.TagFilter
	if s.GetScopes() != nil {
		pats = make([]domain.TagFilter, len(s.GetScopes()))
		for i, p := range s.GetScopes() {
			pats[i] = d.tagFilter(p)
		}
	}
	return domain.ConsolidationStrategy{
		Name: s.GetName(), Scopes: pats, Mission: s.GetMission(),
		MaxObservationsPerScope:            optInt(s.MaxObservationsPerScope),
		SourceFactsMaxTokens:               optInt(s.SourceFactsMaxTokens),
		SourceFactsMaxTokensPerObservation: optInt(s.SourceFactsMaxTokensPerObservation),
		ConsolidateModel:                   bindingPtrFromProto(s.GetConsolidateModel()),
		DedupCheckModel:                    bindingPtrFromProto(s.GetDedupCheckModel()),
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
	var pats []*pb.TagFilter
	if s.Scopes != nil {
		pats = make([]*pb.TagFilter, len(s.Scopes))
		for i, p := range s.Scopes {
			pats[i] = &pb.TagFilter{Tags: p.Tags, Match: toEnum[pb.TagsMatch](p.Match), Groups: tagGroupsToProto(p.Groups)}
		}
	}
	return &pb.ConsolidationStrategy{
		Name: s.Name, Scopes: pats, Mission: s.Mission,
		MaxObservationsPerScope:            optInt32(s.MaxObservationsPerScope),
		SourceFactsMaxTokens:               optInt32(s.SourceFactsMaxTokens),
		SourceFactsMaxTokensPerObservation: optInt32(s.SourceFactsMaxTokensPerObservation),
		ConsolidateModel:                   bindingPtrToProto(s.ConsolidateModel),
		DedupCheckModel:                    bindingPtrToProto(s.DedupCheckModel),
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
		ArmBoosts:             r.GetArmBoosts(),
		Arms:                  r.GetArms(),
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
		ArmBoosts:             r.ArmBoosts,
		Arms:                  r.Arms,
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
		Mission:                           r.GetMission(),
		Disposition:                       dispositionFromProto(r.GetDisposition()),
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
		Mission:                           r.Mission,
		Disposition:                       dispositionToProto(r.Disposition),
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

// bindingPtrFromProto converts a ModelBinding; nil = nil (unset).
func bindingPtrFromProto(b *pb.ModelBinding) *domain.ModelBinding {
	if b == nil {
		return nil
	}
	return &domain.ModelBinding{
		Model: b.GetModel(), Temperature: optCopy(b.Temperature), TopP: optCopy(b.TopP),
		MaxOutputTokens: optInt(b.MaxOutputTokens), ReasoningEffort: b.GetReasoningEffort(),
		TimeoutMS: optInt(b.TimeoutMs), MaxRetries: optInt(b.MaxRetries), Extra: structFromProto(b.GetExtra()),
	}
}

// bindingPtrToProto converts a ModelBinding; nil = nil.
func bindingPtrToProto(b *domain.ModelBinding) *pb.ModelBinding {
	if b == nil {
		return nil
	}
	return &pb.ModelBinding{
		Model: b.Model, Temperature: optCopy(b.Temperature), TopP: optCopy(b.TopP),
		MaxOutputTokens: optInt32(b.MaxOutputTokens), ReasoningEffort: b.ReasoningEffort,
		TimeoutMs: optInt32(b.TimeoutMS), MaxRetries: optInt32(b.MaxRetries), Extra: structToProto(b.Extra),
	}
}

// modelBindingsFromProto converts ModelBindings; nil = nil.
func modelBindingsFromProto(m *pb.ModelBindings) *domain.ModelBindings {
	if m == nil {
		return nil
	}
	return &domain.ModelBindings{
		Default:          bindingPtrFromProto(m.GetDefault()),
		Extract:          bindingPtrFromProto(m.GetExtract()),
		TimeParse:        bindingPtrFromProto(m.GetTimeParse()),
		Consolidate:      bindingPtrFromProto(m.GetConsolidate()),
		DedupCheck:       bindingPtrFromProto(m.GetDedupCheck()),
		Reflect:          bindingPtrFromProto(m.GetReflect()),
		StructuredOutput: bindingPtrFromProto(m.GetStructuredOutput()),
		MentalModel:      bindingPtrFromProto(m.GetMentalModel()),
		Rerank:           bindingPtrFromProto(m.GetRerank()),
		Embed:            bindingPtrFromProto(m.GetEmbed()),
	}
}

// modelBindingsToProto converts ModelBindings; nil = nil.
func modelBindingsToProto(m *domain.ModelBindings) *pb.ModelBindings {
	if m == nil {
		return nil
	}
	return &pb.ModelBindings{
		Default:          bindingPtrToProto(m.Default),
		Extract:          bindingPtrToProto(m.Extract),
		TimeParse:        bindingPtrToProto(m.TimeParse),
		Consolidate:      bindingPtrToProto(m.Consolidate),
		DedupCheck:       bindingPtrToProto(m.DedupCheck),
		Reflect:          bindingPtrToProto(m.Reflect),
		StructuredOutput: bindingPtrToProto(m.StructuredOutput),
		MentalModel:      bindingPtrToProto(m.MentalModel),
		Rerank:           bindingPtrToProto(m.Rerank),
		Embed:            bindingPtrToProto(m.Embed),
	}
}

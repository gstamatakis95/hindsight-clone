package grpcapi

import (
	pb "memd/gen/memory/v1"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/mentalmodel"
	"strings"
)

// The per-RPC override messages. Each holds only what its RPC can change and converts to the BankConfig level
// the engines merge over the stored ones; the *ToProto direction reads such a level back (a document's stored
// retain overrides, a mental model's stored refresh overrides). nil converts to nil (no overrides).

// someBindings returns m, or nil when it binds nothing.
func someBindings(m domain.ModelBindings) *domain.ModelBindings {
	if m == (domain.ModelBindings{}) {
		return nil
	}
	return &m
}

// models returns c's bindings, or the zero bindings when it has none.
func models(c *domain.BankConfig) domain.ModelBindings {
	if c == nil || c.Models == nil {
		return domain.ModelBindings{}
	}
	return *c.Models
}

// retainOverrides converts RetainOverrides.
func (d *decoder) retainOverrides(o *pb.RetainOverrides) *domain.BankConfig {
	if o == nil {
		return nil
	}
	return &domain.BankConfig{
		Retain: d.retainConfig(o.GetRetain()),
		Models: someBindings(domain.ModelBindings{
			Extract: bindingPtrFromProto(o.GetExtractModel()), TimeParse: bindingPtrFromProto(o.GetTimeParseModel()),
		}),
	}
}

// retainOverridesToProto converts a retain overrides level back.
func retainOverridesToProto(c *domain.BankConfig) *pb.RetainOverrides {
	if c == nil {
		return nil
	}
	m := models(c)
	return &pb.RetainOverrides{
		Retain:         retainConfigToProto(c.Retain),
		ExtractModel:   bindingPtrToProto(m.Extract),
		TimeParseModel: bindingPtrToProto(m.TimeParse),
	}
}

// noOverrideArms rejects overrides.recall.arms: a Recall or Reflect picks its arms with the request's own arms field.
func (d *decoder) noOverrideArms(r *pb.RecallConfig) {
	if len(r.GetArms()) > 0 {
		d.fail(domain.InvalidField("overrides.recall.arms",
			"overrides.recall.arms: set the request's arms field instead"))
	}
}

// recallOverrides converts RecallOverrides; recall.arms must be empty.
func (d *decoder) recallOverrides(o *pb.RecallOverrides) *domain.BankConfig {
	if o == nil {
		return nil
	}
	d.noOverrideArms(o.GetRecall())
	return &domain.BankConfig{
		Recall: d.recallConfig(o.GetRecall()),
		Models: someBindings(domain.ModelBindings{
			TimeParse: bindingPtrFromProto(o.GetTimeParseModel()), Rerank: bindingPtrFromProto(o.GetRerankModel()),
		}),
	}
}

// reflectOverrides converts ReflectOverrides; recall.arms must be empty.
func (d *decoder) reflectOverrides(o *pb.ReflectOverrides) *domain.BankConfig {
	if o == nil {
		return nil
	}
	d.noOverrideArms(o.GetRecall())
	return &domain.BankConfig{
		Reflect: d.reflectConfig(o.GetReflect()),
		Recall:  d.recallConfig(o.GetRecall()),
		Models: someBindings(domain.ModelBindings{
			Reflect:          bindingPtrFromProto(o.GetReflectModel()),
			StructuredOutput: bindingPtrFromProto(o.GetStructuredOutputModel()),
			Rerank:           bindingPtrFromProto(o.GetRerankModel()),
		}),
	}
}

// consolidateOverrides converts ConsolidateOverrides.
func (d *decoder) consolidateOverrides(o *pb.ConsolidateOverrides) *domain.BankConfig {
	if o == nil {
		return nil
	}
	return &domain.BankConfig{
		Observations: &domain.ObservationConfig{
			Mission:                            o.GetMission(),
			MaxObservationsPerScope:            optInt(o.MaxObservationsPerScope),
			SourceFactsMaxTokens:               optInt(o.SourceFactsMaxTokens),
			SourceFactsMaxTokensPerObservation: optInt(o.SourceFactsMaxTokensPerObservation),
			LLMBatchSize:                       optInt(o.LlmBatchSize),
			MaxUnitsPerRun:                     optInt(o.MaxUnitsPerRun),
			DedupThreshold:                     optCopy(o.DedupThreshold),
			Strategies:                         d.strategies(o.GetStrategies()),
		},
		Models: someBindings(domain.ModelBindings{
			Consolidate: bindingPtrFromProto(o.GetConsolidateModel()),
			DedupCheck:  bindingPtrFromProto(o.GetDedupCheckModel()),
		}),
	}
}

// rederiveOverrides converts RederiveOverrides.
func rederiveOverrides(o *pb.RederiveOverrides) *domain.BankConfig {
	if o == nil {
		return nil
	}
	c := &domain.BankConfig{Models: someBindings(domain.ModelBindings{
		Extract: bindingPtrFromProto(o.GetExtractModel()), TimeParse: bindingPtrFromProto(o.GetTimeParseModel()),
	})}
	if o.EntityMatchThreshold != nil {
		c.Retain = &domain.RetainConfig{EntityMatchThreshold: optCopy(o.EntityMatchThreshold)}
	}
	return c
}

// refreshOverrides converts RefreshOverrides.
func (d *decoder) refreshOverrides(o *pb.RefreshOverrides) *domain.BankConfig {
	if o == nil {
		return nil
	}
	c := &domain.BankConfig{
		Reflect: d.reflectConfig(o.GetReflect()),
		Models: someBindings(domain.ModelBindings{
			Reflect:          bindingPtrFromProto(o.GetReflectModel()),
			MentalModel:      bindingPtrFromProto(o.GetMentalModelModel()),
			StructuredOutput: bindingPtrFromProto(o.GetStructuredOutputModel()),
		}),
	}
	if o.MaxDeltaRatio != nil {
		c.MentalModels = &domain.MentalModelConfig{MaxDeltaRatio: optCopy(o.MaxDeltaRatio)}
	}
	return c
}

// refreshOverridesToProto converts a refresh overrides level back.
func refreshOverridesToProto(c *domain.BankConfig) *pb.RefreshOverrides {
	if c == nil {
		return nil
	}
	m := models(c)
	out := &pb.RefreshOverrides{
		Reflect:               reflectConfigToProto(c.Reflect),
		ReflectModel:          bindingPtrToProto(m.Reflect),
		MentalModelModel:      bindingPtrToProto(m.MentalModel),
		StructuredOutputModel: bindingPtrToProto(m.StructuredOutput),
	}
	if c.MentalModels != nil {
		out.MaxDeltaRatio = optCopy(c.MentalModels.MaxDeltaRatio)
	}
	return out
}

// refreshPath maps a RefreshOverrides FieldMask path (inside MentalModelSpec.overrides) to the path of the
// domain config it converts to; ok is false for a path with no counterpart.
func refreshPath(p string) (string, bool) {
	switch {
	case p == "reflect" || strings.HasPrefix(p, "reflect."):
		return p, true
	case p == "max_delta_ratio":
		return "mental_models.max_delta_ratio", true
	case p == "reflect_model":
		return "models.reflect", true
	case p == "mental_model_model":
		return "models.mental_model", true
	case p == "structured_output_model":
		return "models.structured_output", true
	default:
		return "", false
	}
}

// validate checks a request's or a stored spec's overrides with config.Validate; nil passes.
func validate(o *domain.BankConfig) error {
	if o == nil {
		return nil
	}
	return config.Validate(*o) //nolint:wrapcheck // config errors are already domain error classes
}

// validatedSpec wraps patch so the patched spec's overrides pass the same check as CreateMentalModel. It
// stays pure: the implementation runs it inside its transaction.
func validatedSpec(patch mentalmodel.SpecPatch) mentalmodel.SpecPatch {
	return func(cur domain.MentalModelSpec) (domain.MentalModelSpec, error) {
		out, err := patch(cur)
		if err != nil {
			return domain.MentalModelSpec{}, err
		}
		if err := validate(out.Overrides); err != nil {
			return domain.MentalModelSpec{}, err
		}
		return out, nil
	}
}

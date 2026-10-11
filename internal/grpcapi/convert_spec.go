package grpcapi

import (
	"memd/internal/domain"

	pb "memd/gen/memory/v1"
)

// spec converts a MentalModelSpec; nil = the zero spec.
func (d *decoder) spec(s *pb.MentalModelSpec) domain.MentalModelSpec {
	if s == nil {
		return domain.MentalModelSpec{}
	}
	return domain.MentalModelSpec{
		ID:          domain.MentalModelID(s.GetId()),
		Name:        s.GetName(),
		SourceQuery: s.GetSourceQuery(),
		FolderPath:  s.GetFolderPath(),
		Tags:        s.GetTags(),
		MaxTokens:   optInt(s.MaxTokens),
		Trigger:     d.trigger(s.GetTrigger()),
		Overrides:   d.refreshOverrides(s.GetOverrides()),
	}
}

// specToProto converts a MentalModelSpec.
func specToProto(s domain.MentalModelSpec) *pb.MentalModelSpec {
	return &pb.MentalModelSpec{
		Id:          string(s.ID),
		Name:        s.Name,
		SourceQuery: s.SourceQuery,
		FolderPath:  s.FolderPath,
		Tags:        s.Tags,
		MaxTokens:   optInt32(s.MaxTokens),
		Trigger:     triggerToProto(s.Trigger),
		Overrides:   refreshOverridesToProto(s.Overrides),
	}
}

// specsFromProto converts repeated MentalModelSpecs; nil stays nil.
func (d *decoder) specs(in []*pb.MentalModelSpec) []domain.MentalModelSpec {
	if in == nil {
		return nil
	}
	out := make([]domain.MentalModelSpec, len(in))
	for i, s := range in {
		out[i] = d.spec(s)
	}
	return out
}

// specsToProto converts mental-model specs; nil stays nil.
func specsToProto(in []domain.MentalModelSpec) []*pb.MentalModelSpec {
	if in == nil {
		return nil
	}
	out := make([]*pb.MentalModelSpec, len(in))
	for i := range in {
		out[i] = specToProto(in[i])
	}
	return out
}

// trigger converts a RefreshTrigger; nil = the zero trigger.
func (d *decoder) trigger(t *pb.RefreshTrigger) domain.RefreshTrigger {
	if t == nil {
		return domain.RefreshTrigger{}
	}
	return domain.RefreshTrigger{
		Mode:                              fromEnum[domain.RefreshMode](d, t.GetMode()),
		Strategy:                          fromEnum[domain.RefreshStrategy](d, t.GetStrategy()),
		RefreshAfterConsolidation:         t.GetRefreshAfterConsolidation(),
		RefreshCron:                       t.GetRefreshCron(),
		MinRefreshIntervalSeconds:         optInt(t.MinRefreshIntervalSeconds),
		Tags:                              d.tagFilter(t.GetTags()),
		FactTypes:                         fromEnums[domain.FactType](d, t.GetFactTypes()),
		ExcludeMentalModels:               t.GetExcludeMentalModels(),
		ExcludeMentalModelIDs:             strs[domain.MentalModelID](t.GetExcludeMentalModelIds()),
		IncludeChunks:                     optCopy(t.IncludeChunks),
		RecallMaxTokens:                   optInt(t.RecallMaxTokens),
		RecallChunksMaxTokens:             optInt(t.RecallChunksMaxTokens),
		SearchObservationsMaxTokens:       optInt(t.SearchObservationsMaxTokens),
		SearchObservationsIncludeEntities: optCopy(t.SearchObservationsIncludeEntities),
		Budget:                            fromEnum[domain.Budget](d, t.GetBudget()),
		ResponseSchemaJSON:                t.GetResponseSchemaJson(),
		KeepTrace:                         t.GetKeepTrace(),
	}
}

// triggerToProto converts a RefreshTrigger.
func triggerToProto(t domain.RefreshTrigger) *pb.RefreshTrigger {
	return &pb.RefreshTrigger{
		Mode:                              toEnum[pb.RefreshMode](t.Mode),
		Strategy:                          toEnum[pb.RefreshStrategy](t.Strategy),
		RefreshAfterConsolidation:         t.RefreshAfterConsolidation,
		RefreshCron:                       t.RefreshCron,
		MinRefreshIntervalSeconds:         optInt32(t.MinRefreshIntervalSeconds),
		Tags:                              tagFilterToProto(t.Tags),
		FactTypes:                         toEnums[pb.FactType](t.FactTypes),
		ExcludeMentalModels:               t.ExcludeMentalModels,
		ExcludeMentalModelIds:             strs[string](t.ExcludeMentalModelIDs),
		IncludeChunks:                     optCopy(t.IncludeChunks),
		RecallMaxTokens:                   optInt32(t.RecallMaxTokens),
		RecallChunksMaxTokens:             optInt32(t.RecallChunksMaxTokens),
		SearchObservationsMaxTokens:       optInt32(t.SearchObservationsMaxTokens),
		SearchObservationsIncludeEntities: optCopy(t.SearchObservationsIncludeEntities),
		Budget:                            toEnum[pb.Budget](t.Budget),
		ResponseSchemaJson:                t.ResponseSchemaJSON,
		KeepTrace:                         t.KeepTrace,
	}
}

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
		ID:                    domain.MentalModelID(s.GetId()),
		Name:                  s.GetName(),
		SourceQuery:           s.GetSourceQuery(),
		FolderPath:            s.GetFolderPath(),
		Tags:                  s.GetTags(),
		Schedule:              schedule(s.GetSchedule()),
		Scope:                 d.scope(s.GetScope()),
		Mode:                  fromEnum[domain.RefreshMode](d, s.GetMode()),
		Strategy:              fromEnum[domain.RefreshStrategy](d, s.GetStrategy()),
		Budget:                fromEnum[domain.Budget](d, s.GetBudget()),
		MaxTokens:             optInt(s.MaxTokens),
		IncludeChunks:         optCopy(s.IncludeChunks),
		RecallChunksMaxTokens: optInt(s.RecallChunksMaxTokens),
		ExcludeMentalModels:   s.GetExcludeMentalModels(),
		ExcludeMentalModelIDs: strs[domain.MentalModelID](s.GetExcludeMentalModelIds()),
		ResponseSchemaJSON:    s.GetResponseSchemaJson(),
		KeepTrace:             s.GetKeepTrace(),
		Overrides:             d.refreshOverrides(s.GetOverrides()),
	}
}

// specToProto converts a MentalModelSpec.
func specToProto(s domain.MentalModelSpec) *pb.MentalModelSpec {
	return &pb.MentalModelSpec{
		Id:                    string(s.ID),
		Name:                  s.Name,
		SourceQuery:           s.SourceQuery,
		FolderPath:            s.FolderPath,
		Tags:                  s.Tags,
		Schedule:              scheduleToProto(s.Schedule),
		Scope:                 scopeToProto(s.Scope),
		Mode:                  toEnum[pb.RefreshMode](s.Mode),
		Strategy:              toEnum[pb.RefreshStrategy](s.Strategy),
		Budget:                toEnum[pb.Budget](s.Budget),
		MaxTokens:             optInt32(s.MaxTokens),
		IncludeChunks:         optCopy(s.IncludeChunks),
		RecallChunksMaxTokens: optInt32(s.RecallChunksMaxTokens),
		ExcludeMentalModels:   s.ExcludeMentalModels,
		ExcludeMentalModelIds: strs[string](s.ExcludeMentalModelIDs),
		ResponseSchemaJson:    s.ResponseSchemaJSON,
		KeepTrace:             s.KeepTrace,
		Overrides:             refreshOverridesToProto(s.Overrides),
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

// schedule converts a RefreshSchedule; nil = the zero schedule (manual refreshes only). The oneof carries at
// most one of after_consolidation and cron.
func schedule(s *pb.RefreshSchedule) domain.RefreshSchedule {
	if s == nil {
		return domain.RefreshSchedule{}
	}
	return domain.RefreshSchedule{
		AfterConsolidation: s.GetAfterConsolidation(),
		Cron:               s.GetCron(),
		MinIntervalSeconds: optInt(s.MinIntervalSeconds),
	}
}

// scheduleToProto converts a RefreshSchedule; the zero schedule is nil. after_consolidation wins the oneof if
// both are set.
func scheduleToProto(s domain.RefreshSchedule) *pb.RefreshSchedule {
	if s == (domain.RefreshSchedule{}) {
		return nil
	}
	out := &pb.RefreshSchedule{MinIntervalSeconds: optInt32(s.MinIntervalSeconds)}
	switch {
	case s.AfterConsolidation:
		out.When = &pb.RefreshSchedule_AfterConsolidation{AfterConsolidation: true}
	case s.Cron != "":
		out.When = &pb.RefreshSchedule_Cron{Cron: s.Cron}
	}
	return out
}

// scope converts a MentalModelScope; nil = the zero scope.
func (d *decoder) scope(s *pb.MentalModelScope) domain.MentalModelScope {
	return domain.MentalModelScope{
		Tags:      d.tagFilter(s.GetTags()),
		FactTypes: fromEnums[domain.FactType](d, s.GetFactTypes()),
	}
}

// scopeToProto converts a MentalModelScope; the zero scope is nil.
func scopeToProto(s domain.MentalModelScope) *pb.MentalModelScope {
	tags := tagFilterToProto(s.Tags)
	if tags == nil && len(s.FactTypes) == 0 {
		return nil
	}
	return &pb.MentalModelScope{Tags: tags, FactTypes: toEnums[pb.FactType](s.FactTypes)}
}

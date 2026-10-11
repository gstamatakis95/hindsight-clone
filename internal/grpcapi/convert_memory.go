package grpcapi

import (
	"memd/internal/domain"
	"memd/internal/engine/recall"
	reflecteng "memd/internal/engine/reflect"
	"memd/internal/engine/retain"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "memd/gen/memory/v1"
)

// Converters for MemoryService (Retain, Recall, Reflect).

// ---------- retain ----------

// retainRequestFromProto converts a RetainRequest; Bank carries only bank_id (the handler sets the tenant).
func retainRequestFromProto(r *pb.RetainRequest) (retain.IngestRequest, error) {
	var d decoder
	out := retain.IngestRequest{
		Bank:              domain.BankRef{Bank: r.GetBankId()},
		Items:             d.retainItems(r.GetItems()),
		DocumentTags:      r.GetDocumentTags(),
		Overrides:         d.retainOverrides(r.GetOverrides()),
		SkipConsolidation: r.GetSkipConsolidation(),
		RunAsync:          r.GetRunAsync(),
	}
	return out, d.err
}

// retainItem converts a RetainItem.
func (d *decoder) retainItem(it *pb.RetainItem) retain.IngestItem {
	return retain.IngestItem{
		Content:           it.GetContent(),
		Context:           it.GetContext(),
		Timestamp:         d.tsPtr(it.GetTimestamp()),
		Timeless:          it.GetTimeless(),
		DocumentID:        domain.DocumentID(it.GetDocumentId()),
		Tags:              it.GetTags(),
		ObservationScopes: d.scopes(it.GetObservationScopes()),
		Metadata:          it.GetMetadata(),
		Entities:          entityHintsFromProto(it.GetEntities()),
		ResolveEntities:   optCopy(it.ResolveEntities),
		Overrides:         d.retainOverrides(it.GetOverrides()),
		ForceReextract:    it.GetForceReextract(),
	}
}

// retainItems converts repeated RetainItems; nil stays nil.
func (d *decoder) retainItems(in []*pb.RetainItem) []retain.IngestItem {
	if in == nil {
		return nil
	}
	out := make([]retain.IngestItem, len(in))
	for i, it := range in {
		out[i] = d.retainItem(it)
	}
	return out
}

// retainedDocToProto converts a RetainedDocument.
func retainedDocToProto(x retain.RetainedDocument) *pb.RetainedDocument {
	return &pb.RetainedDocument{
		DocumentId: string(x.DocumentID), UnitsCreated: i32(x.UnitsCreated), Unchanged: x.Unchanged,
		OperationId: string(x.OperationID),
	}
}

// retainResultToProto converts a retain outcome to a RetainResponse; Usage is the per-slot token usage of a
// synchronous retain (nil when run_async).
func retainResultToProto(r retain.IngestResult) *pb.RetainResponse {
	var docs []*pb.RetainedDocument
	if r.Documents != nil {
		docs = make([]*pb.RetainedDocument, len(r.Documents))
		for i := range r.Documents {
			docs[i] = retainedDocToProto(r.Documents[i])
		}
	}
	return &pb.RetainResponse{
		Documents: docs, ParentOperationId: string(r.ParentOperationID), Usage: usagesToProto(r.Usage),
	}
}

// ---------- recall ----------

// recallRequestFromProto converts a RecallRequest; Bank carries only bank_id (the handler sets the tenant).
func recallRequestFromProto(r *pb.RecallRequest) (recall.RecallRequest, error) {
	var d decoder
	out := recall.RecallRequest{
		Bank:               domain.BankRef{Bank: r.GetBankId()},
		Query:              r.GetQuery(),
		Types:              fromEnums[domain.FactType](&d, r.GetTypes()),
		Tags:               d.tagFilter(r.GetTags(), r.GetTagsMatch(), r.GetTagGroups()),
		Budget:             fromEnum[domain.Budget](&d, r.GetBudget()),
		MaxTokens:          int(r.GetMaxTokens()),
		QueryTime:          d.tsPtr(r.GetQueryTime()),
		Window:             d.window(r.GetTimeWindowStart(), r.GetTimeWindowEnd()),
		PreferObservations: r.GetPreferObservations(),
		MinScores:          minScoresFromProto(r.GetMinScores()),
		Include:            recallIncludeFromProto(r.GetInclude()),
		Trace:              r.GetTrace(),
		Overrides:          d.recallOverrides(r.GetOverrides()),
	}
	return out, d.err
}

// window builds an explicit time window from its bounds; both nil = nil (derive it from the query).
func (d *decoder) window(start, end *timestamppb.Timestamp) *domain.TimeWindow {
	if start == nil && end == nil {
		return nil
	}
	return &domain.TimeWindow{Start: d.ts(start), End: d.ts(end)}
}

// recallIncludeFromProto converts a RecallInclude; nil = nothing attached.
func recallIncludeFromProto(x *pb.RecallInclude) domain.RecallInclude {
	if x == nil {
		return domain.RecallInclude{}
	}
	return domain.RecallInclude{
		Chunks: optCopy(x.Chunks), ChunksMaxTokens: optInt(x.ChunksMaxTokens),
		SourceFacts: optCopy(x.SourceFacts), SourceFactsMaxTokens: optInt(x.SourceFactsMaxTokens),
		Entities: optCopy(x.Entities),
	}
}

// entityStateToProto converts an EntityState.
func entityStateToProto(x domain.EntityState) *pb.EntityState {
	return &pb.EntityState{
		EntityId: string(x.EntityID), CanonicalName: x.CanonicalName, ObservationIds: strs[string](x.ObservationIDs),
	}
}

// recallResultToProto converts a RecallResult to a RecallResponse.
func recallResultToProto(r recall.RecallResult) *pb.RecallResponse {
	out := &pb.RecallResponse{
		Results:              memoriesToProto(r.Results),
		SourceFactsTruncated: r.SourceFactsTruncated,
		Trace:                recallTraceToProto(r.Trace),
	}
	if r.SourceFacts != nil {
		out.SourceFacts = make(map[string]*pb.Memory, len(r.SourceFacts))
		for k, u := range r.SourceFacts {
			out.SourceFacts[string(k)] = unitToProto(u)
		}
	}
	if r.Chunks != nil {
		out.Chunks = make(map[string]*pb.Chunk, len(r.Chunks))
		for k, c := range r.Chunks {
			out.Chunks[string(k)] = chunkToProto(c)
		}
	}
	if r.Entities != nil {
		out.Entities = make(map[string]*pb.EntityState, len(r.Entities))
		for k, e := range r.Entities {
			out.Entities[string(k)] = entityStateToProto(e)
		}
	}
	return out
}

// memoriesToProto converts recall results; nil stays nil.
func memoriesToProto(in []recall.Memory) []*pb.Memory {
	if in == nil {
		return nil
	}
	out := make([]*pb.Memory, len(in))
	for i := range in {
		out[i] = memoryToProto(in[i])
	}
	return out
}

// ---------- reflect ----------

// reflectRequestFromProto converts a ReflectRequest; Bank carries only bank_id (the handler sets the
// tenant). The internal-only fields (Seq, Types, exclusions, RecallInclude, AsDocument) stay zero.
func reflectRequestFromProto(r *pb.ReflectRequest) (reflecteng.ReflectRequest, error) {
	var d decoder
	out := reflecteng.ReflectRequest{
		Bank:               domain.BankRef{Bank: r.GetBankId()},
		Query:              r.GetQuery(),
		Tags:               d.tagFilter(r.GetTags(), r.GetTagsMatch(), r.GetTagGroups()),
		Budget:             fromEnum[domain.Budget](&d, r.GetBudget()),
		MaxTokens:          optInt(r.MaxTokens),
		ResponseSchemaJSON: r.GetResponseSchemaJson(),
		ApplyAllDirectives: r.GetApplyAllDirectives(),
		ExtraDirectives:    directivesFromProto(r.GetExtraDirectives()),
		Include:            reflectIncludeFromProto(r.GetInclude()),
		QueryTime:          d.tsPtr(r.GetQueryTime()),
		Overrides:          d.reflectOverrides(r.GetOverrides()),
	}
	return out, d.err
}

// reflectIncludeFromProto converts a ReflectInclude; nil = the defaults (all nil / false).
func reflectIncludeFromProto(x *pb.ReflectInclude) reflecteng.ReflectInclude {
	if x == nil {
		return reflecteng.ReflectInclude{}
	}
	return reflecteng.ReflectInclude{
		Facts: optCopy(x.Facts), ToolCalls: x.GetToolCalls(), ToolOutputs: optCopy(x.ToolOutputs),
	}
}

// basedOnToProto converts a BasedOn; one with no citations at all is nil (include.facts = false).
func basedOnToProto(b reflecteng.BasedOn) *pb.BasedOn {
	if len(b.Memories) == 0 && len(b.MentalModelIDs) == 0 && len(b.DirectiveIDs) == 0 {
		return nil
	}
	return &pb.BasedOn{
		Memories:       unitsToProto(b.Memories),
		MentalModelIds: strs[string](b.MentalModelIDs),
		DirectiveIds:   strs[string](b.DirectiveIDs),
	}
}

// reflectResultToProto converts a ReflectResult to a ReflectResponse.
func reflectResultToProto(r reflecteng.ReflectResult) *pb.ReflectResponse {
	return &pb.ReflectResponse{
		Text:                  r.Text,
		StructuredOutputJson:  string(r.StructuredOutputJSON),
		StructuredOutputError: r.StructuredOutputError,
		BasedOn:               basedOnToProto(r.BasedOn),
		Trace:                 reflectTraceToProto(r.Trace),
		Usage:                 usagesToProto(r.Usage),
	}
}

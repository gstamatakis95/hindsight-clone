package server

import (
	"memd/internal/domain"
	"memd/internal/engine/recall"
	reflecteng "memd/internal/engine/reflect"
	"memd/internal/engine/retain"
	"memd/internal/port"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "memd/gen/memory/v1"
)

// Converters for memory.proto (MemoryService: Retain, Recall, Reflect).

// ---------- retain ----------

// retainResult is the server-side counterpart of RetainResponse: the engine's IngestResult plus the token
// usage of a synchronous retain (approved CCR WP-07-1), which IngestResult does not carry yet.
type retainResult struct {
	// IngestResult is the engine's per-document outcome.
	retain.IngestResult
	// Usage is per-slot token usage of a synchronous retain; nil when run_async.
	Usage []domain.Usage
}

// retainRequestFromProto converts a RetainRequest; Bank carries only bank_id (the handler sets the tenant).
func retainRequestFromProto(r *pb.RetainRequest) (retain.IngestRequest, error) {
	var d decoder
	out := retain.IngestRequest{
		Bank:              port.BankRef{Bank: r.GetBankId()},
		Items:             d.retainItems(r.GetItems()),
		DocumentTags:      r.GetDocumentTags(),
		Overrides:         d.configPtr(r.GetOverrides()),
		SkipConsolidation: r.GetSkipConsolidation(),
		RunAsync:          r.GetRunAsync(),
	}
	return out, d.err
}

// retainRequestToProto converts an IngestRequest back to a RetainRequest (bank_id = Bank.Bank).
func retainRequestToProto(r retain.IngestRequest) *pb.RetainRequest {
	return &pb.RetainRequest{
		BankId:            r.Bank.Bank,
		Items:             retainItemsToProto(r.Items),
		DocumentTags:      r.DocumentTags,
		Overrides:         configToProto(r.Overrides),
		SkipConsolidation: r.SkipConsolidation,
		RunAsync:          r.RunAsync,
	}
}

// retainItem converts a RetainItem.
func (d *decoder) retainItem(it *pb.RetainItem) retain.IngestItem {
	return retain.IngestItem{
		Content:           it.GetContent(),
		Context:           it.GetContext(),
		Timestamp:         d.tsPtr(it.GetTimestamp()),
		Timeless:          it.GetTimeless(),
		DocumentID:        port.DocumentID(it.GetDocumentId()),
		Tags:              it.GetTags(),
		ObservationScopes: d.scopes(it.GetObservationScopes()),
		Metadata:          it.GetMetadata(),
		Entities:          entityHintsFromProto(it.GetEntities()),
		ResolveEntities:   optCopy(it.ResolveEntities),
		Overrides:         d.configPtr(it.GetOverrides()),
		ForceReextract:    it.GetForceReextract(),
	}
}

// retainItemToProto converts an IngestItem.
func retainItemToProto(it retain.IngestItem) *pb.RetainItem {
	return &pb.RetainItem{
		Content:           it.Content,
		Context:           it.Context,
		Timestamp:         tsPtrToProto(it.Timestamp),
		Timeless:          it.Timeless,
		DocumentId:        string(it.DocumentID),
		Tags:              it.Tags,
		ObservationScopes: scopesToProto(it.ObservationScopes),
		Metadata:          it.Metadata,
		Entities:          entityHintsToProto(it.Entities),
		ResolveEntities:   optCopy(it.ResolveEntities),
		Overrides:         configToProto(it.Overrides),
		ForceReextract:    it.ForceReextract,
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

// retainItemsToProto converts IngestItems; nil stays nil.
func retainItemsToProto(in []retain.IngestItem) []*pb.RetainItem {
	if in == nil {
		return nil
	}
	out := make([]*pb.RetainItem, len(in))
	for i := range in {
		out[i] = retainItemToProto(in[i])
	}
	return out
}

// retainedDocFromProto converts a RetainedDocument.
func retainedDocFromProto(x *pb.RetainedDocument) retain.RetainedDocument {
	return retain.RetainedDocument{
		DocumentID: port.DocumentID(x.GetDocumentId()), UnitsCreated: int(x.GetUnitsCreated()),
		Unchanged: x.GetUnchanged(), OperationID: port.OperationID(x.GetOperationId()),
	}
}

// retainedDocToProto converts a RetainedDocument.
func retainedDocToProto(x retain.RetainedDocument) *pb.RetainedDocument {
	return &pb.RetainedDocument{
		DocumentId: string(x.DocumentID), UnitsCreated: i32(x.UnitsCreated), Unchanged: x.Unchanged,
		OperationId: string(x.OperationID),
	}
}

// retainResultFromProto converts a RetainResponse.
func retainResultFromProto(r *pb.RetainResponse) (retainResult, error) {
	var d decoder
	var docs []retain.RetainedDocument
	if r.GetDocuments() != nil {
		docs = make([]retain.RetainedDocument, len(r.GetDocuments()))
		for i, x := range r.GetDocuments() {
			docs[i] = retainedDocFromProto(x)
		}
	}
	out := retainResult{
		IngestResult: retain.IngestResult{
			Documents: docs, ParentOperationID: port.OperationID(r.GetParentOperationId()),
		},
		Usage: d.usages(r.GetUsage()),
	}
	return out, d.err
}

// retainResultToProto converts a retain outcome to a RetainResponse.
func retainResultToProto(r retainResult) *pb.RetainResponse {
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
		Bank:               port.BankRef{Bank: r.GetBankId()},
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
		Overrides:          d.configPtr(r.GetOverrides()),
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

// recallRequestToProto converts a RecallRequest back to proto (bank_id = Bank.Bank).
func recallRequestToProto(r recall.RecallRequest) *pb.RecallRequest {
	out := &pb.RecallRequest{
		BankId:             r.Bank.Bank,
		Query:              r.Query,
		Types:              toEnums[pb.FactType](r.Types),
		Tags:               r.Tags.Tags,
		TagsMatch:          toEnum[pb.TagsMatch](r.Tags.Match),
		TagGroups:          tagGroupsToProto(r.Tags.Groups),
		Budget:             toEnum[pb.Budget](r.Budget),
		MaxTokens:          i32(r.MaxTokens),
		QueryTime:          tsPtrToProto(r.QueryTime),
		PreferObservations: r.PreferObservations,
		MinScores:          minScoresToProto(r.MinScores),
		Include:            recallIncludeToProto(r.Include),
		Trace:              r.Trace,
		Overrides:          configToProto(r.Overrides),
	}
	if r.Window != nil {
		out.TimeWindowStart = tsToProto(r.Window.Start)
		out.TimeWindowEnd = tsToProto(r.Window.End)
	}
	return out
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

// recallIncludeToProto converts a RecallInclude; nothing set = nil.
func recallIncludeToProto(x domain.RecallInclude) *pb.RecallInclude {
	if x == (domain.RecallInclude{}) {
		return nil
	}
	return &pb.RecallInclude{
		Chunks: optCopy(x.Chunks), ChunksMaxTokens: optInt32(x.ChunksMaxTokens),
		SourceFacts: optCopy(x.SourceFacts), SourceFactsMaxTokens: optInt32(x.SourceFactsMaxTokens),
		Entities: optCopy(x.Entities),
	}
}

// entityStateFromProto converts an EntityState.
func entityStateFromProto(x *pb.EntityState) domain.EntityState {
	return domain.EntityState{
		EntityID: domain.EntityID(x.GetEntityId()), CanonicalName: x.GetCanonicalName(),
		ObservationIDs: strs[domain.UnitID](x.GetObservationIds()),
	}
}

// entityStateToProto converts an EntityState.
func entityStateToProto(x domain.EntityState) *pb.EntityState {
	return &pb.EntityState{
		EntityId: string(x.EntityID), CanonicalName: x.CanonicalName, ObservationIds: strs[string](x.ObservationIDs),
	}
}

// recallResultFromProto converts a RecallResponse. Source facts are plain units: their scores are dropped.
func recallResultFromProto(r *pb.RecallResponse) (recall.RecallResult, error) {
	var d decoder
	out := recall.RecallResult{
		Results:              d.memories(r.GetResults()),
		SourceFactsTruncated: r.GetSourceFactsTruncated(),
		Trace:                d.recallTrace(r.GetTrace()),
	}
	if r.GetSourceFacts() != nil {
		out.SourceFacts = make(map[port.UnitID]domain.Unit, len(r.GetSourceFacts()))
		for k, m := range r.GetSourceFacts() {
			out.SourceFacts[port.UnitID(k)] = d.unit(m)
		}
	}
	if r.GetChunks() != nil {
		out.Chunks = make(map[port.ChunkID]recall.Chunk, len(r.GetChunks()))
		for k, c := range r.GetChunks() {
			out.Chunks[port.ChunkID(k)] = chunkFromProto(c)
		}
	}
	if r.GetEntities() != nil {
		out.Entities = make(map[port.EntityID]domain.EntityState, len(r.GetEntities()))
		for k, e := range r.GetEntities() {
			out.Entities[port.EntityID(k)] = entityStateFromProto(e)
		}
	}
	return out, d.err
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

// memories converts scored Memories; nil stays nil.
func (d *decoder) memories(in []*pb.Memory) []recall.Memory {
	if in == nil {
		return nil
	}
	out := make([]recall.Memory, len(in))
	for i, m := range in {
		out[i] = d.memory(m)
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
		Bank:               port.BankRef{Bank: r.GetBankId()},
		Query:              r.GetQuery(),
		Tags:               d.tagFilter(r.GetTags(), r.GetTagsMatch(), r.GetTagGroups()),
		Budget:             fromEnum[domain.Budget](&d, r.GetBudget()),
		MaxTokens:          optInt(r.MaxTokens),
		ResponseSchemaJSON: r.GetResponseSchemaJson(),
		ApplyAllDirectives: r.GetApplyAllDirectives(),
		ExtraDirectives:    directivesFromProto(r.GetExtraDirectives()),
		Include:            reflectIncludeFromProto(r.GetInclude()),
		QueryTime:          d.tsPtr(r.GetQueryTime()),
		Overrides:          d.configPtr(r.GetOverrides()),
	}
	return out, d.err
}

// reflectRequestToProto converts a ReflectRequest back to proto (bank_id = Bank.Bank).
func reflectRequestToProto(r reflecteng.ReflectRequest) *pb.ReflectRequest {
	return &pb.ReflectRequest{
		BankId:             r.Bank.Bank,
		Query:              r.Query,
		Tags:               r.Tags.Tags,
		TagsMatch:          toEnum[pb.TagsMatch](r.Tags.Match),
		TagGroups:          tagGroupsToProto(r.Tags.Groups),
		Budget:             toEnum[pb.Budget](r.Budget),
		MaxTokens:          optInt32(r.MaxTokens),
		ResponseSchemaJson: r.ResponseSchemaJSON,
		ApplyAllDirectives: r.ApplyAllDirectives,
		ExtraDirectives:    directivesToProto(r.ExtraDirectives),
		Include:            reflectIncludeToProto(r.Include),
		QueryTime:          tsPtrToProto(r.QueryTime),
		Overrides:          configToProto(r.Overrides),
	}
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

// reflectIncludeToProto converts a ReflectInclude; nothing set = nil.
func reflectIncludeToProto(x reflecteng.ReflectInclude) *pb.ReflectInclude {
	if x == (reflecteng.ReflectInclude{}) {
		return nil
	}
	return &pb.ReflectInclude{Facts: optCopy(x.Facts), ToolCalls: x.ToolCalls, ToolOutputs: optCopy(x.ToolOutputs)}
}

// basedOn converts a BasedOn; memories are plain units (scores dropped).
func (d *decoder) basedOn(b *pb.BasedOn) reflecteng.BasedOn {
	return reflecteng.BasedOn{
		Memories:       d.units(b.GetMemories()),
		MentalModelIDs: strs[port.MentalModelID](b.GetMentalModelIds()),
		DirectiveIDs:   strs[port.DirectiveID](b.GetDirectiveIds()),
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

// reflectResultFromProto converts a ReflectResponse.
func reflectResultFromProto(r *pb.ReflectResponse) (reflecteng.ReflectResult, error) {
	var d decoder
	out := reflecteng.ReflectResult{
		Text:                  r.GetText(),
		StructuredOutputJSON:  rawFromString(r.GetStructuredOutputJson()),
		StructuredOutputError: r.GetStructuredOutputError(),
		BasedOn:               d.basedOn(r.GetBasedOn()),
		Trace:                 reflectTraceFromProto(r.GetTrace()),
		Usage:                 d.usages(r.GetUsage()),
	}
	return out, d.err
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

package grpcapi

import (
	"encoding/json"
	"errors"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/memory"
	"memd/internal/recall"
	reflecteng "memd/internal/reflect"
	"reflect"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "memd/gen/memory/v1"
)

// goldenTime returns a distinct UTC instant per n.
func goldenTime(n int) time.Time {
	return time.Date(2024, time.Month(n), n, n, n, n, n*1000, time.UTC)
}

// checkGolden fails unless the converted request got equals want.
func checkGolden(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("proto -> domain mismatch\ngot:  %+v\nwant: %+v", got, want)
	}
}

// TestGoldenRetainRequest checks every RetainRequest and RetainItem field lands on its own domain field.
func TestGoldenRetainRequest(t *testing.T) {
	in := &pb.RetainRequest{
		BankId: "bank-1",
		Items: []*pb.RetainItem{{
			Content:    "content-1",
			Context:    "context-1",
			Timestamp:  timestamppb.New(goldenTime(1)),
			Timeless:   true,
			DocumentId: "doc-1",
			Tags:       []string{"item-tag"},
			ObservationScopeModes: &pb.ObservationScopeModes{
				Mode: pb.ObservationScopeModes_MODE_PER_TAG, MaxCombinations: ptr[int32](7),
			},
			Metadata:        map[string]string{"meta-k": "meta-v"},
			Entities:        []*pb.EntityHint{{Text: "Alice", Type: "person"}},
			ResolveEntities: ptr(false),
			Overrides: &pb.RetainOverrides{
				Retain: &pb.RetainConfig{Mission: "item-mission"}, ExtractModel: &pb.ModelBinding{Model: "item-extract"},
			},
			ForceReextract: false,
		}},
		DocumentTags: []string{"doc-tag"},
		Overrides: &pb.RetainOverrides{
			Retain:         &pb.RetainConfig{Mission: "request-mission", ChunkMaxTokens: ptr[int32](9)},
			TimeParseModel: &pb.ModelBinding{Model: "request-time", Temperature: ptr(0.1)},
		},
		SkipConsolidation: true,
		RunAsync:          false,
	}
	want := memory.RetainRequest{
		Items: []memory.RetainItem{{
			Content:    "content-1",
			Context:    "context-1",
			Timestamp:  ptr(goldenTime(1)),
			Timeless:   true,
			DocumentID: "doc-1",
			Tags:       []string{"item-tag"},
			ObservationScopeModes: &domain.ObservationScopeModes{
				Mode: domain.ScopeModePerTag, MaxCombinations: ptr(7),
			},
			Metadata:        map[string]string{"meta-k": "meta-v"},
			Entities:        []domain.EntityHint{{Text: "Alice", Type: "person"}},
			ResolveEntities: ptr(false),
			Overrides: &domain.BankConfig{
				Retain: &domain.RetainConfig{Mission: "item-mission"},
				Models: &domain.ModelBindings{Extract: &domain.ModelBinding{Model: "item-extract"}},
			},
		}},
		DocumentTags: []string{"doc-tag"},
		Overrides: &domain.BankConfig{
			Retain: &domain.RetainConfig{Mission: "request-mission", ChunkMaxTokens: ptr(9)},
			Models: &domain.ModelBindings{TimeParse: &domain.ModelBinding{Model: "request-time", Temperature: ptr(0.1)}},
		},
		SkipConsolidation: true,
	}
	got, err := retainRequestFromProto(in)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, got, want)
}

// TestGoldenRecallRequest checks every RecallRequest field, the tag filter and the time window.
func TestGoldenRecallRequest(t *testing.T) {
	in := &pb.RecallRequest{
		BankId:    "bank-2",
		Query:     "what happened",
		FactTypes: []pb.FactType{pb.FactType_FACT_TYPE_EXPERIENCE, pb.FactType_FACT_TYPE_OBSERVATION},
		Tags: &pb.TagFilter{
			Tags: []string{"flat-tag"}, Match: pb.TagsMatch_TAGS_MATCH_ALL_STRICT,
			Groups: []*pb.TagGroup{{Node: &pb.TagGroup_Leaf{Leaf: &pb.TagLeaf{
				Tags: []string{"group-tag"}, Match: pb.TagsMatch_TAGS_MATCH_EXACT,
				Resolve: pb.TagLeaf_RESOLVE_FUZZY,
			}}}},
		},
		Budget:             pb.Budget_BUDGET_HIGH,
		MaxTokens:          1234,
		QueryTime:          timestamppb.New(goldenTime(2)),
		TimeWindowStart:    timestamppb.New(goldenTime(3)),
		TimeWindowEnd:      timestamppb.New(goldenTime(4)),
		PreferObservations: true,
		Arms:               []string{"graph", "vector"},
		MinScores: &pb.MinScores{
			Arms: map[string]float64{"vector": 0.1, "text": 0.2}, Reranker: ptr(0.3), Final: ptr(0.4),
		},
		Include: &pb.RecallInclude{
			Chunks: ptr(true), ChunksMaxTokens: ptr[int32](11), SourceFacts: ptr(false),
			SourceFactsMaxTokens: ptr[int32](22), Entities: ptr(true),
		},
		Trace: false,
		Overrides: &pb.RecallOverrides{
			Recall: &pb.RecallConfig{RrfK: ptr[int32](7)}, TimeParseModel: &pb.ModelBinding{Model: "recall-time"},
			RerankModel: &pb.ModelBinding{Model: "recall-rerank"},
		},
	}
	want := recall.Request{
		Query:     "what happened",
		FactTypes: []domain.FactType{domain.FactTypeExperience, domain.FactTypeObservation},
		Tags: domain.TagFilter{
			Tags: []string{"flat-tag"}, Match: domain.TagsMatchAllStrict,
			Groups: []domain.TagGroup{{Kind: domain.TagGroupLeaf, Leaf: &domain.TagLeaf{
				Tags: []string{"group-tag"}, Match: domain.TagsMatchExact, Resolve: domain.TagResolveFuzzy,
			}}},
		},
		Budget:             domain.BudgetHigh,
		MaxTokens:          1234,
		QueryTime:          ptr(goldenTime(2)),
		Window:             &domain.TimeWindow{Start: goldenTime(3), End: goldenTime(4)},
		PreferObservations: true,
		Arms:               []string{"graph", "vector"},
		MinScores: domain.MinScores{
			Arms: map[string]float64{"vector": 0.1, "text": 0.2}, Reranker: ptr(0.3), Final: ptr(0.4),
		},
		Include: domain.RecallInclude{
			Chunks: ptr(true), ChunksMaxTokens: ptr(11), SourceFacts: ptr(false), SourceFactsMaxTokens: ptr(22),
			Entities: ptr(true),
		},
		Overrides: &domain.BankConfig{
			Recall: &domain.RecallConfig{RRFK: ptr(7)},
			Models: &domain.ModelBindings{
				TimeParse: &domain.ModelBinding{Model: "recall-time"}, Rerank: &domain.ModelBinding{Model: "recall-rerank"},
			},
		},
	}
	got, err := recallRequestFromProto(in)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, got, want)
	in.Overrides.Recall.Arms = []string{"vector"}
	if _, err := recallRequestFromProto(in); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("overrides.recall.arms: err = %v, want INVALID_ARGUMENT", err)
	}
}

// TestGoldenReflectRequest checks every ReflectRequest field, the tag filter and the include flags.
func TestGoldenReflectRequest(t *testing.T) {
	in := &pb.ReflectRequest{
		BankId: "bank-3",
		Query:  "why",
		Tags: &pb.TagFilter{
			Tags: []string{"reflect-tag"}, Match: pb.TagsMatch_TAGS_MATCH_ANY_STRICT,
			Groups: []*pb.TagGroup{{Node: &pb.TagGroup_NoneOf{NoneOf: &pb.TagGroup{
				Node: &pb.TagGroup_Leaf{Leaf: &pb.TagLeaf{Tags: []string{"not-tag"}, Match: pb.TagsMatch_TAGS_MATCH_ANY}},
			}}}},
		},
		Budget:             pb.Budget_BUDGET_MID,
		MaxTokens:          ptr[int32](321),
		ResponseSchemaJson: `{"type":"object"}`,
		ApplyAllDirectives: true,
		ExtraDirectives: []*pb.Directive{{
			Id: "dir-1", Name: "dir-name", Rules: []string{"be brief"}, Tags: []string{"dir-tag"}, Active: true,
		}},
		Include:   &pb.ReflectInclude{BasedOn: ptr(false), Trace: true, ToolOutputs: ptr(false)},
		QueryTime: timestamppb.New(goldenTime(5)),
		Arms:      []string{"text"},
		Overrides: &pb.ReflectOverrides{
			Reflect: &pb.ReflectConfig{Mission: "reflect-overrides", Disposition: &pb.Disposition{Skepticism: 2}},
			Recall:  &pb.RecallConfig{BudgetLow: ptr[int32](5)}, ReflectModel: &pb.ModelBinding{Model: "reflect-model"},
			StructuredOutputModel: &pb.ModelBinding{Model: "reflect-structured"},
			RerankModel:           &pb.ModelBinding{Model: "reflect-rerank"},
		},
	}
	want := reflecteng.Request{
		Query: "why",
		Tags: domain.TagFilter{
			Tags: []string{"reflect-tag"}, Match: domain.TagsMatchAnyStrict,
			Groups: []domain.TagGroup{{Kind: domain.TagGroupNoneOf, Not: &domain.TagGroup{
				Kind: domain.TagGroupLeaf, Leaf: &domain.TagLeaf{Tags: []string{"not-tag"}, Match: domain.TagsMatchAny},
			}}},
		},
		Budget:             domain.BudgetMid,
		MaxTokens:          ptr(321),
		ResponseSchemaJSON: `{"type":"object"}`,
		ApplyAllDirectives: true,
		ExtraDirectives: []domain.Directive{{
			ID: "dir-1", Name: "dir-name", Rules: []string{"be brief"}, Tags: []string{"dir-tag"}, Active: true,
		}},
		Include:   reflecteng.Include{BasedOn: ptr(false), Trace: true, ToolOutputs: ptr(false)},
		QueryTime: ptr(goldenTime(5)),
		Arms:      []string{"text"},
		Overrides: &domain.BankConfig{
			Reflect: &domain.ReflectConfig{Mission: "reflect-overrides", Disposition: &domain.Disposition{Skepticism: 2}},
			Recall:  &domain.RecallConfig{BudgetLow: ptr(5)},
			Models: &domain.ModelBindings{
				Reflect:          &domain.ModelBinding{Model: "reflect-model"},
				StructuredOutput: &domain.ModelBinding{Model: "reflect-structured"},
				Rerank:           &domain.ModelBinding{Model: "reflect-rerank"},
			},
		},
	}
	got, err := reflectRequestFromProto(in)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, got, want)
}

// TestUpdateMemoryMask checks the UpdateMemory mask: empty = the populated MemoryEdit fields, a named field
// applies even when unset (a clear), and a path below a top-level field is INVALID_ARGUMENT on update_mask.
func TestUpdateMemoryMask(t *testing.T) {
	implicit, err := updateMemoryRequestFromProto(&pb.UpdateMemoryRequest{MemoryId: "m", Memory: &pb.MemoryEdit{
		Text: "new", OccurredStart: timestamppb.New(goldenTime(1)),
	}})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(implicit.Fields)
	if want := []memory.EditField{memory.EditOccurredStart, memory.EditText}; !slices.Equal(implicit.Fields, want) {
		t.Errorf("implicit mask fields = %v, want %v", implicit.Fields, want)
	}
	cleared, err := updateMemoryRequestFromProto(&pb.UpdateMemoryRequest{
		MemoryId: "m", UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"entities", "occurred_end"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []memory.EditField{memory.EditEntities, memory.EditOccurredEnd}; !slices.Equal(cleared.Fields, want) ||
		cleared.Edit.Entities != nil || cleared.Edit.OccurredEnd != nil {
		t.Errorf("explicit mask = %v %+v, want %v with the values cleared", cleared.Fields, cleared.Edit, want)
	}
	_, err = updateMemoryRequestFromProto(&pb.UpdateMemoryRequest{
		MemoryId: "m", UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"occurred_start.seconds"}},
	})
	if v := domain.FieldViolations(err); len(v) != 1 || v[0].Field != "update_mask" {
		t.Errorf("nested path: err = %v, want a violation on update_mask", err)
	}
}

// TestUnknownEnumRejected checks an undefined enum number is INVALID_ARGUMENT.
func TestUnknownEnumRejected(t *testing.T) {
	var d decoder
	d.config(&pb.BankConfig{Retain: &pb.RetainConfig{ExtractionMode: pb.RetainConfig_ExtractionMode(99)}})
	if err := d.err; err == nil || !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
	if got := toEnum[pb.FactType](domain.FactType("bogus")); got != pb.FactType_FACT_TYPE_UNSPECIFIED {
		t.Fatalf("unknown domain value = %v, want UNSPECIFIED", got)
	}
}

// anyListSet reports whether any of o's clearable lists or maps is non-nil (an explicit value or clear).
func anyListSet(o domain.BankConfig) bool {
	return o.Recall.Arms != nil || o.Recall.ArmBoosts != nil || o.Retain != nil ||
		o.Observations != nil || o.Models != nil
}

// TestEmptyRepeatedOverrideIsNotAClear sends a Recall override that sets only recall.rrf_k over the wire
// and merges it over a tenant level with list and map fields: proto3 can't tell an absent repeated field from
// an empty one, so the converter must leave them nil and the tenant's lists must survive the merge.
func TestEmptyRepeatedOverrideIsNotAClear(t *testing.T) {
	rrf := int32(7)
	wire, err := proto.Marshal(&pb.RecallRequest{
		BankId: "b", Query: "q", Overrides: &pb.RecallOverrides{Recall: &pb.RecallConfig{RrfK: &rrf}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var req pb.RecallRequest
	if err := proto.Unmarshal(wire, &req); err != nil {
		t.Fatal(err)
	}
	in, err := recallRequestFromProto(&req)
	if err != nil {
		t.Fatal(err)
	}
	o := in.Overrides
	if o == nil || o.Recall == nil {
		t.Fatalf("overrides lost: %+v", o)
	}
	if anyListSet(*o) {
		t.Fatalf("absent lists decoded as explicit clears: %+v", o)
	}
	tenant := domain.BankConfig{
		Recall: &domain.RecallConfig{
			Arms: []string{"vector"}, ArmBoosts: map[string]float64{"vector": 2},
		},
		Retain: &domain.RetainConfig{EntityLabels: []domain.EntityLabel{{Key: "k"}}},
	}
	got := config.Merge(tenant, *o)
	if len(got.Recall.Arms) != 1 || len(got.Recall.ArmBoosts) != 1 || len(got.Retain.EntityLabels) != 1 {
		t.Fatalf("tenant lists cleared by the override: %+v", got)
	}
	if got.Recall.RRFK == nil || *got.Recall.RRFK != 7 {
		t.Fatalf("rrf_k not applied: %+v", got.Recall)
	}
}

// viewModel is a model with every view-dependent field set.
func viewModel() domain.MentalModel {
	return domain.MentalModel{
		Spec:                 domain.MentalModelSpec{ID: "m1", Name: "page"},
		Bank:                 domain.NewBankRef("t", "org", "b"),
		ContentMD:            "# page",
		StructuredOutputJSON: json.RawMessage(`{"a":1}`),
		Version:              3,
		SourceIDs:            []domain.UnitID{"u1"},
		LastTrace:            &domain.RefreshTrace{Outcome: domain.RefreshOutcomeContentWritten, DurationMS: 5},
		CreatedAt:            time.Unix(100, 0),
	}
}

// presence reports which view-dependent fields of m are set.
type presence struct {
	// content is content_md, structured is structured_output_json, sources is source_memory_ids, trace is
	// last_trace.
	content, structured, sources, trace bool
}

// presenceOf reads the view-dependent fields of m; it also checks the always-present ones.
func presenceOf(t *testing.T, m *pb.MentalModel) presence {
	t.Helper()
	if m.GetSpec().GetId() != "m1" || m.GetVersion() != 3 {
		t.Fatalf("basic fields missing: %v", m)
	}
	return presence{
		content: m.GetContentMd() != "", structured: m.GetStructuredOutputJson() != "",
		sources: len(m.GetSourceMemoryIds()) > 0, trace: m.GetLastTrace() != nil,
	}
}

// TestMentalModelView checks the view trimming of mentalModelToProto: BASIC is the spec plus status, CONTENT
// adds content and structured output, FULL adds source ids and the last trace.
func TestMentalModelView(t *testing.T) {
	m := viewModel()
	trims := []struct {
		view domain.MentalModelView
		want presence
	}{
		{domain.MentalModelViewBasic, presence{}},
		{domain.MentalModelViewContent, presence{content: true, structured: true}},
		{domain.MentalModelViewFull, presence{content: true, structured: true, sources: true, trace: true}},
	}
	for _, tc := range trims {
		if got := presenceOf(t, mentalModelToProto(m, tc.view)); got != tc.want {
			t.Errorf("view %q: got %+v, want %+v", tc.view, got, tc.want)
		}
	}
}

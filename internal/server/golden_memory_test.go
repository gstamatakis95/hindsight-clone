package server

import (
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"memd/internal/domain"
	"memd/internal/engine/curate"
	"memd/internal/engine/recall"
	reflecteng "memd/internal/engine/reflect"
	"memd/internal/engine/retain"
	"memd/internal/port"

	pb "memd/gen/memory/v1"
)

// goldenTime returns a distinct UTC instant per n.
func goldenTime(n int) time.Time {
	return time.Date(2024, time.Month(n), n, n, n, n, n*1000, time.UTC)
}

// checkGolden fails unless got equals want (domain side) and back equals in (proto side).
func checkGolden(t *testing.T, got, want any, back, in proto.Message) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("proto -> domain mismatch\ngot:  %+v\nwant: %+v", got, want)
	}
	if !proto.Equal(back, in) {
		t.Errorf("domain -> proto mismatch\ngot:  %v\nwant: %v", back, in)
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
			ObservationScopes: &pb.ObservationScopes{
				Mode: pb.ObservationScopes_MODE_PER_TAG, MaxCombinations: ptr[int32](7),
			},
			Metadata:        map[string]string{"meta-k": "meta-v"},
			Entities:        []*pb.EntityHint{{Text: "Alice", Type: "person"}},
			ResolveEntities: ptr(false),
			Overrides:       &pb.BankConfig{RetainMission: "item-mission"},
			ForceReextract:  false,
		}},
		DocumentTags:      []string{"doc-tag"},
		Overrides:         &pb.BankConfig{RetainMission: "request-mission"},
		SkipConsolidation: true,
		RunAsync:          false,
	}
	want := retain.IngestRequest{
		Bank: port.BankRef{Bank: "bank-1"},
		Items: []retain.IngestItem{{
			Content:    "content-1",
			Context:    "context-1",
			Timestamp:  ptr(goldenTime(1)),
			Timeless:   true,
			DocumentID: "doc-1",
			Tags:       []string{"item-tag"},
			ObservationScopes: &domain.ObservationScopes{
				Mode: domain.ScopeModePerTag, MaxCombinations: ptr(7),
			},
			Metadata:        map[string]string{"meta-k": "meta-v"},
			Entities:        []domain.EntityHint{{Text: "Alice", Type: "person"}},
			ResolveEntities: ptr(false),
			Overrides:       &domain.BankConfig{RetainMission: "item-mission"},
		}},
		DocumentTags:      []string{"doc-tag"},
		Overrides:         &domain.BankConfig{RetainMission: "request-mission"},
		SkipConsolidation: true,
	}
	got, err := retainRequestFromProto(in)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, got, want, retainRequestToProto(got), in)
}

// TestGoldenRecallRequest checks every RecallRequest field, the tag filter and the time window.
func TestGoldenRecallRequest(t *testing.T) {
	in := &pb.RecallRequest{
		BankId:    "bank-2",
		Query:     "what happened",
		Types:     []pb.FactType{pb.FactType_FACT_TYPE_EXPERIENCE, pb.FactType_FACT_TYPE_OBSERVATION},
		Tags:      []string{"flat-tag"},
		TagsMatch: pb.TagsMatch_TAGS_MATCH_ALL_STRICT,
		TagGroups: []*pb.TagGroup{{Node: &pb.TagGroup_Leaf{Leaf: &pb.TagLeaf{
			Tags: []string{"group-tag"}, Match: pb.TagsMatch_TAGS_MATCH_EXACT,
			Resolve: pb.TagLeaf_RESOLVE_FUZZY,
		}}}},
		Budget:             pb.Budget_BUDGET_HIGH,
		MaxTokens:          1234,
		QueryTime:          timestamppb.New(goldenTime(2)),
		TimeWindowStart:    timestamppb.New(goldenTime(3)),
		TimeWindowEnd:      timestamppb.New(goldenTime(4)),
		PreferObservations: true,
		MinScores:          &pb.MinScores{Semantic: ptr(0.1), Keyword: ptr(0.2), Reranker: ptr(0.3), Final: ptr(0.4)},
		Include: &pb.RecallInclude{
			Chunks: ptr(true), ChunksMaxTokens: ptr[int32](11), SourceFacts: ptr(false),
			SourceFactsMaxTokens: ptr[int32](22), Entities: ptr(true),
		},
		Trace:     false,
		Overrides: &pb.BankConfig{ReflectMission: "recall-overrides"},
	}
	want := recall.RecallRequest{
		Bank:  port.BankRef{Bank: "bank-2"},
		Query: "what happened",
		Types: []domain.FactType{domain.FactTypeExperience, domain.FactTypeObservation},
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
		MinScores:          domain.MinScores{Semantic: ptr(0.1), Keyword: ptr(0.2), Reranker: ptr(0.3), Final: ptr(0.4)},
		Include: domain.RecallInclude{
			Chunks: ptr(true), ChunksMaxTokens: ptr(11), SourceFacts: ptr(false), SourceFactsMaxTokens: ptr(22),
			Entities: ptr(true),
		},
		Overrides: &domain.BankConfig{ReflectMission: "recall-overrides"},
	}
	got, err := recallRequestFromProto(in)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, got, want, recallRequestToProto(got), in)
}

// TestGoldenReflectRequest checks every ReflectRequest field, the tag filter and the include flags.
func TestGoldenReflectRequest(t *testing.T) {
	in := &pb.ReflectRequest{
		BankId:    "bank-3",
		Query:     "why",
		Tags:      []string{"reflect-tag"},
		TagsMatch: pb.TagsMatch_TAGS_MATCH_ANY_STRICT,
		TagGroups: []*pb.TagGroup{{Node: &pb.TagGroup_NoneOf{NoneOf: &pb.TagGroup{
			Node: &pb.TagGroup_Leaf{Leaf: &pb.TagLeaf{Tags: []string{"not-tag"}, Match: pb.TagsMatch_TAGS_MATCH_ANY}},
		}}}},
		Budget:             pb.Budget_BUDGET_MID,
		MaxTokens:          ptr[int32](321),
		ResponseSchemaJson: `{"type":"object"}`,
		ApplyAllDirectives: true,
		ExtraDirectives: []*pb.Directive{{
			Id: "dir-1", Name: "dir-name", Rules: "be brief", Tags: []string{"dir-tag"}, Active: true,
		}},
		Include:   &pb.ReflectInclude{Facts: ptr(false), ToolCalls: true, ToolOutputs: ptr(false)},
		QueryTime: timestamppb.New(goldenTime(5)),
		Overrides: &pb.BankConfig{ReflectMission: "reflect-overrides"},
	}
	want := reflecteng.ReflectRequest{
		Bank:  port.BankRef{Bank: "bank-3"},
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
			ID: "dir-1", Name: "dir-name", Rules: "be brief", Tags: []string{"dir-tag"}, Active: true,
		}},
		Include:   reflecteng.ReflectInclude{Facts: ptr(false), ToolCalls: true, ToolOutputs: ptr(false)},
		QueryTime: ptr(goldenTime(5)),
		Overrides: &domain.BankConfig{ReflectMission: "reflect-overrides"},
	}
	got, err := reflectRequestFromProto(in)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, got, want, reflectRequestToProto(got), in)
}

// TestUpdateMemoryEntitiesPresence checks that an unset EntityList (unchanged) and a set but empty one
// (clear) stay distinct in both directions.
func TestUpdateMemoryEntitiesPresence(t *testing.T) {
	unset, err := updateMemoryRequestFromProto(&pb.UpdateMemoryRequest{MemoryId: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if unset.Entities != nil {
		t.Errorf("unset entities = %#v, want nil (unchanged)", unset.Entities)
	}
	cleared, err := updateMemoryRequestFromProto(&pb.UpdateMemoryRequest{MemoryId: "m", Entities: &pb.EntityList{}})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Entities == nil || len(cleared.Entities) != 0 {
		t.Errorf("empty entity list = %#v, want a non-nil empty slice (clear)", cleared.Entities)
	}
	if got := updateMemoryRequestToProto(curate.UpdateMemoryRequest{}).GetEntities(); got != nil {
		t.Errorf("nil entities -> %v, want an unset EntityList", got)
	}
	if got := updateMemoryRequestToProto(curate.UpdateMemoryRequest{Entities: []string{}}).GetEntities(); got == nil {
		t.Error("empty entities -> unset EntityList, want a set empty one")
	}
}

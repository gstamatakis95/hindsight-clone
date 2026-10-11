package grpcapi

import (
	"errors"
	"memd/internal/domain"
	"memd/internal/memory"
	"memd/internal/recall"
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	reflecteng "memd/internal/reflect"

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
			ObservationScopes: &pb.ObservationScopes{
				Mode: pb.ObservationScopes_MODE_PER_TAG, MaxCombinations: ptr[int32](7),
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
		Bank: domain.BankRef{Bank: "bank-1"},
		Items: []memory.RetainItem{{
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
		BankId: "bank-2",
		Query:  "what happened",
		Types:  []pb.FactType{pb.FactType_FACT_TYPE_EXPERIENCE, pb.FactType_FACT_TYPE_OBSERVATION},
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
		Bank:  domain.BankRef{Bank: "bank-2"},
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
		Include:   &pb.ReflectInclude{Facts: ptr(false), ToolCalls: true, ToolOutputs: ptr(false)},
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
		Bank:  domain.BankRef{Bank: "bank-3"},
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
		Include:   reflecteng.Include{Facts: ptr(false), ToolCalls: true, ToolOutputs: ptr(false)},
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

// TestUpdateMemoryEntitiesPresence checks that an unset EntityList (unchanged) and a set but empty one
// (clear) stay distinct.
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
}

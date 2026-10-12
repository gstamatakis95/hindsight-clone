package grpcapi

import (
	"encoding/json"
	"memd/internal/domain"
	"reflect"
	"testing"
	"time"

	pb "memd/gen/memory/v1"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// TestTagFilterFieldNames checks the one tag-filter shape: no request carries the old tags_match / tag_groups
// fields, and every TagFilter field is named tags (scopes on a strategy).
func TestTagFilterFieldNames(t *testing.T) {
	filterName := (&pb.TagFilter{}).ProtoReflect().Descriptor().FullName()
	msgs := pb.File_memory_v1_memory_proto.Messages()
	var walk func(protoreflect.MessageDescriptors)
	walk = func(ms protoreflect.MessageDescriptors) {
		for i := range ms.Len() {
			md := ms.Get(i)
			for j := range md.Fields().Len() {
				fd := md.Fields().Get(j)
				switch {
				case fd.Name() == "tags_match" || fd.Name() == "tag_groups":
					t.Errorf("%s still has %s", md.FullName(), fd.Name())
				case fd.Message() != nil && fd.Message().FullName() == filterName &&
					fd.Name() != "tags" && fd.Name() != "scopes":
					t.Errorf("%s.%s is a TagFilter not named tags", md.FullName(), fd.Name())
				}
			}
			walk(md.Messages())
		}
	}
	walk(msgs)
}

// TestTagFilterEverySite checks that each request site converts the same TagFilter message to the same
// domain.TagFilter, and that the reverse conversion round-trips it.
func TestTagFilterEverySite(t *testing.T) {
	in := &pb.TagFilter{
		Tags: []string{"a", "b"}, Match: pb.TagsMatch_TAGS_MATCH_ALL_STRICT,
		Groups: []*pb.TagGroup{{Node: &pb.TagGroup_Leaf{Leaf: &pb.TagLeaf{Tags: []string{"c"}}}}},
	}
	want := domain.TagFilter{
		Tags: []string{"a", "b"}, Match: domain.TagsMatchAllStrict,
		Groups: []domain.TagGroup{{Kind: domain.TagGroupLeaf, Leaf: &domain.TagLeaf{Tags: []string{"c"}}}},
	}
	sites := map[string]func() (domain.TagFilter, error){
		"Recall": func() (domain.TagFilter, error) {
			r, err := recallRequestFromProto(&pb.RecallRequest{Tags: in})
			return r.Tags, err
		},
		"Reflect": func() (domain.TagFilter, error) {
			r, err := reflectRequestFromProto(&pb.ReflectRequest{Tags: in})
			return r.Tags, err
		},
		"ListMemories": func() (domain.TagFilter, error) {
			r, err := listMemoriesRequestFromProto(&pb.ListMemoriesRequest{Tags: in})
			return r.Tags, err
		},
		"ListDocuments": func() (domain.TagFilter, error) {
			r, err := listDocumentsFromProto(&pb.ListDocumentsRequest{Tags: in})
			return r.Tags, err
		},
		"ListMentalModels": func() (domain.TagFilter, error) {
			r, err := listMentalModelsFromProto(&pb.ListMentalModelsRequest{Tags: in})
			return r.Tags, err
		},
		"ListDirectives": func() (domain.TagFilter, error) {
			r, err := listDirectivesFromProto(&pb.ListDirectivesRequest{Tags: in})
			return r.Tags, err
		},
		"MentalModelScope": func() (domain.TagFilter, error) {
			var d decoder
			return d.scope(&pb.MentalModelScope{Tags: in}).Tags, d.err
		},
		"ConsolidationStrategy.scopes": func() (domain.TagFilter, error) {
			var d decoder
			return d.strategy(&pb.ConsolidationStrategy{Scopes: []*pb.TagFilter{in}}).Scopes[0], d.err
		},
	}
	for name, conv := range sites {
		got, err := conv()
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v %v, want %+v", name, got, err, want)
		}
	}
	if got := tagFilterToProto(want); !reflect.DeepEqual(got.GetTags(), in.GetTags()) || got.GetMatch() != in.GetMatch() ||
		len(got.GetGroups()) != 1 {
		t.Errorf("tagFilterToProto = %v, want %v", got, in)
	}
}

// TestOperationShape checks the Operation returned for async work: it carries its bank id, and the job result goes out
// as compact JSON text in debug_summary.
func TestOperationShape(t *testing.T) {
	at := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	op := domain.Operation{
		ID: "op-1", Bank: domain.BankRef{Tenant: "t", Bank: "b"}, Kind: domain.OperationKindRefreshMentalModel,
		Status: domain.OperationStatusCompleted, Result: json.RawMessage(`{ "strategy": "single_shot" }`),
		CreatedAt: at, UpdatedAt: at,
	}
	got := operationToProto(op)
	if got.GetId() != "op-1" || got.GetBankId() != "b" ||
		got.GetKind() != pb.OperationKind_OPERATION_KIND_REFRESH_MENTAL_MODEL ||
		got.GetDebugSummary() != `{"strategy":"single_shot"}` {
		t.Fatalf("operation = %v", got)
	}
	if got := operationToProto(domain.Operation{ID: "op-2"}); got.GetDebugSummary() != "" {
		t.Fatalf("no result: debug_summary = %q, want empty", got.GetDebugSummary())
	}
}

package server

import (
	"errors"
	"memd/internal/domain"
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	pb "memd/gen/memory/v1"
)

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// mask builds a FieldMask.
func mask(paths ...string) *fieldmaskpb.FieldMask { return &fieldmaskpb.FieldMask{Paths: paths} }

// baseConfig is a stored config with nested values and an explicit clear.
func baseConfig() domain.BankConfig {
	return domain.BankConfig{
		RetainMission:  "keep",
		DedupThreshold: ptr(0.9),
		Recall:         &domain.RecallConfig{RRFK: ptr(60), RecencyAlpha: ptr(0.2)},
		EntityLabels:   []domain.EntityLabel{},
		Models: []domain.SlotBinding{
			{Slot: domain.ModelSlotExtract, Binding: domain.ModelBinding{Model: "small"}},
		},
	}
}

// TestFieldMaskConfigPatch checks path replacement, clears, nested paths and that untouched fields
// (including an explicit clear) survive.
func TestFieldMaskConfigPatch(t *testing.T) {
	tests := []struct {
		name  string
		patch *pb.BankConfig
		mask  *fieldmaskpb.FieldMask
		want  func(c *domain.BankConfig)
	}{
		{
			"replace scalar", &pb.BankConfig{RetainMission: "new"}, mask("retain_mission"),
			func(c *domain.BankConfig) { c.RetainMission = "new" },
		},
		{
			"unset value clears", &pb.BankConfig{}, mask("dedup_threshold"),
			func(c *domain.BankConfig) { c.DedupThreshold = nil },
		},
		{
			"nested leaf only", &pb.BankConfig{Recall: &pb.RecallConfig{RrfK: ptr(int32(10))}}, mask("recall.rrf_k"),
			func(c *domain.BankConfig) { c.Recall = &domain.RecallConfig{RRFK: ptr(10), RecencyAlpha: ptr(0.2)} },
		},
		{
			"whole message", &pb.BankConfig{Recall: &pb.RecallConfig{RrfK: ptr(int32(10))}}, mask("recall"),
			func(c *domain.BankConfig) { c.Recall = &domain.RecallConfig{RRFK: ptr(10)} },
		},
		{
			"empty list is an explicit clear", &pb.BankConfig{}, mask("models"),
			func(c *domain.BankConfig) { c.Models = []domain.SlotBinding{} },
		},
		{
			"empty nested list is an explicit clear", &pb.BankConfig{}, mask("recall.enabled_strategies"),
			func(c *domain.BankConfig) {
				c.Recall = &domain.RecallConfig{RRFK: ptr(60), RecencyAlpha: ptr(0.2), EnabledStrategies: []string{}}
			},
		},
		{
			"no mask = populated fields", &pb.BankConfig{ReflectMission: "r"}, nil,
			func(c *domain.BankConfig) { c.ReflectMission = "r" },
		},
		{
			"unmasked fields ignored", &pb.BankConfig{RetainMission: "x", ReflectMission: "r"}, mask("reflect_mission"),
			func(c *domain.BankConfig) { c.ReflectMission = "r" },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch, err := configPatch(tt.patch, tt.mask, nil)
			if err != nil {
				t.Fatal(err)
			}
			cur := baseConfig()
			got, err := patch(cur)
			if err != nil {
				t.Fatal(err)
			}
			want := baseConfig()
			tt.want(&want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got  %+v\nwant %+v", got, want)
			}
			if !reflect.DeepEqual(cur, baseConfig()) {
				t.Fatalf("patch mutated its input: %+v", cur)
			}
		})
	}
}

// TestFieldMaskInvalid checks invalid paths are INVALID_ARGUMENT.
func TestFieldMaskInvalid(t *testing.T) {
	for _, m := range []*fieldmaskpb.FieldMask{
		mask("nope"), mask("recall.nope"), mask("models.slot"), mask("retain_mission.x"),
	} {
		if _, err := configPatch(&pb.BankConfig{}, m, nil); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("mask %v: err = %v, want ErrInvalidArgument", m.GetPaths(), err)
		}
	}
	if _, err := specPatch(&pb.MentalModelSpec{Id: "x"}, mask("id"), nil); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("id path: err = %v, want ErrInvalidArgument", err)
	}
	if _, err := resetPatch([]string{"bogus"}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("reset bogus: err = %v, want ErrInvalidArgument", err)
	}
}

// TestFieldMaskReset checks ResetBankConfig paths go back to unset and no paths resets everything.
func TestFieldMaskReset(t *testing.T) {
	patch, err := resetPatch([]string{"recall.rrf_k", "entity_labels", "retain_mission"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := patch(baseConfig())
	if err != nil {
		t.Fatal(err)
	}
	want := baseConfig()
	want.Recall = &domain.RecallConfig{RecencyAlpha: ptr(0.2)}
	want.EntityLabels = nil
	want.RetainMission = ""
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	all, err := resetPatch(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := all(baseConfig()); !reflect.DeepEqual(got, domain.BankConfig{}) {
		t.Fatalf("reset all = %+v", got)
	}
}

// TestFieldMaskSpecPatch checks a spec patch, including a nested trigger path; the id is kept.
func TestFieldMaskSpecPatch(t *testing.T) {
	patch, err := specPatch(&pb.MentalModelSpec{
		Name: "new", Trigger: &pb.RefreshTrigger{RefreshCron: "0 * * * *"},
	}, mask("name", "trigger.refresh_cron", "tags"), nil)
	if err != nil {
		t.Fatal(err)
	}
	cur := domain.MentalModelSpec{
		ID: "m", Name: "old", Tags: []string{"a"}, Trigger: domain.RefreshTrigger{KeepTrace: true},
	}
	got, err := patch(cur)
	if err != nil {
		t.Fatal(err)
	}
	want := domain.MentalModelSpec{
		ID: "m", Name: "new", Tags: []string{}, Trigger: domain.RefreshTrigger{KeepTrace: true, RefreshCron: "0 * * * *"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

// TestFieldMaskImpliedLeaves checks a request with no mask patches only its populated leaves: sibling
// fields of a set nested message survive, and a set but empty nested message changes nothing.
func TestFieldMaskImpliedLeaves(t *testing.T) {
	patch, err := configPatch(&pb.BankConfig{Recall: &pb.RecallConfig{Rerank: ptr(false)}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := patch(domain.BankConfig{Recall: &domain.RecallConfig{BudgetLow: ptr(50), RRFK: ptr(10)}})
	if err != nil {
		t.Fatal(err)
	}
	want := domain.BankConfig{Recall: &domain.RecallConfig{BudgetLow: ptr(50), RRFK: ptr(10), Rerank: ptr(false)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got.Recall, want.Recall)
	}
	empty, err := configPatch(&pb.BankConfig{Recall: &pb.RecallConfig{}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := empty(baseConfig()); !reflect.DeepEqual(got, baseConfig()) {
		t.Fatalf("empty nested message changed the config: %+v", got)
	}
	spec, err := specPatch(&pb.MentalModelSpec{Trigger: &pb.RefreshTrigger{RefreshCron: "0 * * * *"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cur := domain.MentalModelSpec{ID: "m", Tags: []string{"a"}, Trigger: domain.RefreshTrigger{
		TagsMatch: domain.TagsMatchAll, FactTypes: []domain.FactType{domain.FactTypeWorld},
	}}
	gotSpec, err := spec(cur)
	if err != nil {
		t.Fatal(err)
	}
	wantSpec := cur
	wantSpec.Trigger.RefreshCron = "0 * * * *"
	if !reflect.DeepEqual(gotSpec, wantSpec) {
		t.Fatalf("got  %+v\nwant %+v", gotSpec, wantSpec)
	}
}

// TestFieldMaskPathsResolve checks every maskable proto path of BankConfig and MentalModelSpec (through
// singular message fields) resolves to a domain field by JSON name, so no valid mask can fail at apply.
func TestFieldMaskPathsResolve(t *testing.T) {
	check := func(msg proto.Message, target any) {
		var walk func(md protoreflect.MessageDescriptor, prefix string)
		walk = func(md protoreflect.MessageDescriptor, prefix string) {
			for i := range md.Fields().Len() {
				fd := md.Fields().Get(i)
				path := prefix + string(fd.Name())
				if _, err := fieldByPath(reflect.ValueOf(target).Elem(), path, true); err != nil {
					t.Errorf("%s: %v", path, err)
				}
				if fd.Message() != nil && !fd.IsList() && !fd.IsMap() &&
					fd.Message().FullName() != "google.protobuf.Struct" {
					walk(fd.Message(), path+".")
				}
			}
		}
		walk(msg.ProtoReflect().Descriptor(), "")
	}
	check(&pb.BankConfig{}, &domain.BankConfig{})
	check(&pb.MentalModelSpec{}, &domain.MentalModelSpec{})
}

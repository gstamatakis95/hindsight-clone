package grpcapi

import (
	"errors"
	"memd/internal/domain"
	"reflect"
	"testing"

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
		Retain:       &domain.RetainConfig{Mission: "keep", EntityLabels: []domain.EntityLabel{}},
		Observations: &domain.ObservationConfig{DedupThreshold: ptr(0.9)},
		Recall:       &domain.RecallConfig{RRFK: ptr(60), RecencyAlpha: ptr(0.2)},
		Models: &domain.ModelBindings{
			Extract: &domain.ModelBinding{Model: "small", Temperature: ptr(0.3)},
		},
	}
}

// TestFieldMaskConfigPatch checks path replacement, clears, nested paths, whole model bindings and that
// untouched fields (including an explicit clear) survive.
func TestFieldMaskConfigPatch(t *testing.T) {
	tests := []struct {
		name  string
		patch *pb.BankConfig
		mask  *fieldmaskpb.FieldMask
		want  func(c *domain.BankConfig)
	}{
		{
			"replace scalar", &pb.BankConfig{Retain: &pb.RetainConfig{Mission: "new"}}, mask("retain.mission"),
			func(c *domain.BankConfig) { c.Retain.Mission = "new" },
		},
		{
			"unset value clears", &pb.BankConfig{}, mask("observations.dedup_threshold"),
			func(c *domain.BankConfig) { c.Observations.DedupThreshold = nil },
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
			"a binding is replaced whole",
			&pb.BankConfig{Models: &pb.ModelBindings{Extract: &pb.ModelBinding{Model: "big"}}}, mask("models.extract"),
			func(c *domain.BankConfig) { c.Models.Extract = &domain.ModelBinding{Model: "big"} },
		},
		{
			"an unset binding clears it", &pb.BankConfig{}, mask("models.extract"),
			func(c *domain.BankConfig) { c.Models.Extract = nil },
		},
		{
			"no mask: a binding is one leaf",
			&pb.BankConfig{Models: &pb.ModelBindings{
				Extract: &pb.ModelBinding{Model: "big"},
				Default: &pb.ModelBinding{Model: "d"},
			}}, nil,
			func(c *domain.BankConfig) {
				c.Models.Extract = &domain.ModelBinding{Model: "big"}
				c.Models.Default = &domain.ModelBinding{Model: "d"}
			},
		},
		{
			"empty nested list is an explicit clear", &pb.BankConfig{}, mask("recall.arms"),
			func(c *domain.BankConfig) {
				c.Recall = &domain.RecallConfig{RRFK: ptr(60), RecencyAlpha: ptr(0.2), Arms: []string{}}
			},
		},
		{
			"no mask = populated fields", &pb.BankConfig{Reflect: &pb.ReflectConfig{Mission: "r"}}, nil,
			func(c *domain.BankConfig) { c.Reflect = &domain.ReflectConfig{Mission: "r"} },
		},
		{
			"unmasked fields ignored",
			&pb.BankConfig{Retain: &pb.RetainConfig{Mission: "x"}, Reflect: &pb.ReflectConfig{Mission: "r"}},
			mask("reflect.mission"),
			func(c *domain.BankConfig) { c.Reflect = &domain.ReflectConfig{Mission: "r"} },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch, err := configPatch(tt.patch, tt.mask)
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

// TestFieldMaskInvalid checks invalid paths, paths inside a model binding and an embed binding below the server
// level are INVALID_ARGUMENT.
func TestFieldMaskInvalid(t *testing.T) {
	for _, m := range []*fieldmaskpb.FieldMask{
		mask("nope"), mask("recall.nope"), mask("models.slot"), mask("retain.mission.x"),
	} {
		if _, err := configPatch(&pb.BankConfig{}, m); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("mask %v: err = %v, want ErrInvalidArgument", m.GetPaths(), err)
		}
	}
	for name, p := range map[string]*pb.BankConfig{
		"path inside a binding":  {Models: &pb.ModelBindings{Extract: &pb.ModelBinding{Model: "m"}}},
		"embed below the server": {Models: &pb.ModelBindings{Embed: &pb.ModelBinding{Model: "e"}}},
	} {
		m := mask("models.extract.model")
		if name == "embed below the server" {
			m = nil
		}
		patch, err := configPatch(p, m)
		if err == nil {
			_, err = patch(domain.BankConfig{})
		}
		if !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("%s: err = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := specPatch(&pb.MentalModelSpec{Id: "x"}, mask("id")); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("id path: err = %v, want ErrInvalidArgument", err)
	}
}

// TestFieldMaskSpecPatch checks a spec patch, including a nested trigger path and RefreshOverrides paths
// mapped onto the stored config; the id is kept.
func TestFieldMaskSpecPatch(t *testing.T) {
	patch, err := specPatch(&pb.MentalModelSpec{
		Name: "new", Trigger: &pb.RefreshTrigger{RefreshCron: "0 * * * *"},
		Overrides: &pb.RefreshOverrides{
			Reflect: &pb.ReflectConfig{Mission: "m"}, MaxDeltaRatio: ptr(0.5),
			MentalModelModel: &pb.ModelBinding{Model: "cheap"},
		},
	}, mask("name", "trigger.refresh_cron", "tags", "overrides.reflect.mission", "overrides.max_delta_ratio",
		"overrides.mental_model_model"))
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
		Overrides: &domain.BankConfig{
			Reflect:      &domain.ReflectConfig{Mission: "m"},
			MentalModels: &domain.MentalModelConfig{MaxDeltaRatio: ptr(0.5)},
			Models:       &domain.ModelBindings{MentalModel: &domain.ModelBinding{Model: "cheap"}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

// TestFieldMaskImpliedLeaves checks a request with no mask patches only its populated leaves: sibling
// fields of a set nested message survive, and a set but empty nested message changes nothing.
func TestFieldMaskImpliedLeaves(t *testing.T) {
	patch, err := configPatch(&pb.BankConfig{Recall: &pb.RecallConfig{Rerank: ptr(false)}}, nil)
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
	empty, err := configPatch(&pb.BankConfig{Recall: &pb.RecallConfig{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := empty(baseConfig()); !reflect.DeepEqual(got, baseConfig()) {
		t.Fatalf("empty nested message changed the config: %+v", got)
	}
	spec, err := specPatch(&pb.MentalModelSpec{Trigger: &pb.RefreshTrigger{RefreshCron: "0 * * * *"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cur := domain.MentalModelSpec{ID: "m", Tags: []string{"a"}, Trigger: domain.RefreshTrigger{
		Tags: domain.TagFilter{Match: domain.TagsMatchAll}, FactTypes: []domain.FactType{domain.FactTypeWorld},
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
// singular message fields, stopping at model bindings) applies to the domain struct, so no valid mask can fail
// at apply.
func TestFieldMaskPathsResolve(t *testing.T) {
	var walk func(md protoreflect.MessageDescriptor, prefix string, out *[]string)
	walk = func(md protoreflect.MessageDescriptor, prefix string, out *[]string) {
		for i := range md.Fields().Len() {
			fd := md.Fields().Get(i)
			path := prefix + string(fd.Name())
			*out = append(*out, path)
			if fd.Message() != nil && !fd.IsList() && !fd.IsMap() && !leafMessages[fd.Message().FullName()] {
				walk(fd.Message(), path+".", out)
			}
		}
	}
	var cfgPaths, specPaths []string
	walk((&pb.BankConfig{}).ProtoReflect().Descriptor(), "", &cfgPaths)
	walk((&pb.MentalModelSpec{}).ProtoReflect().Descriptor(), "", &specPaths)
	for _, p := range cfgPaths {
		patch, err := configPatch(&pb.BankConfig{}, mask(p))
		if err == nil {
			_, err = patch(domain.BankConfig{})
		}
		if err != nil {
			t.Errorf("config %s: %v", p, err)
		}
	}
	for _, p := range specPaths {
		if p == "id" {
			continue
		}
		patch, err := specPatch(&pb.MentalModelSpec{}, mask(p))
		if err == nil {
			_, err = patch(domain.MentalModelSpec{})
		}
		if err != nil {
			t.Errorf("spec %s: %v", p, err)
		}
	}
}

package server

import (
	"fmt"
	"memd/internal/config"
	"memd/internal/domain"
	"reflect"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	pb "memd/gen/memory/v1"
)

// FieldMask patches (decision 604). Paths are proto field names, validated against the proto message;
// they are applied to the domain struct, whose JSON tags are the proto field names (decision 010), so a
// stored value that has no proto twin (a non-nil empty list = an explicit clear) survives patches of other
// fields. A masked path replaces the target with the request's value; an unset value clears it, and a
// masked list or map with no elements becomes a non-nil empty one (an explicit clear). An absent or empty
// mask means every populated leaf of the request: set singular messages are recursed into (a set but empty
// one yields no path), and lists, maps and Structs are leaves, so a sibling the request leaves unset is
// never touched. One exception (SPEC "Model routing", decision 604): with an implicit mask, models (and a
// spec's overrides.models) merge per slot into the stored bindings, so binding one slot never drops
// another; an explicit models path still replaces the whole list, which is how a client replaces or
// clears it.

// maskPaths validates mask against msg's type and returns its normalized paths; an empty mask yields
// msg's populated leaf paths. Paths in immutable are rejected.
func maskPaths(msg proto.Message, mask *fieldmaskpb.FieldMask, immutable ...string) ([]string, error) {
	if len(mask.GetPaths()) == 0 {
		var paths []string
		populatedLeaves(msg.ProtoReflect(), "", &paths)
		return dropImmutable(paths, immutable), nil
	}
	m := &fieldmaskpb.FieldMask{Paths: append([]string(nil), mask.GetPaths()...)}
	if !m.IsValid(msg) {
		return nil, fmt.Errorf("%w: invalid update_mask %v for %s", domain.ErrInvalidArgument, mask.GetPaths(),
			msg.ProtoReflect().Descriptor().FullName())
	}
	m.Normalize()
	for _, p := range m.GetPaths() {
		for _, im := range immutable {
			if p == im || strings.HasPrefix(p, im+".") {
				return nil, fmt.Errorf("%w: %s cannot be updated", domain.ErrInvalidArgument, p)
			}
		}
	}
	return m.GetPaths(), nil
}

// populatedLeaves appends the paths of m's populated leaves: it recurses into set singular messages and
// stops at lists, maps, Structs and scalars.
func populatedLeaves(m protoreflect.Message, prefix string, out *[]string) {
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		path := prefix + string(fd.Name())
		if fd.Message() != nil && !fd.IsList() && !fd.IsMap() && fd.Message().FullName() != "google.protobuf.Struct" {
			populatedLeaves(v.Message(), path+".", out)
		} else {
			*out = append(*out, path)
		}
		return true
	})
}

// dropImmutable removes immutable top-level paths from an implicit (populated-fields) mask.
func dropImmutable(paths, immutable []string) []string {
	out := paths[:0]
	for _, p := range paths {
		keep := true
		for _, im := range immutable {
			keep = keep && p != im
		}
		if keep {
			out = append(out, p)
		}
	}
	return out
}

// configPatch builds the ConfigPatch of an Update*Config request. check (nil = none) validates the
// patched level inside the patch and, with an implicit mask, the request level first: the per-slot merge
// would otherwise hide a slot the request binds twice.
func configPatch(patch *pb.BankConfig, mask *fieldmaskpb.FieldMask, check func(domain.BankConfig) error) (
	ConfigPatch, error,
) {
	if patch == nil {
		patch = &pb.BankConfig{}
	}
	paths, err := maskPaths(patch, mask)
	if err != nil {
		return nil, err
	}
	var d decoder
	src := d.config(patch)
	if d.err != nil {
		return nil, d.err
	}
	paths, merge := splitModels(paths, mask, "models")
	if merge && check != nil {
		if err := check(src); err != nil {
			return nil, err
		}
	}
	return func(cur domain.BankConfig) (domain.BankConfig, error) {
		out := cur
		if err := applyPaths(&out, &src, paths); err != nil {
			return domain.BankConfig{}, err
		}
		if merge {
			if err := mergeModelsAt(&out, &src, "models"); err != nil {
				return domain.BankConfig{}, err
			}
		}
		if check != nil {
			if err := check(out); err != nil {
				return domain.BankConfig{}, err
			}
		}
		return out, nil
	}, nil
}

// splitModels removes modelsPath from an implicit mask's paths and reports whether it was there, so the
// caller merges it per slot instead; an explicit mask's paths come back unchanged (a masked list
// replaces).
func splitModels(paths []string, mask *fieldmaskpb.FieldMask, modelsPath string) ([]string, bool) {
	if len(mask.GetPaths()) != 0 {
		return paths, false
	}
	rest := make([]string, 0, len(paths))
	found := false
	for _, p := range paths {
		if p == modelsPath {
			found = true
			continue
		}
		rest = append(rest, p)
	}
	return rest, found
}

// mergeModelsAt merges the slot bindings at path in src into those at path in dst, per slot (the
// config.Merge rule: src's binding of a slot replaces dst's, dst's other slots are kept). Pointer structs
// along the path are copied before the write, as in applyPaths, and the result shares no memory with
// either input.
func mergeModelsAt[T any](dst, src *T, path string) error {
	val, err := fieldByPath(reflect.ValueOf(src).Elem(), path, false)
	if err != nil || !val.IsValid() {
		return err
	}
	target, err := fieldByPath(reflect.ValueOf(dst).Elem(), path, true)
	if err != nil {
		return err
	}
	cur, okCur := target.Interface().([]domain.SlotBinding)
	add, okAdd := val.Interface().([]domain.SlotBinding)
	if !okCur || !okAdd {
		return fmt.Errorf("%w: path %q is not a model list", domain.ErrInvalidArgument, path)
	}
	merged := config.Merge(domain.BankConfig{Models: cur}, domain.BankConfig{Models: add}).Models
	target.Set(reflect.ValueOf(merged))
	return nil
}

// resetPatch builds the ConfigPatch of ResetBankConfig: the paths go back to unset (inherit); no paths =
// the zero config.
func resetPatch(paths []string) (ConfigPatch, error) {
	if len(paths) == 0 {
		return func(domain.BankConfig) (domain.BankConfig, error) { return domain.BankConfig{}, nil }, nil
	}
	norm, err := maskPaths(&pb.BankConfig{}, &fieldmaskpb.FieldMask{Paths: paths})
	if err != nil {
		return nil, err
	}
	return func(cur domain.BankConfig) (domain.BankConfig, error) {
		out := cur
		if err := applyPaths(&out, nil, norm); err != nil {
			return domain.BankConfig{}, err
		}
		return out, nil
	}, nil
}

// specPatch builds the SpecPatch of UpdateMentalModel; the id cannot be changed. With an implicit mask,
// overrides.models merges per slot, and check (nil = none) first validates the request's overrides (the
// caller validates the patched spec's).
func specPatch(patch *pb.MentalModelSpec, mask *fieldmaskpb.FieldMask, check func(domain.BankConfig) error) (
	SpecPatch, error,
) {
	if patch == nil {
		patch = &pb.MentalModelSpec{}
	}
	paths, err := maskPaths(patch, mask, "id")
	if err != nil {
		return nil, err
	}
	var d decoder
	src := d.spec(patch)
	if d.err != nil {
		return nil, d.err
	}
	paths, merge := splitModels(paths, mask, "overrides.models")
	if merge && check != nil && src.Overrides != nil {
		if err := check(*src.Overrides); err != nil {
			return nil, err
		}
	}
	return func(cur domain.MentalModelSpec) (domain.MentalModelSpec, error) {
		out := cur
		if err := applyPaths(&out, &src, paths); err != nil {
			return domain.MentalModelSpec{}, err
		}
		if merge {
			if err := mergeModelsAt(&out, &src, "overrides.models"); err != nil {
				return domain.MentalModelSpec{}, err
			}
		}
		return out, nil
	}, nil
}

// applyPaths copies each path from src into dst (both pointers to the same domain struct type); src nil
// resets the paths to their zero value. Pointer structs along a path are copied before they are written,
// so values shared with the caller's original are never mutated.
func applyPaths[T any](dst, src *T, paths []string) error {
	for _, p := range paths {
		target, err := fieldByPath(reflect.ValueOf(dst).Elem(), p, true)
		if err != nil {
			return err
		}
		if src == nil {
			target.Set(reflect.Zero(target.Type()))
			continue
		}
		val, err := fieldByPath(reflect.ValueOf(src).Elem(), p, false)
		if err != nil {
			return err
		}
		target.Set(explicit(val, target.Type()))
	}
	return nil
}

// explicit returns v, turning an empty list or map into a non-nil empty one (an explicit clear); an
// invalid v (a nil parent in the patch) is the zero value of t.
func explicit(v reflect.Value, t reflect.Type) reflect.Value {
	if !v.IsValid() {
		v = reflect.Zero(t)
	}
	switch {
	case t.Kind() == reflect.Slice && v.Len() == 0:
		return reflect.MakeSlice(t, 0, 0)
	case t.Kind() == reflect.Map && v.Len() == 0:
		return reflect.MakeMap(t)
	default:
		return v
	}
}

// fieldByPath walks a dotted path of JSON field names through v. With write set, each pointer struct it
// passes through is replaced by a fresh copy (allocated when nil) so the caller can set the result;
// without it, a nil pointer ends the walk with an invalid Value.
func fieldByPath(v reflect.Value, path string, write bool) (reflect.Value, error) {
	parts := strings.Split(path, ".")
	for i, name := range parts {
		if v.Kind() == reflect.Pointer {
			if !write && v.IsNil() {
				return reflect.Value{}, nil
			}
			if write {
				fresh := reflect.New(v.Type().Elem())
				if !v.IsNil() {
					fresh.Elem().Set(v.Elem())
				}
				v.Set(fresh)
			}
			v = v.Elem()
		}
		f, ok := jsonField(v, name)
		if !ok {
			return reflect.Value{}, fmt.Errorf("%w: path %q has no domain field at %q", domain.ErrInvalidArgument,
				path, strings.Join(parts[:i+1], "."))
		}
		v = f
	}
	return v, nil
}

// jsonField returns the field of struct v whose JSON name is name.
func jsonField(v reflect.Value, name string) (reflect.Value, bool) {
	if v.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	t := v.Type()
	for i := range t.NumField() {
		tag, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if tag == name {
			return v.Field(i), true
		}
	}
	return reflect.Value{}, false
}

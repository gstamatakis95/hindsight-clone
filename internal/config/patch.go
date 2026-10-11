package config

import (
	"fmt"
	"memd/internal/domain"
	"reflect"
	"strings"
)

// Paths are FieldMask paths: proto field names, which are the json tags of the domain structs, dotted into
// nested messages ("recall.rrf_k"). Lists, maps and model bindings are leaves: "models.extract" is one path,
// and no path goes inside a binding.

// ResetAll is the reset path that resets every field.
const ResetAll = "*"

// ApplyPaths copies each path from src into dst (pointers to the same domain struct: a BankConfig, or a struct
// holding one such as a MentalModelSpec). A masked path replaces the target with src's value; an unset value
// clears it, and an empty list or map becomes a non-nil empty one (an explicit clear). src nil resets the paths
// to unset. Pointer structs along a path are copied before they are written, so values shared with the caller's
// original are never mutated. An unknown path, or one inside a model binding, is domain.ErrInvalidArgument.
func ApplyPaths[T any](dst, src *T, paths []string) error {
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
		target.Set(explicit(deepCopyValue(val), target.Type()))
	}
	return nil
}

// ResetPaths returns cur with paths unset, so they inherit again. paths must not be empty (resetting
// everything by omission is too easy a mistake); [ResetAll] resets every field. An unknown path is
// domain.ErrInvalidArgument.
func ResetPaths(cur domain.BankConfig, paths []string) (domain.BankConfig, error) {
	if len(paths) == 0 {
		return domain.BankConfig{}, domain.InvalidField("reset_mask", "reset needs at least one path (%q resets everything)",
			ResetAll)
	}
	for _, p := range paths {
		if p == ResetAll {
			return domain.BankConfig{}, nil
		}
	}
	out := cur
	if err := ApplyPaths(&out, nil, paths); err != nil {
		return domain.BankConfig{}, err
	}
	return out, nil
}

// deepCopyValue copies v unless it is invalid (a nil parent in the patch).
func deepCopyValue(v reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}
	return deepCopy(v)
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
		if v.Type() == modelBindingType {
			return reflect.Value{}, fmt.Errorf("%w: path %q goes inside a model binding; a binding is set whole",
				domain.ErrInvalidArgument, path)
		}
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
			return reflect.Value{}, fmt.Errorf("%w: unknown config path %q at %q", domain.ErrInvalidArgument,
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
		if tag == name && t.Field(i).IsExported() {
			return v.Field(i), true
		}
	}
	return reflect.Value{}, false
}

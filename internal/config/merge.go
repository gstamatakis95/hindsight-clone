package config

import (
	"memd/internal/domain"
	"reflect"
)

// One reflection walker serves Merge, ApplyMask, ResetPaths and SetPaths, so "is set" means the same
// thing everywhere (decision 010): a pointer, slice or map is set when non-nil (a non-nil empty list is
// an explicit clear), a plain string or number when non-zero. A pointer to a struct is a nested message
// that merges field by field; BankConfig.Models merges per slot; every other field is a leaf.

var slotBindingsType = reflect.TypeFor[[]domain.SlotBinding]()

// Merge combines levels from least to most specific (server, tenant, bank, strategy, request, item):
// a set field replaces the field below it, nested messages merge field by field, models merge per slot
// (the most specific level that binds a slot supplies its whole binding) and other lists and maps are
// replaced, never appended. The result shares no memory with the inputs, which are left untouched.
func Merge(levels ...domain.BankConfig) domain.BankConfig {
	var out domain.BankConfig
	dst := reflect.ValueOf(&out).Elem()
	for i := range levels {
		mergeStruct(dst, reflect.ValueOf(levels[i]))
	}
	return out
}

// mergeStruct merges every field of src into dst (both of the same struct type).
func mergeStruct(dst, src reflect.Value) {
	for i := range src.NumField() {
		d, s := dst.Field(i), src.Field(i)
		switch {
		case s.Type() == slotBindingsType:
			mergeModels(d, s)
		case isMessage(s.Type()):
			if s.IsNil() {
				continue
			}
			if d.IsNil() {
				d.Set(reflect.New(s.Type().Elem()))
			}
			mergeStruct(d.Elem(), s.Elem())
		case isSet(s):
			d.Set(deepCopy(s))
		}
	}
}

// mergeModels replaces dst's binding for every slot src binds and appends slots dst lacks.
func mergeModels(dst, src reflect.Value) {
	if src.IsNil() {
		return
	}
	out, _ := dst.Interface().([]domain.SlotBinding)
	if out == nil {
		out = []domain.SlotBinding{}
	}
	for _, sb := range src.Interface().([]domain.SlotBinding) {
		cp, _ := deepCopy(reflect.ValueOf(sb)).Interface().(domain.SlotBinding)
		out = upsertSlot(out, cp)
	}
	dst.Set(reflect.ValueOf(out))
}

// upsertSlot replaces the binding of sb.Slot in list, or appends it.
func upsertSlot(list []domain.SlotBinding, sb domain.SlotBinding) []domain.SlotBinding {
	for i := range list {
		if list[i].Slot == sb.Slot {
			list[i] = sb
			return list
		}
	}
	return append(list, sb)
}

// isMessage reports whether t is a nested config message (a pointer to a struct).
func isMessage(t reflect.Type) bool {
	return t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct
}

// isSet reports whether a leaf value is set.
func isSet(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
		return !v.IsNil()
	default:
		return !v.IsZero()
	}
}

// deepCopy returns a copy of v that shares no pointers, slices or maps with it. Nil stays nil and a
// non-nil empty slice or map stays non-nil and empty.
func deepCopy(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		n := reflect.New(v.Type().Elem())
		n.Elem().Set(deepCopy(v.Elem()))
		return n
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		n := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			n.Index(i).Set(deepCopy(v.Index(i)))
		}
		return n
	case reflect.Map:
		return copyMap(v)
	case reflect.Struct:
		n := reflect.New(v.Type()).Elem()
		n.Set(v)
		for i := range n.NumField() {
			if n.Field(i).CanSet() {
				n.Field(i).Set(deepCopy(v.Field(i)))
			}
		}
		return n
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		n := reflect.New(v.Type()).Elem()
		n.Set(deepCopy(v.Elem()))
		return n
	default:
		return v
	}
}

// copyMap deep-copies a map value.
func copyMap(v reflect.Value) reflect.Value {
	if v.IsNil() {
		return reflect.Zero(v.Type())
	}
	n := reflect.MakeMapWithSize(v.Type(), v.Len())
	it := v.MapRange()
	for it.Next() {
		n.SetMapIndex(it.Key(), deepCopy(it.Value()))
	}
	return n
}

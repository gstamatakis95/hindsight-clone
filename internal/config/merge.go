package config

import (
	"memd/internal/domain"
	"reflect"
)

// One reflection walker serves Merge and the FieldMask paths, so "is set" means the same thing
// everywhere: a pointer, slice or map is set when non-nil (a non-nil empty list is an explicit clear), a
// plain string or number when non-zero. A pointer to a struct is a nested message that merges field by
// field, except a *domain.ModelBinding, which is an atomic leaf; every other field is a leaf.

var modelBindingType = reflect.TypeFor[*domain.ModelBinding]()

// Merge combines levels from least to most specific (server, tenant, org, bank, strategy, request, item):
// a set field replaces the field below it, nested messages merge field by field, a model binding is
// replaced whole, and lists and maps are replaced, never appended. Each level's models are flattened
// first (every chat-model slot the level leaves unset takes the level's default), so for each slot the
// most specific level that yields a binding, its own or its default, wins. The result shares no memory
// with the inputs, which are left untouched.
func Merge(levels ...domain.BankConfig) domain.BankConfig {
	var out domain.BankConfig
	dst := reflect.ValueOf(&out).Elem()
	for i := range levels {
		mergeStruct(dst, reflect.ValueOf(flatten(levels[i])))
	}
	return out
}

// flatten returns c with each chat-model slot its models leave unset bound to its models' default (a
// shallow copy; mergeStruct copies what it keeps).
func flatten(c domain.BankConfig) domain.BankConfig {
	if c.Models == nil || c.Models.Default == nil {
		return c
	}
	m := *c.Models
	for _, s := range domain.LLMSlots {
		if f := m.Slot(s); *f == nil {
			*f = m.Default
		}
	}
	c.Models = &m
	return c
}

// mergeStruct merges every field of src into dst (both of the same struct type).
func mergeStruct(dst, src reflect.Value) {
	for i := range src.NumField() {
		d, s := dst.Field(i), src.Field(i)
		switch {
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

// isMessage reports whether t is a nested config message: a pointer to a struct other than a model
// binding, which is atomic.
func isMessage(t reflect.Type) bool {
	return t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct && t != modelBindingType
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

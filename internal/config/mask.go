package config

import (
	"fmt"
	"memd/internal/domain"
	"reflect"
	"sort"
	"strings"
)

// Paths are FieldMask paths: proto field names (which are the json tags of domain.BankConfig), dotted
// into nested messages ("recall.rrf_k"). Lists and maps are leaves; "models" is one path.

var bankConfigType = reflect.TypeFor[domain.BankConfig]()

// jsonName returns the proto field name of a struct field (its json tag without options).
func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "" {
		return f.Name
	}
	return name
}

// fieldIndex resolves a dotted path to the chain of struct field indexes, or ErrInvalidArgument.
func fieldIndex(path string) ([]int, error) {
	t := bankConfigType
	var idx []int
	parts := strings.Split(path, ".")
	for i, part := range parts {
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return nil, fmt.Errorf("%w: config path %q: %q is not a message", domain.ErrInvalidArgument, path, parts[i-1])
		}
		f, ok := fieldByJSON(t, part)
		if !ok {
			return nil, fmt.Errorf("%w: unknown config path %q", domain.ErrInvalidArgument, path)
		}
		idx = append(idx, f.Index[0])
		t = f.Type
	}
	return idx, nil
}

// fieldByJSON finds the field of struct type t whose proto name is name.
func fieldByJSON(t reflect.Type, name string) (reflect.StructField, bool) {
	for i := range t.NumField() {
		if f := t.Field(i); f.IsExported() && jsonName(f) == name {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// fieldAt walks idx from v (a BankConfig value). With alloc it allocates nil nested messages and
// returns a settable field; without it a nil message on the way yields the zero value of the leaf.
func fieldAt(v reflect.Value, idx []int, alloc bool) reflect.Value {
	for i, j := range idx {
		v = v.Field(j)
		if i == len(idx)-1 {
			return v
		}
		if v.IsNil() {
			if !alloc {
				return reflect.Zero(leafType(v.Type().Elem(), idx[i+1:]))
			}
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	return v
}

// leafType is the type reached from struct type t by idx.
func leafType(t reflect.Type, idx []int) reflect.Type {
	for _, j := range idx {
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		t = t.Field(j).Type
	}
	return t
}

// ApplyMask patches base with patch the way UpdateBankConfig and UpdateTenantConfig do: every masked
// path takes patch's value, whatever it is. A masked scalar or message that patch leaves unset becomes
// unset (inherit from the level below); a masked list or map that patch leaves empty becomes an explicit
// empty value, which clears the inherited one (the only way to clear a list). An empty mask merges patch
// over base (set fields replace). Unknown paths are domain.ErrInvalidArgument. Inputs are not modified.
func ApplyMask(base, patch domain.BankConfig, paths []string) (domain.BankConfig, error) {
	if len(paths) == 0 {
		return Merge(base, patch), nil
	}
	out := Merge(base)
	dst, src := reflect.ValueOf(&out).Elem(), reflect.ValueOf(patch)
	for _, p := range paths {
		idx, err := fieldIndex(p)
		if err != nil {
			return domain.BankConfig{}, err
		}
		val := deepCopy(fieldAt(src, idx, false))
		switch val.Kind() {
		case reflect.Slice:
			if val.IsNil() {
				val = reflect.MakeSlice(val.Type(), 0, 0)
			}
		case reflect.Map:
			if val.IsNil() {
				val = reflect.MakeMap(val.Type())
			}
		}
		fieldAt(dst, idx, true).Set(val)
	}
	pruneEmpty(dst)
	return out, nil
}

// ResetPaths removes the given overrides from cfg so they inherit again (ResetBankConfig); no paths
// resets everything. Unknown paths are domain.ErrInvalidArgument. cfg is not modified.
func ResetPaths(cfg domain.BankConfig, paths []string) (domain.BankConfig, error) {
	if len(paths) == 0 {
		return domain.BankConfig{}, nil
	}
	out := Merge(cfg)
	dst := reflect.ValueOf(&out).Elem()
	for _, p := range paths {
		idx, err := fieldIndex(p)
		if err != nil {
			return domain.BankConfig{}, err
		}
		f := fieldAt(dst, idx, true)
		f.Set(reflect.Zero(f.Type()))
	}
	pruneEmpty(dst)
	return out, nil
}

// pruneEmpty sets nested messages with no set field back to nil, recursively.
func pruneEmpty(v reflect.Value) bool {
	empty := true
	for i := range v.NumField() {
		f := v.Field(i)
		if isMessage(f.Type()) && !f.IsNil() && pruneEmpty(f.Elem()) {
			f.Set(reflect.Zero(f.Type()))
		}
		if isSet(f) {
			empty = false
		}
	}
	return empty
}

// SetPaths lists the leaf paths cfg sets, sorted ("models" counts as one leaf).
func SetPaths(cfg domain.BankConfig) []string {
	var out []string
	collectSet(reflect.ValueOf(cfg), "", &out)
	sort.Strings(out)
	return out
}

func collectSet(v reflect.Value, prefix string, out *[]string) {
	t := v.Type()
	for i := range v.NumField() {
		f := v.Field(i)
		name := prefix + jsonName(t.Field(i))
		switch {
		case isMessage(f.Type()) && !f.IsNil():
			collectSet(f.Elem(), name+".", out)
		case !isMessage(f.Type()) && isSet(f):
			*out = append(*out, name)
		}
	}
}

// LeafPaths lists every leaf path of BankConfig, sorted; it is what SetPaths can return.
func LeafPaths() []string {
	var out []string
	collectLeaves(bankConfigType, "", &out)
	sort.Strings(out)
	return out
}

func collectLeaves(t reflect.Type, prefix string, out *[]string) {
	for i := range t.NumField() {
		f := t.Field(i)
		name := prefix + jsonName(f)
		if isMessage(f.Type) {
			collectLeaves(f.Type.Elem(), name+".", out)
			continue
		}
		*out = append(*out, name)
	}
}

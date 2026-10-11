package config

import (
	"fmt"
	"memd/internal/domain"
	"reflect"
	"sort"
	"strings"
	"testing"
)

var bankConfigType = reflect.TypeFor[domain.BankConfig]()

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

// leafPaths lists every leaf path of BankConfig, sorted; it is what setPaths can return.
func leafPaths() []string {
	var out []string
	collectLeaves(bankConfigType, "", &out)
	sort.Strings(out)
	return out
}

// collectLeaves appends the leaf paths of struct type t, each prefixed with prefix.
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

// TestSetPaths checks the leaf paths a config sets: an explicit empty list counts, an empty nested message
// does not, and "models" is one leaf.
func TestSetPaths(t *testing.T) {
	cfg := domain.BankConfig{
		RetainMission: "m",
		EntityLabels:  []domain.EntityLabel{},
		Recall:        &domain.RecallConfig{RRFK: ptr(1)},
		Reflect:       &domain.ReflectConfig{},
		Models:        []domain.SlotBinding{bind(domain.ModelSlotExtract, "e")},
	}
	want := []string{"entity_labels", "models", "recall.rrf_k", "retain_mission"}
	if got := setPaths(cfg); !reflect.DeepEqual(got, want) {
		t.Fatalf("setPaths = %v, want %v", got, want)
	}
}

package config

import (
	"memd/internal/domain"
	"reflect"
	"sort"
	"strings"
)

// Paths are FieldMask paths: proto field names (which are the json tags of domain.BankConfig), dotted
// into nested messages ("recall.rrf_k"). Lists and maps are leaves; "models" is one path.

// jsonName returns the proto field name of a struct field (its json tag without options).
func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "" {
		return f.Name
	}
	return name
}

// setPaths lists the leaf paths cfg sets, sorted ("models" counts as one leaf).
func setPaths(cfg domain.BankConfig) []string {
	var out []string
	collectSet(reflect.ValueOf(cfg), "", &out)
	sort.Strings(out)
	return out
}

// collectSet appends the set leaf paths of struct v, each prefixed with prefix.
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

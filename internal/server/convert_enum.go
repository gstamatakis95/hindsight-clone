package server

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"memd/internal/domain"
)

// Conversion conventions (decision 603):
//   - proto -> domain goes through a decoder, which records the first error (an unknown enum number or an
//     invalid timestamp, both INVALID_ARGUMENT) and returns zero values after it, so converters stay
//     branch-free. Top-level xFromProto functions return (value, d.err).
//   - domain -> proto never fails; an unknown domain enum string becomes *_UNSPECIFIED.
//   - Conversion is structural only: no tag normalization, defaults or business validation (engines own
//     those).
//   - Enums: a domain enum string is the lowercase proto value name without the enum's prefix (decision
//     010), so one descriptor-driven pair of helpers serves every enum.
//   - A zero time.Time is a nil Timestamp; a nil *time.Time is a nil Timestamp.

// decoder accumulates the first error of a proto -> domain conversion.
type decoder struct {
	// err is the first error met; later errors are dropped.
	err error
}

// fail records err unless an earlier error is already recorded.
func (d *decoder) fail(err error) {
	if d.err == nil {
		d.err = err
	}
}

// protoEnum is a generated proto enum type.
type protoEnum interface {
	~int32
	// Descriptor returns the enum's descriptor.
	Descriptor() protoreflect.EnumDescriptor
}

// enumPrefix is the value-name prefix of an enum: its zero value's name without "UNSPECIFIED".
func enumPrefix(ed protoreflect.EnumDescriptor) string {
	return strings.TrimSuffix(string(ed.Values().ByNumber(0).Name()), "UNSPECIFIED")
}

// enumName returns the domain string of proto enum number n of ed, and false for an undefined number.
func enumName(ed protoreflect.EnumDescriptor, n protoreflect.EnumNumber) (string, bool) {
	if n == 0 {
		return "", true
	}
	v := ed.Values().ByNumber(n)
	if v == nil {
		return "", false
	}
	return strings.ToLower(strings.TrimPrefix(string(v.Name()), enumPrefix(ed))), true
}

// enumNumber returns the proto enum number of domain string s in ed; unknown or empty = 0.
func enumNumber(ed protoreflect.EnumDescriptor, s string) protoreflect.EnumNumber {
	if s == "" {
		return 0
	}
	v := ed.Values().ByName(protoreflect.Name(enumPrefix(ed) + strings.ToUpper(s)))
	if v == nil {
		return 0
	}
	return v.Number()
}

// fromEnum converts a proto enum to its domain string; an undefined number fails the decoder.
func fromEnum[T ~string, E protoEnum](d *decoder, e E) T {
	ed := e.Descriptor()
	s, ok := enumName(ed, protoreflect.EnumNumber(e))
	if !ok {
		d.fail(fmt.Errorf("%w: unknown %s value %d", domain.ErrInvalidArgument, ed.FullName(), int32(e)))
	}
	return T(s)
}

// toEnum converts a domain enum string to the proto enum E; unknown = *_UNSPECIFIED.
func toEnum[E protoEnum, T ~string](s T) E {
	var zero E
	return E(enumNumber(zero.Descriptor(), string(s)))
}

// fromEnums converts a repeated proto enum; nil stays nil.
func fromEnums[T ~string, E protoEnum](d *decoder, es []E) []T {
	if es == nil {
		return nil
	}
	out := make([]T, len(es))
	for i, e := range es {
		out[i] = fromEnum[T](d, e)
	}
	return out
}

// toEnums converts domain enum strings to a repeated proto enum; nil stays nil.
func toEnums[E protoEnum, T ~string](ts []T) []E {
	if ts == nil {
		return nil
	}
	out := make([]E, len(ts))
	for i, t := range ts {
		out[i] = toEnum[E](t)
	}
	return out
}

// tsFromProto converts a Timestamp; nil = zero time, an invalid one fails the decoder.
func (d *decoder) ts(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}
	if err := ts.CheckValid(); err != nil {
		d.fail(fmt.Errorf("%w: %w", domain.ErrInvalidArgument, err))
		return time.Time{}
	}
	return ts.AsTime()
}

// tsPtr converts a Timestamp to *time.Time; nil = nil.
func (d *decoder) tsPtr(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := d.ts(ts)
	return &t
}

// tsToProto converts a time; zero = nil.
func tsToProto(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// tsPtrToProto converts a *time.Time; nil = nil.
func tsPtrToProto(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

// i32 converts an int to int32, clamped to the int32 range.
func i32(v int) int32 {
	switch {
	case v > math.MaxInt32:
		return math.MaxInt32
	case v < math.MinInt32:
		return math.MinInt32
	default:
		return int32(v)
	}
}

// optInt converts an optional int32 to *int; nil = nil.
func optInt(p *int32) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

// optInt32 converts a *int to an optional int32 (clamped); nil = nil.
func optInt32(p *int) *int32 {
	if p == nil {
		return nil
	}
	v := i32(*p)
	return &v
}

// optCopy copies an optional scalar so domain and proto values never share memory; nil = nil.
func optCopy[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// strs converts a slice of string-kinded values; nil stays nil.
func strs[To, From ~string](in []From) []To {
	if in == nil {
		return nil
	}
	out := make([]To, len(in))
	for i, v := range in {
		out[i] = To(v)
	}
	return out
}

// mapFromProto converts a proto map[string]V to a map keyed by a string-kinded domain type; nil stays nil.
func mapFromProto[K ~string, V any](in map[string]V) map[K]V {
	if in == nil {
		return nil
	}
	out := make(map[K]V, len(in))
	for k, v := range in {
		out[K(k)] = v
	}
	return out
}

// mapToProto converts a map keyed by a string-kinded domain type to a proto map[string]V; nil stays nil.
func mapToProto[K ~string, V any](in map[K]V) map[string]V {
	if in == nil {
		return nil
	}
	out := make(map[string]V, len(in))
	for k, v := range in {
		out[string(k)] = v
	}
	return out
}

// structFromProto converts a Struct to a map; nil = nil, an empty Struct = an empty non-nil map.
func structFromProto(s *structpb.Struct) map[string]any {
	if s == nil {
		return nil
	}
	return s.AsMap()
}

// structToProto converts a map to a Struct; nil = nil. A map holding non-JSON values yields nil.
func structToProto(m map[string]any) *structpb.Struct {
	if m == nil {
		return nil
	}
	s, err := structpb.NewStruct(m)
	if err != nil {
		return nil
	}
	return s
}

// jsonFromStruct encodes a Struct as JSON; nil = nil.
func (d *decoder) jsonFromStruct(s *structpb.Struct) json.RawMessage {
	if s == nil {
		return nil
	}
	b, err := protojson.Marshal(s)
	if err != nil {
		d.fail(fmt.Errorf("%w: %w", domain.ErrInvalidArgument, err))
		return nil
	}
	return b
}

// jsonToStruct decodes a JSON object into a Struct; empty or non-object JSON = nil.
func jsonToStruct(raw json.RawMessage) *structpb.Struct {
	if len(raw) == 0 {
		return nil
	}
	s := &structpb.Struct{}
	if err := protojson.Unmarshal(raw, s); err != nil {
		return nil
	}
	return s
}

// rawFromString converts a JSON string field to RawMessage; "" = nil.
func rawFromString(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

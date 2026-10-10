package server

import (
	"math/rand/v2"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"memd/internal/domain"
)

// maxDepth bounds message nesting in generated values (TagGroup.none_of and EntityLabel.fields recurse).
const maxDepth = 5

// fieldHook generates the whole value of one field (a list for a repeated field).
type fieldHook func(g *gen, m protoreflect.Message, fd protoreflect.FieldDescriptor)

// fieldHooks generate fields whose strings the converter parses, so random values must be canonical.
var fieldHooks = map[protoreflect.FullName]fieldHook{
	"memory.v1.StrategyMatch.matched_scopes":  scopeStrings,
	"memory.v1.StrategyMatch.shadowed_scopes": scopeStrings,
}

// scopeStrings fills a repeated string field with length-prefixed scope encodings.
func scopeStrings(g *gen, m protoreflect.Message, fd protoreflect.FieldDescriptor) {
	l := m.Mutable(fd).List()
	for range g.r.IntN(4) {
		tags := make([]string, g.r.IntN(3))
		for i := range tags {
			tags[i] = g.str()
		}
		l.Append(protoreflect.ValueOfString(domain.EncodeScopeTags(tags)))
	}
}

// gen fills proto messages with random, convertible values: defined enum numbers only, exactly one oneof
// branch (or none at the depth limit), valid timestamps away from the zero time, JSON-safe Struct values,
// and at least one populated field in every message it sets (so "empty message" and "unset" never
// collide where the domain keeps a value, not a pointer).
type gen struct {
	// r is the deterministic source.
	r *rand.Rand
}

// newGen returns a generator seeded with seed.
func newGen(seed uint64) *gen {
	return &gen{r: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))}
}

// message returns a new random message of m's type.
func (g *gen) message(m proto.Message) proto.Message {
	out := m.ProtoReflect().New()
	g.fill(out, 0)
	return out.Interface()
}

// fill populates m at nesting depth.
func (g *gen) fill(m protoreflect.Message, depth int) {
	md := m.Descriptor()
	switch md.FullName() {
	case "google.protobuf.Timestamp":
		g.timestamp(m)
		return
	case "google.protobuf.Struct":
		proto.Merge(m.Interface(), g.structValue(depth))
		return
	}
	for i := range md.Oneofs().Len() {
		od := md.Oneofs().Get(i)
		if depth < maxDepth && !od.IsSynthetic() {
			g.field(m, od.Fields().Get(g.r.IntN(od.Fields().Len())), depth)
		}
	}
	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() {
			continue
		}
		if g.r.IntN(10) < 7 {
			g.field(m, fd, depth)
		}
	}
	g.ensureNonEmpty(m, depth)
}

// ensureNonEmpty populates one scalar field when m ended up empty.
func (g *gen) ensureNonEmpty(m protoreflect.Message, depth int) {
	empty := true
	m.Range(func(protoreflect.FieldDescriptor, protoreflect.Value) bool {
		empty = false
		return false
	})
	if !empty {
		return
	}
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		od := fd.ContainingOneof()
		if fd.Message() == nil && !fd.IsList() && !fd.IsMap() && (od == nil || od.IsSynthetic()) {
			for !m.Has(fd) {
				g.field(m, fd, depth)
			}
			return
		}
	}
}

// field sets one field of m to a random value.
func (g *gen) field(m protoreflect.Message, fd protoreflect.FieldDescriptor, depth int) {
	if hook, ok := fieldHooks[fd.FullName()]; ok {
		hook(g, m, fd)
		return
	}
	switch {
	case fd.IsMap():
		g.mapField(m, fd, depth)
	case fd.IsList():
		if fd.Message() != nil && depth >= maxDepth {
			return
		}
		l := m.Mutable(fd).List()
		for range g.r.IntN(4) {
			if fd.Message() != nil {
				v := l.NewElement()
				g.fill(v.Message(), depth+1)
				l.Append(v)
			} else {
				l.Append(g.scalar(fd))
			}
		}
	case fd.Message() != nil:
		if depth >= maxDepth {
			return
		}
		g.fill(m.Mutable(fd).Message(), depth+1)
	default:
		m.Set(fd, g.scalar(fd))
	}
}

// mapField fills a map field with up to three entries.
func (g *gen) mapField(m protoreflect.Message, fd protoreflect.FieldDescriptor, depth int) {
	if fd.MapValue().Message() != nil && depth >= maxDepth {
		return
	}
	mp := m.Mutable(fd).Map()
	for range g.r.IntN(4) {
		k := g.scalar(fd.MapKey()).MapKey()
		if fd.MapValue().Message() != nil {
			v := mp.NewValue()
			g.fill(v.Message(), depth+1)
			mp.Set(k, v)
		} else {
			mp.Set(k, g.scalar(fd.MapValue()))
		}
	}
}

// scalar returns a random value of a non-message field kind.
func (g *gen) scalar(fd protoreflect.FieldDescriptor) protoreflect.Value {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(g.r.IntN(2) == 1)
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return protoreflect.ValueOfInt32(g.r.Int32N(20001) - 10000)
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return protoreflect.ValueOfInt64(g.r.Int64N(2_000_000_001) - 1_000_000_000)
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return protoreflect.ValueOfUint32(g.r.Uint32())
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return protoreflect.ValueOfUint64(g.r.Uint64())
	case protoreflect.DoubleKind:
		return protoreflect.ValueOfFloat64(g.r.Float64()*200 - 100)
	case protoreflect.FloatKind:
		return protoreflect.ValueOfFloat32(g.r.Float32())
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(g.str())
	case protoreflect.BytesKind:
		return protoreflect.ValueOfBytes([]byte(g.str()))
	case protoreflect.EnumKind:
		vals := fd.Enum().Values()
		return protoreflect.ValueOfEnum(vals.Get(g.r.IntN(vals.Len())).Number())
	default:
		panic("unsupported kind " + fd.Kind().String())
	}
}

// alphabet mixes ASCII, punctuation the encodings use and non-ASCII letters.
const alphabet = "abcXYZ019 _-:,.é日本🙂"

// str returns a random string of up to eight runes.
func (g *gen) str() string {
	runes := []rune(alphabet)
	var b strings.Builder
	for range g.r.IntN(9) {
		b.WriteRune(runes[g.r.IntN(len(runes))])
	}
	return b.String()
}

// timestamp sets a valid Timestamp between 2000 and 2040.
func (g *gen) timestamp(m protoreflect.Message) {
	base := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	ts := &timestamppb.Timestamp{Seconds: base + g.r.Int64N(40*365*24*3600), Nanos: g.r.Int32N(1_000_000_000)}
	proto.Merge(m.Interface(), ts)
}

// structValue returns a random JSON-safe Struct (finite numbers only).
func (g *gen) structValue(depth int) *structpb.Struct {
	s := &structpb.Struct{Fields: map[string]*structpb.Value{}}
	for range g.r.IntN(4) {
		s.Fields[g.str()] = g.jsonValue(depth + 1)
	}
	return s
}

// jsonValue returns a random JSON value.
func (g *gen) jsonValue(depth int) *structpb.Value {
	n := 4
	if depth < maxDepth {
		n = 6
	}
	switch g.r.IntN(n) {
	case 0:
		return structpb.NewNullValue()
	case 1:
		return structpb.NewBoolValue(g.r.IntN(2) == 1)
	case 2:
		return structpb.NewNumberValue(float64(g.r.Int64N(1_000_000)) / 8)
	case 3:
		return structpb.NewStringValue(g.str())
	case 4:
		return structpb.NewStructValue(g.structValue(depth))
	default:
		l := &structpb.ListValue{}
		for range g.r.IntN(3) {
			l.Values = append(l.Values, g.jsonValue(depth+1))
		}
		return structpb.NewListValue(l)
	}
}

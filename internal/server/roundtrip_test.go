package server

import (
	"hash/fnv"
	"slices"
	"testing"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	pb "memd/gen/memory/v1"
)

// roundTrip converts a proto message to its domain (or engine, or server) counterpart and back.
type roundTrip struct {
	// rt converts proto -> domain -> proto.
	rt func(proto.Message) (proto.Message, error)
	// norm adjusts a clone of the input for documented lossy spots before comparing; nil = none.
	norm func(proto.Message)
}

// roundTrips holds one entry per message with a counterpart, keyed by full name. Files add entries in init.
var roundTrips = map[protoreflect.FullName]roundTrip{}

// exempt lists messages with no counterpart type, each with the reason. Files add entries in init.
var exempt = map[protoreflect.FullName]string{}

// addRT registers the round trip of message type M; norm may be nil.
func addRT[M proto.Message](rt func(M) (M, error), norm func(M)) {
	var zero M
	name := zero.ProtoReflect().Descriptor().FullName()
	entry := roundTrip{rt: func(m proto.Message) (proto.Message, error) { return rt(m.(M)) }}
	if norm != nil {
		entry.norm = func(m proto.Message) { norm(m.(M)) }
	}
	roundTrips[name] = entry
}

// addExempt registers message type M as having no counterpart.
func addExempt[M proto.Message](reason string) {
	var zero M
	exempt[zero.ProtoReflect().Descriptor().FullName()] = reason
}

// memoryV1Messages returns the full names of every message (nested ones included) in package memory.v1,
// sorted.
func memoryV1Messages() []protoreflect.FullName {
	var names []protoreflect.FullName
	var walk func(protoreflect.MessageDescriptors)
	walk = func(ms protoreflect.MessageDescriptors) {
		for i := range ms.Len() {
			md := ms.Get(i)
			if !md.IsMapEntry() {
				names = append(names, md.FullName())
			}
			walk(md.Messages())
		}
	}
	protoregistry.GlobalFiles.RangeFilesByPackage("memory.v1", func(fd protoreflect.FileDescriptor) bool {
		walk(fd.Messages())
		return true
	})
	slices.Sort(names)
	return names
}

// samples is how many random values each message round-trips per run.
const samples = 200

// TestRoundTripEveryMessage round-trips random values of every memory.v1 message that has a counterpart,
// and fails when a message has neither a round trip nor an exemption.
func TestRoundTripEveryMessage(t *testing.T) {
	_ = pb.File_memory_v1_common_proto
	names := memoryV1Messages()
	if len(names) < 100 {
		t.Fatalf("found only %d memory.v1 messages; is the registry loaded?", len(names))
	}
	for _, name := range names {
		entry, ok := roundTrips[name]
		if !ok {
			if _, ok := exempt[name]; !ok {
				t.Errorf("%s has neither a round trip nor an exemption", name)
			}
			continue
		}
		t.Run(string(name), func(t *testing.T) {
			for i := range samples {
				checkRoundTrip(t, name, entry, seedFor(name, uint64(i)))
			}
		})
	}
	for name := range roundTrips {
		if _, ok := exempt[name]; ok {
			t.Errorf("%s is both round-tripped and exempt", name)
		}
	}
}

// FuzzRoundTrip round-trips every registered message from one fuzzed seed.
func FuzzRoundTrip(f *testing.F) {
	f.Add(uint64(1))
	f.Add(uint64(42))
	f.Fuzz(func(t *testing.T, seed uint64) {
		for name, entry := range roundTrips {
			checkRoundTrip(t, name, entry, seedFor(name, seed))
		}
	})
}

// valueFields are message fields whose domain counterpart is a value, not a pointer: unset converts back
// as an empty message, wherever the field appears.
var valueFields = map[protoreflect.FullName]bool{
	"memory.v1.SlotBinding.binding":     true,
	"memory.v1.MentalModelSpec.trigger": true,
}

// fillValueFields sets every unset value field in m's tree to an empty message.
func fillValueFields(m protoreflect.Message) {
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if valueFields[fd.FullName()] && !m.Has(fd) {
			m.Set(fd, protoreflect.ValueOfMessage(m.NewField(fd).Message()))
		}
	}
	walkMessages(m, fillValueFields)
}

// walkMessages calls f on every direct child message of m (singular, list elements and map values).
func walkMessages(m protoreflect.Message, f func(protoreflect.Message)) {
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsMap() && fd.MapValue().Message() != nil:
			v.Map().Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
				f(mv.Message())
				return true
			})
		case fd.IsList() && fd.Message() != nil:
			for i := range v.List().Len() {
				f(v.List().Get(i).Message())
			}
		case !fd.IsMap() && !fd.IsList() && fd.Message() != nil:
			f(v.Message())
		}
		return true
	})
}

// seedFor derives a per-message seed so each message gets its own stream.
func seedFor(name protoreflect.FullName, i uint64) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return h.Sum64() ^ (i * 0x9e3779b97f4a7c15)
}

// checkRoundTrip generates one value of name from seed and checks it survives the round trip.
func checkRoundTrip(t *testing.T, name protoreflect.FullName, entry roundTrip, seed uint64) {
	t.Helper()
	mt, err := protoregistry.GlobalTypes.FindMessageByName(name)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	in := newGen(seed).message(mt.Zero().Interface())
	want := proto.Clone(in)
	got, err := entry.rt(in)
	if err != nil {
		t.Fatalf("%s seed %d: round trip failed: %v\ninput: %s", name, seed, err, prototext.Format(in))
	}
	fillValueFields(want.ProtoReflect())
	if entry.norm != nil {
		entry.norm(want)
	}
	if !proto.Equal(want, got) {
		t.Fatalf("%s seed %d: round trip changed the value\nwant: %s\ngot:  %s", name, seed,
			prototext.Format(want), prototext.Format(got))
	}
}

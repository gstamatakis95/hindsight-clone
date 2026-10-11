package pgtest

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// names maps the UUIDs ID hands out back to their names.
var names sync.Map

// ID returns a stable version-4-shaped UUID for a readable test name ("f1", "hub"), as any string-based id
// type. Postgres stores unit, chunk, entity and operation ids as uuid, so tests name their fixtures and
// convert at the store boundary; Name maps an id back for assertions and scripted replies.
func ID[T ~string](name string) T {
	sum := sha256.Sum256([]byte(name))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	id := h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
	names.Store(id, name)
	return T(id)
}

// IDs maps ID over names.
func IDs[T ~string](names ...string) []T {
	out := make([]T, len(names))
	for i, n := range names {
		out[i] = ID[T](n)
	}
	return out
}

// Name returns the name ID turned into id, or id itself when ID never produced it.
func Name[T ~string](id T) string {
	if n, ok := names.Load(string(id)); ok {
		s, _ := n.(string)
		return s
	}
	return string(id)
}

// Names maps Name over ids.
func Names[T ~string](ids []T) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = Name(id)
	}
	return out
}

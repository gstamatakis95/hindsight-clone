package fake

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"memd/internal/domain"
	"memd/internal/port"
	"sort"
	"strings"
	"sync"
)

// blobObject is one stored object.
type blobObject struct {
	data        []byte
	contentType string
}

// Blob is an in-memory port.Blob keyed by the full object key. It stores copies, so callers may reuse
// their buffers, and it is safe for concurrent use.
//
// Put reads r to the end; when size >= 0 and the byte count differs from size it stores nothing and
// returns an error wrapping domain.ErrInvalidArgument (size < 0 means unknown). Get of a missing key
// returns an error wrapping domain.ErrNotFound; Delete of a missing key succeeds. Every method returns
// ctx.Err() (wrapped) when ctx is already done.
type Blob struct {
	mu   sync.Mutex
	objs map[string]blobObject
}

var _ port.Blob = (*Blob)(nil)

// NewBlob returns an empty Blob.
func NewBlob() *Blob {
	return &Blob{objs: map[string]blobObject{}}
}

// Put stores the bytes of r under key with contentType, replacing any existing object.
func (b *Blob) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("fake blob put %q: %w", key, err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("fake blob put %q: read: %w", key, err)
	}
	if size >= 0 && int64(len(data)) != size {
		return fmt.Errorf("%w: fake blob put %q: read %d bytes, size says %d",
			domain.ErrInvalidArgument, key, len(data), size)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objs[key] = blobObject{data: data, contentType: contentType}
	return nil
}

// Get returns a reader over a copy of the object under key.
func (b *Blob) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("fake blob get %q: %w", key, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	obj, ok := b.objs[key]
	if !ok {
		return nil, fmt.Errorf("%w: fake blob %q", domain.ErrNotFound, key)
	}
	return io.NopCloser(bytes.NewReader(bytes.Clone(obj.data))), nil
}

// Delete removes the object under key; a missing key is not an error.
func (b *Blob) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("fake blob delete %q: %w", key, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objs, key)
	return nil
}

// DeletePrefix removes every object whose key starts with prefix and returns how many it removed.
func (b *Blob) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("fake blob delete prefix %q: %w", prefix, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for k := range b.objs {
		if strings.HasPrefix(k, prefix) {
			delete(b.objs, k)
			n++
		}
	}
	return n, nil
}

// Keys returns every stored key in ascending order, for assertions.
func (b *Blob) Keys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	keys := make([]string, 0, len(b.objs))
	for k := range b.objs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ContentType returns the content type stored with key and whether key exists.
func (b *Blob) ContentType(key string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	obj, ok := b.objs[key]
	return obj.contentType, ok
}

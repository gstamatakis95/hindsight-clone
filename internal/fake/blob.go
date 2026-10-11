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
// ctx.Err() (wrapped) when ctx is already done, and the FailWith error while one is set.
type Blob struct {
	mu   sync.Mutex
	objs map[string]blobObject
	fail error
}

var _ port.Blob = (*Blob)(nil)

// NewBlob returns an empty Blob.
func NewBlob() *Blob {
	return &Blob{objs: map[string]blobObject{}}
}

// FailWith makes every later call return an error wrapping err (errors.Is matches; use an error wrapping
// domain.ErrUnavailable to mimic S3 being down) until FailWith(nil). Stored objects are kept.
func (b *Blob) FailWith(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fail = err
}

// gate returns the error a call must fail with: a done ctx or the FailWith error.
func (b *Blob) gate(ctx context.Context, op, key string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("fake blob %s %q: %w", op, key, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail != nil {
		return fmt.Errorf("fake blob %s %q: %w", op, key, b.fail)
	}
	return nil
}

// Put stores the bytes of r under key with contentType, replacing any existing object.
func (b *Blob) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if err := b.gate(ctx, "put", key); err != nil {
		return err
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
	if err := b.gate(ctx, "get", key); err != nil {
		return nil, err
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
	if err := b.gate(ctx, "delete", key); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objs, key)
	return nil
}

// DeletePrefix removes every object whose key starts with prefix and returns how many it removed.
func (b *Blob) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	if err := b.gate(ctx, "delete prefix", prefix); err != nil {
		return 0, err
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

// List returns up to limit keys starting with prefix and sorting strictly after after, in ascending byte
// order. An empty prefix or a limit <= 0 is domain.ErrInvalidArgument.
func (b *Blob) List(ctx context.Context, prefix, after string, limit int) ([]string, error) {
	if prefix == "" || limit <= 0 {
		return nil, fmt.Errorf("%w: fake blob list: empty prefix or limit %d <= 0", domain.ErrInvalidArgument, limit)
	}
	if err := b.gate(ctx, "list", prefix); err != nil {
		return nil, err
	}
	keys := []string{}
	for _, k := range b.Keys() {
		if strings.HasPrefix(k, prefix) && k > after {
			keys = append(keys, k)
			if len(keys) == limit {
				break
			}
		}
	}
	return keys, nil
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

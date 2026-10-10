package port

import (
	"context"
	"io"
)

// ---------- blobs ----------

// Blob stores original document text under {tenant}/{bank}/docs/{document_id}/{content_hash}
// (domain.DocumentBlobKey).
type Blob interface {
	// Put stores size bytes from r under key, replacing any existing object.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// Get opens the object; a missing key returns domain.ErrNotFound. The caller closes the reader.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete removes the object; deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error
	// DeletePrefix removes every object whose key starts with prefix (bank delete: "{tenant}/{bank}/",
	// domain.BankBlobPrefix) and returns how many it removed.
	DeletePrefix(ctx context.Context, prefix string) (int, error)
}

package blob

import (
	"context"
	"io"
	"memd/internal/domain"
	"strings"
)

// Store holds the original text of retained documents. It is provider-neutral: keys are plain strings built by
// [Key], and every error is already a domain error class (a missing key is domain.ErrNotFound, a provider or transport
// failure domain.ErrUnavailable).
type Store interface {
	// Put stores size bytes from r under key, replacing any existing object. A body whose length differs from size is
	// domain.ErrInvalidArgument.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// Get opens the object under key; a missing key returns domain.ErrNotFound. The caller closes the reader.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// List returns up to limit (> 0) keys that start with prefix and sort strictly after after ("" = from the start),
	// in ascending byte order. It returns fewer than limit keys only when no key is left after the last one returned
	// (so a short page, and an empty one, means the listing is done), however the provider pages internally. An empty
	// prefix or a limit <= 0 is domain.ErrInvalidArgument, so a computed prefix can never list the whole store.
	List(ctx context.Context, prefix, after string, limit int) ([]string, error)
	// DeletePrefix removes every object whose key starts with prefix and returns how many it removed; a prefix that
	// matches nothing is not an error. A failure part-way returns the count removed so far, and a rerun removes the
	// rest. An empty prefix is domain.ErrInvalidArgument, never "empty the store".
	DeletePrefix(ctx context.Context, prefix string) (int, error)
}

// Key is where a document's original text is stored: "{tenant}/{bank}/docs/{document_id}/{content_hash}".
func Key(b domain.BankRef, doc domain.DocumentID, contentHash string) string {
	return strings.Join([]string{b.Tenant, b.Bank, "docs", string(doc), contentHash}, "/")
}

// BankPrefix is the prefix holding every object of a bank, "{tenant}/{bank}/": deleting the bank removes it.
func BankPrefix(b domain.BankRef) string {
	return b.Tenant + "/" + b.Bank + "/"
}

package porttest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"memd/internal/domain"
	"memd/internal/port"
	"testing"
)

// manyKeys is the DeletePrefix volume test's key count: more than one S3 ListObjectsV2 page and one
// DeleteObjects batch (1,000 each).
const manyKeys = 1050

// blobBank is the bank whose keys the blob tests use: prefix "t/b/".
var blobBank = port.BankRef{Tenant: "t", Bank: "b"}

// RunBlobSuite runs the contract tests of port.Blob: Put/Get round trip, overwrite, missing keys, Delete and
// DeletePrefix (including more keys than one S3 page). newBlob is called once per subtest and must return
// an empty bucket or namespace.
func RunBlobSuite(t *testing.T, newBlob BlobFactory) {
	t.Helper()
	cases := []struct {
		name string
		fn   func(t *testing.T, b port.Blob)
	}{
		{"PutGetRoundTrip", testBlobRoundTrip},
		{"GetMissing", testBlobGetMissing},
		{"PutOverwrites", testBlobOverwrite},
		{"DeleteThenGet", testBlobDelete},
		{"DeleteMissing", testBlobDeleteMissing},
		{"DeletePrefixExact", testBlobDeletePrefix},
		{"DeletePrefixManyKeys", testBlobDeletePrefixMany},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.fn(t, newBlob(t))
		})
	}
}

// put stores data under key and fails the test on error.
func put(t *testing.T, b port.Blob, key string, data []byte) {
	t.Helper()
	if err := b.Put(t.Context(), key, bytes.NewReader(data), int64(len(data)), "text/plain; charset=utf-8"); err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
}

// get reads the whole object under key; a missing key returns Get's error.
func get(t *testing.T, b port.Blob, key string) ([]byte, error) {
	t.Helper()
	rc, err := b.Get(t.Context(), key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	return data, nil
}

// wantContent fails the test unless key holds want.
func wantContent(t *testing.T, b port.Blob, key string, want []byte) {
	t.Helper()
	got, err := get(t, b, key)
	if err != nil {
		t.Errorf("Get(%q): %v", key, err)
		return
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Get(%q) = %q, want %q", key, got, want)
	}
}

// wantMissing fails the test unless Get(key) is ErrNotFound.
func wantMissing(t *testing.T, b port.Blob, key string) {
	t.Helper()
	_, err := get(t, b, key)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get(%q) error = %v, want ErrNotFound", key, err)
	}
}

// deletePrefix runs DeletePrefix and fails the test on error.
func deletePrefix(t *testing.T, b port.Blob, prefix string) int {
	t.Helper()
	n, err := b.DeletePrefix(t.Context(), prefix)
	if err != nil {
		t.Fatalf("DeletePrefix(%q): %v", prefix, err)
	}
	return n
}

// testBlobRoundTrip: Get returns exactly what Put stored, including non-ASCII and empty objects.
func testBlobRoundTrip(t *testing.T, b port.Blob) {
	key := domain.DocumentBlobKey(blobBank, "doc-1", "hash-1")
	data := []byte("Original text of doc-1.\nSecond line: café ☕\n")
	put(t, b, key, data)
	wantContent(t, b, key, data)
	empty := domain.DocumentBlobKey(blobBank, "doc-empty", "hash-0")
	put(t, b, empty, []byte{})
	wantContent(t, b, empty, []byte{})
}

// testBlobGetMissing: Get of a missing key is ErrNotFound.
func testBlobGetMissing(t *testing.T, b port.Blob) {
	wantMissing(t, b, domain.DocumentBlobKey(blobBank, "nope", "nope"))
}

// testBlobOverwrite: a second Put replaces the object.
func testBlobOverwrite(t *testing.T, b port.Blob) {
	key := domain.DocumentBlobKey(blobBank, "doc-1", "hash-1")
	put(t, b, key, []byte("first version, longer than the second"))
	put(t, b, key, []byte("second"))
	wantContent(t, b, key, []byte("second"))
}

// testBlobDelete: Delete removes the object and nothing else.
func testBlobDelete(t *testing.T, b port.Blob) {
	key, keep := domain.DocumentBlobKey(blobBank, "doc-1", "h"), domain.DocumentBlobKey(blobBank, "doc-2", "h")
	put(t, b, key, []byte("one"))
	put(t, b, keep, []byte("two"))
	if err := b.Delete(t.Context(), key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantMissing(t, b, key)
	wantContent(t, b, keep, []byte("two"))
}

// testBlobDeleteMissing: deleting a missing key is not an error.
func testBlobDeleteMissing(t *testing.T, b port.Blob) {
	if err := b.Delete(t.Context(), domain.DocumentBlobKey(blobBank, "never", "stored")); err != nil {
		t.Errorf("Delete(missing) = %v, want nil", err)
	}
}

// testBlobDeletePrefix: DeletePrefix("t/b/") removes exactly the keys under it and counts them; "t/b2/..."
// and "t/bb/..." keys sharing the leading characters survive.
func testBlobDeletePrefix(t *testing.T, b port.Blob) {
	prefix := domain.BankBlobPrefix(blobBank)
	if prefix != "t/b/" {
		t.Fatalf("BankBlobPrefix = %q, want t/b/", prefix)
	}
	gone := []string{"t/b/docs/d1/h1", "t/b/docs/d2/h2", "t/b/other"}
	kept := []string{"t/b2/docs/d1/h1", "t/bb/docs/d1/h1", "u/b/docs/d1/h1", "t/bx"}
	for _, k := range append(append([]string{}, gone...), kept...) {
		put(t, b, k, []byte("data of "+k))
	}
	if n := deletePrefix(t, b, prefix); n != len(gone) {
		t.Errorf("DeletePrefix(%q) = %d, want %d", prefix, n, len(gone))
	}
	for _, k := range gone {
		wantMissing(t, b, k)
	}
	for _, k := range kept {
		wantContent(t, b, k, []byte("data of "+k))
	}
	if n := deletePrefix(t, b, prefix); n != 0 {
		t.Errorf("second DeletePrefix(%q) = %d, want 0", prefix, n)
	}
}

// testBlobDeletePrefixMany: DeletePrefix over more keys than one listing page or delete batch counts all.
func testBlobDeletePrefixMany(t *testing.T, b port.Blob) {
	key := func(i int) string { return fmt.Sprintf("t/many/docs/d%04d/h", i) }
	for i := range manyKeys {
		put(t, b, key(i), []byte(key(i)))
	}
	put(t, b, "t/other/docs/d/h", []byte("kept"))
	if n := deletePrefix(t, b, "t/many/"); n != manyKeys {
		t.Errorf("DeletePrefix over %d keys = %d", manyKeys, n)
	}
	for _, i := range []int{0, 999, 1000, manyKeys - 1} {
		wantMissing(t, b, key(i))
	}
	wantContent(t, b, "t/other/docs/d/h", []byte("kept"))
}

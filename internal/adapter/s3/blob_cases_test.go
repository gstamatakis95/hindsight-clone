package s3

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

// TestBlob covers port.Blob on MinIO: Put/Get round trip, overwrite, missing keys, Delete and DeletePrefix
// (including more keys than one S3 page) and List paging. Each case runs in parallel on its own bucket.
func TestBlob(t *testing.T) {
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
		{"ListPaging", testBlobListPaging},
		{"ListManyKeys", testBlobListManyKeys},
		{"ListRejects", testBlobListRejects},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.fn(t, newTestBlob(t))
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

// list calls List and fails the test on error.
func list(t *testing.T, b port.Blob, prefix, after string, limit int) []string {
	t.Helper()
	keys, err := b.List(t.Context(), prefix, after, limit)
	if err != nil {
		t.Fatalf("List(%q, %q, %d): %v", prefix, after, limit, err)
	}
	return keys
}

// testBlobListPaging: List pages through a prefix in ascending byte order with after and limit, never
// returns another prefix's keys, and returns an empty page once no key is left.
func testBlobListPaging(t *testing.T, b port.Blob) {
	// Byte order: "A" < "a" < "docs" < "é".
	mine := []string{"t/b/A", "t/b/a", "t/b/docs/d1/h1", "t/b/docs/d2/h2", "t/b/é"}
	for _, k := range append([]string{"t/bb/docs/d1/h1", "t/b2/x", "u/b/docs/d1/h1", "t/a"}, mine...) {
		put(t, b, k, []byte(k))
	}
	var got []string
	after := ""
	for range len(mine) + 1 {
		page := list(t, b, domain.BankBlobPrefix(blobBank), after, 2)
		if len(page) == 0 {
			break
		}
		if len(page) > 2 {
			t.Fatalf("List page %v exceeds limit 2", page)
		}
		got = append(got, page...)
		after = page[len(page)-1]
	}
	if fmt.Sprint(got) != fmt.Sprint(mine) {
		t.Errorf("paged List = %v, want %v", got, mine)
	}
	if page := list(t, b, "t/b/", "t/b/é", 10); len(page) != 0 {
		t.Errorf("List after the last key = %v, want empty", page)
	}
	if page := list(t, b, "t/b/", "t/b/a", 1); fmt.Sprint(page) != "[t/b/docs/d1/h1]" {
		t.Errorf("List after t/b/a limit 1 = %v, want [t/b/docs/d1/h1]", page)
	}
}

// testBlobListManyKeys: one List with a limit above one S3 page (1,000 keys) returns every key in order.
func testBlobListManyKeys(t *testing.T, b port.Blob) {
	key := func(i int) string { return fmt.Sprintf("t/many/docs/d%04d/h", i) }
	for i := range manyKeys {
		put(t, b, key(i), []byte(key(i)))
	}
	put(t, b, "t/other/docs/d/h", []byte("kept"))
	keys := list(t, b, "t/many/", "", manyKeys+10)
	if len(keys) != manyKeys {
		t.Fatalf("List over %d keys returned %d", manyKeys, len(keys))
	}
	for i, k := range keys {
		if k != key(i) {
			t.Fatalf("List[%d] = %q, want %q", i, k, key(i))
		}
	}
	want := []string{key(1000), key(1001), key(1002)}
	if keys := list(t, b, "t/many/", key(999), 3); fmt.Sprint(keys) != fmt.Sprint(want) {
		t.Errorf("List after key 999 = %v, want %v", keys, want)
	}
}

// testBlobListRejects: an empty prefix or a limit <= 0 is ErrInvalidArgument, never the whole store or an
// empty page that reads as "nothing left".
func testBlobListRejects(t *testing.T, b port.Blob) {
	put(t, b, "t/b/docs/d1/h1", []byte("x"))
	for _, c := range []struct {
		prefix string
		limit  int
	}{{"", 10}, {"t/b/", 0}, {"t/b/", -1}} {
		if _, err := b.List(t.Context(), c.prefix, "", c.limit); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("List(%q, limit %d) err = %v, want ErrInvalidArgument", c.prefix, c.limit, err)
		}
	}
}

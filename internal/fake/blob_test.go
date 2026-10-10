package fake_test

import (
	"context"
	"errors"
	"io"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/port"
	"memd/internal/porttest"
	"slices"
	"strings"
	"testing"
)

func TestBlobContract(t *testing.T) {
	porttest.RunBlobSuite(t, func(*testing.T) port.Blob { return fake.NewBlob() })
}

func TestBlobSizeMismatch(t *testing.T) {
	cases := []struct {
		name    string
		data    string
		size    int64
		wantErr bool
	}{
		{"exact", "hello", 5, false},
		{"unknown size", "hello", -1, false},
		{"short", "hello", 6, true},
		{"long", "hello", 4, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := fake.NewBlob()
			err := b.Put(t.Context(), "k", strings.NewReader(c.data), c.size, "text/plain")
			if c.wantErr {
				if !errors.Is(err, domain.ErrInvalidArgument) {
					t.Fatalf("Put error = %v, want ErrInvalidArgument", err)
				}
				if keys := b.Keys(); len(keys) != 0 {
					t.Errorf("Keys() = %v after failed Put, want none", keys)
				}
				return
			}
			if err != nil {
				t.Fatalf("Put: %v", err)
			}
			rc, err := b.Get(t.Context(), "k")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			got, _ := io.ReadAll(rc)
			if string(got) != c.data {
				t.Errorf("Get = %q, want %q", got, c.data)
			}
		})
	}
}

func TestBlobKeysAndContentType(t *testing.T) {
	b := fake.NewBlob()
	for _, k := range []string{"t/b/z", "t/a/x", "t/b/a"} {
		if err := b.Put(t.Context(), k, strings.NewReader(k), int64(len(k)), "text/"+k[2:3]); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := b.Keys(), []string{"t/a/x", "t/b/a", "t/b/z"}; !slices.Equal(got, want) {
		t.Errorf("Keys() = %v, want %v", got, want)
	}
	if ct, ok := b.ContentType("t/a/x"); !ok || ct != "text/a" {
		t.Errorf("ContentType(t/a/x) = %q, %v; want text/a, true", ct, ok)
	}
	if _, ok := b.ContentType("missing"); ok {
		t.Error("ContentType(missing) ok = true")
	}
}

func TestBlobStoresCopies(t *testing.T) {
	b := fake.NewBlob()
	buf := []byte("original")
	if err := b.Put(t.Context(), "k", strings.NewReader(string(buf)), -1, ""); err != nil {
		t.Fatal(err)
	}
	rc, _ := b.Get(t.Context(), "k")
	got, _ := io.ReadAll(rc)
	got[0] = 'X'
	rc, _ = b.Get(t.Context(), "k")
	again, _ := io.ReadAll(rc)
	if string(again) != "original" {
		t.Errorf("stored object changed through a returned buffer: %q", again)
	}
}

func TestBlobCancelledContext(t *testing.T) {
	b := fake.NewBlob()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := b.Put(ctx, "k", strings.NewReader("x"), 1, ""); !errors.Is(err, context.Canceled) {
		t.Errorf("Put error = %v, want context.Canceled", err)
	}
	if _, err := b.Get(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("Get error = %v, want context.Canceled", err)
	}
}

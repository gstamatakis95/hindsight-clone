package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"memd/internal/domain"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// minioImage must match the minio service image in docker-compose.yml.
const minioImage = "pgsty/minio:RELEASE.2026-08-04T00-00-00Z"

// Test MinIO credentials and port.
const (
	minioUser     = "memd"
	minioPassword = "memd-secret"
	minioPort     = "9000/tcp"
)

var (
	// minioEndpoint is the base URL of the shared MinIO container, set by TestMain.
	minioEndpoint string
	// minioErr records why MinIO could not start (e.g. no Docker); tests that need it skip.
	minioErr error
	// bucketSeq numbers the per-test buckets.
	bucketSeq atomic.Int64
)

// TestMain starts one MinIO container for the whole test binary and terminates it afterwards.
func TestMain(m *testing.M) {
	os.Exit(runWithMinio(m))
}

// runWithMinio runs the tests against a fresh MinIO container. A start failure is recorded in minioErr
// rather than failing the binary, so the tests skip when Docker is unavailable.
func runWithMinio(m *testing.M) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ctr, err := testcontainers.Run(ctx, minioImage,
		testcontainers.WithCmd("server", "/data"),
		testcontainers.WithEnv(map[string]string{"MINIO_ROOT_USER": minioUser, "MINIO_ROOT_PASSWORD": minioPassword}),
		testcontainers.WithExposedPorts(minioPort),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/minio/health/live").WithPort(minioPort).WithStartupTimeout(2*time.Minute)),
	)
	defer func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			log.Printf("terminate minio: %v", err)
		}
	}()
	if err != nil {
		minioErr = fmt.Errorf("start minio: %w", err)
		return m.Run()
	}
	minioEndpoint, err = ctr.PortEndpoint(ctx, minioPort, "http")
	if err != nil {
		minioErr = fmt.Errorf("minio endpoint: %w", err)
	}
	return m.Run()
}

// newTestBlob returns a Blob over a new, empty, uniquely named bucket, or skips without MinIO.
func newTestBlob(t *testing.T) *Blob {
	t.Helper()
	if minioErr != nil {
		t.Skipf("MinIO unavailable: %v", minioErr)
	}
	bucket := fmt.Sprintf("memd-test-%d", bucketSeq.Add(1))
	b, err := NewBlob(t.Context(), Config{
		Endpoint:        minioEndpoint,
		Bucket:          bucket,
		AccessKeyID:     minioUser,
		SecretAccessKey: minioPassword,
		UsePathStyle:    true,
	})
	if err != nil {
		t.Fatalf("NewBlob: %v", err)
	}
	if _, err := b.client.CreateBucket(t.Context(), &awss3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("CreateBucket(%q): %v", bucket, err)
	}
	return b
}

// TestPutNonSeekableReader stores bodies from readers that are not io.Seekers, including an empty one,
// and rejects a body whose length disagrees with size.
func TestPutNonSeekableReader(t *testing.T) {
	b := newTestBlob(t)
	for key, want := range map[string]string{"t/b/docs/d/h": "streamed body, café ☕", "t/b/docs/e/h": ""} {
		r := struct{ io.Reader }{io.MultiReader(strings.NewReader(want))}
		if err := b.Put(t.Context(), key, r, int64(len(want)), "text/plain; charset=utf-8"); err != nil {
			t.Fatalf("Put(%q): %v", key, err)
		}
		if got := mustGet(t, b, key); got != want {
			t.Errorf("Get(%q) = %q, want %q", key, got, want)
		}
	}
	short := struct{ io.Reader }{strings.NewReader("abc")}
	if err := b.Put(t.Context(), "t/b/short", short, 10, "text/plain"); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("Put with size mismatch error = %v, want ErrInvalidArgument", err)
	}
}

// TestDeletePrefixEmpty refuses to empty the whole bucket.
func TestDeletePrefixEmpty(t *testing.T) {
	b := newTestBlob(t)
	mustPut(t, b, "t/b/x", "x")
	if _, err := b.DeletePrefix(t.Context(), ""); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("DeletePrefix(\"\") error = %v, want ErrInvalidArgument", err)
	}
	if got := mustGet(t, b, "t/b/x"); got != "x" {
		t.Errorf("Get after rejected DeletePrefix = %q, want x", got)
	}
}

// TestMissingBucketUnavailable maps a non-key failure (no such bucket) to ErrUnavailable, not ErrNotFound.
func TestMissingBucketUnavailable(t *testing.T) {
	b := newTestBlob(t)
	b.bucket = "memd-test-missing-bucket"
	if _, err := b.Get(t.Context(), "k"); !errors.Is(err, domain.ErrUnavailable) || errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get on missing bucket error = %v, want ErrUnavailable only", err)
	}
	if err := b.Put(t.Context(), "k", strings.NewReader("v"), 1, ""); !errors.Is(err, domain.ErrUnavailable) {
		t.Errorf("Put on missing bucket error = %v, want ErrUnavailable", err)
	}
}

// mustPut stores s under key and fails the test on error.
func mustPut(t *testing.T, b *Blob, key, s string) {
	t.Helper()
	if err := b.Put(t.Context(), key, strings.NewReader(s), int64(len(s)), "text/plain"); err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
}

// mustGet reads the object under key as a string and fails the test on error.
func mustGet(t *testing.T, b *Blob, key string) string {
	t.Helper()
	rc, err := b.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	return string(data)
}

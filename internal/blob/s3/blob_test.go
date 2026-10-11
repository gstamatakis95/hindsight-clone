package s3

import (
	"context"
	"errors"
	"fmt"
	"log"
	"memd/internal/blob"
	"memd/internal/blob/blobtest"
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

// requireDockerEnv makes a missing MinIO (no Docker) fail the tests instead of skipping them.
const requireDockerEnv = "MEMD_REQUIRE_DOCKER"

// newTestStore returns a Store over a new, empty, uniquely named bucket, or skips without MinIO (fails when
// MEMD_REQUIRE_DOCKER is set).
func newTestStore(t *testing.T) *Store {
	t.Helper()
	if minioErr != nil {
		if os.Getenv(requireDockerEnv) != "" {
			t.Fatalf("MinIO unavailable and %s is set: %v", requireDockerEnv, minioErr)
		}
		t.Skipf("MinIO unavailable: %v", minioErr)
	}
	bucket := fmt.Sprintf("memd-test-%d", bucketSeq.Add(1))
	b, err := New(t.Context(), Config{
		Endpoint:        minioEndpoint,
		Bucket:          bucket,
		AccessKeyID:     minioUser,
		SecretAccessKey: minioPassword,
		UsePathStyle:    true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := b.client.CreateBucket(t.Context(), &awss3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("CreateBucket(%q): %v", bucket, err)
	}
	return b
}

// TestStore runs the blob contract suite on MinIO, one bucket per case.
func TestStore(t *testing.T) {
	blobtest.Run(t, func(t *testing.T) blob.Store { return newTestStore(t) })
}

// TestMissingBucketUnavailable maps a non-key failure (no such bucket) to ErrUnavailable, not ErrNotFound.
func TestMissingBucketUnavailable(t *testing.T) {
	b := newTestStore(t)
	b.bucket = "memd-test-missing-bucket"
	if _, err := b.Get(t.Context(), "k"); !errors.Is(err, domain.ErrUnavailable) || errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get on missing bucket error = %v, want ErrUnavailable only", err)
	}
	if err := b.Put(t.Context(), "k", strings.NewReader("v"), 1, ""); !errors.Is(err, domain.ErrUnavailable) {
		t.Errorf("Put on missing bucket error = %v, want ErrUnavailable", err)
	}
}

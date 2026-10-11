package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"memd/internal/domain"
	"memd/internal/port"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// defaultRegion is the signing region used when Config.Region is empty; MinIO accepts any region.
const defaultRegion = "us-east-1"

// Config locates the bucket and carries static credentials. Nothing is read from ~/.aws or the
// environment: memd's config comes from env and flags, resolved in cmd/memd.
type Config struct {
	// Endpoint is the base URL of the S3 API, e.g. http://localhost:59000 for docker-compose MinIO.
	// Empty means the AWS endpoint for Region.
	Endpoint string
	// Region is the signing region; empty defaults to us-east-1.
	Region string
	// Bucket holds every object this adapter reads or writes. It must already exist.
	Bucket string
	// AccessKeyID is the static access key (MINIO_ROOT_USER for docker-compose MinIO).
	AccessKeyID string
	// SecretAccessKey is the static secret key (MINIO_ROOT_PASSWORD for docker-compose MinIO).
	SecretAccessKey string
	// UsePathStyle addresses objects as {endpoint}/{bucket}/{key} instead of {bucket}.{endpoint}/{key};
	// MinIO needs true.
	UsePathStyle bool
}

// Blob implements port.Blob on one S3-compatible bucket.
type Blob struct {
	client *awss3.Client
	bucket string
}

var _ port.Blob = (*Blob)(nil)

// NewBlob builds a Blob over cfg.Bucket. It does not contact the server, so the bucket may be created
// after memd starts; a missing bucket surfaces as domain.ErrUnavailable on first use.
func NewBlob(_ context.Context, cfg Config) (*Blob, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("%w: s3 bucket is required", domain.ErrInvalidArgument)
	}
	region := cfg.Region
	if region == "" {
		region = defaultRegion
	}
	opts := awss3.Options{
		Region:       region,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		UsePathStyle: cfg.UsePathStyle,
		// Only send and verify checksums where S3 requires them: S3-compatible servers vary in
		// support for the SDK's default flexible checksums.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	if cfg.Endpoint != "" {
		opts.BaseEndpoint = aws.String(cfg.Endpoint)
	}
	return &Blob{client: awss3.New(opts), bucket: cfg.Bucket}, nil
}

// Put stores size bytes from r under key, replacing any existing object. A reader that is not an
// io.ReadSeeker is buffered in memory first, because the SDK must rewind the body to sign it.
func (b *Blob) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	body, ok := r.(io.ReadSeeker)
	if !ok {
		data, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("s3 put %q: read body: %w", key, err)
		}
		if int64(len(data)) != size {
			return fmt.Errorf("%w: s3 put %q: body has %d bytes, size says %d",
				domain.ErrInvalidArgument, key, len(data), size)
		}
		body = bytes.NewReader(data)
	}
	in := &awss3.PutObjectInput{
		Bucket:        aws.String(b.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	if _, err := b.client.PutObject(ctx, in); err != nil {
		return wrapErr("put", key, err)
	}
	return nil
}

// Get opens the object under key; a missing key returns domain.ErrNotFound. The caller closes the reader.
func (b *Blob) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := b.client.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, wrapErr("get", key, err)
	}
	return out.Body, nil
}

// Delete removes the object under key; deleting a missing key is not an error.
func (b *Blob) Delete(ctx context.Context, key string) error {
	_, err := b.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)})
	if err != nil && !isNotFound(err) {
		return wrapErr("delete", key, err)
	}
	return nil
}

// DeletePrefix removes every object whose key starts with prefix and returns how many it removed. It
// lists one page (up to 1,000 keys) at a time and deletes each page in one DeleteObjects batch. Any
// per-key failure stops it with an error carrying the count removed so far. An empty prefix is rejected
// with domain.ErrInvalidArgument rather than emptying the whole bucket.
func (b *Blob) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	if prefix == "" {
		return 0, fmt.Errorf("%w: s3 delete prefix: empty prefix", domain.ErrInvalidArgument)
	}
	pages := awss3.NewListObjectsV2Paginator(b.client, &awss3.ListObjectsV2Input{
		Bucket: aws.String(b.bucket),
		Prefix: aws.String(prefix),
	})
	removed := 0
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return removed, fmt.Errorf("s3 delete prefix %q after %d removed: %w", prefix, removed,
				wrapErr("list", prefix, err))
		}
		n, err := b.deleteBatch(ctx, page.Contents)
		removed += n
		if err != nil {
			return removed, fmt.Errorf("s3 delete prefix %q after %d removed: %w", prefix, removed, err)
		}
	}
	return removed, nil
}

// List returns up to limit keys starting with prefix and sorting strictly after after, in ascending byte
// order (S3 lists keys in UTF-8 binary order). It follows ListObjectsV2 pages (StartAfter, then
// continuation tokens) until limit keys are read or none are left. An empty prefix or a limit <= 0 is
// rejected with domain.ErrInvalidArgument: a listing of the whole bucket, or an empty page a caller could
// read as "nothing left", is never returned.
func (b *Blob) List(ctx context.Context, prefix, after string, limit int) ([]string, error) {
	if prefix == "" || limit <= 0 {
		return nil, fmt.Errorf("%w: s3 list: empty prefix or limit %d <= 0", domain.ErrInvalidArgument, limit)
	}
	in := &awss3.ListObjectsV2Input{Bucket: aws.String(b.bucket), Prefix: aws.String(prefix)}
	if after != "" {
		in.StartAfter = aws.String(after)
	}
	keys := []string{}
	for {
		in.MaxKeys = aws.Int32(int32(min(limit-len(keys), maxListKeys))) //nolint:gosec // bounded by maxListKeys
		out, err := b.client.ListObjectsV2(ctx, in)
		if err != nil {
			return nil, wrapErr("list", prefix, err)
		}
		for _, o := range out.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
		if len(keys) >= limit || !aws.ToBool(out.IsTruncated) || out.NextContinuationToken == nil {
			return keys, nil
		}
		in.ContinuationToken = out.NextContinuationToken
	}
}

// maxListKeys is the largest page ListObjectsV2 returns.
const maxListKeys = 1000

// deleteBatch deletes the listed objects (at most 1,000, one ListObjectsV2 page) in one quiet
// DeleteObjects call and returns how many were removed. Quiet mode reports only failures, so the count is
// the batch size minus the failed keys; any failed key is an error.
func (b *Blob) deleteBatch(ctx context.Context, objs []types.Object) (int, error) {
	if len(objs) == 0 {
		return 0, nil
	}
	ids := make([]types.ObjectIdentifier, 0, len(objs))
	for _, o := range objs {
		ids = append(ids, types.ObjectIdentifier{Key: o.Key})
	}
	out, err := b.client.DeleteObjects(ctx, &awss3.DeleteObjectsInput{
		Bucket: aws.String(b.bucket),
		Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(true)},
	})
	if err != nil {
		return 0, wrapErr("delete objects", aws.ToString(ids[0].Key), err)
	}
	if len(out.Errors) == 0 {
		return len(ids), nil
	}
	first := out.Errors[0]
	return len(ids) - len(out.Errors), fmt.Errorf("%w: s3 delete objects: %d of %d keys failed, first %q: %s: %s",
		domain.ErrUnavailable, len(out.Errors), len(ids), aws.ToString(first.Key),
		aws.ToString(first.Code), aws.ToString(first.Message))
}

// isNotFound reports whether err is S3's missing-key error, typed or by API error code.
func isNotFound(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var nf *types.NotFound
	if errors.As(err, &nf) {
		return true
	}
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return true
		}
	}
	return false
}

// wrapErr maps an SDK error to a domain error class: a missing key to domain.ErrNotFound, anything
// else to domain.ErrUnavailable. The SDK error stays in the chain for errors.Is/As.
func wrapErr(op, key string, err error) error {
	if isNotFound(err) {
		return fmt.Errorf("s3 %s %q: %w: %w", op, key, domain.ErrNotFound, err)
	}
	return fmt.Errorf("s3 %s %q: %w: %w", op, key, domain.ErrUnavailable, err)
}

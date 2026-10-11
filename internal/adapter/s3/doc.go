// Package s3 implements port.Blob on S3-compatible object storage (MinIO in docker-compose) with the AWS SDK for Go v2.
// memd stores the original text of every retained document there.
//
// # Architecture
//
// The package is an adapter in memd's ports-and-adapters layout (domain -> port -> engine/adapter -> server/worker ->
// cmd/memd/wire.go). It imports internal/domain and internal/port and never internal/engine, internal/server or
// internal/worker. Engines never import the S3 SDK (depguard enforces it); they reach blob storage through port.Blob.
// It holds no business logic: key layout and when to delete are decided by the callers.
//
// # Usage
//
// [NewBlob] builds a [Blob] from a [Config]; cmd/memd/wire.go fills it from MEMD_S3_ENDPOINT, MEMD_S3_BUCKET,
// MEMD_S3_ACCESS_KEY, MEMD_S3_SECRET_KEY, MEMD_S3_REGION (default us-east-1) and MEMD_S3_PATH_STYLE (default false;
// MinIO needs true). The package itself reads no environment variables and never loads ~/.aws or the SDK's default
// credential chain: credentials are static.
//
//	blob, err := s3.NewBlob(ctx, s3.Config{
//		Endpoint: "http://localhost:59000", Bucket: "memd", UsePathStyle: true,
//		AccessKeyID: user, SecretAccessKey: secret,
//	})
//	key := domain.DocumentBlobKey(bank, docID, contentHash)
//	err = blob.Put(ctx, key, strings.NewReader(text), int64(len(text)), "text/plain")
//
// NewBlob does not contact the server, so the bucket may be created after memd starts; a missing bucket shows up as
// domain.ErrUnavailable on first use. An empty Bucket is domain.ErrInvalidArgument.
//
// Keys are chosen by the callers: a document original lives at domain.DocumentBlobKey,
// "{tenant}/{bank}/docs/{document_id}/{content_hash}" (written by retain, read by the retain job and the document
// reads), and the delete_bank job removes everything under domain.BankBlobPrefix, "{tenant}/{bank}/": page by page with
// [Blob.List] and [Blob.Delete] when the bank engine is given a lister (as cmd/memd wires it), else with one
// [Blob.DeletePrefix].
//
// # Behaviour
//
//   - [Blob.Put] replaces any existing object. A reader that is not an io.ReadSeeker is buffered in memory first,
//     because the SDK must rewind the body to sign it over plain HTTP; a buffered length that differs from size is
//     domain.ErrInvalidArgument.
//   - [Blob.Get] returns the object body, which the caller closes; a missing key is domain.ErrNotFound.
//   - [Blob.Delete] of a missing key is not an error.
//   - [Blob.DeletePrefix] lists one ListObjectsV2 page (up to 1,000 keys) at a time and deletes it in one quiet
//     DeleteObjects call; any failed key stops it with domain.ErrUnavailable and the count removed so far, and a rerun
//     picks up the rest. An empty prefix is domain.ErrInvalidArgument, never "empty the bucket" (the bucket holds every
//     tenant's data).
//   - [Blob.List] returns up to limit keys under prefix sorting strictly after after, in byte order, following
//     continuation tokens; an empty prefix or limit <= 0 is domain.ErrInvalidArgument.
//
// Checksums are computed and validated only where S3 requires them, since S3-compatible servers differ in their support
// for the SDK's default flexible checksums.
//
// # Errors
//
// NoSuchKey and NotFound, typed or matched by API error code, map to domain.ErrNotFound; every other SDK or transport
// error maps to domain.ErrUnavailable. The SDK error stays in the chain for errors.Is and errors.As.
//
// # Observability
//
// Config.APIOptions takes extra SDK middleware for every call. cmd/memd/wire.go passes the Datadog S3 middleware from
// internal/observability when MEMD_DATADOG_ENABLED is set, and nil otherwise; this package imports nothing from
// Datadog.
//
// # Testing
//
// blob_test.go starts one MinIO container per test binary (testcontainers, the docker-compose image), gives every test
// its own bucket and runs the shared internal/porttest blob suite (porttest.RunBlobSuite) plus adapter-specific tests
// (multi-page DeletePrefix, non-seekable readers, missing bucket). Without Docker the tests skip. Governing SPEC
// sections: "Ports" (Blob) and "Behavioural rules" (DeleteBank).
package s3

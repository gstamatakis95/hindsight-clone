// Package s3 implements blob.Store on S3-compatible object storage (AWS S3, or MinIO in docker-compose) with the AWS
// SDK for Go v2. memd stores the original text of every retained document there.
//
// # Where it sits
//
// The package is the S3 adapter of internal/blob. It imports internal/blob and internal/domain and never a service,
// library or another adapter; nothing outside it (and the optional Datadog middleware in internal/observability)
// imports the AWS SDK. It holds no business logic: the key layout (blob.Key, blob.BankPrefix) and when to delete are
// decided by the callers.
//
// # Usage
//
// [New] builds a [Store] from a [Config]; cmd/memd fills it from MEMD_S3_ENDPOINT, MEMD_S3_BUCKET, MEMD_S3_ACCESS_KEY,
// MEMD_S3_SECRET_KEY, MEMD_S3_REGION (default us-east-1) and MEMD_S3_PATH_STYLE (default false; MinIO needs true) when
// MEMD_BLOB_PROVIDER is s3. The package itself reads no environment variables and never loads ~/.aws or the SDK's
// default credential chain: credentials are static.
//
//	st, err := s3.New(ctx, s3.Config{
//		Endpoint: "http://localhost:59000", Bucket: "memd", UsePathStyle: true,
//		AccessKeyID: user, SecretAccessKey: secret,
//	})
//	key := blob.Key(bank, docID, contentHash)
//	err = st.Put(ctx, key, strings.NewReader(text), int64(len(text)), "text/plain")
//
// New does not contact the server, so the bucket may be created after memd starts; a missing bucket shows up as
// domain.ErrUnavailable on first use. An empty Bucket is domain.ErrInvalidArgument.
//
// # Behaviour
//
//   - [Store.Put] replaces any existing object. A reader that is not an io.ReadSeeker is buffered in memory first,
//     because the SDK must rewind the body to sign it over plain HTTP; a buffered length that differs from size is
//     domain.ErrInvalidArgument.
//   - [Store.Get] returns the object body, which the caller closes; a missing key is domain.ErrNotFound.
//   - [Store.List] returns up to limit keys under prefix sorting strictly after after, in byte order, following
//     continuation tokens; an empty prefix or limit <= 0 is domain.ErrInvalidArgument.
//   - [Store.DeletePrefix] lists one ListObjectsV2 page (up to 1,000 keys) at a time and deletes it in one quiet
//     DeleteObjects call; any failed key stops it with domain.ErrUnavailable and the count removed so far, and a rerun
//     picks up the rest. An empty prefix is domain.ErrInvalidArgument, never "empty the bucket" (the bucket holds every
//     tenant's data).
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
// Config.APIOptions takes extra SDK middleware for every call. cmd/memd passes the Datadog S3 middleware from
// internal/observability when MEMD_DATADOG_ENABLED is set, and nil otherwise; this package imports nothing from
// Datadog.
//
// # Testing
//
// blob_test.go starts one MinIO container per test binary (testcontainers, the docker-compose image) and runs the
// shared contract suite (internal/blob/blobtest) with a fresh bucket per case, plus the S3-specific missing-bucket
// mapping. Without Docker the tests skip, or fail when MEMD_REQUIRE_DOCKER is set.
package s3

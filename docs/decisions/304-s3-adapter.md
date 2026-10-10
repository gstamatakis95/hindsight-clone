# 304 — S3 adapter details

## Context
port.Blob fixes the semantics of Put, Get, Delete and DeletePrefix. It leaves open how the client is built,
how a non-seekable reader is handled, which checksums are sent and what `DeletePrefix("")` does.

## Decision
- **Client.** `NewBlob` builds `s3.New(s3.Options{...})` directly from `s3.Config`: static credentials,
  `BaseEndpoint`, `UsePathStyle` (true for MinIO), and a region that defaults to `us-east-1`. It never calls
  `config.LoadDefaultConfig`, which would read `~/.aws` and environment credentials; memd's config comes from
  env and flags. `NewBlob` does not contact the server, so a missing bucket shows up as `ErrUnavailable` on
  first use.
- **Checksums** are computed and validated only when S3 requires them (`WhenRequired`). S3-compatible servers
  differ in their support for the SDK's default flexible checksums. DeleteObjects still sends its required
  checksum.
- **Non-seekable readers.** The SDK must rewind a body to sign it over plain HTTP, so `Put` buffers a reader
  that is not an `io.ReadSeeker` in memory. Document originals are text of bounded size. If the buffered
  length differs from `size`, Put returns `ErrInvalidArgument`.
- **DeletePrefix** lists one ListObjectsV2 page (up to 1,000 keys) at a time and deletes it in one quiet
  DeleteObjects call.
  - Quiet mode reports only failures, so each batch counts as its size minus the failed keys.
  - Any failed key stops the call with `ErrUnavailable`, reporting the count removed so far. A rerun picks
    up what is left.
- **Empty prefix.** `DeletePrefix("")` is rejected with `ErrInvalidArgument` rather than emptying the whole
  bucket, which holds every tenant's data.
- **Errors.** `NoSuchKey` and `NotFound`, whether typed or matched by API code, become `ErrNotFound`.
  Anything else becomes `ErrUnavailable`. The SDK error stays in the chain.

## Alternatives
- Stream non-seekable bodies with an unsigned payload: rejected, because it needs TLS plus trailing
  checksums, which MinIO over plain HTTP in docker-compose doesn't have.
- The `feature/s3/manager` uploader: rejected; another module, for objects that are small text.

## Spec lines involved
Ports (Blob); Behavioural rules (DeleteBank removes the blob prefix `{tenant}/{bank}/`).

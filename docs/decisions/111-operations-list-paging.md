# 111: Operation ids, created_at and List paging

Status: accepted (WP-01)

## Context

`Queue.List` pages newest first. The adapter must choose a default and maximum page size (port doc
`OperationFilter.PageSize`) and a token format. "Oldest first" in Claim and "newest first" in List need a
stable order. Postgres `now()` is the transaction start time, so all the jobs one transaction enqueues
(retain_batch children, for example) would share `created_at`.

## Decision

- New operation ids are UUIDv7. `created_at` and `updated_at` are set to `clock_timestamp()` on insert, so
  jobs enqueued in one transaction keep their insertion order. The id breaks any remaining tie. The claim's
  `ORDER BY o.created_at` is unchanged.
- List orders by `(created_at DESC, id DESC)` and uses keyset paging. The page token is base64url of
  `<created_at in Unix microseconds>/<id>`, which round-trips Postgres's microsecond precision exactly. A
  malformed token returns `domain.ErrInvalidArgument`.
- The page size defaults to 50 when it is 0 or negative, and is capped at 500. List fetches one extra row to
  decide whether to return a next-page token.
- Filters use nullable sqlc parameters instead of dynamic SQL. `ExcludeParents` drops `retain_batch` rows.

## Alternatives

- Offset tokens. Rejected: they skip or repeat rows while the queue changes.
- Database `now()` for `created_at`. Rejected: it would tie jobs from one transaction, so the claim would
  order them by chance.

## Spec lines

`ListOperationsRequest.page_size` ("0 = server default"). SPEC "Jobs" claim SQL (1928). `internal/port/jobs.go`
`OperationFilter`.

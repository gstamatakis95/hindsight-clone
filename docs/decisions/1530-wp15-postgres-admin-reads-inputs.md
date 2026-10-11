# 1530 — Postgres admin reads: inputs that decision 1500 leaves open

Status: accepted (WP-15)

## Context

Decision 1500 pins the semantics of the WP-15 listings and maintenance reads. A few input cases are not
covered. The Postgres adapter (internal/adapter/postgres/reads_admin.go, reads_units.go) has to do something
with them.

## Decision

- **Unknown enum values are `domain.ErrInvalidArgument`.** This covers TagCounts with an unknown TagSource
  (unspecified still means memories), ListUnits with an unknown State or TimeField, and ListDocuments with an
  unknown DocTimeField. The API layer maps the proto enums, so only a programming error can reach these cases.
- **A malformed `UnitQuery.EntityID` matches no unit.** ListUnits checks the bank first and then returns an
  empty list. This follows 1500's rule that malformed ids match no row.
- **A malformed cursor id (`UnitQuery.After.ID`) is `domain.ErrInvalidArgument`.** Engines build cursors from
  ids they listed, so a bad one means a tampered or corrupt page token.
- **FailedRefreshes orders a NULL `finished_at` last.** A terminal operation always has finished_at set, so
  this only matters for corrupt rows. Ordering NULL last matches the fake treating a missing time as zero.
- **Tag-match filters with unknown modes act as `any`,** the same as `domain.MatchTags`.

## Alternatives

- Treating an unknown TagSource as memories. Rejected because it would hide a mapping bug.
- Rejecting a malformed EntityID. Rejected because GetUnits, the id-keyed lookups and 1500 all treat
  malformed ids as matching nothing.

## Spec lines

SPEC 126, 143-144, 155, 616-631, 1985; decision 1500; CCR WP-15-1 §0.

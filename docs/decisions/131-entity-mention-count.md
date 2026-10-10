# 131 — Who maintains entities.mention_count

## Context

`domain.Entity.MentionCount` is "how many units mention the entity", but no port method sets it; co-occurrence
counts are explicitly not decremented on Detach (accepted known gap).

## Decision

The store maintains `mention_count` itself: `Attach` and `Restore` add one per new `unit_entities` row;
`Detach`, `DeleteByDocument` and `Archive` subtract one per removed row. `EntityRepo.Upsert` does not overwrite
it. Co-occurrence counts follow the port docs (bumped on Attach, never decremented).

## Alternatives

- Computing it on read with `COUNT(*)`: correct by construction but costs a join on every entity read.
- Leaving it to the caller via Upsert: every caller would need to read-modify-write the count.

## Spec lines involved

`internal/port/repos.go` (EntityRepo); `internal/domain/entity.go` (MentionCount); docs/STATUS.md known gaps.

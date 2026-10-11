# 1510 — The fake's CCR WP-15-1 reads

Status: accepted (WP-15)

## Context

The in-memory fake (internal/fake) implements the CCR WP-15-1 methods with the semantics of decision 1500.
1500 leaves a few details open, and the fake lacked two pieces of state that Postgres has: the
observation_scopes table and the entity names of an archived unit.

## Decision

- **observation_scopes.** The fake keeps a `scopes` table keyed by (bank, `ScopeKey.String()`) with the
  normalized tags. It is written like Postgres's `upsertScope`: by `UnitRepo.Insert` of an observation and
  by every `UpsertObservation`, on the update path too, using the given tags. Nothing deletes a row except
  `BankRepo.DeleteBatch`/`Delete`, so a recreated bank starts with no scopes.
- **ListScopes cursor** follows decision 1560: `after == ""` lists from the start, the shared scope first.
  TagCounts uses the same rule, which only matters for a stored empty tag.
- **Unknown enums and bad cursors** follow decision 1530: an unknown TagSource, UnitQuery State or
  TimeField, or DocTimeField is `ErrInvalidArgument`, and so is a `UnitQuery.After.ID` that is not a UUID.
  A malformed EntityID matches no unit.
- **Archived units.** `Archive` now records the entity names next to the entity ids, like
  `entities_snapshot`. GetUnit and ListUnits return those names sorted by name, the same order as a live
  unit's Entities. A later PruneOrphans therefore does not lose them. `Archive` also takes the bank's
  change seq and stamps the archived unit with it, as decision 130 and the Postgres adapter already do.
  Before this, the fake kept the unit's old seq and did not bump change_seq.
- **Argument checks come first.** ListScopes, ListUnits and ListDocuments reject `limit <= 0`, and
  ListMentalModels rejects a negative PageSize, with `ErrInvalidArgument` before they check the bank. So a
  bad limit on an unknown bank is `ErrInvalidArgument`.
- **TagCounts** counts a tag once per item, even when an unnormalized mental-model tag list repeats it
  ("the number of items carrying the tag").

## Alternatives

- Deriving ListScopes from live observations instead of a table. Rejected: the rows must survive
  DeleteScopes and DeleteAllObservations (CCR WP-15-1 ruling).
- Resolving archived entity names from the entity table at read time. Rejected: a pruned entity would lose
  its name, which Postgres keeps in the JSON snapshot.

## Spec lines

CCR WP-15-1 §2 (ListScopes, GetUnit); decisions 130, 1500, 1530 and 1560.

# 1500 — Pinned semantics of the CCR WP-15-1 port methods

Status: accepted (WP-15)

## Context

CCR WP-15-1 (docs/ccr/WP-15-1.md) adds 19 Reader methods, four repository methods, the temporal relevance
filter and `Document.ObservationScopes`. The port doc comments and the batch's §0 fix most behaviour. The
fake and the Postgres adapter must agree on the remaining details, and the porttest suites must assert them.
This note pins those details. Where it is silent, the port doc and §0 of the CCR apply.

## Decision

### General
- **Bank keys.** Every read is keyed by (tenant, bank). The same bank id under another tenant never leaks.
- **Tombstoned** means `banks.deleting_at` is set. Listings and stats (GetUnit, ListUnits,
  ObservationHistory, BankStats, TagCounts, ListDocuments, ListScopes, ListMentalModels) return an error
  wrapping `domain.ErrNotFound` for an unknown or tombstoned bank. Id-keyed lookups never check the bank:
  they return no error, and on an unknown bank (or a tombstoned bank already emptied by delete_bank) they
  return empty. On a tombstoned bank that still holds rows they may return them. Every caller of an
  id-keyed lookup already holds ids it read from a live bank.
- **Enums.** An unknown TagSource, MemoryState, TimeField or DocTimeField, and a malformed cursor id, is
  `domain.ErrInvalidArgument` (decision 1530).
- **Empty results** are non-nil empty slices and maps. Tests stay nil/empty-agnostic anyway.
- **Malformed ids** (not a UUID) are skipped by id-keyed lookups. GetUnit and ObservationHistory treat them
  as unknown (ErrNotFound).
- **Fact** means a live `memory_units` row whose fact_type is world or experience. **Observation** means a
  live row whose fact_type is observation. **Unconsolidated** (CCR §0) means a fact with
  `consolidated_at IS NULL AND consolidation_failed_at IS NULL`. A fact is **in scope** S when its tags
  contain every tag of S, so the shared scope holds every fact.
- **Byte order.** Text keysets and orderings compare bytes (Postgres `COLLATE "C"`, Go `<`). Ids are
  canonical lowercase UUIDs, so their byte order equals Postgres uuid order.
- **Refresh operations link to their model by serial key `"mm:" + model id`.** This is the convention of the
  consolidate and mental-model engines (see the WP-10 and WP-12 branches), and the same bank is required.
- **Inside a transaction**, every new Reader method on Tx sees the transaction's own uncommitted writes.
- **Limits.** ListScopes, ListUnits and ListDocuments require `limit > 0`; anything else is
  `domain.ErrInvalidArgument`. For TagCounts, `limit <= 0` means all. For ListMentalModels, `PageSize == 0`
  means all, and a negative PageSize is `domain.ErrInvalidArgument`.

### Per method
- **GetChunks.** b's chunks in the order of ids. Unknown ids, other-bank ids and malformed ids are skipped.
  A repeated id is returned once.
- **EntityStates.** The entities attached (unit_entities) to any of the given live units of b, ordered by
  entity id, with CanonicalName set. ObservationIDs are the sorted, distinct live observations of b that
  are either attached to the entity or cite (observation_evidence) a live fact attached to it. That fact
  need not be one of the input units.
- **StaleScopes.** Each input scope is evaluated against bank b (its own BankRef is ignored) and returned
  unchanged, in input order, when an unconsolidated fact of b is in scope. Duplicates are kept.
- **TagCounts.** Memories (also the default for unspecified) are all live `memory_units` rows of b,
  observations included. Mental models are `mental_models.tags` of b and documents are `documents.tags` of
  b. Count is the number of items carrying the tag. Prefix is a byte prefix; `%`, `_` and `\` are literal.
  `after` is exclusive. Results are ordered by tag.
- **CountObservations.** Live observations of s's bank whose scope_key equals
  `NewScopeKey(s.BankRef, s.Tags).String()`.
- **ListScopes.** The scopes are b's `observation_scopes` rows, ordered by scope_key. `after == ""` applies
  no filter, so the shared scope (key `""`) comes first; any other `after` filters `scope_key > after`. To
  continue after a page whose last row is the shared scope, callers pass `after = "0"` (decision 1560). Scope.Tags holds the decoded tags (empty for the shared scope). Observations counts
  the live observations with that scope_key. UnconsolidatedUnits counts the unconsolidated facts in scope.
- **ListMentalModels.** FolderPrefix p matches a model when `folder_path == p` or the path starts with
  `p + "/"`. An empty p matches everything. When Tags is non-empty, models are kept if
  `domain.MatchTags(model tags, q.Tags, q.TagsMatch)` holds. RefreshAfterConsolidation keeps only models
  whose `Spec.Trigger.RefreshAfterConsolidation` is set. Models are ordered by id and filtered
  `id > PageToken`. NextPageToken is the last returned id when more matching models follow, and "" otherwise.
  Models come back as `MentalModelRepo.Get` returns them.
- **NewestSeqInScope.** The max seq over b's live units (facts and observations) with fact type in Types
  (empty means all), `Tags.Eval(tags)` and `Seq.Contains(seq)`. It is 0 when nothing matches, including for
  an unknown bank.
- **CronModels.** Every model whose `refresh_cron` is set and non-empty, on a bank that is not tombstoned.
  LastRefreshEnqueuedAt is the max `created_at` of the model's refresh_mental_model operations, whatever
  their status, and zero when there are none. Ordered by (tenant, bank, model id).
- **FailedRefreshes.** For each existing model on a bank that is not tombstoned, take its latest
  COMPLETED or FAILED refresh_mental_model operation, ordered by `finished_at DESC`, then `created_at DESC`,
  then `id DESC`. CANCELLED operations are ignored, so a later cancel cannot hide a failure (coordinator
  ruling on CCR WP-12-1). Report the operation when it is FAILED. FailedAt is its finished_at
  and Error its error text. Ordered by (tenant, bank, model id).
- **GetUnit.** A live unit comes back with State valid, Entities, SourceIDs and (for observations) Stale.
  Otherwise an archived unit comes back with State invalidated, its snapshot entity names (sorted by name)
  and Stale false. Anything else is ErrNotFound. The archive table has fewer columns, so an archived unit
  has empty SourceIDs, ProofCount 0, Version 0, Embedding nil and ConsolidatedAt nil, and its UpdatedAt is
  `archived_at`, the time of its last change. ListUnits's updated_at bound uses the same value. An
  observation is **Stale** when an unconsolidated fact of its bank is in its scope (its tags).
- **ListUnits.** State unspecified reads both tables, valid reads only live units and invalidated reads
  only archived units. The filters are:
  - Types.
  - Q: a case-insensitive substring of the text, with `%`, `_` and `\` literal.
  - DocumentID.
  - EntityID: live units attached to the entity, so archived units never match.
  - TimeField: Start is inclusive and End exclusive. A null field never matches a set bound.
  - Tags: MatchTags, applied when non-empty.

  Units are ordered by `created_at DESC, id DESC`, and After keeps `(created_at, id) < (After.CreatedAt,
  After.ID)`. Each unit is filled like GetUnit.
- **ObservationHistory.** b's observation_versions rows for id, ordered by version DESC and then at DESC.
  No rows is ErrNotFound.
- **BankStats.**
  - UnitsByFactType counts live memory_units per fact type, with only present types as keys.
  - InvalidatedUnits counts archive rows.
  - Documents, Entities and MentalModels count rows.
  - PendingOperationsByKind counts pending and processing operations per kind.
  - UnconsolidatedUnits counts unconsolidated facts.
  - LastMemoryWriteAt and ChangeSeq come from the bank row.
- **ListDocuments.** The filters are:
  - Q: a case-insensitive substring of the id, with `%`, `_` and `\` literal.
  - Tags: MatchTags over the document's tags, applied when non-empty.
  - Start and End on created_at or updated_at.

  Documents are ordered and paged by `(created_at, id) DESC`. UnitCount and UnitsByFactType count live
  units, and every other field is filled as `DocumentRepo.Get` fills it.
- **ModelsCiting.** The distinct, sorted ids of b's models whose source_ids intersect ids.
- **Consolidation candidates skip units without an embedding** (WP-13 review ruling). The two reads that
  feed consolidation are `UnitRepo.Unconsolidated` (existing) and BanksToConsolidate. They use the §0
  predicate AND `embedding IS NOT NULL`, so consolidation never decides on a unit that is waiting for
  rederive. Once rederive sets the vector, the unit is a candidate again. The staleness and count reads
  keep the plain §0 predicate: StaleScopes, Unit.Stale, ScopeInfo.UnconsolidatedUnits and
  BankStats.UnconsolidatedUnits. A fact waiting for rederive still awaits consolidation, so its scope is
  stale.
- **BanksToConsolidate.** Banks that are not tombstoned and have an unconsolidated fact with an
  embedding, ordered by (tenant, bank).
- **IDsByDocument.** The sorted ids of b's live units whose document is doc. It writes nothing.
- **CitingScopes.** The distinct `ScopeKey{BankRef: b, Tags: observation tags}` of b's live observations
  that cite any of ids, ordered by `String()`.
- **DeleteScopes.** Each scope is matched under b by `NewScopeKey(b, s.Tags).String()`; its own BankRef is
  ignored. It locks the matching live observations FOR UPDATE, in id order, before it reads their sources.
  Then it deletes them exactly as DeleteCiting does, and resets and returns their cited units exactly as
  DeleteCiting does. No units are excluded, because none were named. The observation_scopes rows are kept.
  An empty or nil scopes argument does nothing and returns empty results.
- **DeleteAllObservations.** The same as DeleteScopes, over every live observation of b.
- **SearchTemporal.** The filter is on when `len(Embedding) > 0 && MinSimilarity > 0`. While it is on, units
  with a null embedding are excluded and the rest are kept only if `cosine(embedding, Embedding) >=
  MinSimilarity`. The filter applies before bucketing and Limit. The hit score stays the temporal score.
- **Document.ObservationScopes.** Upsert stores nil as SQL NULL, never JSON `null`, and Get, ListDocuments
  and Upsert's replace round-trip it.
- **DeleteCiting** already locks its observations FOR UPDATE, in id order, before it reads their co-sources
  (query `EvidenceCiting`). WP-15 keeps that and gives DeleteScopes and DeleteAllObservations the same lock.

## Alternatives
- Counting only facts in TagCounts(memories). Rejected: the port says "live units", and observation tags
  are always subsets of the tags of the facts they rest on.
- Returning ErrNotFound from ObservationHistory only when the unit itself is unknown. Rejected because
  every observation has at least one version row (written on create), so "no rows" is the simpler test.

## Spec lines
SPEC 126-128, 143-144, 155, 163, 402-404, 536, 616-631, 670, 693-697, 1953, 1964, 1969-1971, 1979,
1985, 1998, 2002-2003, 2023; CCR WP-15-1 §0 and §2; decision 012 R10'.

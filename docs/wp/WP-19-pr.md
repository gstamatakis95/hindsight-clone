# WP-19 — A retag keeps per-unit label tags (final review B, H1; CCR WP-19-1)

## What was built
- [x] CCR WP-19-1 (approved), recorded in `docs/ccr/WP-19-1.md`:
  - New signature: `UnitRepo.SetDocumentTags(ctx, b, doc, oldTags, newTags, seq)`.
  - Per-unit semantics: `tags = normalize((tags − oldTags) ∪ newTags)`, on live and archived units.
  - The live units' `seq` is still set and both consolidation stamps are still cleared, as before. It still
    returns the live ids.
  - Both tag lists must be normalized, else `INVALID_ARGUMENT`.
- [x] Postgres: one `UPDATE` per table (`memory_units`, `memory_units_archive`), scoped by tenant, bank and
  document. It builds the array with `ARRAY(SELECT … GROUP BY t ORDER BY t COLLATE "C")`, which gives byte
  order like `domain.NormalizeTags` and never NULL.
- [x] Fake: the same semantics (`replaceDocTags`). It now also retags archived units, as Postgres already did.
- [x] Callers updated:
  - **`docs.Retagger.Retag`:** on a real change it rewrites the units old → new in the same transaction as the
    document row, under `tx.Seq`. The old tags are the document's tags, re-read under the lock.
  - **`Apply` (retag_document):** calls `SetDocumentTags(cur, cur)`. That re-asserts the tags, resets the
    stamps and returns the ids for `DeleteCiting`. It is idempotent.
  - Test callers in consolidate, fake, postgres and porttest.
- [x] Mocks regenerated.

## Why `Retag`, not `Apply`, passes the old tags (decision 1900)
`Apply` can't read the previous tags: `Retag` has already written the new ones to the document row by then.
A job-payload copy would be wrong after a FAILED or CANCELLED retag:
- the units may still carry either tag set;
- `domain.Operation` does not expose the payload, so that set can't be recovered;
- stale document tags would then stay on the units for good.

Rewriting in `Retag`'s transaction keeps the document row and its units in step atomically.

Observable change: units carry the new tags as soon as UpdateDocument returns. The observations are still
cleared and re-consolidated by the job, as before.

## Acceptance tests
| Accept item | Test | Result |
|---|---|---|
| Porttest (fake + Postgres): doc tags [a] plus label tags retagged [a]→[b] end [b] plus the labels, live and archived; stamps reset; the other tenant is untouched | `TestStoreContract/UnitsSetDocumentTagsKeepsExtraTags` in `internal/fake` and `internal/adapter/postgres` (also checks byte sort order with punctuation, and that unnormalized old tags are INVALID_ARGUMENT) | pass |
| Existing SetDocumentTags contract | `TestStoreContract/UnitsSetDocumentTags`, `TestStoreContract/UpdateAndRetagResetConsolidation`, `TestStoreGapsContract/TenantIsolationSetDocumentTags` | pass |
| Engine: a retag keeps the write_to_tags label tags, and a set-equal retag is still a no-op | `TestRetagKeepsLabelTags` (live through Retag, then Apply run twice; archived via Restore; a set-equal no-op with no seq bump), `TestRetagSetEqualIsNoop` | pass |
| `make gen`, `make lint BREAKING_AGAINST=contracts-v1`, `make test`, `make e2e` | — | pass, with no diff after `make gen` |

## Decisions and CCRs
- CCR `docs/ccr/WP-19-1.md` (approved).
- Decision `docs/decisions/1900-retag-keeps-label-tags.md`.

## Known gaps
- A tag that is both a document tag and a label tag is ambiguous. If a retag removes it from the document, it
  also leaves the units.
- One edit is outside the card's Owns list: `internal/engine/retain/retag_test.go`. One assertion flips from
  1 stale unit to 0 before retag_document, because UpdateDocument now retags the units that are already
  written. Decision 1900 records that it supersedes that bullet of decision 1700.
- The comment on `docSuperseded` in `internal/engine/retain/retainer.go` still says retag_document retags the
  earlier units. It is now merely redundant (Retag already did it). It was left untouched because it is
  outside Owns.

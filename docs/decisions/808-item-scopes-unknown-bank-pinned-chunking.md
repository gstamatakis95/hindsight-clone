# 808 — Item observation scopes, unknown banks, pinned chunking

Status: accepted (WP-08 review round 1: H5 and MEDIUMs)

## Decision
- **Item observation scopes.** `RetainItem.observation_scopes` is carried in `DocumentPayload`. The Ingestor
  writes it, in the same transaction as `DocumentRepo.Upsert`, through the consumer-declared
  `DocumentScopesWriter.SetObservationScopes`.
  - When the writer is nil, the scopes are not stored.
  - Once WP-15 adds `domain.Document.ObservationScopes` (migration 0010, from the approved CCR batch), the
    writer becomes setting that field on `Upsert`. That is a one-line swap.
  - The scopes are not folded into the stored overrides: ReprocessDocument replaces those.
- **The no-op hash covers the scopes.** `retain.NoopHash(overrides, scopes)` is the overrides hash. When the
  item sets scopes, it is extended with their JSON. The Ingestor's check and the Retainer's check both use
  it, so a retain that changes only the scopes re-runs. The Retainer's ownership check (decision 807)
  compares the stored overrides alone.
- **Unknown banks.** The Ingestor no longer calls `Ensure` before validation.
  - For an unknown bank, validation uses the tenant level merged with the item's overrides. The server level
    only adds defaults that cannot fail validation: combined scopes and concise mode.
  - The write transaction calls `Banks().Ensure` before `Seq`. A rejected retain therefore leaves no bank
    behind.
- **Pinned chunking.** The payload pins `chunk_max_tokens`, `chunk_overlap_tokens` and
  `structured_chunk_max_tokens` as resolved at ingest. A config change between attempts cannot move chunk
  boundaries under a resuming job.

## Spec lines involved
SPEC `RetainItem.observation_scopes`, "Per-request overrides" (ReprocessDocument replaces the stored
overrides), "Behavioural rules" (a write to an unknown bank creates it; fan-out rejected before anything is
stored). CCR WP-08-2 is superseded by the WP-15 batch. Tests:
- `TestIngestStoresItemScopes`;
- `TestIngestFanOutRejectionUnknownBankCreatesNothing`.

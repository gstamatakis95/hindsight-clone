# 800 — Retain no-op check, job payload and batch parent

Status: accepted (WP-08)

## Context
The spec says a retain whose content hash and overrides hash both match the document's last completed retain
is a no-op (`unchanged=true`). It does not say:
- where the check runs;
- what the overrides hash covers;
- what a `retain_document` job carries.

`DocumentRepo.Upsert` clears `extracted_at`. The documents table has no columns for most item fields
(context, timestamp, metadata, hints, `resolve_entities`, observation scopes).

## Decision
- **The check runs in the Ingestor, before any write.** An unchanged item writes nothing: no blob, no
  `Upsert`, no job, no `Seq` bump and no model call. It is reported as `Unchanged` with no operation id. The
  Retainer repeats the check, so a replayed job (for example after a lost `Complete`) is a no-op too.
- **Hashes.** The content hash is the hex SHA-256 of the original text. The overrides hash is the SHA-256 of
  the JSON of the merged request < item overrides; no overrides hash to `""`. Tags, context, timestamp and
  metadata are not part of either hash. A tag-only change on unchanged content is a no-op;
  `UpdateDocument` is the way to retag.
- **Job payload.** `retain.DocumentPayload` (exported, so WP-13's ReprocessDocument can enqueue the same
  thing) pins three things:
  - the content hash and blob key;
  - the item fields: tags, context, timestamp, timeless, metadata, hints, `resolve_entities`, observation
    scopes, force, `skip_consolidation`;
  - the resolved timestamp ("now" at ingest when the item had none).

  `Job.Overrides` carries the merged overrides. The Retainer reads the blob at the pinned key, never at
  `doc.BlobKey`, so a resumed job always processes its own content.
- **Batch parent.** A `retain_batch` parent is created only for an async retain that enqueues two or more
  children. A sync retain runs inline and creates no operation rows.
- **Validation.** All items are validated first: fan-out, custom mode without a prompt, and duplicate
  document ids. Any failure fails the whole request before anything is stored.

## Alternatives
- Checking in the Retainer only: the Ingestor has already called `Upsert` (clearing `extracted_at`), so the
  check could never succeed.
- Re-reading the item fields from the document row: they are not stored there.

## Spec lines involved
SPEC "Behavioural rules" (retain idempotency), "Per-request overrides" (Retain overrides stored on the
document), "Jobs" (retain_document, retain_batch).

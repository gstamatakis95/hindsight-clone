# 1307 — Retag and document admin details

## Context
UpdateDocument replaces a document's tag set: a set-equal update is a no-op; a real change clears the
document's observations, resets their co-sources and queues re-consolidation under the new tags (SPEC
"Behavioural rules"). The scaffold had the retagger consume `consolidate.Invalidator.ClearDocumentUnits`
(WP-10, built in parallel).

## Decision
- **Ports, not the Invalidator.** The retag_document job uses the ports directly:
  `UnitRepo.SetDocumentTags` (rewrites the units' tags and seq, clears their consolidation stamps, returns
  their ids), then `EvidenceRepo.DeleteCiting(ids)` (deletes the citing observations and resets their
  co-sources), then a `{}` consolidate with `DedupePending`, in one transaction. That is exactly the SPEC
  behaviour, is testable on `internal/fake` today and removes a dependency on WP-10. The docs-declared
  `Invalidator` was dropped; `NewRetagger(s port.Store)` takes no invalidator.
- **No seq bump on a no-op.** Retag reads the bank (unknown = `ErrNotFound`, tombstoned =
  `ErrBankDeleting`) and the document first; a set-equal update returns `""` having written nothing. A
  real change takes `tx.Seq` (which serializes concurrent retags of the bank on the bank row), re-reads the
  document under that lock, and only then sets the tags and enqueues; if a concurrent retag already set the
  same tags it returns `""` too.
- **Tags live on the document row** (decision 1301): the job re-reads them, so a retag joining a pending
  one is applied with the latest tags. A job whose document (or bank) is gone succeeds as a no-op; a
  tombstoned bank fails with `ErrBankDeleting` from `Tx.Seq`, which the runner treats like any write
  refused on a deleting bank.
- **DocumentAdmin.** `ListDocuments` defaults `tags_match` to `any_strict` (proto comment), normalizes tags,
  pages 100 by default (max 1000) with an opaque base64-JSON `(created_at, id)` keyset token, asking the
  source for one extra row. `GetDocument` reads in a short transaction (tombstoned bank = `ErrNotFound`) and
  the original text from the blob store only when asked. `ReprocessDocument` delegates to the retain
  engine's `Reprocessor` and returns the queued operation.

## Alternatives
- Calling `consolidate.Invalidator.ClearDocumentUnits`: rejected for the reasons above; WP-14 may still
  switch if WP-10's Invalidator grows behaviour retag needs (it has none per SPEC).
- Always taking `tx.Seq` first: correct but bumps `change_seq` on every no-op update.

## Spec lines involved
SPEC "Behavioural rules" (UpdateDocument), "Jobs" (retag_document row), gRPC surface (documents).

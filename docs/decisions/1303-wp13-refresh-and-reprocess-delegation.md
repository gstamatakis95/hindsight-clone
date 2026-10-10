# 1303 — Refresh and reprocess enqueues are delegated to their owning engines

## Context
Invalidate must queue "a refresh of every model whose `source_ids` include the unit or a deleted
observation", and CreateBankFromTemplate must queue "a first build of every mental model". ReprocessDocument
must re-run retain over the stored text. The refresh_mental_model payload, its debounce / interval floor /
paused rules and upgrade-on-join belong to WP-12; the retain_document payload and the curation reset of a
reprocess belong to WP-08. Neither exists yet.

## Decision
- `bank.RefreshQueuer` and `curate.RefreshQueuer` (identical method):
  `QueueRefresh(ctx, tx port.Tx, b, id port.MentalModelID) (port.OperationID, error)` enqueues (or joins) a
  refresh inside the caller's transaction; the caller has already called `tx.Seq(b)`. WP-12 (or WP-14 over
  WP-12's scheduler) implements it. Whether an invalidate-triggered refresh honours `auto_refresh_paused`
  is WP-12's rule; WP-13 only asks.
- The citing-model lookup is a CCR'd read (`ModelsCiting`, CCR WP-13-1) done by the curator, so the
  refresh queuer stays a one-model primitive reusable by consolidation and templates.
- `docs.Reprocessor.Reprocess(ctx, b, id, overrides) (port.OperationID, error)` is the retain engine's
  (WP-08) reprocess entry point; `DocumentAdmin.ReprocessDocument` calls it and loads the operation.
- Tests use in-package stubs (the refresh stub enqueues a `refresh_mental_model` row in the same tx, so the
  acceptance test sees a queued job atomically with the invalidate).

## Alternatives
- Building `{"model_id": ...}` refresh jobs in WP-13: rejected, it would fix WP-12's payload and skip its
  debounce/pause rules.
- Using `consolidate.Invalidator.ClearForMemory` for the invalidate: rejected, it doesn't return the deleted
  observation ids the refresh lookup needs; the curator calls `EvidenceRepo.DeleteCiting` directly.

## Spec lines involved
SPEC "Curation" (invalidate row), "Behavioural rules" (mental models: debounce, paused, joins), gRPC
CreateBankFromTemplate and ReprocessDocument.

# 142 — Small reading choices in the document, model, directive and event repositories

Status: accepted (WP-01)

## Context

The port docs leave a few details open that the Postgres adapter (and WP-02's fake) must agree on.

## Decision

- **Unknown parent on a write.** `BankRepo`'s doc says a write to an unknown bank creates it (Ensure);
  that is the engine's job, which calls Ensure (or Tx.Seq) before writing. The repositories never create
  a bank implicitly: a write whose bank, document or mental model does not exist fails its foreign key
  and returns `domain.ErrNotFound`. `EventRepo.Append` has no foreign key (lifecycle_events does not
  reference banks) and is always preceded by `Tx.Seq`, which refuses an unknown bank.
- **Limits.** `EventRepo.List` and `MentalModelRepo.History` treat `limit <= 0` as "no limit".
- **EventRepo.List** computes the page and `lastSeq` in one statement (one snapshot), so a seq group that
  commits between two statements can never be skipped by an advanced `lastSeq`. The page ends at the seq
  of the limit-th matching event; with fewer matches `lastSeq` is the highest seq after `afterSeq`
  (filtered-out events included), or `afterSeq` itself when there is none.
- **EventRepo.Append** stores `e.At`, or the database's `now()` when it is zero.
- **Mental models.** `IsStale` is not stored (readers compute it; the `is_stale` column stays at its
  default) and Get returns false. `refresh_cron` is written from `Spec.Trigger.RefreshCron` on Create and
  Update. A malformed source id is `ErrInvalidArgument`, never silently dropped. History entries are
  ordered newest first by `(changed_at, id)` both when trimming and when reading; a zero `ChangedAt` takes
  `now()`; a zero `Version` is stored as NULL. History of an unknown model is empty, not an error.
- **Directives.** A new directive gets a UUID string id. Upsert never changes `created_at`, so List
  (`created_at, id`) keeps a replaced directive in its place.

## Alternatives

- Ensure inside every repository write: hides engine bugs and takes the bank row lock out of order.
- Storing `IsStale`: a cached flag that readers would have to override anyway.

## Spec lines involved

- SPEC.md 1979 (unknown bank), 1955 (events), 1996-2003 (mental-model staleness and history);
  decision 012 R6 (event paging).

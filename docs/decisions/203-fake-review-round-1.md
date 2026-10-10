# 203 — Fake strictness after WP-02 review round 1

## Context
Review round 1 ruled that the fakes must never be looser than production, because every Phase 2 engine
tests against them. Several write paths accepted input that Postgres or the gateway rejects, and a few
choices were undocumented.

## Decision
- **Structured output is validated against `req.Schema`.** The gateway's JSON-schema subset validator
  moved to the shared package `internal/jsonschema` (orchestrator ruling; WP-03's tests moved with it).
  - An empty or malformed schema is `domain.ErrInvalidArgument`, as in the gateway. The call is neither
    recorded nor served.
  - Scripted output that doesn't match the schema, or doesn't decode into `out`, is
    `domain.ErrUnavailable` (decision 202).
- **Self-links** are `domain.ErrInvalidArgument`, mirroring the CHECK `memory_links_no_self_link`.
- **Label entities** always resolve by (bank, kind, label value), whatever `e.ID` says. The existing row is
  then updated (LastSeen and the other fields), so there is never a second row for one label.
- **Restore** requires that the transaction already took `Tx.Seq` for the bank, per the port doc ("the
  caller calls Tx.Seq first"). Otherwise it returns `domain.ErrFailedPrecondition`.
- **A context that ends while the InTx callback runs** aborts the commit. `InTx` then returns an error
  wrapping `ctx.Err()` (and `domain.ErrUnavailable`).
- **Tags must be normalized by writers.** `domain.Unit.Tags` and `domain.Document.Tags` are documented
  as "normalized tags". These writes return `domain.ErrInvalidArgument` when tags differ from
  `domain.NormalizeTags(tags)`:
  - unit Insert / UpsertObservation;
  - SetDocumentTags;
  - Documents.Upsert and SetTags.
- **Fact types.** Update edits world/experience facts only. UpsertObservation takes observations only.
  Evidence ties an observation to facts. Insert accepts only the three fact types (the
  `fact_type` CHECK). All of these return `domain.ErrInvalidArgument`.
- **Unknown banks.** `BankRepo.SetConfig` and `MarkDeleting` on an unknown bank return
  `domain.ErrNotFound`; they don't create it. The port's "a write to an unknown bank creates it" is done
  by the caller through `Ensure`. Admin RPCs that need an existing bank get NOT_FOUND.
- **Blob.FailWith** injects storage failures. The similar-mode Embedder has no stopword list, and its doc
  says so.

## Alternatives
- Normalizing tags silently in the fake: this would hide a writer that forgets to normalize, which
  production stores verbatim.
- Restore taking Seq itself: friendlier, but the port puts the obligation on the caller.

## Spec lines involved
Ports (UnitRepo.Restore, EntityRepo.Upsert, LLM.Structured), schema/schema.sql CHECKs, domain unit.go
tag comments, decision 202, WP-02 review round 1.

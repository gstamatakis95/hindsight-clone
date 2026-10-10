# 201 — Fake store strictness and search approximations (WP-02)

## Context
The ports leave some behaviour to the adapter: what happens on foreign-key violations, how the keyword,
temporal and graph arms score, which trigram threshold Candidates uses, and what Limit 0 means. The fake
has to pick something; engine tests should catch bugs, not hide them.

## Decision
- **Foreign keys** from schema/schema.sql are enforced and reported as domain.ErrNotFound: units,
  documents, entities, mental models and directives need their bank; units with a document id and chunks
  need the document; links need two live units of one bank; evidence needs a live observation and live
  cited units of its bank; Attach needs a live unit and entities of its bank. Cascades are mirrored
  (removing a unit removes its links, unit_entities and evidence rows; replacing a chunk clears unit
  chunk ids).
- **Duplicate unit ids** on Insert are domain.ErrAlreadyExists.
- **Event stamping:** EventRepo.Append rejects an event whose Seq is not the Seq this transaction took
  for the event's bank (domain.ErrInvalidArgument). The port says "e.Seq must be the tx's change seq";
  the fake checks it.
- **Embedding dimension** must be domain.EmbeddingDim on write and on SearchVector
  (domain.ErrInvalidArgument), like the vector(768) column.
- **Tenant isolation:** rows are keyed like the schema's primary keys; every by-id write that takes a
  bank returns domain.ErrNotFound for another bank's row.
- **Search scoring:** SearchVector is exact cosine. SearchText is Okapi BM25 (k1 1.2, b 0.75) over
  lowercase letter/digit tokens without stemming, document frequency over the bank's live units, matching
  units only. SearchTemporal treats occurred_start..occurred_end (a missing bound copies the other, else
  mentioned_at) as the occurrence, keeps overlaps with the half-open window and scores
  1 / (1 + days from the window centre); Buckets > 1 picks round-robin from equal sub-windows. ExpandGraph
  follows outgoing links only (the UnitsArchiveRestore contract test requires direction) and scores the
  best product of edge weights. Candidates uses pg_trgm's similarity() with pg_trgm's default threshold
  0.3.
- **Limit <= 0** on searches and list-style reads means no cap (tests can omit it); List page size <= 0
  means 100, capped at 1000; page tokens are offsets.

## Alternatives
- Lenient fake (no FK checks, no event-seq check): engine bugs that Postgres would reject pass in unit
  tests. Rejected.
- Limit 0 = no rows (SQL LIMIT 0): surprising in tests; engines always pass a limit in production.

## Spec lines involved
Ports (Reader arms, repos), schema/schema.sql foreign keys, decision 005, the STATUS.md known gap
"GraphQuery direction unstated".

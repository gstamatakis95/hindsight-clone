# 1540 — Readings taken by the WP-15 reads contract suite

Status: accepted (WP-15)

## Context

`internal/porttest/reads_suite.go` (RunReadsSuite) proves the CCR WP-15-1 §3 Accept items for GetChunks,
EntityStates, StaleScopes, TagCounts, CountObservations, NewestSeqInScope, ModelsCiting, the temporal
relevance filter and Document.ObservationScopes, following decision 1500. Two Accept items can't be tested
literally.

1. **Id-keyed lookups on a tombstoned bank.** CCR §3 says "each id-keyed lookup returns empty with no error"
   on an unknown or tombstoned bank. 1500 says id-keyed lookups "return empty with no error and never check
   the bank". The two disagree for a tombstoned bank that still holds rows: by 1500 those rows may come back.
2. **`"B" < "a"`.** The TagCounts byte-order item uses an uppercase tag. Both stores reject tags that are
   not `domain.NormalizeTags` output (lowercased), so "B" can't be stored.

## Decision

1. The suite asserts "empty, no error" only on an unknown bank and on a tombstoned bank that holds no
   matching rows. On a tombstoned bank that still holds rows it asserts only "no error" for the id-keyed
   lookups (and ErrNotFound for TagCounts). Both readings of the CCR pass.
2. Byte order is shown with `"_x" < "a"` and `"z" < "é"` (é as U+00E9, NFC). Both pairs flip under a locale
   collation such as en_US.utf8 (which ignores "_" at the first level and sorts "é" next to "e"), which is
   the default of the test image (paradedb/paradedb, `datcollate = en_US.utf8`), so a missing
   `COLLATE "C"` fails the test. Digits do not discriminate and are not relied on. The prefix cases use
   `%`, `_` and `\` to show the prefix is literal.

## Alternatives
- Seed a tombstoned bank and assert empty lookups. Rejected: a correct implementation of 1500 ("never
  check the bank") would fail it.
- Store an uppercase tag through a back door. Rejected: tests write only through the port, and the port
  never stores one.

## Spec lines
CCR WP-15-1 §0 and §3; decision 1500 (General: tombstoned, byte order; TagCounts).

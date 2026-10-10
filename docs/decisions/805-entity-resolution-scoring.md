# 805 — Entity resolution scoring

Status: accepted (WP-08)

## Context
The spec says "Score = name similarity + co-occurrence overlap + temporal proximity". It gives no weights.
`EntityRepo.Cooccurrence` takes entity ids, while Hindsight's nearby set is entity names.

## Decision
Hindsight's resolver, adapted to the ports.

**Names**
- A fact's names are its mentions plus the item's hints. Whitespace is collapsed. Blank names and names over
  512 characters are dropped. Duplicates (case-insensitive) are merged.

**Candidates**
- Candidates come from `EntityRepo.Candidates` (trigram, with pg_trgm's 0.3 floor).
- Label entities are skipped.

**Gates (before scoring)**
Hindsight's gates, applied in order. A candidate that fails any gate is never scored, however recent or
co-occurring it is.
1. **Identical names.** An identical name (case-insensitive, or trigram similarity 1) passes every gate,
   whatever its kind, as in Hindsight. "Kubernetes" extracted once as a technology and once as a concept is
   one entity. Literal hints match by identical name only, so they are never kind-gated either.
2. **Kind (fuzzy matches only).** For a non-identical name, when both the mention's kind and the candidate's
   kind are set, they must be equal (case-insensitive). This gate is memd's addition; Hindsight has none. An
   entity with an unknown kind, or a mention with no kind, passes.
3. **Trigram floor.** Trigram similarity must be at least 0.3 (Hindsight's
   `DEFAULT_ENTITY_MERGE_MIN_SIMILARITY`). This is a package constant, not a bank knob, because BankConfig is
   frozen and the spec names no such setting.
4. **Word by word.** A port of Hindsight's `_tokens_are_compatible`.
   - **Numbers.** When each name has a number the other lacks, the names are different things: "Room 101" vs
     "Room 102", "UA123" vs "UA124". Numbers are normalized first: leading zeros are dropped, and so is an
     all-zero fraction ("4.0" is 4; "3.10" stays distinct from "3.1").
   - **Single words.** When neither name has two or more words, the names pass.
   - **Otherwise,** every word of the shorter name must match some word of the longer one. Two words match
     when they are equal, when one is a prefix of the other (abbreviations: corp / corporation), or when their
     SequenceMatcher ratio is at least 0.6. john / jane is 0.5, so "John Smith" and "Jane Smith" stay apart
     even when they co-occur with the same entity on the same day.

**Score**
- An identical name scores 1.
- Otherwise the score is the sum of:
  - 0.5 × trigram similarity;
  - 0.3 × matched / (the fact's number of other names). A name counts as matched when the entity it would
    itself merge into on the name alone co-occurs with this candidate. That entity is its first candidate
    that passes the gates and has an identical name. Hindsight matches co-occurring entities by name, so a
    merely similar entity ("Acme Labs" for the mention "Acme") does not count, and names with no existing
    entity still count in the denominator;
  - 0.2 × (1 − days / 7) when the candidate was last seen within 7 days of the fact's time (occurred start,
    else mention time).

**Merge or create**
- The best candidate merges when its score, rounded to 6 places, is at or above `entity_match_threshold`
  (default 0.6). A merge widens `first_seen / last_seen`.
- Otherwise a new entity is created with the mention's kind.

**Literal names**
- `resolve_entities = false` applies to the caller's hint names only, as in Hindsight. They match an existing
  entity only by exact case-insensitive name. Extracted mentions always resolve, unless a hint names them too.

**Labels**
- Labels resolve by exact match. A value or multi_values label is kept only with a declared value (stored in
  the declared spelling). Map fields are `key:field`. `write_to_tags` adds `key:value` to the unit's tags.

## Alternatives
- SequenceMatcher-style whole-name similarity in the score, as Hindsight uses: it is not available in
  Postgres, and trigram is what the candidate query already returns. SequenceMatcher's ratio is ported to Go
  (Ratcliff/Obershelp on runes) only for the per-word gate, where it is what tells a typo from a different word.
- Hindsight's degree damping of co-occurrence (hub entities weigh less): not ported; every co-occurring name
  weighs 1. This matches Hindsight when nothing is known about degrees.

## Spec lines involved
SPEC "Engines (retain)" (`EntityResolver`), `EntityLabel` proto comments; Hindsight
`memories/pg/entity_resolver.py`. Tests: `TestEntityResolverMergeVersusSplit`, `TestEntityResolverMergeGates`,
`TestEntityResolverNearbyNeedsIdenticalName`, `TestScoreCandidateThreshold`, `TestResolverTokensCompatible`,
`TestLabelResolver`.

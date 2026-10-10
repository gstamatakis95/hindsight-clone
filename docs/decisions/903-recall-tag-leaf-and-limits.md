# 903 — Empty tag-group leaves and the per-arm limit

## Context
WP-09 review round 1 raised two places where recall departs from Hindsight:
- **An empty tag-group leaf.** Hindsight's `_match_group` makes an empty `any_strict` leaf match nothing
  (no overlap is possible, and untagged rows are excluded). Frozen `domain.MatchGroup` and
  `port.SQLPredicate.Eval` treat a leaf with no tags as no filter, except `exact`, where it means untagged
  only.
- **Candidate limits.** Hindsight runs its arms once per fact type, each with the full budget, so a recall
  over three types sees up to three times as many candidates. memd runs each arm once over every requested
  type.

## Decision
- **Empty leaves keep the frozen domain semantics** (orchestrator ruling). An empty non-exact leaf is no
  filter, and an empty exact leaf selects untagged units. Changing it would mean a CCR on `domain` and
  `port`, and the Postgres renderer already agrees with Eval. `TestRecallTagGroups` pins both cases.
- **The per-arm limit is global** across fact types (100 / 300 / 1000 by budget). The spec defines the
  budget as "the per-arm candidate cap", and one query per arm keeps the four arms concurrent.

## Alternatives
- Matching Hindsight's empty-leaf behaviour: needs a CCR on frozen semantics for an edge case a client can
  avoid (by omitting the leaf).
- A per-fact-type limit: up to 3x the arm queries and candidates for no spec requirement.

## Spec lines involved
RecallRequest.budget comment ("the per-arm candidate cap"); TagLeaf; `domain.MatchGroup`;
`port.SQLPredicate.Eval`.

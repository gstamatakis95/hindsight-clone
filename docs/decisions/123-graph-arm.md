# 123 — Graph arm: direction, hops and scoring

## Context

`GraphQuery` expands "from seed units over memory_links" with `MaxHops` 1-2 and a "path weight" score; the
direction was left open (WP-00 known gap "GraphQuery direction unstated"; decision 009 tests forward edges).

## Decision

- A recursive CTE follows links forward (from -> to) from the seeds, up to `MaxHops` hops (clamped to 1-2),
  restricted to `Kinds` at every step and to links of the query's tenant and bank.
- A reached unit scores the best (max) product of link weights over its paths. Seeds are never hits. Reached
  units must be live units of the bank and pass Types, Tags and Seq; intermediate units on a path are not
  filtered. Best first, ties by id, then Limit.

## Alternatives

- Both directions: doubles the fan-out and lets an effect pull in its causes; the suite asserts forward only.
  Callers that want both can seed from either end.
- Sum of path weights: rewards highly connected hubs over close neighbours.

## Spec lines involved

SPEC "Recall pipeline" (graph: 1-2 hops over links), "Decisions: Graph"; `internal/port/queries.go`.

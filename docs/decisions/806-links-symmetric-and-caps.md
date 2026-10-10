# 806 — Links: symmetric edges, caps in neighbours, Link vs LinkWith

Status: accepted (WP-08)

## Context
The graph arm follows links forward only (decision 123). `LinkRepo.Count` counts links touching a unit in
either direction. Hindsight writes temporal and semantic links in both directions. `Linker.Link` has no
config parameter, but `semantic_link_k` and the other link knobs are per-request retain overrides.

## Decision
- **Symmetric edges.** Temporal and semantic links are written as two directed rows (u -> v and v -> u), so
  the graph arm reaches a neighbour from either end.
- **Caps count neighbours, on the linked unit's side only.** A cap is measured as links touching the unit / 2.
  A pair is added while the unit being linked is below its cap; a neighbour already at its cap still takes
  the pair (as Hindsight's `_cap_links_per_unit`, which caps per `from_unit_id`). Refusing full neighbours
  starved later units in a dense cluster (30 similar units, cap 3: many ended with no links). So old units
  can exceed the cap; a unit's own pass never adds more than the cap.
- **Causal links** run from cause to effect, one row, uncapped. They come from the batch-local `caused_by`
  indexes.
- **Semantic links.** These are the nearest facts by cosine (`SearchVector`, facts only) at or above
  `semantic_link_min_similarity`, up to `semantic_link_k`, with weight = similarity.
- **Temporal links.** These are the facts whose time is within `temporal_link_window_hours`:
  - nearest first, up to `temporal_link_cap`;
  - weight = max(0.3, 1 − gap / window);
  - a unit's time is its occurred start, else its mention time;
  - timeless units get no temporal links.
- **Link vs LinkWith.** `Linker.LinkWith` takes the caller's effective config; the Retainer uses it. `Link`
  keeps the signature maintenance consumes and reads the bank's own tuning (tenant < bank) inside the tx.
  The ConfigResolver can't be called inside a tx.

## Alternatives
- One direction only: the graph arm could not reach newer units from older seeds.
- Counting only outgoing links: there is no port method for it.

## Spec lines involved
SPEC "Engines (retain)" (`Linker`), "Behavioural rules" (graph_maintenance refills links below caps),
`RetainTuning` comments; decision 123; Hindsight `memories/pg/links.py`. Test: `TestLinkerCapsHold`.

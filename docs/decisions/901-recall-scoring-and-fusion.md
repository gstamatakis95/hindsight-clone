# 901 — Recall fusion, scoring and floors

## Context
The spec fixes RRF k = 60, "rerank top 300", "boost: recency, temporal, proof" and per-arm `strategy_boosts`
as `map<string, double>`. It does not say:
- how a numeric boost enters fusion;
- what the base score is without a reranker;
- how the temporal boost measures proximity;
- how min_scores.reranker treats candidates that weren't reranked.

Amended after WP-09 review round 1: the temporal proximity, graph link kinds, boost placement and
prefer_observations items.

## Decision
- **Fusion:**
  - RRF = sum over arms of `1 / (k + rank)`, summed in a fixed arm order (semantic, keyword, graph, temporal)
    so float sums are deterministic.
  - Ties keep first-seen order (arm order, then rank), like Hindsight's stable sort.
- **Strategy boosts** (Hindsight's stage 1 only):
  - A boost `w` divides the arm's rank, `1 / (k + rank / w)`; absent or <= 0 means 1.
  - It is used only to choose which candidates survive the `reranker_max_candidates` cut, and only when the
    pool exceeds the cap. `Candidate.RRF` stays unboosted.
  - With rerank off, the passthrough base is computed from the unboosted RRF order, as in Hindsight.
  - Hindsight's stage-2 additive post-rerank bump is not built (orchestrator ruling).
- **Rerank window:** only the candidates that survive the cut are hydrated and scored, and only they can be
  returned. The tail is dropped, as in Hindsight.
- **Base score:**
  - The cross-encoder score in [0,1]; the adapter normalizes it.
  - With rerank off, or no reranker, the base comes from the unboosted RRF rank, 1.0 down to 0.1
    (Hindsight's passthrough rule).
  - A reranker error fails the recall, because reflect is strict.
- **Boosts** are Hindsight's multiplicative ones:
  `final = base * (1 + a_r (recency - 0.5)) * (1 + a_t (temporal - 0.5)) * (1 + a_p (proof - 0.5))`.
  - **Recency:** linear decay over recency_horizon_days down to a 0.1 floor. Age is measured from
    occurred_start, else mentioned_at, else occurred_end. Timeless and undated units are neutral. A date
    covering a whole calendar month or year is aged from its end and capped at neutral.
  - **Temporal:** Hindsight's linear proximity (pg/recall.py), computed from the hydrated unit and the query
    window for candidates the temporal arm returned:
    - `1 - min(|best - mid| / (total_days / 2), 1)`;
    - `best` is the occurrence midpoint, else occurred_start, else occurred_end, else mentioned_at;
    - an undated unit gets 0.5, and a zero-length window gives 1;
    - **a window with one open bound** anchors on the set bound, with `recency_horizon_days` as the
      half-width;
    - candidates the temporal arm didn't return are neutral;
    - the adapter's temporal score only orders the arm's own list (and its buckets).
  - **Proof:** `0.5 + ln(proof_count) / 10` for observations, clamped to [0,1]; neutral otherwise.
  - The rerank document is Hindsight's `[Date: June 05, 2022 (2022-06-05)] context: text`.
- **Graph arm:** follows semantic and causal links only, never temporal ones. Time is the temporal arm's
  job.
- **Floors:**
  - Semantic and keyword are arm floors passed into the queries. Semantic defaults to 0.3 (decision 900).
    The graph arm's seed search keeps its own 0.3.
  - The reranker floor applies only to candidates the cross-encoder scored, and is compared as float32 (the
    scores' precision). The final floor applies to all.
- **prefer_observations** follows Hindsight's window rule:
  - every observation within the first `2 * per-arm limit` ranked candidates supersedes the raw facts it
    cites, which are dropped wherever they rank;
  - the rest is packed, so the freed budget backfills;
  - an observation below that window drops nothing.

## Alternatives
- Weighted RRF (`w / (k + rank)`): Hindsight measured it collapsing recall@20 once w exceeds RRF's dynamic
  range (issue #3956).
- Boosting the stored RRF score: it would reorder the passthrough base too, which Hindsight avoids.
- Scoring candidates beyond the rerank window by RRF only: that mixes two score scales in one list.
- Using the adapter's `1 / (1 + days)` temporal score as the boost signal: most in-window units fall below
  neutral, which penalises the units the query asked about.

## Spec lines involved
Recall pipeline diagram; RecallConfig; MinScores; the RecallRequest.prefer_observations comment; Behavioural
rules (Reflect strict); decision 122.

# 901 — Recall fusion, scoring and floors

## Context
The spec fixes RRF k = 60, "rerank top 300", "boost: recency, temporal, proof" and per-arm `strategy_boosts`
as `map<string, double>`. It does not say:
- how a numeric boost enters fusion;
- what the base score is without a reranker;
- how min_scores.reranker treats candidates that weren't reranked.

## Decision
- **Fusion:**
  - RRF sums the arms in a fixed order (semantic, keyword, graph, temporal) so float sums are deterministic.
    Ties break by id.
  - A strategy boost `w` divides the arm's rank: `1 / (k + rank / w)`. This is Hindsight's stage-1
    rank-space boost, which stops the boosted arm from filling the whole reranker window. Absent or <= 0
    means 1.
  - Hindsight's stage-2 additive post-rerank bump is not built.
  - `Fuser.Fuse` takes the boosts as a third argument; the skeleton had none.
- **Rerank window:** only the top `reranker_max_candidates` fused candidates are hydrated and scored, and
  only they can be returned. The tail is dropped, as in Hindsight.
- **Base score:**
  - The cross-encoder score in [0,1]; the adapter normalizes it.
  - With rerank off, or no reranker, the base comes from the RRF rank, 1.0 down to 0.1 (Hindsight's
    passthrough rule), so boosts adjust relevance instead of replacing it.
  - A reranker error fails the recall, because reflect is strict.
- **Boosts** are Hindsight's multiplicative ones:
  `final = base * (1 + a_r (recency - 0.5)) * (1 + a_t (temporal - 0.5)) * (1 + a_p (proof - 0.5))`.
  - **Recency:** linear decay over recency_horizon_days down to a 0.1 floor. Age is measured from
    occurred_start, else mentioned_at, else occurred_end. Timeless and undated units are neutral. A date
    covering a whole calendar month or year is aged from its end and capped at neutral.
  - **Temporal:** the temporal arm's proximity score; neutral for units the arm didn't return.
  - **Proof:** `0.5 + ln(proof_count) / 10` for observations, clamped to [0,1]; neutral otherwise.
  - The rerank document is Hindsight's `[Date: June 05, 2022 (2022-06-05)] context: text`.
- **Floors:**
  - Semantic and keyword are arm floors passed into the queries. The graph arm's seed search keeps its own
    0.3.
  - The reranker floor applies only to candidates the cross-encoder scored. The final floor applies to all.
- **prefer_observations:**
  - Drop the raw facts cited by a *returned* observation, then backfill. "Returned" is computed by packing
    to a fixed point; the dropped set only grows, so the loop ends.
  - Hindsight instead uses the observations within the top `2 * budget`. Following the proto comment
    ("returned") means a fact is never dropped for an observation the caller doesn't get.

## Alternatives
- Weighted RRF (`w / (k + rank)`): Hindsight measured it collapsing recall@20 once w exceeds RRF's dynamic
  range (issue #3956).
- Scoring candidates beyond the rerank window by RRF only: that mixes two score scales in one list.

## Spec lines involved
Recall pipeline diagram; RecallConfig; MinScores; the RecallRequest.prefer_observations comment; Behavioural
rules (Reflect strict).

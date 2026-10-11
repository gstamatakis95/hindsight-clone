// Package arms holds memd's built-in recall arms, one file each, every one a [recall.Arm] that takes only the
// boundary it reads:
//
//   - [Vector] ("vector"): store.Search.SearchVector, HNSW cosine similarity. Floor: min_scores.arms["vector"],
//     default 0.3; an explicit 0 disables it.
//   - [Text] ("text"): store.Search.SearchText, pg_search BM25 over unit text. Floor: min_scores.arms["text"], none
//     by default.
//   - [Graph] ("graph"): its own top 20 vector seeds at cosine >= 0.3 (store.Search), then graph.Reader.Expand over
//     semantic and causal links (never temporal ones) up to recall.graph_max_hops (default 1, clamped to 1..2).
//   - [Temporal] ("temporal"): store.Search.SearchTemporal, units whose occurrence overlaps the query's time window,
//     spread over recall.temporal_buckets (default 4), dropping those with cosine < 0.1 to the query. Without a
//     window it returns recall.ErrSkip. It is a recall.WindowArm: its hits get the temporal-proximity boost.
//
// cmd/memd/wire_services.go registers them, in this order, through recall.Deps.Arms; the order is the fusion order. The
// arms never import the recall engine's internals or one another; vector.go holds the one shared helper (hits). Every
// arm runs the contract suite in internal/recall/armtest (arms_test.go, on pgtest).
package arms

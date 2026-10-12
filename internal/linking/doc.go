// Package linking connects freshly written memory units to the rest of the bank: [Linker] adds their temporal,
// semantic and causal links, [Resolver] maps a fact's entity mentions to existing or new entities, and
// [LabelResolver] maps extracted key:value labels to label entities and tags.
//
// # Where it sits
//
// linking is a library over the store boundary: it imports domain, graph and store and never a service, an adapter,
// gen, pgx, net/http, the S3 SDK or Datadog (depguard). Every method takes the caller's store.Tx and makes no model
// call, so it runs inside the write transaction that inserted the units (retain's chunk batches, rederive,
// graph_maintenance's top-up). Links are written through store.Tx.Graph; entities through store.EntityRepo.
//
// # Linker
//
// For each unit, the semantic pass links it to its nearest facts (store.Search.SearchVector) at or above
// retain.semantic_link_min_similarity ([DefaultSemanticLinkMinSimilarity] 0.7), up to retain.semantic_link_k
// ([DefaultSemanticLinkK] 50) neighbours; the temporal pass links it to the facts nearest in time within
// retain.temporal_link_window_hours ([DefaultTemporalLinkWindowHours] 24) of its occurrence (else mention) time, up
// to retain.temporal_link_cap ([DefaultTemporalLinkCap] 20), with a weight falling from 1 to 0.3 across the window.
// Both kinds are written in both directions; the cap bounds only the linked unit's own pass. Causal links run
// cause -> effect from the [CauseMap] and are not capped. Observations are never linked. Link reads the tuning from
// the bank's stored config (tenant < org < bank); LinkWith takes the caller's effective config.
//
// # Resolver
//
// Each distinct mention (and caller hint) is matched against the bank's trigram candidates: same kind, trigram
// similarity >= 0.3, and word-by-word agreement. The rest score 0.5 x name similarity + 0.3 x co-occurrence
// overlap + 0.2 x temporal proximity (7 days), and the best at or above the threshold
// ([DefaultEntityMatchThreshold] 0.6) wins; otherwise a new entity is created. Literal hints match only by exact
// name.
//
// # LabelResolver
//
// Only labels declared in retain.entity_labels are kept; each (key, value) becomes one label entity, and labels
// declared with write_to_tags also add a "key:value" tag to the unit.
package linking

// Package maintenance repairs the memory graph after units are removed. GraphMaintainer is the
// graph_maintenance job: it drains graph_dirty, topping up temporal and semantic links for neighbours
// that fell below their caps (temporal_link_cap, semantic_link_k), and entity_maintenance, pruning
// entities and co-occurrences no live unit references. It works in short committed batches within a
// time budget measured on the injected port.Clock and resumes where it stopped.
//
// maintenance declares the small Linker it consumes; retain.Linker satisfies it.
package maintenance

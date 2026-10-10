// Package docs holds document-level operations beyond retain. Retagger implements UpdateDocument's tag
// replacement: a set-equal update (same tags, any order) is a no-op; a real change enqueues
// retag_document, which rewrites the tags of the document's units, clears the observations derived
// from them, resets their co-sources and queues re-consolidation under the new tags.
//
// docs declares the small Invalidator it consumes (a subset of consolidate.Invalidator) instead of
// importing consolidate.
package docs

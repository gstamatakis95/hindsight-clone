// Package docs holds document-level operations beyond retain.
//
// Retagger implements UpdateDocument's tag replacement: a set-equal update (same tags, any order) is a
// no-op; a real change commits the new tag set on the document and enqueues retag_document, whose job
// (Retagger.Apply) rewrites the tags of the document's units, deletes the observations derived from them,
// resets their co-sources and queues re-consolidation under the new tags.
//
// DocumentAdmin lists and reads documents (with their original text from the blob store) and hands
// ReprocessDocument to the retain engine through the small Reprocessor interface declared here.
package docs

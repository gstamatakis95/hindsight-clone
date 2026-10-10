// Package timex resolves time for retain and recall. At retain time it normalizes an extracted fact's
// occurrence from its raw time expression ("last spring") against the item's reference time; at query
// time it turns a temporal cue in a recall query into a domain.TimeWindow for the temporal arm.
//
// Deterministic rules run first; the TIME_PARSE model slot (through port.ModelRouter and port.LLM) is a
// fallback used only when a temporal cue is present and the rules cannot resolve it. A query with no
// cue gets no window and causes no LLM call. The reference time always comes from the caller or an
// injected port.Clock, never time.Now.
//
// timex works on domain.ExtractedFact rather than a retain type so that retain can import timex without
// a cycle.
package timex

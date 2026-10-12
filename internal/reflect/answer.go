package reflect

import (
	"memd/internal/domain"
	"slices"
	"strings"
)

// validCitations keeps the cited ids some tool actually retrieved (a unit or a mental model), in citation
// order without duplicates; anything the agent never saw is dropped.
func validCitations(st *agentState, cited []string) []string {
	out := make([]string, 0, len(cited))
	for _, id := range cited {
		if slices.Contains(out, id) {
			continue
		}
		_, unit := st.RetrievedUnits[domain.UnitID(id)]
		_, model := st.RetrievedModels[domain.MentalModelID(id)]
		if unit || model {
			out = append(out, id)
		}
	}
	return out
}

// citedBasedOn builds BasedOn from the done call's valid citations: units and mental models.
func citedBasedOn(st *agentState, cited []string) BasedOn {
	var b BasedOn
	for _, id := range validCitations(st, cited) {
		if u, ok := st.RetrievedUnits[domain.UnitID(id)]; ok {
			b.Memories = append(b.Memories, u)
			continue
		}
		b.MentalModelIDs = append(b.MentalModelIDs, domain.MentalModelID(id))
	}
	return b
}

// allBasedOn builds BasedOn from everything retrieved, for answers synthesised without a done call (the
// answer drew on every tool result). Ids are sorted so the result is deterministic.
func allBasedOn(st *agentState) BasedOn {
	var b BasedOn
	ids := make([]domain.UnitID, 0, len(st.RetrievedUnits))
	for id := range st.RetrievedUnits {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		b.Memories = append(b.Memories, st.RetrievedUnits[id])
	}
	for id := range st.RetrievedModels {
		b.MentalModelIDs = append(b.MentalModelIDs, id)
	}
	slices.Sort(b.MentalModelIDs)
	return b
}

// directiveIDs lists the ids of the applied stored directives (callers pass the stored selection only, so
// request-only extras never appear; an empty id is skipped).
func directiveIDs(ds []domain.Directive) []domain.DirectiveID {
	var out []domain.DirectiveID
	for _, d := range ds {
		if d.ID != "" {
			out = append(out, d.ID)
		}
	}
	return out
}

// doneDocument is the structured document the document-mode done tool takes.
type doneDocument struct {
	// Sections are the document's sections in order.
	Sections []docSection `json:"sections"`
}

// docSection is one section of a doneDocument.
type docSection struct {
	// Heading is the heading text without '#'; empty = no heading line.
	Heading string `json:"heading"`
	// Level is the heading level, 1-6 (clamped).
	Level int `json:"level"`
	// Blocks are the section's paragraphs, lists, tables or code fences.
	Blocks []string `json:"blocks"`
}

// markdown renders the document: each section's heading as an ATX heading at its level (clamped to 1-6),
// then its non-empty blocks, everything separated by blank lines.
func (d doneDocument) markdown() string {
	var parts []string
	for _, s := range d.Sections {
		if h := strings.TrimSpace(s.Heading); h != "" {
			level := min(max(s.Level, 1), 6)
			parts = append(parts, strings.Repeat("#", level)+" "+h)
		}
		for _, b := range s.Blocks {
			if b = strings.TrimSpace(b); b != "" {
				parts = append(parts, b)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

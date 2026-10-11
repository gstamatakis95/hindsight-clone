package reflect

import (
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
)

// validCitations keeps the cited ids some tool actually retrieved (a unit or a mental model), in citation
// order without duplicates; anything the agent never saw is dropped.
func validCitations(st *agentState, cited []string) []string {
	out := make([]string, 0, len(cited))
	for _, id := range cited {
		if slices.Contains(out, id) {
			continue
		}
		_, unit := st.RetrievedUnits[port.UnitID(id)]
		_, model := st.RetrievedModels[port.MentalModelID(id)]
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
		if u, ok := st.RetrievedUnits[port.UnitID(id)]; ok {
			b.Memories = append(b.Memories, u)
			continue
		}
		b.MentalModelIDs = append(b.MentalModelIDs, port.MentalModelID(id))
	}
	return b
}

// allBasedOn builds BasedOn from everything retrieved, for answers synthesised without a done call (the
// answer drew on every tool result; decision 1102). Ids are sorted so the result is deterministic.
func allBasedOn(st *agentState) BasedOn {
	var b BasedOn
	ids := make([]port.UnitID, 0, len(st.RetrievedUnits))
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
func directiveIDs(ds []domain.Directive) []port.DirectiveID {
	var out []port.DirectiveID
	for _, d := range ds {
		if d.ID != "" {
			out = append(out, d.ID)
		}
	}
	return out
}

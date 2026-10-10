package consolidate

import (
	"memd/internal/domain"
	"memd/internal/port"
)

// ActionKind is what a consolidation decision does to a scope's observations.
type ActionKind string

// Action kinds.
const (
	// ActionUnspecified is the zero value; never applied.
	ActionUnspecified ActionKind = ""
	// ActionCreate adds a new observation.
	ActionCreate ActionKind = "create"
	// ActionUpdate rewrites an existing observation and bumps its version.
	ActionUpdate ActionKind = "update"
	// ActionNone records that the facts add nothing; the units are still stamped.
	ActionNone ActionKind = "none"
)

// ConsolidationAction is one decision of the CONSOLIDATE call for one scope.
type ConsolidationAction struct {
	// Kind is Create | Update | None.
	Kind ActionKind
	// ObservationID is the observation to rewrite, for Update.
	ObservationID port.UnitID
	// Text is the new observation text (Create and Update).
	Text string
	// Evidence is each source unit + exact quote the observation rests on.
	Evidence []domain.Evidence
}

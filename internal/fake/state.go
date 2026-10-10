package fake

import (
	"encoding/json"
	"maps"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"time"
)

// state is one immutable snapshot of the whole fake database. A transaction works on a clone and
// commit publishes the clone; the published snapshot is never written again, so readers use it
// without locks.
//
// Every table is a flat map (or slice) of value types. Slice, map and pointer fields inside the values
// are treated as immutable: writers replace them instead of mutating them, and values are deep-copied
// on the way in and on the way out. That keeps a shallow maps.Clone per table a real copy.
type state struct {
	// banks is keyed by (tenant, bank), like the banks primary key.
	banks map[port.BankRef]bankRow
	// tenants holds tenant configs as JSON (jsonb round trip).
	tenants map[string][]byte
	// units are live memory units, keyed by their globally unique id (memory_units primary key).
	units map[port.UnitID]unitRow
	// archive holds invalidated units with their entity and causal-link snapshots.
	archive map[port.UnitID]archiveRow
	// documents is keyed by (bank, id).
	documents map[docKey]docRow
	// chunks is keyed by (bank, document, index).
	chunks map[chunkKey]domain.Chunk
	// entities is keyed by the globally unique entity id.
	entities map[port.EntityID]entityRow
	// unitEntities is the unit_entities join table.
	unitEntities map[unitEntityKey]struct{}
	// cooc counts co-occurrences per ordered entity pair (a < b).
	cooc map[entityPair]int
	// links is memory_links keyed by (from, to, kind).
	links map[linkKey]linkRow
	// dirty is graph_dirty: unit -> insertion order.
	dirty map[bankUnitKey]int64
	// entMaint is entity_maintenance: entity -> insertion order.
	entMaint map[bankEntityKey]int64
	// evidence is observation_evidence keyed by (observation, unit).
	evidence map[evidenceKey]string
	// obsVersions is observation_versions in insertion order.
	obsVersions []obsVersionRow
	// scopes is observation_scopes: every scope that has held an observation, with its normalized tags.
	scopes map[scopeRowKey][]string
	// audit is memory_audit in insertion order.
	audit []domain.AuditEntry
	// models is mental_models keyed by (bank, id).
	models map[modelKey]modelRow
	// history is mental_model_history in insertion order.
	history []historyRow
	// directives is keyed by (bank, id).
	directives map[directiveKey]directiveRow
	// events is lifecycle_events in insertion order.
	events []eventRow
	// ops is the operations table keyed by the globally unique operation id.
	ops map[port.OperationID]opRow
	// order is a monotonic counter used to break created_at ties (insertion order).
	order int64
}

// newState returns an empty database.
func newState() *state {
	return &state{
		banks:        map[port.BankRef]bankRow{},
		tenants:      map[string][]byte{},
		units:        map[port.UnitID]unitRow{},
		archive:      map[port.UnitID]archiveRow{},
		documents:    map[docKey]docRow{},
		chunks:       map[chunkKey]domain.Chunk{},
		entities:     map[port.EntityID]entityRow{},
		unitEntities: map[unitEntityKey]struct{}{},
		cooc:         map[entityPair]int{},
		links:        map[linkKey]linkRow{},
		dirty:        map[bankUnitKey]int64{},
		entMaint:     map[bankEntityKey]int64{},
		evidence:     map[evidenceKey]string{},
		scopes:       map[scopeRowKey][]string{},
		models:       map[modelKey]modelRow{},
		directives:   map[directiveKey]directiveRow{},
		ops:          map[port.OperationID]opRow{},
	}
}

// clone returns a copy of s that can be written without affecting s.
func (s *state) clone() *state {
	return &state{
		banks:        maps.Clone(s.banks),
		tenants:      maps.Clone(s.tenants),
		units:        maps.Clone(s.units),
		archive:      maps.Clone(s.archive),
		documents:    maps.Clone(s.documents),
		chunks:       maps.Clone(s.chunks),
		entities:     maps.Clone(s.entities),
		unitEntities: maps.Clone(s.unitEntities),
		cooc:         maps.Clone(s.cooc),
		links:        maps.Clone(s.links),
		dirty:        maps.Clone(s.dirty),
		entMaint:     maps.Clone(s.entMaint),
		evidence:     maps.Clone(s.evidence),
		obsVersions:  slices.Clone(s.obsVersions),
		scopes:       maps.Clone(s.scopes),
		audit:        slices.Clone(s.audit),
		models:       maps.Clone(s.models),
		history:      slices.Clone(s.history),
		directives:   maps.Clone(s.directives),
		events:       slices.Clone(s.events),
		ops:          maps.Clone(s.ops),
		order:        s.order,
	}
}

// next returns the next insertion-order value.
func (s *state) next() int64 {
	s.order++
	return s.order
}

// ---------- rows and keys ----------

// bankRow is one banks row; the config is stored as JSON.
type bankRow struct {
	bank   domain.Bank
	config []byte
}

// unitRow is one memory_units row.
type unitRow struct {
	u     domain.Unit
	order int64
}

// archiveRow is one memory_units_archive row with its snapshots.
type archiveRow struct {
	u        domain.Unit
	entities []port.EntityID
	// names are the canonical names of entities at archive time, sorted (the entities_snapshot names).
	names  []string
	links  []domain.Link
	reason string
	at     time.Time
}

// docKey is a document's primary key.
type docKey struct {
	bank port.BankRef
	id   port.DocumentID
}

// docRow is one documents row; overrides and observation scopes are stored as JSON (nil = SQL NULL).
type docRow struct {
	d         domain.Document
	overrides []byte
	scopes    []byte
}

// chunkKey is a chunk's (bank, document, index) unique key.
type chunkKey struct {
	bank  port.BankRef
	doc   port.DocumentID
	index int
}

// entityRow is one entities row.
type entityRow struct {
	e     domain.Entity
	order int64
}

// unitEntityKey is one unit_entities row.
type unitEntityKey struct {
	unit   port.UnitID
	entity port.EntityID
}

// entityPair is an ordered pair of entities (a < b) for co-occurrence counts.
type entityPair struct {
	a, b port.EntityID
}

// newPair orders x and y into an entityPair.
func newPair(x, y port.EntityID) entityPair {
	if y < x {
		x, y = y, x
	}
	return entityPair{a: x, b: y}
}

// linkKey is a link's primary key.
type linkKey struct {
	from, to port.UnitID
	kind     domain.LinkKind
}

// linkRow is one memory_links row.
type linkRow struct {
	l     domain.Link
	order int64
}

// bankUnitKey scopes a unit id to a bank (graph_dirty).
type bankUnitKey struct {
	bank port.BankRef
	unit port.UnitID
}

// bankEntityKey scopes an entity id to a bank (entity_maintenance).
type bankEntityKey struct {
	bank   port.BankRef
	entity port.EntityID
}

// evidenceKey is one observation_evidence row.
type evidenceKey struct {
	obs, unit port.UnitID
}

// scopeRowKey is an observation_scopes primary key: the bank and the scope's ScopeKey.String().
type scopeRowKey struct {
	bank port.BankRef
	key  string
}

// obsVersionRow is one observation_versions row.
type obsVersionRow struct {
	bank port.BankRef
	obs  port.UnitID
	v    domain.ObservationVersion
}

// modelKey is a mental model's primary key.
type modelKey struct {
	bank port.BankRef
	id   port.MentalModelID
}

// modelRow is one mental_models row; the spec is stored as JSON.
type modelRow struct {
	m    domain.MentalModel
	spec []byte
}

// historyRow is one mental_model_history row.
type historyRow struct {
	key   modelKey
	e     domain.MentalModelHistoryEntry
	order int64
}

// directiveKey is a directive's primary key.
type directiveKey struct {
	bank port.BankRef
	id   port.DirectiveID
}

// directiveRow is one directives row.
type directiveRow struct {
	d     domain.Directive
	order int64
}

// eventRow is one lifecycle_events row.
type eventRow struct {
	e     domain.LifecycleEvent
	order int64
}

// opRow is one operations row.
type opRow struct {
	op              domain.Operation
	payload         json.RawMessage
	overrides       []byte
	runAt           time.Time
	joinable        bool
	cancelRequested *time.Time
	finishedAt      *time.Time
	order           int64
}

// ---------- deep copies ----------

// ptrCopy returns a pointer to a copy of *p, or nil.
func ptrCopy[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// cloneUnit deep-copies the slice, map and pointer fields of u.
func cloneUnit(u domain.Unit) domain.Unit {
	u.Metadata = maps.Clone(u.Metadata)
	u.Tags = slices.Clone(u.Tags)
	u.Entities = slices.Clone(u.Entities)
	u.Embedding = slices.Clone(u.Embedding)
	u.SourceIDs = slices.Clone(u.SourceIDs)
	u.OccurredStart, u.OccurredEnd = ptrCopy(u.OccurredStart), ptrCopy(u.OccurredEnd)
	u.MentionedAt, u.EditedAt = ptrCopy(u.MentionedAt), ptrCopy(u.EditedAt)
	u.ConsolidatedAt, u.ConsolidationFailedAt = ptrCopy(u.ConsolidatedAt), ptrCopy(u.ConsolidationFailedAt)
	return u
}

// jsonOf marshals v as jsonb would store it; nil for a nil pointer.
func jsonOf[T any](v *T) []byte {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic("fake: marshal " + err.Error()) // domain config types always marshal
	}
	return b
}

// fromJSON decodes stored JSON into a fresh value; nil for empty input.
func fromJSON[T any](b []byte) *T {
	if b == nil {
		return nil
	}
	v := new(T)
	if err := json.Unmarshal(b, v); err != nil {
		panic("fake: unmarshal " + err.Error()) // only ever decodes what jsonOf wrote
	}
	return v
}

// configOf decodes a stored config, returning the zero config for nil.
func configOf(b []byte) domain.BankConfig {
	if c := fromJSON[domain.BankConfig](b); c != nil {
		return *c
	}
	return domain.BankConfig{}
}

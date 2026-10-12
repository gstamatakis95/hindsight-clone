package domain

import "time"

// Org groups a tenant's banks. It carries the org config level (server < tenant < org < bank) and must exist
// before anything is written under it.
type Org struct {
	// Ref is the org's tenant and id.
	Ref OrgRef
	// Name is a display name.
	Name string
	// Config is the org's own config level.
	Config BankConfig
	// DeletingAt is set once the org is tombstoned for deletion; reads under it are then not found and writes fail
	// with ErrOrgDeleting.
	DeletingAt *time.Time
	// CreatedAt is the creation time.
	CreatedAt time.Time
	// UpdatedAt is the last update time.
	UpdatedAt time.Time
}

// Deleting reports whether the org is tombstoned.
func (o Org) Deleting() bool { return o.DeletingAt != nil }

// Bank is one memory bank. Its Config holds only the bank's own overrides; the effective view comes
// from merging server < tenant < bank.
type Bank struct {
	// Ref is the bank's tenant, org and id.
	Ref BankRef
	// Name is a display name.
	Name string
	// Config is the bank's own overrides.
	Config BankConfig
	// ChangeSeq is the bank's commit-ordered change counter (bumped by Tx.Seq).
	ChangeSeq int64
	// DeletingAt is set once DeleteBank tombstoned the bank; writes then fail with ErrBankDeleting.
	DeletingAt *time.Time
	// LastMemoryWriteAt is when memory content last changed.
	LastMemoryWriteAt *time.Time
	// CreatedAt is the creation time.
	CreatedAt time.Time
	// UpdatedAt is the last update time.
	UpdatedAt time.Time
}

// Deleting reports whether the bank is tombstoned.
func (b Bank) Deleting() bool { return b.DeletingAt != nil }

// BankStats answers GetBankStats from indexed COUNT queries.
type BankStats struct {
	// MemoriesByFactType counts live units per fact type.
	MemoriesByFactType map[FactType]int64
	// InvalidatedMemories counts archived units.
	InvalidatedMemories int64
	// Documents counts documents.
	Documents int64
	// Entities counts entities.
	Entities int64
	// MentalModels counts mental models.
	MentalModels int64
	// PendingOperationsByKind counts pending and processing operations per kind (the eval barrier).
	PendingOperationsByKind map[OperationKind]int64
	// UnconsolidatedMemories counts units not yet consolidated or parked.
	UnconsolidatedMemories int64
	// LastMemoryWriteAt is when memory content last changed.
	LastMemoryWriteAt *time.Time
	// ChangeSeq is the bank's latest committed change sequence.
	ChangeSeq int64
}

// TagSource selects where ListTags counts tags.
type TagSource string

// Tag sources, mirroring memory.v1.ListTagsRequest.TagSource.
const (
	// TagSourceUnspecified is the zero value (memories).
	TagSourceUnspecified TagSource = ""
	// TagSourceMemories counts tags on live memory units.
	TagSourceMemories TagSource = "memories"
	// TagSourceMentalModels counts tags on mental models.
	TagSourceMentalModels TagSource = "mental_models"
	// TagSourceDocuments counts tags on documents.
	TagSourceDocuments TagSource = "documents"
)

// TagCount is one tag and how many items carry it.
type TagCount struct {
	// Tag is the normalized tag.
	Tag string
	// Count is the number of items carrying it.
	Count int64
}

// BankTemplate is a bank's portable setup: config, directives and mental-model specs (no memories).
type BankTemplate struct {
	// Config is the bank's own overrides.
	Config BankConfig `json:"config"`
	// Directives are the bank's directives (ids are reassigned on create).
	Directives []Directive `json:"directives,omitempty"`
	// MentalModels are the writable specs of the bank's mental models; each gets a first build on create.
	MentalModels []MentalModelSpec `json:"mental_models,omitempty"`
}

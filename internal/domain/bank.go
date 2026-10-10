package domain

import (
	"strings"
	"time"
)

// Bank is one memory bank. Its Config holds only the bank's own overrides; the effective view comes
// from merging server < tenant < bank.
type Bank struct {
	// Ref is the bank's tenant and id.
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
	// UnitsByFactType counts live units per fact type.
	UnitsByFactType map[FactType]int64
	// InvalidatedUnits counts archived units.
	InvalidatedUnits int64
	// Documents counts documents.
	Documents int64
	// Entities counts entities.
	Entities int64
	// MentalModels counts mental models.
	MentalModels int64
	// PendingOperationsByKind counts pending and processing operations per kind (the eval barrier).
	PendingOperationsByKind map[OperationKind]int64
	// UnconsolidatedUnits counts units not yet consolidated or parked.
	UnconsolidatedUnits int64
	// LastMemoryWriteAt is when memory content last changed.
	LastMemoryWriteAt *time.Time
	// ChangeSeq is the bank's latest committed change sequence.
	ChangeSeq int64
}

// TagSource selects where ListTags counts tags.
type TagSource string

// Tag sources, mirroring memory.v1.TagSource.
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

// BankBlobPrefix is the blob prefix holding every object of a bank: "{tenant}/{bank}/".
func BankBlobPrefix(b BankRef) string {
	return b.Tenant + "/" + b.Bank + "/"
}

// DocumentBlobKey is where a document's original text is stored:
// "{tenant}/{bank}/docs/{document_id}/{content_hash}".
func DocumentBlobKey(b BankRef, doc DocumentID, contentHash string) string {
	return strings.Join([]string{b.Tenant, b.Bank, "docs", string(doc), contentHash}, "/")
}

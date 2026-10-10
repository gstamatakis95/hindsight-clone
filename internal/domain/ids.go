package domain

// BankRef names a bank. Every repo call, job and handler carries one; ids are unique only within it.
type BankRef struct {
	// Tenant is the tenant id read from the x-tenant-id metadata header.
	Tenant string
	// Bank is the bank id, unique within the tenant.
	Bank string
}

// IsZero reports whether the reference names no bank.
func (b BankRef) IsZero() bool { return b.Tenant == "" && b.Bank == "" }

// UnitID identifies a memory unit (fact or observation). Server-generated UUID in string form.
type UnitID string

// DocumentID identifies a document within a bank. Client-supplied or generated at retain.
type DocumentID string

// ChunkID identifies a stored chunk of a document. Server-generated UUID in string form.
type ChunkID string

// EntityID identifies a resolved entity within a bank. Server-generated UUID in string form.
type EntityID string

// OperationID identifies an operation, which is also the job id. Server-generated UUID in string form.
type OperationID string

// MentalModelID identifies a mental model (page): lowercase alphanumerics and hyphens, unique per bank.
type MentalModelID string

// DirectiveID identifies a directive within a bank.
type DirectiveID string

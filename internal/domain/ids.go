package domain

// OrgRef names an org: a group of a tenant's banks. Orgs are created explicitly (CreateOrg) and never on first
// use; every bank belongs to exactly one.
type OrgRef struct {
	// Tenant is the tenant id read from the x-tenant-id metadata header.
	Tenant string
	// Org is the org id, unique within the tenant.
	Org string
}

// String returns "tenant/org", for error messages and logs.
func (o OrgRef) String() string { return o.Tenant + "/" + o.Org }

// BankRef names a bank. Every repo call, job and handler carries one; ids are unique only within it. Build one
// with [NewBankRef]: a keyed literal that forgets Org compiles, and a conventions test rejects it.
type BankRef struct {
	// Tenant is the tenant id read from the x-tenant-id metadata header.
	Tenant string
	// Org is the id of the org the bank belongs to, unique within the tenant.
	Org string
	// Bank is the bank id, unique within the org.
	Bank string
}

// NewBankRef returns the reference to bank in org of tenant.
func NewBankRef(tenant, org, bank string) BankRef {
	return BankRef{Tenant: tenant, Org: org, Bank: bank}
}

// OrgRef returns the reference to the bank's org.
func (b BankRef) OrgRef() OrgRef { return OrgRef{Tenant: b.Tenant, Org: b.Org} }

// IsZero reports whether the reference names no bank.
func (b BankRef) IsZero() bool { return b.Tenant == "" && b.Org == "" && b.Bank == "" }

// String returns "tenant/org/bank", for error messages and logs.
func (b BankRef) String() string { return b.Tenant + "/" + b.Org + "/" + b.Bank }

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

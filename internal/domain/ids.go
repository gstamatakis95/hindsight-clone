package domain

import "strings"

// MaxNameLen bounds tenant, org and bank ids, in bytes.
const MaxNameLen = 256

// CheckName validates a tenant, org or bank id (what names it in the message): non-empty, not "." or "..", at most
// MaxNameLen bytes, and free of '/' and control characters. Blob keys are "{tenant}/{org}/{bank}/...", so a '/'
// would let one org's or bank's prefix cover another's, and a dot segment would make the key a relative path on
// stores or tools that normalize it. The error is an InvalidField naming field.
func CheckName(field, what, v string) error {
	switch {
	case v == "":
		return InvalidField(field, "%s is required", what)
	case v == "." || v == "..":
		return InvalidField(field, "%s must not be %q", what, v)
	case len(v) > MaxNameLen:
		return InvalidField(field, "%s is longer than %d bytes", what, MaxNameLen)
	case strings.ContainsFunc(v, func(r rune) bool { return r == '/' || r < 0x20 || r == 0x7f }):
		return InvalidField(field, "%s %q contains '/' or a control character", what, v)
	}
	return nil
}

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

// Scope returns the org-level scope of o: a BankRef with an empty bank id, which names the org's own operations
// (delete_org) rather than a bank's.
func (o OrgRef) Scope() BankRef { return NewBankRef(o.Tenant, o.Org, "") }

// OrgScoped reports whether b is an org-level scope (OrgRef.Scope): an org with no bank.
func (b BankRef) OrgScoped() bool { return b.Bank == "" && b.Org != "" }

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

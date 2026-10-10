package port

import "memd/internal/domain"

// ---------- identity & scoping ----------

// BankRef names a bank. Every repo call, job and handler carries one; ids are unique only within it.
// It is an alias of domain.BankRef so domain values (units, documents, events) carry it too.
type BankRef = domain.BankRef

// ScopeKey identifies one observation scope: a canonical tag set. Empty Tags = the shared scope.
// Tags are normalized by domain.NormalizeTags; String() is the length-prefixed encoding
// ("3:foo,5:bar,b"), so no tag content can make two scopes collide. Alias of domain.ScopeKey.
type ScopeKey = domain.ScopeKey

// UnitID identifies a memory unit (fact or observation).
type UnitID = domain.UnitID

// DocumentID identifies a document within a bank.
type DocumentID = domain.DocumentID

// ChunkID identifies a stored chunk.
type ChunkID = domain.ChunkID

// EntityID identifies an entity within a bank.
type EntityID = domain.EntityID

// OperationID identifies an operation; the operation id is the job id.
type OperationID = domain.OperationID

// MentalModelID identifies a mental model within a bank.
type MentalModelID = domain.MentalModelID

// DirectiveID identifies a directive within a bank.
type DirectiveID = domain.DirectiveID

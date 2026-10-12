// Package blob declares [Store], the provider-neutral boundary to object storage, where memd keeps the original text
// of every retained document, plus the key layout ([Key], [BankPrefix], [OrgPrefix]).
//
// # The contract
//
// Store has exactly the four operations memd needs: Put an original under a key, Get it back, List a prefix page by
// page, and DeletePrefix when a bank is deleted. It uses no provider types or terms; keys are plain strings, and the
// only layout is the one [Key], [BankPrefix] and [OrgPrefix] build:
//
//	{tenant}/{org}/{bank}/docs/{document_id}/{content_hash}
//
// Errors are domain error classes: a missing key is domain.ErrNotFound, a bad argument domain.ErrInvalidArgument and
// any provider or transport failure domain.ErrUnavailable, wrapped with %w so callers test them with errors.Is. The
// provider error stays in the chain.
//
// # Providers
//
// internal/blob/s3 implements Store on S3-compatible storage (AWS S3, MinIO). cmd/memd picks the provider from
// MEMD_BLOB_PROVIDER; "s3" is the only value today and anything else fails startup.
//
// Adding a provider (GCS, Azure Blob Storage, ...):
//
//  1. Create internal/blob/<provider> with a concrete type implementing Store and a `var _ blob.Store = (*T)(nil)`
//     assertion. Import only this package, internal/domain and the provider SDK; keep the SDK out of every other
//     package.
//  2. Map the provider's errors as above, and follow the method comments on Store exactly: List pages in byte order
//     strictly after the given key and returns a short page only when nothing is left (follow the provider's own
//     pages until limit keys are read; the bank delete stops at a short page), the empty-prefix and limit
//     refusals, and DeletePrefix's partial-failure count.
//  3. Run the shared contract suite from its tests: blobtest.Run(t, func(t *testing.T) blob.Store { ... }), giving
//     every call a fresh, empty store (a new bucket or container on the provider's emulator in testcontainers).
//  4. Add the provider's settings to cmd/memd's Config and a case to the MEMD_BLOB_PROVIDER switch in wire.go, and
//     document the variables in the README.
package blob

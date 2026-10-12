// Package org owns the org lifecycle. Its one type, [Service], built by [New] from [Deps], serves
// memory.v1.OrgService: creating, reading and listing a tenant's orgs.
//
// # Where it sits
//
// org is a service. It imports domain and the store boundary, and never another service, an adapter, grpcapi,
// maintenance, gen, pgx, net/http, the S3 SDK or Datadog (depguard). cmd/memd/wire_services.go builds the Service
// (wireOrg) and hands it to grpcapi's OrgHandler. An org's config level belongs to internal/configsvc.
//
// The public surface: [Service.CreateOrg], [Service.GetOrg] and [Service.ListOrgs]. OrgService.DeleteOrg is not
// served in this version: grpcapi answers it UNIMPLEMENTED.
//
// # Org rules
//
// An org groups a tenant's banks, and every bank belongs to exactly one. Orgs are never created on first use: a
// write under an unknown org is domain.ErrOrgNotFound (NOT_FOUND), whereas a write to an unknown bank of an existing
// org still creates the bank. A typo'd org id therefore fails loudly instead of scattering data outside the org a
// later delete targets. Reads of an unknown org, or of one being deleted (DeletingAt set), are
// domain.ErrOrgNotFound; creating an org that exists is domain.ErrAlreadyExists, and one being deleted
// domain.ErrOrgDeleting.
//
// Bank creation (BankRepo.Ensure, Create and SetConfig) share-locks the org row first, so an org delete, which locks
// the row FOR UPDATE before it tombstones the org's banks, either sees a bank created before it or makes the
// creation wait and then fail with domain.ErrOrgDeleting. Org config updates take an advisory lock and a plain row
// update, which never block bank creation (docs/design/storage.md#orgs).
package org

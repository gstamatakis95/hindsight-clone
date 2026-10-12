// Package org owns the org lifecycle. Its one type, [Service], built by [New] from [Deps], serves
// memory.v1.OrgService: creating, reading, listing and deleting a tenant's orgs, and the receipts of their deletes.
//
// # Where it sits
//
// org is a service. It imports domain, jobs, blob and the store boundary, and never another service, an adapter,
// grpcapi, maintenance, gen, pgx, net/http, the S3 SDK or Datadog (depguard). cmd/memd/wire_services.go builds the
// Service (wireOrg), hands it to grpcapi's OrgHandler, registers its delete_org job and sets its tick steps. An org's
// config level belongs to internal/configsvc.
//
// The public surface: [Service.CreateOrg], [Service.GetOrg], [Service.ListOrgs], [Service.DeleteOrg] and
// [Service.GetDeletionReceipt]; the delete_org handler [Service.RunDeleteJob]; the tick's
// [Service.ResumeStalledDeletes] and [Service.PruneReceipts].
//
// # Deleting an org
//
// DeleteOrg tombstones the org and its banks in one transaction (the org row locked first), cancels their pending
// jobs, starts an IN_PROGRESS [domain.DeletionReceipt] and enqueues delete_org, an org-scoped job (its operation has
// no bank). The job fans out to the banks' delete_bank jobs (it never imports internal/bank: jobs.EnqueueDeleteBank is
// the sanctioned way to affect it), waits for the bank rows to go, sweeps the org's rows whatever their bank, deletes
// the blobs under blob.OrgPrefix after DeleteConfig.BlobGrace with a fence between pages, and finishes in one
// transaction that deletes the org's operations, events and row and counts the org's rows in every table carrying
// org_id (read from the schema). Rows left roll that transaction back and stamp the receipt FAILED_VERIFICATION; the
// run sweeps again and, failing that, retries. Every step re-reads the org, so a duplicate or resumed run is safe.
// The receipt names the org only by domain.OrgIDHash and outlives it (docs/design/storage.md#deleting-an-org).
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

// Package bank holds the bank-level engines: tenant and bank config admin, directives, bank stats and tag
// listings, prompt preview, bank templates, and the two-step bank delete.
//
// # Where it sits
//
// bank is an engine package: it imports domain, port and prompts and must never import adapters, server,
// worker, gen, pgx, net/http, the S3 SDK or Datadog (depguard). It declares the small interfaces it consumes
// instead of importing other engines: [StatsSource] (port.Reader satisfies it), [JobFence] (port.Claimer),
// [BlobLister] (port.Blob) and [RefreshQueuer] (mentalmodel.RefreshQueuer, since the mental-model engine
// owns the refresh payload and its scheduling rules). cmd/memd/wire.go builds every engine here; the
// server's BankService calls them, the worker's delete_bank handler calls [BankDeleter.Run], and the
// mental-model synthesizers and the prompt previewer use the [DirectiveSelector]. Several interfaces have
// the exact method set of the matching server interface and [ConfigPatch] is aliased by the server, so the
// implementations plug into the server directly.
//
// # Bank existence rules
//
// Reads of an unknown or tombstoned bank are domain.ErrNotFound. Writes call BankRepo.Ensure, which creates
// an unknown bank with tenant defaults, or reports domain.ErrBankDeleting (FAILED_PRECONDITION) for a
// tombstoned one; a few writes that must not create a bank (DeleteDirective) report an unknown bank as
// domain.ErrNotFound instead. SPEC "Behavioural rules" (Banks and documents).
//
// # Config admin
//
// [TenantAdmin] and [BankAdmin] read and patch the stored config levels. A [ConfigPatch] is a pure function
// from the current level to the next; it runs inside the write transaction, and the server builds it from
// the request's FieldMask and validation. Read-modify-writes serialize, so two concurrent patches of
// different fields both survive:
//
//   - UpdateBankConfig: Banks().Ensure, then tx.Seq(bank) (which locks the bank row), then a re-read of the
//     bank (Ensure's read may predate the lock under READ COMMITTED), then patch and SetConfig. A config
//     write therefore bumps the bank's change seq; mental-model staleness looks at in-scope unit seqs, so
//     no model goes stale over it. ResetBankConfig is UpdateBankConfig with a reset patch.
//   - UpdateTenantConfig: Banks().LockTenantConfig(tenant) (a transaction-scoped advisory lock in
//     Postgres, which also works before the tenant's first config row exists), then read, patch and
//     SetTenantConfig.
//
// CreateBank refuses an existing bank with domain.ErrAlreadyExists (with its own message, never the
// store's) and a tombstoned one with domain.ErrBankDeleting. GetBank returns a tombstoned bank too, with
// DeletingAt set; a client watches for NOT_FOUND to learn that a delete finished.
//
// # Directives
//
// [DirectiveAdmin] upserts (normalizing tags; an empty ID creates), lists (filtered by tag overlap, optionally
// active only) and deletes directives. [DirectiveSelector.Select] returns the active directives a request
// sees: untagged directives are global and always apply; tagged ones apply when they share a tag with the
// request; with no request tags only global ones apply; all=true (apply_all_directives) returns every
// active directive. Directives are read in a short transaction because port.Reader has no directive read.
//
// # Stats and tags
//
// [StatsReader.Stats] returns the bank's counts from indexed COUNT queries (no counter tables).
// [StatsReader.Tags] pages tags with counts from a [ListTagsRequest]: source defaults to memories (live
// units), the prefix is normalized like a tag, page_size 0 means [DefaultPageSize] (100) and larger sizes
// are capped at [MaxPageSize] (1000), and the opaque page token carries the last tag of the previous page (a
// malformed token is domain.ErrInvalidArgument). It asks the source for one row more than the page to learn
// whether a next page exists.
//
// # Prompt preview
//
// [PromptPreviewer.Preview](bank, op, overrides) resolves the effective config with the overrides on top and
// renders the exact system and user messages of op from the built-in templates, with no model call:
// retain (the extraction mode's template; chunks mode returns a SkippedReason instead), consolidation,
// reflect, synthesize and mental-model delta (the structured-delta templates). Request-time content (chunk
// text, fact and observation ids and texts, the query, "now") is shown as placeholders such as
// "{chunk text}". Each message is a list of blocks: the rendered template (source builtin) followed by the
// config fields that feed it (source config, active when set), and the preview carries the response schema.
// Reflect and synthesize see the global directives, since a preview has no request tags. An unknown op is
// domain.ErrInvalidArgument.
//
// # Templates
//
// [TemplateCodec.Export] returns a bank's own config, its directives (ids, bank refs and timestamps
// dropped) and its mental-model specs, with no memories. [TemplateCodec.CreateNamed] creates a bank from a
// template in one transaction: Banks().Create, tx.Seq(bank), the directives with new ids, each mental model,
// and each model's first build enqueued through the [RefreshQueuer] (which expects tx.Seq to have been
// taken); it returns the build operations in template order. An existing bank is domain.ErrAlreadyExists, a
// template model without an id is domain.ErrInvalidArgument, and any failure rolls the whole bank back.
//
// # Bank delete
//
// [BankDeleter] deletes a bank in two steps (SPEC "Behavioural rules", DeleteBank):
//
//  1. Tombstone (the DeleteBank RPC), in one transaction: read the bank (unknown = domain.ErrNotFound), set
//     deleting_at (MarkDeleting, which locks the bank row), cancel the bank's pending jobs (Queue.CancelBank
//     spares delete_bank) and enqueue delete_bank (serial key [DeleteBankSerialKey] "bank", payload {},
//     DedupePendingOrRunning), returning that operation. No tx.Seq is taken: Seq refuses a tombstoned bank.
//     From then on writes fail with domain.ErrBankDeleting and reads with domain.ErrNotFound. Re-issuing
//     DeleteBank returns the running delete, or a new one when the previous delete_bank failed or was
//     cancelled, which resumes a half-deleted bank.
//  2. Run (the delete_bank job): up to MaxBatches committed transactions, each removing up to BatchSize rows
//     with BankRepo.DeleteBatch. When the budget runs out it returns done=false and the worker re-enqueues
//     the job. When a batch finds nothing left, it finishes: check the [JobFence] (the job still holds its
//     operation, else domain.ErrCancelled) and that the bank is still tombstoned; if another job of the bank
//     is still PROCESSING (a handler winding down after the tombstone) it enqueues its own continuation
//     [DeleteBlockedDelay] (5 s) later and returns done=false; otherwise it deletes the blobs under
//     {tenant}/{bank}/ (page by page via BlobLister with a fence check between pages; wire.go configures
//     this), then in a last transaction re-checks for processing jobs and calls BankRepo.Delete, which
//     removes the bank row with its remaining events and operations.
//
// A run that finds the bank gone, or no longer tombstoned (a fresh bank took the id), is done and deletes no
// blobs, so a stale duplicate run never touches a new bank's data. No job outlives its bank. The returned
// operation can complete, or vanish with the bank row, while a continuation still runs; GetBank returning
// NOT_FOUND is the completion signal. Defaults ([DeleterConfig] zero values): [DefaultDeleteBatchSize]
// 1000 rows, [DefaultDeleteMaxBatches] 100 batches per run, [DefaultDeleteBlobPageSize] 1000 keys. The
// worker's maintenance tick re-enqueues delete_bank for a tombstoned bank that has none pending or running,
// so a delete survives crashes and attempt caps.
//
// # Transactions
//
// Every transaction here is short and calls no model; the Postgres Store.InTx re-runs a transaction up to
// 3 times in all after a deadlock (40P01) or serialization failure (40001), and every body resets its
// outputs and re-reads its state, so a re-run is safe.
//
// # Tests
//
// Tests run on a pgtest database and fake.Blob: bank create/get/patch and tenant patches, directive selection and
// admin, stats and tag paging, preview of each operation (and the chunks skip), template round trip and
// rollback, and the delete lifecycle (idempotent tombstone, resumption of a half-deleted bank, paged blob
// deletes with fencing, the processing-job re-check in the final transaction, a cancelled run). The
// Postgres store's concurrent-patch tests cover the config locking.
package bank

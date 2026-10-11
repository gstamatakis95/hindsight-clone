// Package configsvc owns the stored config levels and what they feed. Its one type, [Service], built by [New] from
// [Deps], serves memory.v1.TenantService (the tenant-level config) and the config part of memory.v1.BankService:
// the bank's own config and its effective view, the prompt preview and the bank's directives.
//
// # Where it sits
//
// configsvc is a service. It imports domain, the store boundary, config (the concrete *config.Resolver) and prompts,
// and never another service, an adapter, grpcapi, maintenance, gen, pgx, net/http, the S3 SDK or Datadog
// (depguard). It runs no job and no sweep. cmd/memd/wire_config.go builds the Service and hands it to grpcapi's
// TenantHandler and BankHandler.
//
// The public surface:
//
//   - Config: [Service.GetTenantConfig], [Service.UpdateTenantConfig], [Service.GetBankConfig],
//     [Service.UpdateBankConfig], [Service.ResetBankConfig].
//   - [Service.PreviewPrompt].
//   - Directives: [Service.UpsertDirective], [Service.ListDirectives], [Service.DeleteDirective].
//
// # Bank existence rules
//
// Reads of an unknown or tombstoned bank are domain.ErrNotFound. Writes call BankRepo.Ensure, which creates an unknown bank with tenant
// defaults, or reports domain.ErrBankDeleting (FAILED_PRECONDITION) for a tombstoned one; DeleteDirective must not
// create a bank, so it reports an unknown bank as domain.ErrNotFound instead. SPEC "Behavioural rules" (Banks and
// documents).
//
// # Config writes
//
// A [Patch] is a pure function from the current level to the next; it runs inside the write transaction, and
// grpcapi builds it from the request's FieldMask and validation. Read-modify-writes serialize, so two concurrent
// patches of different fields both survive:
//
//   - UpdateBankConfig: Banks().Ensure, then tx.Seq(bank) (which locks the bank row), then a re-read of the bank
//     (Ensure's read may predate the lock under READ COMMITTED), then patch and SetConfig. A config write therefore
//     bumps the bank's change seq; mental-model staleness looks at in-scope unit seqs, so no model goes stale over
//     it. ResetBankConfig checks its paths (config.ResetPaths; "*" resets everything, none at all is
//     domain.ErrInvalidArgument), then runs UpdateBankConfig with the reset patch.
//   - UpdateTenantConfig: Banks().LockTenantConfig(tenant) (a transaction-scoped advisory lock in Postgres, which
//     also works before the tenant's first config row exists), then read, patch and SetTenantConfig.
//
// # Directives
//
// [Service.UpsertDirective] normalizes tags and creates (empty ID) or replaces a directive;
// [Service.ListDirectives] filters by tag overlap, optionally active only; [Service.DeleteDirective] removes one.
// Which directives apply to a request is domain.SelectDirectives, the rule reflect and the mental-model synthesize
// prompt share; the prompt preview applies it with no request tags (the untagged directives). Directives are read
// in a short transaction because store.Reader has no directive read.
//
// # Prompt preview
//
// [Service.PreviewPrompt](bank, op, overrides) resolves the effective config with the overrides on top and renders
// the exact system and user messages of op from the built-in templates, with no model call: retain (the extraction
// mode's template; chunks mode returns a SkippedReason instead), consolidation, reflect, synthesize and mental-model
// delta (the structured-delta templates). Request-time content (chunk text, fact and observation ids and texts,
// the query, "now") is shown as placeholders such as "{chunk text}". Each message is a list of blocks: the rendered
// template (source builtin) followed by the config fields that feed it (source config, active when set), and the
// preview carries the response schema. Reflect and synthesize see the global directives, since a preview has no
// request tags. An unknown op is domain.ErrInvalidArgument.
//
// # Tests
//
// Tests run on a pgtest database: tenant and bank config patches (including a tombstoned bank), reset, directive
// admin, and the preview of each operation (and the chunks skip). The Postgres store's concurrent-patch tests cover
// the config locking.
package configsvc

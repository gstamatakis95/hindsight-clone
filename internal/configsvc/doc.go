// Package configsvc owns the stored config levels and what they feed. Its one type, [Service], built by [New] from
// [Deps], serves memory.v1.ConfigService: the tenant's config, the org's config and the bank's own config, each as a
// [View] (own and effective), the prompt preview and the bank's directives.
//
// # Where it sits
//
// configsvc is a service. It imports domain, the store boundary, config (the concrete *config.Resolver) and prompts,
// and never another service, an adapter, grpcapi, maintenance, gen, pgx, net/http, the S3 SDK or Datadog
// (depguard). It runs no job and no sweep. cmd/memd/wire_services.go builds the Service and hands it to grpcapi's
// ConfigHandler.
//
// The public surface:
//
//   - Config: [Service.GetTenantConfig], [Service.UpdateTenantConfig], [Service.ResetTenantConfig],
//     [Service.GetOrgConfig], [Service.UpdateOrgConfig], [Service.ResetOrgConfig],
//     [Service.GetBankConfig], [Service.UpdateBankConfig], [Service.ResetBankConfig]. Each returns a [View]: Own
//     is what the level stores, Effective is it merged over the less specific levels (config.Resolver.Resolve),
//     from the levels read in the same transaction.
//   - [Service.PreviewPrompt].
//   - Directives: [Service.CreateDirective], [Service.UpdateDirective], [Service.ListDirectives],
//     [Service.DeleteDirective].
//
// # Bank existence rules
//
// Reads of an unknown or tombstoned org or bank are domain.ErrNotFound. Writes call BankRepo.Ensure, which creates an
// unknown bank with empty overrides in its org, or reports domain.ErrOrgNotFound for an unknown org (orgs are never
// created on first use), domain.ErrOrgDeleting or domain.ErrBankDeleting (FAILED_PRECONDITION) for a tombstoned one;
// UpdateDirective and DeleteDirective must not create a bank, so they report an unknown bank as domain.ErrNotFound
// instead (docs/design/curation-and-operations.md#common-rules).
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
//     domain.ErrInvalidArgument), then runs UpdateBankConfig with the reset patch; ResetTenantConfig does the same
//     over UpdateTenantConfig.
//   - UpdateTenantConfig: Banks().LockTenantConfig(tenant) (a transaction-scoped advisory lock in Postgres, which
//     also works before the tenant's first config row exists), then read, patch and SetTenantConfig.
//   - UpdateOrgConfig: Orgs().LockConfig(org) (an advisory lock), then read, patch and Orgs().SetConfig, a plain
//     update of the org row that never blocks bank creation in the org (which only share-locks the org's key).
//     Orgs are never created here: an unknown org is domain.ErrOrgNotFound. ResetOrgConfig runs it with the reset
//     patch.
//
// # Directives
//
// A directive's name identifies it within its bank: it is required, and a taken one is domain.ErrAlreadyExists
// (a unique index, so concurrent creates cannot both win). [Service.CreateDirective] normalizes tags and stores the
// directive under a new id; [Service.UpdateDirective] locks the directive row, applies a pure [DirectivePatch] (built
// by grpcapi from the update_mask) and stores the result, keeping the id;
// [Service.ListDirectives] pages them oldest first (created_at, then id; domain.ListPage), filtered by a flat tag
// filter (unset match = any_strict: a shared tag) and optionally active only; [Service.DeleteDirective] removes one.
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
// Tests run on a pgtest database: tenant and bank config patches and views (including a tombstoned bank), tenant
// and bank reset, directive create, update, list and delete, and the preview of each operation (and the chunks
// skip). The Postgres store's concurrent-patch tests cover the config locking.
package configsvc

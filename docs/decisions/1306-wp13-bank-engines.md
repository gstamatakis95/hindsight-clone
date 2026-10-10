# 1306 — Bank engines: directives, delete budget and fencing, templates, prompt preview

## Context
WP-13 implements the bank package: TemplateCodec, DirectiveSelector/DirectiveAdmin, TenantAdmin,
BankAdmin, StatsReader, PromptPreviewer and BankDeleter. The spec leaves several details open.

## Decision
- **Directive reads without a CCR.** `port.Reader` has no directive read, but `Tx.Directives()` does, so the
  selector and the admin take `port.Store` and read in a short InTx (spec signature
  `NewDirectiveSelector(r port.Reader)` becomes `NewDirectiveSelector(s port.Store)`). Every read checks
  `Banks().Get` and answers `ErrNotFound` for an unknown or tombstoned bank.
- **Directive matching (Select).** Hindsight's isolation-mode rule: untagged directives are global and
  always apply; a tagged directive applies when it shares at least one tag with the request (any
  overlap, both sides normalized). With no request tags only global directives apply. `all=true`
  (apply_all_directives) returns every active directive. `ListDirectives` follows its own doc: tags
  empty = all, otherwise only directives sharing a tag (untagged ones are not listed by a tag filter).
- **DeleteDirective on a tombstoned bank** is a write: `ErrBankDeleting` (FAILED_PRECONDITION), like every
  other write. Unknown bank or directive: `ErrNotFound`.
- **Template create.** `TemplateCodec.CreateNamed(ctx, b, name, t)` returns the bank and the first-build
  operation of every model; `Create` delegates to it with an empty name so the server keeps compiling
  until WP-14 switches it to `CreateNamed`. One transaction: `Banks().Create`, `tx.Seq` (taken before any
  enqueue), directives upserted with new ids and normalized tags, `MentalModels().Create` plus
  `RefreshQueuer.QueueRefresh` per model, `Queue().Get` for each build. A spec without an id is
  `INVALID_ARGUMENT`; a failure anywhere rolls the whole bank back. Export drops directive ids, bank refs
  and timestamps.
- **Delete budget.** `DeleterConfig{BatchSize: 1000, MaxBatches: 100}` by default: one `DeleteBatch` per
  InTx, at most MaxBatches per run, then `done=false` and the handler re-enqueues with DedupePending. A
  batch error that means "nothing to do" (bank gone = `ErrNotFound`, bank not tombstoned =
  `ErrFailedPrecondition` from a fresh bank) ends the run as done without touching blobs.
- **Fencing window.** After the last batch: `JobFence.Alive(job)` (port.Claimer), then a fresh
  `Banks().Get` confirming the bank still exists and is still tombstoned, then `Blob.DeletePrefix`, then
  `Banks().Delete`. The remaining window is between that check and `DeletePrefix`: a duplicate run would
  have to finish `Banks().Delete` and a client write would have to recreate the bank and store a blob in
  it inside that gap. Accepted as the narrow window the orchestrator asked for. `Alive` does not check the
  worker id, so a re-claimed duplicate still passes it; the tombstone re-check is what protects a fresh
  bank. `NewBankDeleter` grew a `JobFence` and a `port.Clock` (deleting_at) parameter.
- **Tombstone** takes no change seq (`Tx.Seq` refuses a tombstoned bank, and nothing written is memory
  content). `MarkDeleting` runs first, so the bank row is locked before `CancelBank` and `Enqueue` take
  their share locks. A second tombstone joins the queued or running delete_bank (DedupePendingOrRunning).
- **Tags paging.** Page tokens are opaque (base64url JSON of the last tag); page size defaults to 100 and
  is clamped to 1000; the source is asked for limit+1 rows to detect a next page. The prefix is passed
  through `domain.NormalizeTag` (NFC, trim, lowercase), because stored tags are normalized; an unspecified
  source is sent as `memories`.
- **Prompt preview placeholders.** Request-time content is shown as placeholders: `{chunk text}`,
  `{item context}`, `{fact id}`/`{fact text}`, `{observation id}`/`{observation text}`, `{query}`,
  `{mental model name}`, `{mental model source query}`, `{section heading}`/`{block markdown}`, and `{now}`
  for the current time (the previewer has no clock; the rendered zero time is replaced). Each message is
  one builtin block with the rendered template, followed (system message only) by config blocks naming
  the BankConfig field that fed it (`retain_mission`, `custom_extraction_prompt`, `entity_labels`,
  `observations_mission`, `reflect_mission`, `disposition`, and directives with an empty field), active
  when set. Retain uses the extraction-mode template and `ExtractSchema(name, false)`; chunks mode returns
  only `SkippedReason`. Consolidation and the structured delta carry their schemas; reflect (tool loop)
  and synthesize (markdown) have none. Reflect previews with the global directives (no request tags),
  budget low, every tool offered, and a `{query}` user turn; the exact reflect user turn is WP-11's.

## Alternatives
- A `Reader` method for directives via the CCR: unnecessary, the transaction already has it.
- Clock-based delete budget: the spec says "a batch budget"; batches are deterministic in tests.
- Rendering preview runtime content as empty strings: hides which sections exist; placeholders show them.

## Spec lines involved
SPEC "Behavioural rules" (banks and documents: DeleteBank, reads NOT_FOUND), "Jobs" (delete_bank, bounded
runs), gRPC surface (BankService: templates, directives, ListTags, PreviewPrompt), Function signatures
(engine/bank), decision 1301 (delete_bank payload and serial key).

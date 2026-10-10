# memd swarm status

Orchestrator log for the build driven by `docs/SWARM_PROMPT.md`. Merges are local only (no remote PRs);
"PR" below means the work package branch handed to review.

## Work packages

| WP | Title | State | Branch | PR | Blockers | Open CCRs | LOW findings |
|----|-------|-------|--------|----|----------|-----------|--------------|
| WP-00 | Contracts and scaffold | merged (0374c87, tag contracts-v1) | wp/WP-00-contracts | f5ab75f | — | WP-00-1 approved + applied | see carried-forward |
| WP-01 | Postgres adapter | in progress | wp/WP-01-postgres | — | — | — | — |
| WP-02 | Fakes | merged | wp/WP-02-fakes | 0d2775e | — | — | 2 cosmetic LOW |
| WP-03 | Gateway and S3 adapters | merged (8dd2090 + follow-up) | wp/WP-03-gateway-s3 | 20fab3d | — | — | 5 LOW accepted (see log) |
| WP-04 | Config, routing, time, prompts | merged (756d945 + follow-up) | wp/WP-04-config-prompts | fb9fa38 | — | WP-04-1 approved + applied | — |
| WP-05 | Worker runtime | merged (08d057d) | wp/WP-05-worker-runtime | 7afa08e | — | — | H3 attempt-charge gap; LOWs in log |
| WP-06 | Server shell | merged | wp/WP-06-server-shell | 0f2815d | — | — | jsonToStruct non-object (LOW) |
| WP-07 | Eval harness | merged (f5da652 + follow-up) | wp/WP-07-eval | 7865932 | — | WP-07-1 approved + applied | 1 LOW accepted (see log) |
| WP-08 | Retain engine | ready (awaiting user go-ahead) | — | — | — | — | — |
| WP-09 | Recall engine | ready (awaiting user go-ahead) | — | — | — | — | — |
| WP-10 | Consolidation engine | ready (awaiting user go-ahead) | — | — | — | — | — |
| WP-11 | Reflect engine | ready (awaiting user go-ahead) | — | — | — | — | — |
| WP-12 | Mental model engine | ready (awaiting user go-ahead) | — | — | — | — | — |
| WP-13 | Bank, curation, documents, maintenance | ready (awaiting user go-ahead) | — | — | — | — | — |
| WP-14 | Integration | waiting | — | — | all | — | — |

## Environment notes

- Toolchain: Go 1.27.2, buf 1.73.0, sqlc 1.31.1, mockery 3.8.0 (v3 config format), golangci-lint 2.14.0,
  Python 3.12 (alembic in a repo-local `.venv`).
- Docker daemon must be running for compose Postgres and testcontainers.
- Hindsight reference source: `third_party/hindsight` (git-ignored, read-only clone).

## Log

- 2026-10-10 setup: moved spec into `docs/`, untracked IDE files, created this file.
- 2026-10-10 WP-00 handed in on wp/WP-00-contracts @ 17fe418; worker reports all Accept checks pass. Reviewer spawned.
- 2026-10-10 ruling CCR WP-00-1 APPROVED: add golang.org/x/text for NFC tag normalization (spec requires NFC;
  stdlib lacks it). Applied on the WP-00 branch before merge, since contracts aren't frozen until merge.
- 2026-10-10 WP-00 review round 1: BLOCK (6 HIGH, 13 MEDIUM, 4 LOW groups). All gates passed; findings are in
  contract semantics (Reschedule/Upgrade/dedupe, ModelBinding and TagGroup comments, event paging, fencing).
- 2026-10-10 ruling: overrides-never-dedupe (SPEC 908/1219/2034) applies to all kinds except
  refresh_mental_model, which always joins the queued refresh and upgrades it (SPEC 1924/2005); specific rule wins.
- 2026-10-10 ruling: Upgrade merges (force_full OR-ed; overrides/strategy replaced only when supplied);
  Reschedule only on PENDING; worker fencing via ErrFailedPrecondition without signature changes; cancelled
  processing jobs keep their serial key until the handler stops; Enqueue on a deleting bank fails except
  delete_bank. Findings and rulings sent back to the WP-00 worker. LOW findings: worker fixes the cheap ones,
  leftovers to be listed here after round 2.
- 2026-10-10 WP-00 review round 2 on 2c8bb22: all round-1 HIGHs verified fixed; BLOCK on 4 new HIGHs (finalizer
  loop, finalizer swallowing a manual refresh, deletes on a live bank, last_seq stalling filtered pollers).
- 2026-10-10 ruling R10': drop the finalizer; runner owns the attempt cap; a refresh at max attempts is FAILED;
  pause/REFRESH_FAILED via an idempotent RunMaintenance reconcile. R8': cancel of a processing job sets a
  cancel-requested flag (status stays processing) instead of a new status.
- 2026-10-10 user guidance: solid early iteration that doesn't lose data and works as advertised; no
  overengineering. Review bar = data loss, double runs, or advertised behaviour broken. Round 3 trimmed to those.
- Deferred to WP-01/WP-02 acceptance: porttest coverage gaps from review round 2 (tenant isolation on new write
  ports, R8 edge cases, tombstoned-bank Fail, Enqueue/Upgrade race). Ruling: internal/porttest is not a frozen
  path; WP-01 may add tests there in new files.
- 2026-10-10 WP-00 review round 3 on f5ab75f: MERGE (all round-2 HIGHs fixed; no CRITICAL/HIGH).
- 2026-10-10 merged wp/WP-00-contracts into main (merge commit 0374c87), tagged contracts-v1. Contracts frozen.
  Gates on main: go build, go vet, make lint, make test pass.
- 2026-10-10 main fix: go.mod `ignore ./third_party` (the git-ignored Hindsight clone has Go examples that broke
  `go build ./...` in checkouts that have it). .claude/ (agent worktrees) git-ignored.
- 2026-10-10 user checkpoint: Phase 1 awaits the user's go-ahead.

## Carried forward (not blocking; assign as acceptance items)

- WP-12/WP-05: the refresh pause reconcile must key on the latest *terminal* refresh op (FAILED and newer than the
  last success), not the latest op, or a quickly re-enqueued refresh hides the failure (SPEC 2003). Exact
  idempotence may need mental_model_history.operation_id (CCR if wanted).
- WP-01/WP-02: porttest gaps from round 2 (tenant isolation on new write ports, Fail(cause, retryAt) on a
  cancel-requested row ends CANCELLED, tombstoned-bank Fail, Enqueue/Upgrade race); adapter obligations in
  port docs (READ COMMITTED, sorted advisory locks, joined row FOR UPDATE, bank FOR KEY SHARE).
- WP-13: check Alive immediately before Blob.DeletePrefix in the bank deleter (narrow fencing window).
- Known gaps accepted (low): co-occurrence counts not decremented on Detach/PruneOrphans; Dirty doesn't purge
  non-live rows; Validate allows Match=EXACT with groups; GraphQuery direction unstated; non-object Upgrade
  payload / empty-key dedupe not rejected; ifacedoc gen/ skip not root-anchored; Complete/Fail have no
  worker token (fencing via heartbeat only).
- 2026-10-10 user said continue: Phase 1 started. WP-01, 02, 03, 04, 06, 07 workers launched in parallel worktrees.
  Decision-note number ranges per WP: WP-0N uses N00-N99. WP-05 launch blocked by the auto-mode permission
  classifier; awaiting the user.
- 2026-10-10 WP-05 launch retried at the user's request; worker running.
- 2026-10-10 WP-03 and WP-07 handed in (e304714, 2b33aa0); reviewers spawned.
- 2026-10-10 ruling CCR WP-07-1 APPROVED and applied on main: RetainResponse.usage (repeated SlotUsage = 3),
  additive, per SPEC Model routing "usage accounting". Affects WP-08 (return usage), WP-06/WP-14 (copy into
  response), WP-07 (read it).
- 2026-10-10 env: a stale golangci-lint v1 in ~/go/bin shadows Homebrew's v2.14; run make with
  PATH="/opt/homebrew/bin:$PATH". Running workers told.
- 2026-10-10 WP-05 handed in (9a9d8fe, rebased on f7f0488); reviewer spawned.
- 2026-10-10 WP-03 review: MERGE (0 CRITICAL/HIGH, 1 MEDIUM, 8 LOW). Merged into main as 8dd2090; gates
  (gen no diff, lint incl. buf breaking vs contracts-v1, test) pass on main. The MEDIUM (usage not summed on a
  no-choices response) and two cheap LOWs (negative config defaults, double Extra copy) went back to the worker
  as a follow-up. Remaining LOWs accepted: decode-retry on caller bug, duplicate rerank indices, seekable size
  check, MinIO suite skips without Docker, validator text sent to model.
- 2026-10-10 WP-07 review: MERGE (0 CRITICAL/HIGH, 4 MEDIUM, 4 LOW). Merged into main as f5da652; gates pass.
  The MEDIUMs went back to the worker as a follow-up, together with reading RetainResponse.usage. Accepted LOW:
  Background sums all bank ops (a rerun needs a fresh -bank-prefix).
- 2026-10-10 WP-03 follow-up 20fab3d (usage sum on no-choices, positive defaults, single Extra copy) checked by
  the orchestrator and merged; lint and gateway tests pass. WP-03 done.
- 2026-10-10 WP-02 handed in (8bf5ef7, rebased on main); reviewer spawned. Fakes pass all 4 porttest suites.
- 2026-10-10 WP-07 follow-up 7865932 merged (eval-only scope checked); lint, eval tests and golden-stub run pass. WP-07 done.
- 2026-10-10 PAUSED on the user's usage limit. In flight at pause: WP-01, WP-04, WP-06 workers; WP-02 and WP-05
  reviewers. Their worktrees under .claude/worktrees/ keep committed work. Next: collect the reviews, merge,
  then ask the user before Phase 2.
- 2026-10-10 RESUMED after the usage-limit pause. WP-01, WP-04, WP-06 workers and the WP-02 reviewer were cut off
  mid-task; all resumed with their context.
- 2026-10-10 WP-05 review: BLOCK. Two HIGH: the same runner can re-claim its own running job (double run), and
  Complete skips the Alive gate on lost jobs. Third HIGH: shutdown releases spend the attempt cap. Ruling:
  documentation route plus a graceful drain before cancel, no CCR. Decisions 502 and 503 accepted. Fixes and
  MEDIUMs (heartbeat timeout and self-fencing, HasPending scope, Complete/Fail retry) sent back to the worker.
- 2026-10-10 WP-04 handed in (f503f7a, rebased on dbc0eb3); reviewer spawned.
- 2026-10-10 ruling CCR WP-04-1 APPROVED and applied on main: drop omitempty on the clearable BankConfig lists and
  maps (entity_labels, consolidation_strategies, recall.strategy_boosts, recall.enabled_strategies,
  reflect.enabled_tools, observation_scopes.custom). Without it an explicit clear was lost when saved to jsonb
  and the tenant value came back (data loss). Checked: [] survives the round trip; nil stays nil.
- 2026-10-10 note for WP-08/09: timex.TimeNormalizer methods take the resolved BankConfig (decision 400).
- 2026-10-10 WP-06 handed in (1d86a70); reviewer spawned. Ruling: the .mockery.yaml line for internal/server is
  accepted (not frozen).
- Carried forward (WP-06): 29 RPCs sit behind 7 server-declared interfaces with no implementation yet
  (TenantAdmin, BankAdmin, DirectiveAdmin, DocumentAdmin, ObservationAdmin, MentalModelAdmin, OperationReader).
  Plan: implement them in WP-13 (tenant/bank/directive/document/observation admin), WP-12 (MentalModelAdmin)
  and WP-14 (OperationReader, wiring). ListDocuments, ListScopes, ListMentalModels and DeleteFolder need
  port.Reader methods, so expect one batched CCR at the start of Phase 2. Override validation must accept the
  RPC names the server passes (Retain, Recall, Reflect, UpdateMemory, Consolidate, ReprocessDocument,
  PreviewPrompt, CreateMentalModel, RefreshMentalModel); check this against WP-04 at WP-14.
  CreateBankFromTemplate drops `name`: WP-13 to extend TemplateCodec.Create.
- 2026-10-10 WP-02 review: BLOCK on 2 HIGH (the fake LLM ignores the schema; the fake LinkRepo accepts
  self-links). Both make the fakes looser than production. Ruling: move the gateway JSON-schema validator to a
  shared internal/jsonschema package (WP-02 may touch internal/adapter/gateway for that move only) so the fake
  validates outputs the same way. Fixes and MEDIUMs sent back to the worker.
- 2026-10-10 WP-05 review round 2 on 1ee03bb: MERGE. H1, H2 and M1-M3 verified; the M2 probe-tx design was
  judged safe against Postgres. One new MEDIUM (a retried Fail(retryAt=now) after a lost ack can reset another
  worker's claim) sent back as a one-line fix before merge.
- Carried forward to WP-14: set RunnerConfig.StaleAfter and MaintenanceDeps.StaleAfter from one value.
- Carried forward to WP-02: InTx must roll back on error (the WP-05 pending probe relies on it).
- 2026-10-10 WP-04 review: BLOCK on 2 HIGH (timex out-of-range windows and fallback errors fail recall and
  consolidation instead of degrading; fact_type label renamed from Hindsight's "assistant"). Fix list sent back.
- Carried forward from the WP-04 review:
  - WP-06/WP-14: proto3 can't tell an absent repeated field from an empty one. The request converter must not
    turn an empty repeated override field into an explicit clear outside a FieldMask.
  - WP-08: map extraction fact_type "assistant" to domain.FactTypeExperience.
  - WP-10: inject the per-batch fact-id enum, minItems and maxItems into the consolidation schema per call.
- 2026-10-10 WP-02 round-1 fixes in at 0d2775e (internal/jsonschema extracted from the gateway); narrow round-2 review spawned.
- 2026-10-10 WP-05 final fix 7afa08e (a Fail due within the settle timeout is tried once, never retried;
  shutdown grace 20s) checked by the orchestrator and merged as 08d057d; gates pass on main. WP-05 done.
  Accepted gaps: a shutdown release still spends an attempt (decision 501); a cancelled older run briefly holds
  an untracked slot; consolidate may take every free slot. k8s terminationGracePeriodSeconds >= 50s (60s
  recommended).
- 2026-10-10 WP-02 review round 2: MERGE. Merged into main; gates pass. WP-02 done. internal/jsonschema is now a
  shared package (gateway + fake). Cosmetic LOWs left: a no-op %w wrap in gateway chat.go:215; a broken doc
  comment in fake/entities.go:58-61.
- 2026-10-10 WP-06 review: BLOCK on 2 HIGH: (1) a no-mask update replaces whole nested sub-structs, silently
  wiping stored overrides (data loss); (2) the bufconn/round-trip acceptance tests never assert what is forwarded
  (a hard-coded tenant passed every test). Tenant isolation, id checks and error mapping were verified correct.
  Fixes, the override-validation MEDIUM and cheap LOWs sent back to the worker.
- For the Phase 2 CCR batch: Memory.stale outside Recall needs a stale field from the curate lister.
- 2026-10-10 WP-06 round-1 fixes in at 0f2815d (leaf-path implied masks; per-RPC forwarding test for all 48 RPCs); narrow round-2 review spawned.
- 2026-10-10 WP-06 review round 2: MERGE (mutations now caught). Merged into main; gates pass. WP-06 done.
- 2026-10-10 WP-04 round-1 fixes in at bd81d61; narrow round-2 review spawned. API notes for Phase 2:
  ConsolidateObservation.SourceMemories is []SourceMemory; the directive done tools are read via
  prompts.DoneTool; WP-08 maps "assistant" to FactTypeExperience; WP-10 injects the source_fact_ids enum,
  minItems and maxItems; engines treat "N/A" and null as empty dimensions.
- 2026-10-10 WP-04 review round 2: MERGE. Merged into main as 756d945; gates pass. Phase 2 dependencies (WP-02,
  WP-04) are now met. Follow-up sent to the WP-04 worker: Normalize must never fail a retain on a TIME_PARSE
  error; the default extraction schema is non-nullable.
- 2026-10-10 user checkpoint: Phase 2 (WP-08..13) awaits the user's go-ahead.
- 2026-10-10 WP-04 follow-up fb9fa38 checked by the orchestrator and merged; gates pass. WP-04 done. Note for WP-08:
  get the extraction schema from prompts.ExtractSchema(name, data.OptionalDimensions), not Schema.
- 2026-10-10 WP-01 handed in (dbbd51b); reviewer spawned. Worker notes: the fake should also run RunStoreGapsSuite/RunQueueGapsSuite (2 lines in internal/fake); delete the porttest_test.go placeholders after merge.

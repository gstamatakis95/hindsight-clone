# memd swarm status

Orchestrator log for the build driven by `docs/SWARM_PROMPT.md`. Merges are local only (no remote PRs);
"PR" below means the work package branch handed to review.

## Work packages

| WP | Title | State | Branch | PR | Blockers | Open CCRs | LOW findings |
|----|-------|-------|--------|----|----------|-----------|--------------|
| WP-00 | Contracts and scaffold | merged (0374c87, tag contracts-v1) | wp/WP-00-contracts | f5ab75f | — | WP-00-1 approved + applied | see carried-forward |
| WP-01 | Postgres adapter | merged (991ae7d + follow-up) | wp/WP-01-postgres | 9d189fa | — | — | LOWs in log |
| WP-02 | Fakes | merged | wp/WP-02-fakes | 0d2775e | — | — | 2 cosmetic LOW |
| WP-03 | Gateway and S3 adapters | merged (8dd2090 + follow-up) | wp/WP-03-gateway-s3 | 20fab3d | — | — | 5 LOW accepted (see log) |
| WP-04 | Config, routing, time, prompts | merged (756d945 + follow-up) | wp/WP-04-config-prompts | fb9fa38 | — | WP-04-1 approved + applied | — |
| WP-05 | Worker runtime | merged (08d057d) | wp/WP-05-worker-runtime | 7afa08e | — | — | H3 attempt-charge gap; LOWs in log |
| WP-06 | Server shell | merged | wp/WP-06-server-shell | 0f2815d | — | — | jsonToStruct non-object (LOW) |
| WP-07 | Eval harness | merged (f5da652 + follow-up) | wp/WP-07-eval | 7865932 | — | WP-07-1 approved + applied | 1 LOW accepted (see log) |
| WP-08 | Retain engine | merged (ada201a) | wp/WP-08-retain | — | — | — | — |
| WP-09 | Recall engine | merged (d790947) | wp/WP-09-recall | — | — | — | — |
| WP-10 | Consolidation engine | merged (fa25d70) | wp/WP-10-consolidate | — | — | — | — |
| WP-11 | Reflect engine | merged (3330af6) | wp/WP-11-reflect | — | — | — | — |
| WP-12 | Mental model engine | merged (dbfa13f) | wp/WP-12-mentalmodel | — | — | — | — |
| WP-13 | Bank, curation, documents, maintenance | merged (fffcf5b) | wp/WP-13-bank-curate-docs-maint | — | — | — | — |
| WP-15 | Port extension (CCR batch) | merged (b5d9e2d) | wp/WP-15-port-extension | 9f9451e | — | batch applied | coverage gap: CitingScopes/IDsByDocument on unknown/tombstoned bank |
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
- 2026-10-10 WP-01 review: MERGE (0 CRITICAL/HIGH). Merged into main as 991ae7d; full gates pass (postgres
  suite 94 s). Phase 1 complete apart from follow-ups. MEDIUMs sent back as a follow-up (SQLSTATE class 22 ->
  InvalidArgument, DeleteBatch tombstone re-check in tx, FK-less tables in the final Delete, tag
  normalization, degenerate temporal window, InTx deadlock retry, test hardening, fake gap-suite wiring).
- Carried forward to engine WPs (Phase 2):
  - handlers test port.LostJobs(err), not errors.Is(err, ErrConflict);
  - call tx.Seq before Enqueue inside a tx (the reverse order can deadlock with ReclaimStale);
  - WP-08: Chunks.Committed also returns the previous completed retain's hashes, and DeleteByDocument doesn't
    delete chunks. Retain must tell "committed in this retain" from leftovers, or an interrupted re-retain
    loses facts.
- 2026-10-10 WP-01 follow-up 9d189fa merged; full gates pass. WP-01 done. PHASE 1 COMPLETE (WP-01..07).
  Notes: InTx retries the whole fn up to 3 times on 40P01/40001, so an InTx fn must have no side effects outside
  the tx (engine WPs). A row Claim fails as undecodable gets no OPERATION_FAILED event. The fake now runs the
  gap suites; the porttest placeholders are gone.
- 2026-10-10 user said yes: Phase 2 started. WP-08..13 workers launched in parallel worktrees (decision ranges
  800-1399). CCR process for this phase: engines declare consumer interfaces and file CCRs for missing port
  methods; the orchestrator batches approvals; a follow-up package (WP-15) implements them in the fake,
  Postgres and porttest before WP-14 wires everything.
- 2026-10-10 WP-09 handed in (c4f22a2); reviewer spawned. CCR WP-09-1 (Reader: BankTags, GetChunks,
  EntityStates, StaleScopes) PENDING, held for the Phase 2 CCR batch. WP-14: wire a tokenizer for the recall
  TokenCounter; Recall must not be called inside Store.InTx.
- 2026-10-10 WP-11 handed in (92941cf); reviewer spawned. CCR WP-11-1 (domain.ErrContextOverflow set by the
  gateway) and CCR WP-11-2 (Reader.GetChunks, overlapping WP-09-1) PENDING for the batch. Wiring notes for
  WP-12/14 are in docs/wp/WP-11-pr.md.
- 2026-10-10 WP-13 handed in (8247d6a); reviewer spawned. CCR WP-13-1 (Reader: GetUnit, ListUnits,
  ObservationHistory, BankStats, TagCounts, ListDocuments, ListScopes, MentalModelSpecs, ModelsCiting,
  BanksToConsolidate; domain.Unit.Stale) PENDING for the batch. WP-14 wiring notes are in docs/wp/WP-13-pr.md
  and decision 1302 (alias the server admin types). WP-10's consolidate handler must accept
  curate.ConsolidatePayload {"scopes":[{"tags":[...]}]}. ServerDefaults should set retain.entity_match_threshold.
- 2026-10-10 PAUSED on the user's usage limit. In flight: WP-08, WP-10, WP-12 workers; WP-09, WP-11, WP-13
  reviewers. Pending CCRs for the batch: WP-09-1, WP-11-1, WP-11-2, WP-13-1. Next: collect the reviews,
  fix/merge, rule on the CCR batch, assign WP-15 (implement the approved port methods in fake + Postgres +
  porttest), then WP-14.
- 2026-10-10 RESUMED after the second usage-limit pause: WP-08, WP-10, WP-12 workers and the WP-09, WP-11, WP-13 reviewers resumed with their context.
- 2026-10-10 WP-10 handed in (8a22c21; PR doc staged, unsigned because 1Password signing is failing); reviewer
  spawned. CCR WP-10-1 (Reader.CountObservations/ListScopes/ListMentalModels, EvidenceRepo.CitingScopes/
  DeleteScopes, UnitRepo.DocumentUnits) PENDING, overlapping WP-13-1. Notes: WP-14 must not re-enqueue on More
  (Run enqueues its own continuation); WP-08 stores the item's observation_scopes in
  Document.Overrides.DefaultObservationScopes; WP-12 accepts refresh payload {"model_id":...}.
- 2026-10-10 WP-11 review: BLOCK on 2 HIGH (the first turns use tool_choice auto, so a prose reply fails as
  UNAVAILABLE; ReflectRequest.Types is not honoured by the tools). Decision 1100 overruled: InvalidArgument,
  FailedPrecondition and NotFound pass through. Carry to WP-12: agentic refresh sets budget MID, AsDocument,
  Types, ExcludeMentalModels and Seq.
- 2026-10-10 BLOCKER: git commit signing via 1Password fails ("failed to fill whole buffer"); the user was asked
  to unlock 1Password. Orchestrator and worker commits are blocked until then.
- 2026-10-10 WP-08 and WP-12 handed in (work staged, unsigned); reviewers spawned. A CCR batch consolidation
  helper is merging WP-08-1/2, WP-09-1, WP-10-1, WP-11-1/2, WP-12-1 and WP-13-1 into one port diff plus a WP-15
  card. Signing note: all existing commits carry signatures; the "N" in %G? only reflects the missing
  gpg.ssh.allowedSignersFile.
- 2026-10-10 WP-09 review: BLOCK on 3 HIGH (temporal boost uses the wrong proximity signal, so it demotes
  in-window hits; no default 0.3 semantic floor; the graph arm follows temporal links). Rulings: CCR WP-09-2
  APPROVED (TemporalQuery gains Embedding + MinSimilarity; added to the batch); the empty tag leaf keeps
  domain.MatchGroup semantics (documented deviation); with rerank off, passthrough is sorted by unboosted RRF;
  no stage-2 boost; prefer_observations uses Hindsight's window rule.
- 2026-10-10 WP-13 review: BLOCK on 3 HIGH (a running job can write into a recreated bank after delete;
  ClearMemoryObservations leaves the memory stamped, losing observations; rederive drops entity edits on a text
  race). Plus a WP-10 HIGH, forwarded to the WP-10 reviewer: consolidation stamps over a concurrent retag/edit
  reset. Rulings: graph_maintenance enqueued from every unit-removal path; DeletePrefix re-checks per page;
  re-issuing DeleteBank resumes a half-deleted bank; WP-14 makes delete_bank retry uncapped; edit/restore keep
  the stamp until rederive writes the embedding.
- 2026-10-10 WP-11 round-1 fixes staged (unsigned) on 92941cf; narrow round-2 review. Ruling: WP-11's internal/prompts additions (reflect_tool_error_* templates) are accepted.
- 2026-10-10 WP-11 review round 2: MERGE (pending commit signing). Small follow-up requested meanwhile: guard reflect_system recall text on .Recall; fail fast with no search tools; split the MentalModels flag.
- 2026-10-10 WP-10 review: BLOCK on 5 HIGH (a poison unit blocks later scopes / the job never terminates;
  concurrent edits and retags overwritten; removed content resurrected; refresh triggers lost on a partial
  run). Rulings: a health probe decides poison vs outage; decide-time Seq/Tags checked at apply; skip whole
  actions on gone evidence (never trim); refreshes enqueued per apply tx; ClearScopes doesn't enqueue (WP-13
  owns it). For WP-15: DeleteCiting must lock observations FOR UPDATE before reading co-sources. For WP-14: the
  consolidate handler must not re-enqueue on More.
- 2026-10-10 WP-08 review: BLOCK on 5 HIGH (a decision-801 resume can lose facts permanently; a sync retain
  interleaves with document jobs; linking starves units in dense clusters; Jane merges into John without
  Hindsight's trigram/token gates; per-item observation_scopes dropped). Rulings: a run-ownership re-check after
  tx.Seq in every batch/complete tx; cap links on the new unit only; port Hindsight's merge gates; CCR WP-08-2
  REJECTED (fold the item's scopes into the document overrides' DefaultObservationScopes instead).
- 2026-10-10 WP-09 round-1 fixes staged (unsigned) on c4f22a2; narrow round-2 review spawned. The temporal-relevance call site waits on CCR WP-09-2 (in the batch).
- 2026-10-10 CCR BATCH RULED (docs/ccr/phase2-batch.md): 20 approved, 5 merged, 1 rejected (WP-08-2 ScopesFor).
  Ruling 1a = A: new nullable column documents.observation_scopes (migration 0010). This supersedes the earlier
  WP-08 H5 ruling (folding into overrides is lost on reprocess); WP-08 and WP-10 were told. DeleteScopes split
  (empty = nothing; DeleteAllObservations separate). WP-15 (port extension) LAUNCHED: it applies the port/domain
  diff itself together with fake + Postgres + porttest + mocks + migration in one merge (exception to the
  orchestrator-applies-CCR rule, so main never breaks), plus ErrContextOverflow classification and DeleteCiting
  FOR UPDATE.
- 2026-10-10 WP-09 review round 2: MERGE (pending commit signing). LOW: fix the stale prefer_observations bullet in the PR doc.
- 2026-10-10 WP-11 round-2 follow-up staged (recall text guarded, no-search fail-fast, split page-tool flags).
  WP-11 approved; merge waits on signing. Note: other ReflectData users must set the per-tool flags.
- 2026-10-10 WP-13 round-1 fixes staged on 8247d6a; narrow round-2 review spawned. CCR WP-13-2 (Blob.List,
  paged) APPROVED and added to WP-15.
- 2026-10-10 WP-12 review: BLOCK on 4 HIGH (fuzzy leaves compiled exact, so a model never builds; the delta
  pre-check skips untagged in-scope units past the watermark; a scope edit during a build isn't a conflict;
  model-row read-modify-write isn't serialized, so reconcile can revert a new version). Ruling: tx.Seq first in
  every model-row tx; a full run with nothing in scope follows SPEC 2002 literally (keep content and sources).
- 2026-10-10 WP-08 round-1 fixes staged on e63d423 (run ownership decision 807, linker caps, resolver gates, item scopes via a stubbed writer); narrow round-2 review spawned.
- 2026-10-10 WP-10 round-1 fixes staged on 8a22c21 (health probe, decide-time seq/tag checks, no resurrection, per-tx refresh triggers); narrow round-2 review asked to verify the Seq-before-reread ordering.
- 2026-10-10 WP-08 review round 2: MERGE (pending signing). Follow-up: kind gate only on fuzzy matches; docSuperseded compares tags.
- 2026-10-10 WP-10 review round 2: BLOCK on 3 HIGH (apply re-reads before tx.Seq; the probe uses the run-level
  binding; a probe success after a recovering outage parks a whole batch). Rulings: Seq first; a binding-scoped
  probe; retry each deferred unit alone before parking; a circuit breaker after 2 all-failed trees.
- 2026-10-10 WP-13 review round 2: BLOCK on 1 HIGH (a backoff-reordered rederive re-applies stale payload
  entities). Ruling: entity edits commit in the RPC's own tx; the rederive payload carries no names.
- Added to WP-15: unconsolidated reads skip NULL-embedding units; FailedRefreshes ignores CANCELLED ops.
- Carried to WP-14: the graph_maintenance handler re-checks Dirty/PendingMaintenance after Complete; a tick
  reconcile re-enqueues rederive for live facts with a NULL embedding.
- 2026-10-10 WP-08 follow-up staged (kind gate only on fuzzy matches; docSuperseded compares tags). WP-08 ready to merge once signing works.
- 2026-10-10 WP-10 round-2 fixes staged (Seq first in apply; scope-binding probe; retry alone before parking; park re-check; circuit breaker). Short round-3 check spawned.
- 2026-10-10 WP-12 round-1 fixes staged (fuzzy scope, untagged pre-check, scope-edit conflict, Seq-first model-row txs, cron, delta retry, fences, ...). Ruling: a run inside the interval floor re-parks to the floor end instead of being dropped; the other design choices are accepted.
- 2026-10-10 WP-10 review round 3: MERGE (pending signing). Full Postgres suite ran (98 s). LOW accepted: back-to-back lone-poison scopes can trip the breaker once per occurrence but converge.
- 2026-10-10 WP-13 review round 3: MERGE (pending signing). Carried to WP-14: wire Resolver and Config into the
  curator (otherwise restores fail); the graph_maintenance post-Complete re-check; the NULL-embedding rederive
  reconcile. LOW accepted: a non-literal re-resolve of snapshot names on restore.
- 2026-10-10 WP-12 review round 2: MERGE (pending signing). Follow-up: keep the partial draft when the retry is empty or rejected; floor tolerance; fresh runs write nothing; decision 1203 cost note. ALL SIX ENGINES APPROVED.
- 2026-10-10 WP-12 follow-up staged (partial draft kept, floor skew tolerance, fresh runs write nothing). WP-12 ready to merge once signing works.
- 2026-10-10 WP-15 handed in (61 files staged, unsigned; patch backup at scratchpad/wp15-foundation.patch);
  reviewer spawned. Follow-ups: ListScopes pagers pass "0" after a page ending at the shared scope; WP-11 uses
  errors.Is(err, ErrContextOverflow); WP-14 passes the blob store as DeleterConfig.Blobs.
- 2026-10-11 Commit signing restored, then failing again intermittently; per the user, commits use --no-verify
  (and -c commit.gpgsign=false where 1Password fails). User rules: at most 2 concurrent agents; run to
  completion without check-ins.
- 2026-10-11 WP-15 review: MERGE (areas a/b/c clean; coverage review found one HIGH test gap, deferred to
  follow-ups). Merged WP-09, WP-11, WP-08, WP-10, WP-12, WP-13 and then WP-15 into main; the full gates (gen,
  lint, go test -race -count=1 incl. Postgres) pass after each. Integration fix 7351751: bank prompt preview
  uses the split ReflectData tool flags (WP-11 x WP-13).
- Next: WP-16 engine follow-ups (swap stubs for the batch's port methods + carried items), WP-14a e2e harness
  (in parallel), then WP-14b integration, then the final review.
- 2026-10-11 WP-14a (emulated gateway + e2e harness) merged as a28bc05; gates pass. No separate review: test
  infrastructure that WP-14b exercises end to end and the final review covers. memd env contract in decision 1411.
- 2026-10-11 WP-16 (engine follow-ups + Reprocessor) merged as 988a886; gates pass. A focused review runs in parallel with WP-14b.
- 2026-10-11 WP-14b (integration) started; a focused WP-16 review runs in parallel (2 agents, no helpers).

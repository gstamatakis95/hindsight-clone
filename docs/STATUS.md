# memd swarm status

Orchestrator log for the build driven by `docs/SWARM_PROMPT.md`. Merges are local only (no remote PRs);
"PR" below means the work package branch handed to review.

## Work packages

| WP | Title | State | Branch | PR | Blockers | Open CCRs | LOW findings |
|----|-------|-------|--------|----|----------|-----------|--------------|
| WP-00 | Contracts and scaffold | merged (0374c87, tag contracts-v1) | wp/WP-00-contracts | f5ab75f | — | WP-00-1 approved + applied | see carried-forward |
| WP-01 | Postgres adapter | in progress | wp/WP-01-postgres | — | — | — | — |
| WP-02 | Fakes | in progress | wp/WP-02-fakes | — | — | — | — |
| WP-03 | Gateway and S3 adapters | merged (8dd2090) | wp/WP-03-gateway-s3 | e304714 | — | — | chat.go usage-sum fix + config defaults (follow-up); 6 LOW in review |
| WP-04 | Config, routing, time, prompts | in progress | wp/WP-04-config-prompts | — | — | — | — |
| WP-05 | Worker runtime | in progress | wp/WP-05-worker-runtime | — | — | — | — |
| WP-06 | Server shell | in progress | wp/WP-06-server-shell | — | — | — | — |
| WP-07 | Eval harness | merged (f5da652) | wp/WP-07-eval | 2b33aa0 | — | WP-07-1 approved + applied | follow-up: usage, timeouts, recall-mode N/A, fixture naming |
| WP-08 | Retain engine | waiting | — | — | WP-02, WP-04 | — | — |
| WP-09 | Recall engine | waiting | — | — | WP-02, WP-04 | — | — |
| WP-10 | Consolidation engine | waiting | — | — | WP-02, WP-04 | — | — |
| WP-11 | Reflect engine | waiting | — | — | WP-02, WP-04 | — | — |
| WP-12 | Mental model engine | waiting | — | — | WP-02, WP-04 | — | — |
| WP-13 | Bank, curation, documents, maintenance | waiting | — | — | WP-02, WP-04 | — | — |
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

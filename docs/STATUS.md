# memd swarm status

Orchestrator log for the build driven by `docs/SWARM_PROMPT.md`. Merges are local only (no remote PRs);
"PR" below means the work package branch handed to review.

## Work packages

| WP | Title | State | Branch | PR | Blockers | Open CCRs | LOW findings |
|----|-------|-------|--------|----|----------|-----------|--------------|
| WP-00 | Contracts and scaffold | fixing review round 1 | wp/WP-00-contracts | 17fe418 | review: 6 HIGH | WP-00-1 (approved, pending apply) | see log |
| WP-01 | Postgres adapter | waiting | — | — | WP-00 | — | — |
| WP-02 | Fakes | waiting | — | — | WP-00 | — | — |
| WP-03 | Gateway and S3 adapters | waiting | — | — | WP-00 | — | — |
| WP-04 | Config, routing, time, prompts | waiting | — | — | WP-00 | — | — |
| WP-05 | Worker runtime | waiting | — | — | WP-00 | — | — |
| WP-06 | Server shell | waiting | — | — | WP-00 | — | — |
| WP-07 | Eval harness | waiting | — | — | WP-00 | — | — |
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

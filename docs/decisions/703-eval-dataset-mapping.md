# 703 — How LongMemEval and LoCoMo map onto banks, documents and questions

## Context
The card asks for one bank per conversation and sync Retain with timestamps. The datasets differ in shape.

## Decision
- **LongMemEval** (`longmemeval_s/m/oracle.json`): every question instance has its own haystack, so one
  instance = one conversation = one bank. One Retain per haystack session, sorted by `haystack_dates`, with
  `document_id` = the session id (a repeated id gets a positional suffix so no session overwrites another),
  timestamp = the session date, and turns rendered as `role: content` lines. The query time is
  `question_date`. Category = `question_type`. Judge kind: `_abs` ids → abstention, temporal-reasoning →
  temporal, knowledge-update → update, single-session-preference → preference.
- **LoCoMo** (`locomo10.json`): one sample = one bank, one Retain per `session_N` in numeric order, timestamp =
  `session_N_date_time`, turns as `speaker: text` (an image's `blip_caption` is appended as text). Categories:
  1 multi-hop, 2 temporal, 3 open-domain, 4 single-hop. Category 5 (adversarial, no gold answer) is skipped, as
  is usual in published LoCoMo numbers. The query time is the last session's time.
- Bank ids are `<bank-prefix><conversation id>` with unsafe characters replaced by `-`. The default prefix holds
  the run's start time, so reruns don't collide; reusing a prefix re-retains idempotently (same document ids
  and content → `unchanged`).

## Alternatives
- One Retain per dataset turn. Rejected: it loses the session context the extractor needs and multiplies calls.
- Score LoCoMo category 5 with the abstention prompt. Deferred: there is no gold answer to judge against.

## Spec lines involved
WP-07 card "Build"; SPEC "Behavioural rules" (retain idempotency).

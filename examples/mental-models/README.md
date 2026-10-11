# Mental models

Living documents built from memory: a status page that refreshes itself after new knowledge is consolidated, a weekly digest on a cron schedule, the choice between an agentic and a single-shot refresh, a dry-run preview, search and the version history.

## What it does

1. `CreateMentalModel` twice: `schedule.after_consolidation` with the agentic strategy (the reflect loop drafts the page) and `schedule.cron` with the single-shot strategy (one recall, one model call).
2. `PreviewMentalModelRefresh` runs the whole pipeline for each strategy and writes nothing.
3. `Retain` new knowledge; after consolidation the status page refreshes by itself (version 2).
4. `ListMentalModelHistory` returns the earlier version, `SearchMentalModels` ranks whole pages and `ListMentalModels` shows the stored cron schedule (the example never waits for a cron tick).

## Setup

Start memd with the gateway emulator as described in [../README.md](../README.md#shared-setup). Nothing else is needed: the example creates its own bank (or tenants) with a fresh name on every run, so it can be run again.

## Run

```sh
go run ./examples/mental-models            # -addr localhost:50051 -tenant examples by default
```

`-addr` and `-tenant` (or `MEMD_ADDR` and `MEMD_TENANT`) choose the server and tenant; `-timeout` bounds the run (default 5m). 

The example exits 1 with `FAILED: ...` when the behaviour it demonstrates does not hold.

## Expected output

From a run against the emulator (ids, counts and tokens differ between runs; the emulator's answers quote the retained text, a real gateway writes real answers):

```text

== create a self-refreshing page and a cron digest ==
atlas-status: first build OPERATION_STATUS_COMPLETED
atlas-weekly: first build OPERATION_STATUS_COMPLETED

== preview a refresh without writing it ==
REFRESH_STRATEGY_AGENTIC       facts=3 would_persist=false
REFRESH_STRATEGY_SINGLE_SHOT   facts=3 would_persist=true

== new knowledge refreshes the status page ==
atlas-status is at version 2: Marta owns the Atlas migration. Project Atlas migrates billing to the new ledger. Marta escalated the Atlas risk. The Atlas migration slipped by two weeks becau...

== history and search ==
  previous version: Marta owns the Atlas migration. Project Atlas migrates billing to the new ledger. The Atlas deadline...
  hit atlas-status   projects/atlas (score 0.03)
  hit atlas-weekly   projects/atlas/reports (score 0.03)
  atlas-weekly refreshes on cron "0 9 * * 1"
```

## With grpcurl

[commands.md](commands.md) has the same calls as grpcurl commands.

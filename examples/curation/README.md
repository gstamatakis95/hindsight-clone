# Curating memory

Fix memory without a per-memory delete. Correct a wrong fact, retire one and bring it back, change the tags of a whole document, re-extract a document and rebuild the observations from scratch.

## What it does

1. `UpdateMemory` with `update_mask: ["text"]` fixes a fact; a `rederive_memory` operation follows.
2. `InvalidateMemory` hides a fact from recall (`ListMemories` with the `INVALIDATED` state still finds it), `RestoreMemory` brings it back.
3. `RetagDocument` replaces a document's tags and `ReprocessDocument` re-extracts it.
4. `ClearObservations` with `all: true` and `reconsolidate: true` rebuilds the derived observations.

## Setup

Start memd with the gateway emulator as described in [../README.md](../README.md#shared-setup). Nothing else is needed: the example creates its own bank (or tenants) with a fresh name on every run, so it can be run again.

## Run

```sh
go run ./examples/curation            # -addr localhost:50051 -tenant examples by default
```

`-addr` and `-tenant` (or `MEMD_ADDR` and `MEMD_TENANT`) choose the server and tenant; `-timeout` bounds the run (default 5m). 

The example exits 1 with `FAILED: ...` when the behaviour it demonstrates does not hold.

## Expected output

From a run against the emulator (ids, counts and tokens differ between runs; the emulator's answers quote the retained text, a real gateway writes real answers):

```text

== correct a wrong fact ==
now reads: Dana lives in Berlin.
rederive: OPERATION_KIND_REDERIVE_MEMORY OPERATION_STATUS_COMPLETED

== invalidate and restore ==
recall returns 4 memories, none about vegetarian
restore: OPERATION_KIND_REDERIVE_MEMORY OPERATION_STATUS_COMPLETED

== retag and reprocess a document ==
retag: OPERATION_KIND_RETAG_DOCUMENT OPERATION_STATUS_COMPLETED
reprocess: OPERATION_KIND_RETAIN_DOCUMENT OPERATION_STATUS_COMPLETED

== clear and rebuild observations ==
deleted 3 observations
reconsolidate: OPERATION_KIND_CONSOLIDATE OPERATION_STATUS_COMPLETED
```

## With grpcurl

[commands.md](commands.md) has the same calls as grpcurl commands.

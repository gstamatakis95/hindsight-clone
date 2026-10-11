# Asynchronous ingestion

Bulk ingestion that does not block the caller. Retain a batch with `run_async`, follow the work through operations and the lifecycle event feed, and cancel and retry an operation.

## What it does

1. `Retain` five documents with `run_async: true`; the response holds the `batch_operation` and one operation per document.
2. `GetOperation` polls the parent (its status aggregates the children); `ListOperations` lists what ran.
3. `ListEvents` follows the bank's events: keep `next_after_seq` and the next poll returns only what is new.
4. `CancelOperation` and `RetryOperation` on a mental-model refresh that waits behind a long debounce, so it is PENDING for as long as it takes to cancel it.

## Setup

Start memd with the gateway emulator as described in [../README.md](../README.md#shared-setup). Nothing else is needed: the example creates its own bank (or tenants) with a fresh name on every run, so it can be run again.

## Run

```sh
go run ./examples/async-ingest            # -addr localhost:50051 -tenant examples by default
```

`-addr` and `-tenant` (or `MEMD_ADDR` and `MEMD_TENANT`) choose the server and tenant; `-timeout` bounds the run (default 5m). 

The example exits 1 with `FAILED: ...` when the behaviour it demonstrates does not hold.

## Expected output

From a run against the emulator (ids, counts and tokens differ between runs; the emulator's answers quote the retained text, a real gateway writes real answers):

```text

== enqueue a batch ==
batch operation 01a12cbd-4507-7ab2-9e17-21ac2faf500a (OPERATION_KIND_RETAIN_BATCH), 5 document operations

== poll the operations ==
batch finished: OPERATION_STATUS_COMPLETED
  OPERATION_KIND_REFRESH_MENTAL_MODEL  OPERATION_STATUS_PENDING
  OPERATION_KIND_CONSOLIDATE           OPERATION_STATUS_COMPLETED
  OPERATION_KIND_CONSOLIDATE           OPERATION_STATUS_COMPLETED
  OPERATION_KIND_RETAIN_DOCUMENT       OPERATION_STATUS_COMPLETED
  OPERATION_KIND_RETAIN_DOCUMENT       OPERATION_STATUS_COMPLETED
  OPERATION_KIND_RETAIN_DOCUMENT       OPERATION_STATUS_COMPLETED
  OPERATION_KIND_RETAIN_DOCUMENT       OPERATION_STATUS_COMPLETED
  OPERATION_KIND_RETAIN_DOCUMENT       OPERATION_STATUS_COMPLETED
  OPERATION_KIND_REFRESH_MENTAL_MODEL  OPERATION_STATUS_COMPLETED

== follow the event feed ==
  seq 10  EVENT_TYPE_RETAIN_COMPLETED          ticket-101
  seq 11  EVENT_TYPE_RETAIN_COMPLETED          ticket-104
  seq 12  EVENT_TYPE_RETAIN_COMPLETED          ticket-102
  seq 13  EVENT_TYPE_RETAIN_COMPLETED          ticket-103
  seq 14  EVENT_TYPE_RETAIN_COMPLETED          ticket-105
  seq 16  EVENT_TYPE_CONSOLIDATION_COMPLETED   
  seq 17  EVENT_TYPE_CONSOLIDATION_COMPLETED   
after one more retain:
  seq 20  EVENT_TYPE_RETAIN_COMPLETED          ticket-106

== cancel and retry ==
cancel: OPERATION_STATUS_CANCELLED
retry: OPERATION_STATUS_PENDING
finished: OPERATION_STATUS_COMPLETED
```

## With grpcurl

[commands.md](commands.md) has the same calls as grpcurl commands.

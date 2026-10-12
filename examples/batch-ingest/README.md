# Batch ingestion

Bulk ingestion from a producer that delivers at least once, such as a queue consumer or a sync job. Send documents
with `RetainBatch`, read a status per item, redeliver safely, ignore late old versions, and back off when the server
is behind.

## What it does

1. `RetainBatch` five items: every item carries its source row id as `document_id` and its row version as
   `revision`. The response has one result per item, in order: three are `ACCEPTED` with their `retain_document`
   operation, the old version of `ticket-203` (delivered after the new one) is `STALE`, and the item without a
   `document_id` is `REJECTED` with its error (`INVALID_ARGUMENT`, field `items[4].document_id`). The bad item does
   not fail the call. `GetOperation` waits for the accepted ones.
2. The same batch again, as a queue redelivers it after a consumer crash: the stored documents are `UNCHANGED`, so
   nothing is written and no model is called. Redelivered while the first retain is still running, an item is
   `ACCEPTED` with the operation already in flight instead.
3. Version 1 of `ticket-203` arrives late, on its own: `STALE`, and `GetDocument` still shows revision 2.

While the bank's or the org's backlog of pending retains is over the server's limit (`ingest.max_pending_per_bank`,
`ingest.max_pending_per_org`), the whole call fails with `RESOURCE_EXHAUSTED` (reason `INGEST_BACKLOG`) before
anything is written, and a `google.rpc.RetryInfo` detail says how long to wait. `send` in `main.go` shows the
producer side: wait that long and send the same batch again.

## Setup

Start memd with the gateway emulator as described in [../README.md](../README.md#shared-setup). The example creates
its own bank with a fresh name on every run, so it can be run again.

## Run

```sh
go run ./examples/batch-ingest            # -addr localhost:50051 -tenant examples by default
```

`-addr` and `-tenant` (or `MEMD_ADDR` and `MEMD_TENANT`) choose the server and tenant; `-timeout` bounds the run
(default 5m).

The example exits 1 with `FAILED: ...` when the behaviour it demonstrates does not hold.

## Expected output

Operation ids differ between runs:

```text

== ingest a batch ==
  ticket-201   STATUS_ACCEPTED    operation 0199a1f2-5c41-7d1e-a0a3-3f0e8c1b2d01
  ticket-202   STATUS_ACCEPTED    operation 0199a1f2-5c41-7d1e-a0a3-3f0e8c1b2d02
  ticket-203   STATUS_ACCEPTED    operation 0199a1f2-5c41-7d1e-a0a3-3f0e8c1b2d03
  ticket-203   STATUS_STALE       
               STATUS_REJECTED    INVALID_ARGUMENT [items[4].document_id]

== redeliver the same batch ==
  ticket-201   STATUS_UNCHANGED   
  ticket-202   STATUS_UNCHANGED   
  ticket-203   STATUS_UNCHANGED   
  ticket-203   STATUS_STALE       
               STATUS_REJECTED    INVALID_ARGUMENT [items[4].document_id]

== an old version arrives late ==
  ticket-203   STATUS_STALE       
ticket-203 stored revision: 2
```

## With grpcurl

[commands.md](commands.md) has the same calls as grpcurl commands.

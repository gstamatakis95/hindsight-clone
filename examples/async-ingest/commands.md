# async-ingest: the same calls with grpcurl

Needs [grpcurl](https://github.com/fullstorydev/grpcurl) and [jq](https://jqlang.org/), and a running memd (see
[../README.md](../README.md)). The `g` helper adds the tenant header and replaces `BANK_ID` in the JSON with
`$BANK`.

```sh
export MEMD=${MEMD:-localhost:50051} TENANT=${TENANT:-examples} BANK=async-ingest-$RANDOM
g() { grpcurl -plaintext -H "x-tenant-id: $TENANT" -d "${2//BANK_ID/$BANK}" "$MEMD" "memory.v1.$1"; }
```

A mental model that refreshes after consolidation, behind a one-hour debounce. Its refresh will wait as a PENDING
operation, which gives the cancel and retry step something stable to work on.

```sh
g ConfigService/UpdateBankConfig '{"bank_id": "BANK_ID", "config": {"mental_models": {"refresh_debounce_seconds": 3600}},
  "update_mask": {"paths": ["mental_models.refresh_debounce_seconds"]}}' > /dev/null
OP=$(g MentalModelService/CreateMentalModel '{"bank_id": "BANK_ID", "spec": {"id": "ingest-digest", "name": "Digest",
  "source_query": "Summarize each ticket and customer request", "schedule": {"after_consolidation": true}}}' | jq -r .operation.id)
until [ "$(g OperationService/GetOperation "{\"bank_id\":\"BANK_ID\",\"operation_id\":\"$OP\"}" | jq -r .operation.status)" = OPERATION_STATUS_COMPLETED ]; do sleep 1; done
```

Retain a batch with `run_async`. The response holds the parent `batch_operation` and one operation per document.

```sh
g MemoryService/Retain '{"bank_id": "BANK_ID", "run_async": true, "items": [
  {"document_id": "ticket-101", "content": "Ticket 101: the invoice export is slow for large accounts."},
  {"document_id": "ticket-102", "content": "Ticket 102: a customer asked for single sign-on."},
  {"document_id": "ticket-103", "content": "Ticket 103: dark mode flickers on the settings page."},
  {"document_id": "ticket-104", "content": "Ticket 104: the API returns 500 when a webhook URL is empty."},
  {"document_id": "ticket-105", "content": "Ticket 105: a customer wants to export audit logs weekly."}]}' > /tmp/batch.json
jq '{batch: .batchOperation.id, documents: [.documents[] | {id: .documentId, operation: .operation.id}]}' /tmp/batch.json
```

Poll the parent until it is done, then list the operations that did the work.

```sh
BATCH=$(jq -r .batchOperation.id /tmp/batch.json)
until [ "$(g OperationService/GetOperation "{\"bank_id\":\"BANK_ID\",\"operation_id\":\"$BATCH\"}" | jq -r .operation.status)" = OPERATION_STATUS_COMPLETED ]; do sleep 1; done
g OperationService/ListOperations '{"bank_id": "BANK_ID", "exclude_parents": true}' | jq -r '.operations[] | "\(.kind)  \(.status)"'
until [ "$(g BankService/GetBankStats '{"bank_id":"BANK_ID"}' | jq '.stats.pendingOperationsByKind | del(.refresh_mental_model) | length')" = 0 ]; do sleep 1; done
```

Follow the event feed. Keep `nextAfterSeq` and pass it as `after_seq` next time to see only what is new.

```sh
g OperationService/ListEvents '{"bank_id": "BANK_ID", "after_seq": 0}' | tee /tmp/events.json | jq -r '.events[] | "\(.seq)  \(.type)  \(.subjectId // "")"'
SEQ=$(jq -r .nextAfterSeq /tmp/events.json)
g MemoryService/Retain '{"bank_id": "BANK_ID", "items": [{"document_id": "ticket-106", "content": "Ticket 106: the mobile app logs users out after an update."}]}' > /dev/null
g OperationService/ListEvents "{\"bank_id\": \"BANK_ID\", \"after_seq\": $SEQ}" | jq -r '.events[] | "\(.seq)  \(.type)  \(.subjectId // "")"'
```

Cancel and retry. The refresh is parked, so it is still PENDING: cancel it, put it back in the queue, and wait.

```sh
REFRESH=$(g OperationService/ListOperations '{"bank_id": "BANK_ID", "kind": "OPERATION_KIND_REFRESH_MENTAL_MODEL", "status": "OPERATION_STATUS_PENDING"}' | jq -r '.operations[0].id')
g OperationService/CancelOperation "{\"bank_id\":\"BANK_ID\",\"operation_id\":\"$REFRESH\"}" | jq -r .operation.status
g OperationService/RetryOperation "{\"bank_id\":\"BANK_ID\",\"operation_id\":\"$REFRESH\"}" | jq -r .operation.status
until [ "$(g OperationService/GetOperation "{\"bank_id\":\"BANK_ID\",\"operation_id\":\"$REFRESH\"}" | jq -r .operation.status)" = OPERATION_STATUS_COMPLETED ]; do sleep 1; done
```

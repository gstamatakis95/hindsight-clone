# curation: the same calls with grpcurl

Needs [grpcurl](https://github.com/fullstorydev/grpcurl) and [jq](https://jqlang.org/), and a running memd (see
[../README.md](../README.md)). The `g` helper adds the tenant header and replaces `BANK_ID` in the JSON with
`$BANK`; `wait_op` polls an operation until it completes.

```sh
export MEMD=${MEMD:-localhost:50051} TENANT=${TENANT:-examples} BANK=curation-$RANDOM
g() { grpcurl -plaintext -H "x-tenant-id: $TENANT" -d "${2//BANK_ID/$BANK}" "$MEMD" "memory.v1.$1"; }
wait_op() { until [ "$(g OperationService/GetOperation "{\"bank_id\":\"BANK_ID\",\"operation_id\":\"$1\"}" | jq -r .operation.status)" = OPERATION_STATUS_COMPLETED ]; do sleep 1; done; }
wait_idle() { until [ "$(g BankService/GetBankStats '{"bank_id":"BANK_ID"}' | jq '.stats.pendingOperationsByKind // {} | length')" = 0 ]; do sleep 1; done; }
```

Retain a profile that contains a wrong fact.

```sh
g MemoryService/Retain '{"bank_id": "BANK_ID", "items": [{"document_id": "profile-dana", "tags": ["draft"],
  "content": "Dana lives in Lisbon. Dana is vegetarian. Dana works on motion planning."}]}' > /dev/null
wait_idle
```

Find the fact, then correct it. `update_mask` names the fields to apply (here only `text`); a named field left unset
is cleared. The edit is recorded in the memory's history and a `rederive_memory` operation re-embeds and re-links it.

```sh
LISBON=$(g MemoryService/ListMemories '{"bank_id": "BANK_ID", "text_contains": "Lisbon", "state": "MEMORY_STATE_VALID",
  "fact_types": ["FACT_TYPE_WORLD", "FACT_TYPE_EXPERIENCE"]}' | jq -r '.memories[0].id')
OP=$(g MemoryService/UpdateMemory "{\"bank_id\": \"BANK_ID\", \"memory_id\": \"$LISBON\", \"memory\": {\"text\": \"Dana lives in Berlin.\"},
  \"update_mask\": {\"paths\": [\"text\"]}, \"reason\": \"Dana moved\"}" | jq -r .operation.id)
wait_op $OP
```

Invalidate a fact: recall and consolidation stop seeing it, but it is kept. Restore brings it back.

```sh
VEG=$(g MemoryService/ListMemories '{"bank_id": "BANK_ID", "text_contains": "vegetarian", "state": "MEMORY_STATE_VALID",
  "fact_types": ["FACT_TYPE_WORLD", "FACT_TYPE_EXPERIENCE"]}' | jq -r '.memories[0].id')
g MemoryService/InvalidateMemory "{\"bank_id\": \"BANK_ID\", \"memory_id\": \"$VEG\", \"reason\": \"unverified\"}" | jq '.memory.state'
g MemoryService/Recall '{"bank_id": "BANK_ID", "query": "What does Dana eat?"}' | jq '[.results[].text]'
g MemoryService/ListMemories '{"bank_id": "BANK_ID", "state": "MEMORY_STATE_INVALIDATED"}' | jq '[.memories[].text]'
OP=$(g MemoryService/RestoreMemory "{\"bank_id\": \"BANK_ID\", \"memory_id\": \"$VEG\", \"reason\": \"confirmed by Dana\"}" | jq -r .operation.id)
wait_op $OP
```

Retag the whole document (the tag set is replaced), then re-extract it from its original text.

```sh
OP=$(g DocumentService/RetagDocument '{"bank_id": "BANK_ID", "document_id": "profile-dana", "tags": {"tags": ["people", "verified"]}}' | jq -r .operation.id)
wait_op $OP
OP=$(g DocumentService/ReprocessDocument '{"bank_id": "BANK_ID", "document_id": "profile-dana"}' | jq -r .operation.id)
wait_op $OP
wait_idle
```

Observations are derived, so clear them and let consolidation rebuild them. `scopes` or `memory_id` would clear less.

```sh
g ObservationService/ClearObservations '{"bank_id": "BANK_ID", "all": true, "reconsolidate": true}' | jq '{deleted, operation: .operation.id}'
wait_idle
```

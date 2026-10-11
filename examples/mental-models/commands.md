# mental-models: the same calls with grpcurl

Needs [grpcurl](https://github.com/fullstorydev/grpcurl) and [jq](https://jqlang.org/), and a running memd (see
[../README.md](../README.md)). The `g` helper adds the tenant header and replaces `BANK_ID` in the JSON with
`$BANK`; `wait_op` polls an operation until it completes.

```sh
export MEMD=${MEMD:-localhost:50051} TENANT=${TENANT:-examples} BANK=mental-models-$RANDOM
g() { grpcurl -plaintext -H "x-tenant-id: $TENANT" -d "${2//BANK_ID/$BANK}" "$MEMD" "memory.v1.$1"; }
wait_op() { until [ "$(g OperationService/GetOperation "{\"bank_id\":\"BANK_ID\",\"operation_id\":\"$1\"}" | jq -r .operation.status)" = OPERATION_STATUS_COMPLETED ]; do sleep 1; done; }
wait_idle() { until [ "$(g BankService/GetBankStats '{"bank_id":"BANK_ID"}' | jq '.stats.pendingOperationsByKind // {} | del(.refresh_mental_model) | length')" = 0 ]; do sleep 1; done; }
```

Refresh one second after consolidation instead of the default thirty, then retain the first facts.

```sh
g ConfigService/UpdateBankConfig '{"bank_id": "BANK_ID", "config": {"mental_models": {"refresh_debounce_seconds": 1}},
  "update_mask": {"paths": ["mental_models.refresh_debounce_seconds"]}}' > /dev/null
g MemoryService/Retain '{"bank_id": "BANK_ID", "items": [{"document_id": "atlas-kickoff",
  "content": "Project Atlas migrates billing to the new ledger. Marta owns the Atlas migration. The Atlas deadline is March."}]}' > /dev/null
wait_idle
```

Create a page that refreshes after consolidation (agentic: the reflect loop drafts it) and a weekly digest on a cron
schedule (UTC, five fields; single-shot: one recall and one call, much cheaper).

```sh
OP=$(g MentalModelService/CreateMentalModel '{"bank_id": "BANK_ID", "spec": {"id": "atlas-status", "name": "Atlas status",
  "folder_path": "projects/atlas", "source_query": "What is the status of project Atlas, who owns it and what are the risks?",
  "schedule": {"after_consolidation": true}, "strategy": "REFRESH_STRATEGY_AGENTIC"}}' | jq -r .operation.id)
wait_op $OP
OP=$(g MentalModelService/CreateMentalModel '{"bank_id": "BANK_ID", "spec": {"id": "atlas-weekly", "name": "Atlas weekly digest",
  "folder_path": "projects/atlas/reports", "source_query": "Summarize everything known about project Atlas this week",
  "schedule": {"cron": "0 9 * * 1"}, "strategy": "REFRESH_STRATEGY_SINGLE_SHOT"}}' | jq -r .operation.id)
wait_op $OP
```

Preview a refresh with either strategy. The full pipeline runs, nothing is written, and `diff` shows the change.

```sh
g MentalModelService/PreviewMentalModelRefresh '{"bank_id": "BANK_ID", "mental_model_id": "atlas-status", "strategy": "REFRESH_STRATEGY_AGENTIC"}' |
  jq '{requestedMode, strategy: .trace.effectiveStrategy, factsRetrieved, wouldPersist: (.wouldPersist // false)}'
g MentalModelService/PreviewMentalModelRefresh '{"bank_id": "BANK_ID", "mental_model_id": "atlas-status", "strategy": "REFRESH_STRATEGY_SINGLE_SHOT"}' |
  jq '{requestedMode, strategy: .trace.effectiveStrategy, factsRetrieved, wouldPersist: (.wouldPersist // false)}'
```

New knowledge arrives. After consolidation the status page refreshes by itself (version 2); wait for it.

```sh
g MemoryService/Retain '{"bank_id": "BANK_ID", "items": [{"document_id": "atlas-update",
  "content": "The Atlas migration slipped by two weeks because the ledger vendor changed its API. Marta escalated the Atlas risk."}]}' > /dev/null
wait_idle
until [ "$(g MentalModelService/GetMentalModel '{"bank_id":"BANK_ID","mental_model_id":"atlas-status","view":"MENTAL_MODEL_VIEW_BASIC"}' | jq -r .mentalModel.version)" = 2 ]; do sleep 1; done
g MentalModelService/GetMentalModel '{"bank_id":"BANK_ID","mental_model_id":"atlas-status"}' | jq '.mentalModel | {version, contentMd, stale}'
```

History keeps every previous version, newest first. Search ranks whole pages. List shows the stored schedules.

```sh
g MentalModelService/ListMentalModelHistory '{"bank_id": "BANK_ID", "mental_model_id": "atlas-status", "kind": "KIND_VERSION"}' |
  jq '.entries[] | {kind, previousContent, outcome}'
g MentalModelService/SearchMentalModels '{"bank_id": "BANK_ID", "query": "Atlas risk", "folder_prefix": "projects/"}' | jq '.hits'
g MentalModelService/ListMentalModels '{"bank_id": "BANK_ID", "folder_prefix": "projects/atlas/reports"}' |
  jq '.mentalModels[].spec | {id, schedule}'
```

A manual refresh ignores the schedule. `force_full` rewrites the page instead of editing it, and a refresh can take
its own strategy and models.

```sh
OP=$(g MentalModelService/RefreshMentalModel '{"bank_id": "BANK_ID", "mental_model_id": "atlas-weekly", "force_full": true}' | jq -r .operation.id)
wait_op $OP
```

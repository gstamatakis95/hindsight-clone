# model-per-step: the same calls with grpcurl

Needs [grpcurl](https://github.com/fullstorydev/grpcurl) and [jq](https://jqlang.org/), and a running memd (see
[../README.md](../README.md)). The `g` helper adds the tenant header and replaces `BANK_ID` and `ORG_ID` in the JSON
with `$BANK` and `$ORG`. Replace the model names with the ones your gateway serves.

```sh
export MEMD=${MEMD:-localhost:50051} TENANT=${TENANT:-examples} ORG=${ORG:-examples} BANK=model-per-step-$RANDOM
g() { local d="${2//BANK_ID/$BANK}"; grpcurl -plaintext -H "x-tenant-id: $TENANT" -d "${d//ORG_ID/$ORG}" "$MEMD" "memory.v1.$1"; }
```

Create the org first: banks live in an org, and a write never creates one. On a rerun this answers
ALREADY_EXISTS, which is fine.

```sh
g OrgService/CreateOrg '{"org_id": "ORG_ID"}'
```

Bind the models on the bank: one cheap model for every chat step, a strong one for reflect and mental models, and
a reranker. `update_mask` names the bindings to set; each is replaced whole.

```sh
g ConfigService/UpdateBankConfig '{
  "org_id": "ORG_ID", "bank_id": "BANK_ID",
  "config": {"models": {
    "default": {"model": "cheap-model"},
    "reflect": {"model": "strong-model"},
    "mental_model": {"model": "strong-model"},
    "rerank": {"model": "rerank-model"}
  }},
  "update_mask": {"paths": ["models.default", "models.reflect", "models.mental_model", "models.rerank"]}
}' | jq '.config.effective.models'
```

Retain twice. The first call takes the bank's extract model (the `models.default` binding); the second overrides it for
this request only. `usage` names the model that served each slot. Consolidation is skipped so it can be run by hand.

```sh
g MemoryService/Retain '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "skip_consolidation": true,
  "items": [{"document_id": "notes-1", "content": "Priya leads the billing team. The billing team ships on Thursdays."}]}' |
  jq '.usage'
g MemoryService/Retain '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "skip_consolidation": true,
  "overrides": {"extract_model": {"model": "one-off-model"}},
  "items": [{"document_id": "notes-2", "content": "Priya reports to Tomas. Tomas runs the platform group."}]}' |
  jq '.usage'
```

Consolidate by hand with its own model, then read the operation's usage.

```sh
OP=$(g ObservationService/Consolidate '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "overrides": {"consolidate_model": {"model": "one-off-model"}}}' | jq -r .operation.id)
until [ "$(g OperationService/GetOperation "{\"org_id\": \"ORG_ID\", \"bank_id\":\"BANK_ID\",\"operation_id\":\"$OP\"}" | jq -r .operation.status)" = OPERATION_STATUS_COMPLETED ]; do sleep 1; done
g OperationService/GetOperation "{\"org_id\": \"ORG_ID\", \"bank_id\":\"BANK_ID\",\"operation_id\":\"$OP\"}" | jq '.operation.usage'
```

Recall with the bank's reranker: `trace.reranked` counts the candidates the cross-encoder scored. A request can
bind another reranker with `"overrides": {"rerank_model": {"model": "..."}}`.

```sh
g MemoryService/Recall '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "query": "Who does Priya report to?", "trace": true}' | jq '.trace.reranked'
```

Reflect on the bank's strong model, then on a one-off model for this call.

```sh
g MemoryService/Reflect '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "query": "Who is in Priya'"'"'s chain of command?"}' | jq '.usage'
g MemoryService/Reflect '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "query": "Who is in Priya'"'"'s chain of command?",
  "overrides": {"reflect_model": {"model": "one-off-model"}}}' | jq '.usage'
```

Build a single-shot mental model; the build operation reports the `MODEL_SLOT_MENTAL_MODEL` usage.

```sh
OP=$(g MentalModelService/CreateMentalModel '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "spec": {"id": "org-chart", "name": "Org chart",
  "source_query": "Who reports to whom?", "strategy": "REFRESH_STRATEGY_SINGLE_SHOT"}}' | jq -r .operation.id)
until [ "$(g OperationService/GetOperation "{\"org_id\": \"ORG_ID\", \"bank_id\":\"BANK_ID\",\"operation_id\":\"$OP\"}" | jq -r .operation.status)" = OPERATION_STATUS_COMPLETED ]; do sleep 1; done
g OperationService/GetOperation "{\"org_id\": \"ORG_ID\", \"bank_id\":\"BANK_ID\",\"operation_id\":\"$OP\"}" | jq '.operation.usage'
```

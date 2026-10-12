# directives-structured: the same calls with grpcurl

Needs [grpcurl](https://github.com/fullstorydev/grpcurl) and [jq](https://jqlang.org/), and a running memd (see
[../README.md](../README.md)). The `g` helper adds the tenant header and replaces `BANK_ID` and `ORG_ID` in the JSON
with `$BANK` and `$ORG`.

```sh
export MEMD=${MEMD:-localhost:50051} TENANT=${TENANT:-examples} ORG=${ORG:-examples} BANK=directives-$RANDOM
g() { local d="${2//BANK_ID/$BANK}"; grpcurl -plaintext -H "x-tenant-id: $TENANT" -d "${d//ORG_ID/$ORG}" "$MEMD" "memory.v1.$1"; }
wait_idle() { until [ "$(g BankService/GetBankStats '{"org_id": "ORG_ID", "bank_id":"BANK_ID"}' | jq '.stats.pendingOperationsByKind // {} | length')" = 0 ]; do sleep 1; done; }
```

Create the org first: banks live in an org, and a write never creates one. On a rerun this answers
ALREADY_EXISTS, which is fine.

```sh
g OrgService/CreateOrg '{"org_id": "ORG_ID"}'
```

Retain an incident report.

```sh
g MemoryService/Retain '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "items": [{"document_id": "incident-77",
  "content": "Incident 77: the payments service returned errors for 20 minutes. The checkout service retried too aggressively and made the outage worse. The on-call engineer rolled back the release."}]}' > /dev/null
wait_idle
```

Add a standing directive. Every Reflect on the bank follows it, and `basedOn.directiveIds` says which directives
shaped the answer.

```sh
DIRECTIVE=$(g ConfigService/CreateDirective '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "directive": {"name": "blameless", "active": true,
  "rules": ["Describe what happened to systems, never blame a person.", "Mention the rollback if there was one."]}}' | jq -r .directive.id)
g MemoryService/Reflect '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "query": "What happened in incident 77?"}' | jq '{answer: .text, directives: .basedOn.directiveIds}'
```

See how the rule reaches the model. `PreviewPrompt` renders the reflect prompt without calling any model.

```sh
g ConfigService/PreviewPrompt '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "operation": "PROMPT_OPERATION_REFLECT"}' |
  jq '.preview.messages[].blocks[] | select(.section == "directives" and .active) | {source, text}'
```

Switch it off. A masked field left unset is cleared, so `active: false` needs `active` in the mask.

```sh
g ConfigService/UpdateDirective "{\"org_id\": \"ORG_ID\", \"bank_id\": \"BANK_ID\", \"directive_id\": \"$DIRECTIVE\", \"directive\": {\"active\": false},
  \"update_mask\": {\"paths\": [\"active\"]}}" | jq ".directive.active // false"
g MemoryService/Reflect '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "query": "What happened in incident 77?"}' | jq ".basedOn.directiveIds // []"
```

A request can add rules for itself. It can never remove a stored directive.

```sh
g MemoryService/Reflect '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "query": "What happened in incident 77?",
  "extra_directives": [{"name": "brief", "rules": ["Answer in one sentence."], "active": true}]}' | jq .text
```

Structured output: Reflect answers in prose, then a second pass (the `structured_output` model slot) projects the answer
into the JSON Schema. `structuredOutputError` says why the projection failed, if it did.

```sh
SCHEMA='{"type":"object","properties":{"summary":{"type":"string"},"services":{"type":"array","items":{"type":"string"}},"severity":{"type":"string","enum":["low","medium","high"]}},"required":["summary","services","severity"],"additionalProperties":false}'
jq -n --arg bank "$BANK" --arg schema "$SCHEMA" '{bank_id: $bank, query: "What happened in incident 77?", response_schema_json: $schema}' |
  grpcurl -plaintext -H "x-tenant-id: $TENANT" -d @ "$MEMD" memory.v1.MemoryService/Reflect |
  jq '{structured: (.structuredOutputJson | fromjson?), error: .structuredOutputError, usage}'
```

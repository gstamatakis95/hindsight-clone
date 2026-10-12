# recall-filters: the same calls with grpcurl

Needs [grpcurl](https://github.com/fullstorydev/grpcurl) and [jq](https://jqlang.org/), and a running memd (see
[../README.md](../README.md)). The `g` helper adds the tenant header and replaces `BANK_ID` and `ORG_ID` in the JSON
with `$BANK` and `$ORG`; `show` prints a recall result as one line per memory.

```sh
export MEMD=${MEMD:-localhost:50051} TENANT=${TENANT:-examples} ORG=${ORG:-examples} BANK=recall-filters-$RANDOM
g() { local d="${2//BANK_ID/$BANK}"; grpcurl -plaintext -H "x-tenant-id: $TENANT" -d "${d//ORG_ID/$ORG}" "$MEMD" "memory.v1.$1"; }
show() { jq -r '.results[]? | "\(.factType | sub("FACT_TYPE_"; ""))  \(.text)  \(.tags // [])"'; }
wait_idle() { until [ "$(g BankService/GetBankStats '{"org_id": "ORG_ID", "bank_id":"BANK_ID"}' | jq '.stats.pendingOperationsByKind // {} | length')" = 0 ]; do sleep 1; done; }
```

Create the org first: banks live in an org, and a write never creates one. On a rerun this answers
ALREADY_EXISTS, which is fine.

```sh
g OrgService/CreateOrg '{"org_id": "ORG_ID"}'
```

Retain four tagged documents from different weeks.

```sh
g MemoryService/Retain '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "items": [
  {"document_id": "eng-1", "timestamp": "2026-09-01T12:00:00Z", "tags": ["team:eng", "project:atlas"], "content": "Engineering shipped the Atlas ledger importer."},
  {"document_id": "sales-1", "timestamp": "2026-09-10T12:00:00Z", "tags": ["team:sales", "project:atlas"], "content": "Sales signed Acme as the first Atlas customer."},
  {"document_id": "eng-2", "timestamp": "2026-09-20T12:00:00Z", "tags": ["team:eng", "project:legacy"], "content": "Engineering retired the legacy ledger cron runner."},
  {"document_id": "ops-1", "timestamp": "2026-09-25T12:00:00Z", "tags": ["team:ops"], "content": "Operations upgraded the ledger database cluster."}]}' > /dev/null
wait_idle
```

Tag groups: engineering memories that are not about the legacy project. Groups nest with `all_of`, `any_of` and
`none_of`; the top-level groups are AND-ed. A flat filter would be `"tags": {"tags": ["team:eng"], "match": "TAGS_MATCH_ANY_STRICT"}`.

```sh
g MemoryService/Recall '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "query": "What happened with the ledger?", "tags": {"groups": [
  {"leaf": {"tags": ["team:eng"]}},
  {"none_of": {"leaf": {"tags": ["project:legacy"]}}}]}}' | show
```

A time window for the temporal arm and the time boost. Without one, memd looks for a time expression in the query.

```sh
g MemoryService/Recall '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "query": "What happened with the ledger?", "trace": true,
  "time_window_start": "2026-09-20T00:00:00Z", "time_window_end": "2026-09-30T00:00:00Z"}' |
  jq '.trace | {timeWindowStart, timeWindowEnd, arms: [.arms[].arm]}'
```

Budgets: `budget` sets the candidates each arm may return, `max_tokens` caps what is packed into the answer.

```sh
g MemoryService/Recall '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "query": "What happened with the ledger?", "budget": "BUDGET_LOW", "max_tokens": 24, "trace": true}' |
  jq '.trace | {fused, packed, tokensUsed, tokenBudget}'
g MemoryService/Recall '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "query": "What happened with the ledger?", "budget": "BUDGET_HIGH", "max_tokens": 4096, "trace": true}' |
  jq '.trace | {fused, packed, tokensUsed, tokenBudget}'
```

Observations first, with the raw facts behind them attached under `sourceFacts`. Raw facts can bring the chunks
they were read from.

```sh
g MemoryService/Recall '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "query": "What happened with the ledger?", "prefer_observations": true,
  "include": {"source_facts": true}}' | jq '{results: [.results[].text], sourceFacts: (.sourceFacts | length)}'
g MemoryService/Recall '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "query": "What happened with the ledger?", "fact_types": ["FACT_TYPE_WORLD"],
  "include": {"chunks": true}}' | jq '{results: [.results[].text], chunks: (.chunks | length)}'
```

Recall arms: list them, then run only graph and vector. An unknown arm is `INVALID_ARGUMENT`.

```sh
g MemoryService/ListRecallArms '{}' | jq -r '.arms[] | "\(.name)  \(.description)"'
g MemoryService/Recall '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "query": "What happened with the ledger?", "arms": ["graph", "vector"], "trace": true}' |
  jq '[.trace.arms[].arm]'
g MemoryService/Recall '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "query": "ledger", "arms": ["telepathy"]}' || true
```

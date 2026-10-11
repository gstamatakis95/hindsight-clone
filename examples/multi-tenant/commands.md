# multi-tenant: the same calls with grpcurl

Needs [grpcurl](https://github.com/fullstorydev/grpcurl) and [jq](https://jqlang.org/), and a running memd (see
[../README.md](../README.md)). The tenant is the `x-tenant-id` header, so `g` takes it as its first argument.
Tenant ids are fresh per run, so the commands can be pasted again.

```sh
export MEMD=${MEMD:-localhost:50051} ACME=acme-$RANDOM GLOBEX=globex-$RANDOM
g() { grpcurl -plaintext -H "x-tenant-id: $1" -d "$3" "$MEMD" "memory.v1.$2"; }
```

Tenant config: acme defaults to a skeptical assistant. `own` is what the tenant sets, `effective` is the result
after the server defaults.

```sh
g $ACME ConfigService/UpdateTenantConfig '{"config": {"reflect": {"disposition": {"skepticism": 5, "literalism": 3, "empathy": 2}}},
  "update_mask": {"paths": ["reflect.disposition"]}}' | jq '.config.own'
g $GLOBEX ConfigService/GetTenantConfig '{}' | jq '{own: .config.own, skepticism: .config.effective.reflect.disposition.skepticism}'
```

A bank with its own mission and disposition, and a standing directive.

```sh
g $ACME BankService/CreateBank '{"bank_id": "support", "name": "Support desk", "config": {
  "retain": {"mission": "Extract customer problems, product names and promised follow-ups."},
  "reflect": {"mission": "You help support agents prepare for a call.", "disposition": {"empathy": 5}}}}'
g $ACME ConfigService/CreateDirective '{"bank_id": "support", "directive": {"name": "no-refund-promises",
  "rules": ["Never promise a refund; say a human will confirm."], "active": true}}'
```

Export the bank as a template and create the same bank in another tenant from it. The template carries the config,
the directives and the mental-model specs, not the memories.

```sh
g $ACME BankService/ExportBankTemplate '{"bank_id": "support"}' | jq .template > /tmp/support-template.json
g $GLOBEX BankService/CreateBank "{\"bank_id\": \"support\", \"name\": \"Globex support\", \"template\": $(cat /tmp/support-template.json)}"
g $GLOBEX ConfigService/UpdateBankConfig '{"bank_id": "support", "config": {"reflect": {"mission": "You help support agents of a logistics company."}},
  "update_mask": {"paths": ["reflect.mission"]}}' | jq '.config.effective.reflect'
```

Isolation: the same bank id holds different memories per tenant.

```sh
g $ACME MemoryService/Retain '{"bank_id": "support", "items": [{"document_id": "ticket-1",
  "content": "Customer Rivera reported that the export button crashes the app."}]}' > /dev/null
g $ACME BankService/GetBankStats '{"bank_id": "support"}' | jq ".stats.documents // 0"
g $GLOBEX BankService/GetBankStats '{"bank_id": "support"}' | jq ".stats.documents // 0"
```

A bank the tenant does not have is `NOT_FOUND`, and a call without a tenant is `INVALID_ARGUMENT`.

```sh
g $GLOBEX BankService/GetBank '{"bank_id": "acme-only"}' || true
grpcurl -plaintext -d '{}' "$MEMD" memory.v1.BankService/ListBanks || true
```

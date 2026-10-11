# chat-assistant: the same calls with grpcurl

Needs [grpcurl](https://github.com/fullstorydev/grpcurl) and [jq](https://jqlang.org/), and a running memd (see
[../README.md](../README.md)). The `g` helper adds the tenant header and replaces `BANK_ID` in the JSON with
`$BANK`, so every command below can be pasted as it is.

```sh
export MEMD=${MEMD:-localhost:50051} TENANT=${TENANT:-examples} BANK=chat-assistant-$RANDOM
g() { grpcurl -plaintext -H "x-tenant-id: $TENANT" -d "${2//BANK_ID/$BANK}" "$MEMD" "memory.v1.$1"; }
```

Retain the conversation. The bank is created by this first write.

```sh
g MemoryService/Retain '{
  "bank_id": "BANK_ID",
  "items": [{
    "document_id": "chat-2026-10-04",
    "context": "A chat between the assistant and the user Dana",
    "tags": ["user:dana"],
    "content": "Dana: I just moved from Lisbon to Berlin for a job at a robotics startup.\nDana: I am vegetarian, so I am looking for good restaurants near Kreuzberg.\nDana: My sister Marta visits on the first weekend of every month, and she is allergic to peanuts."
  }]
}'
```

Wait until consolidation has finished: `pending_operations_by_kind` is empty when nothing is queued or running.

```sh
until [ "$(g BankService/GetBankStats '{"bank_id":"BANK_ID"}' | jq '.stats.pendingOperationsByKind // {} | length')" = 0 ]; do sleep 1; done
```

Recall the memories relevant to the next user message.

```sh
g MemoryService/Recall '{"bank_id": "BANK_ID", "query": "Where should I take Dana for dinner?", "max_tokens": 1024}' |
  jq -r '.results[] | "\(.factType)  \(.text)"'
```

Reflect: memd searches the memories itself and answers. `basedOn` lists the memories the answer cites.

```sh
g MemoryService/Reflect '{"bank_id": "BANK_ID", "query": "What should I keep in mind when I plan a dinner for Dana and her sister?"}' |
  jq '{answer: .text, basedOn: [.basedOn.memories[]?.text], usage}'
```

# batch-ingest: the same calls with grpcurl

Needs [grpcurl](https://github.com/fullstorydev/grpcurl) and [jq](https://jqlang.org/), and a running memd (see
[../README.md](../README.md)). The `g` helper adds the tenant header and replaces `BANK_ID` and `ORG_ID` in the JSON
with `$BANK` and `$ORG`.

```sh
export MEMD=${MEMD:-localhost:50051} TENANT=${TENANT:-examples} ORG=${ORG:-examples} BANK=batch-ingest-$RANDOM
g() { local d="${2//BANK_ID/$BANK}"; grpcurl -plaintext -H "x-tenant-id: $TENANT" -d "${d//ORG_ID/$ORG}" "$MEMD" "memory.v1.$1"; }
```

Create the org first: banks live in an org, and a write never creates one. On a rerun this answers
ALREADY_EXISTS, which is fine.

```sh
g OrgService/CreateOrg '{"org_id": "ORG_ID"}'
```

A batch: every item has a `document_id` and a `revision` (int64 values are JSON strings). The old version of
ticket-203 comes after the new one, and the last item has no id. The response has one result per item.

```sh
BATCH='{"org_id": "ORG_ID", "bank_id": "BANK_ID", "items": [
  {"document_id": "ticket-201", "revision": "1", "content": "Ticket 201: the invoice export is slow for large accounts."},
  {"document_id": "ticket-202", "revision": "1", "content": "Ticket 202: a customer asked for single sign-on."},
  {"document_id": "ticket-203", "revision": "2", "content": "Ticket 203: dark mode flickers on the settings page and the billing page."},
  {"document_id": "ticket-203", "revision": "1", "content": "Ticket 203: dark mode flickers on the settings page."},
  {"content": "Ticket 204: no id, so it can never be deduplicated."}]}'
g MemoryService/RetainBatch "$BATCH" | jq -c '.results[] | {document_id, status, op: .operation.id, error}'
```

Wait for the work to finish (`ListOperations` with `status` `OPERATION_STATUS_PENDING` comes back empty), then
send the same batch again: the stored documents are `STATUS_UNCHANGED`.

```sh
until [ "$(g OperationService/ListOperations '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "status": "OPERATION_STATUS_PENDING"}' | jq '.operations | length')" = 0 ]; do sleep 1; done
sleep 2
g MemoryService/RetainBatch "$BATCH" | jq -c '.results[] | {document_id, status}'
```

A late old version is `STATUS_STALE`, and the document keeps revision 2:

```sh
g MemoryService/RetainBatch '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "items": [
  {"document_id": "ticket-203", "revision": "1", "content": "Ticket 203: dark mode flickers on the settings page."}]}' | jq -c '.results[]'
g DocumentService/GetDocument '{"org_id": "ORG_ID", "bank_id": "BANK_ID", "document_id": "ticket-203"}' | jq .document.revision
```

A backlog rejection looks like this (`grpcurl` prints the status and its details):

```text
ERROR:
  Code: ResourceExhausted
  Message: resource exhausted: retain backlog over its limit: bank ... has more than 5000 pending retains
  Details:
  1)	{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "domain": "memd", "reason": "INGEST_BACKLOG"}
  2)	{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "10s"}
```

## The same documents from a Kafka topic

`memd ingest` retains messages from a Kafka topic through the same path as RetainBatch (see the README's Queue
ingestion section). With a local broker and [kcat](https://github.com/edenhill/kcat), create the topic and its
dead-letter topic (memd never creates topics), start the consumer next to `memd all`, and publish one envelope per
document, keyed by the document id:

```sh
docker run -d --name memd-kafka -p 9092:9092 apache/kafka:4.1.1
topics() { docker exec memd-kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 "$@"; }
until topics --list >/dev/null 2>&1; do sleep 1; done   # wait for the broker
for t in memd-ingest memd-ingest-dlq; do topics --create --topic "$t"; done

# The shared setup's S3 variables are still exported; the DSN is the one `make run` uses. No gateway is needed.
MEMD_DSN=postgresql://memd:memd@localhost:55432/memd MEMD_CONFIG=memd.yaml \
  MEMD_INGEST_PROVIDER=kafka MEMD_INGEST_KAFKA_BROKERS=localhost:9092 MEMD_INGEST_KAFKA_TOPIC=memd-ingest \
  MEMD_INGEST_KAFKA_DLQ_TOPIC=memd-ingest-dlq MEMD_INGEST_TENANT=$TENANT go run ./cmd/memd ingest &

for i in 301 302; do
  printf 'ticket-%s|{"org_id": "%s", "bank_id": "%s", "item": {"document_id": "ticket-%s", "revision": "1", "content": "Ticket %s: the app logs users out after an update."}}\n' \
    "$i" "$ORG" "$BANK" "$i" "$i"
done | kcat -b localhost:9092 -t memd-ingest -P -K '|'
echo 'junk|not an envelope' | kcat -b localhost:9092 -t memd-ingest -P -K '|'
```

The two tickets become documents; the junk message lands on the dead-letter topic with its reason in a header:

```sh
g DocumentService/ListDocuments '{"org_id": "ORG_ID", "bank_id": "BANK_ID"}' | jq -c '[.documents[].id]'
kcat -b localhost:9092 -t memd-ingest-dlq -C -e -f '%k %h\n'   # junk memd-dlq-reason=UNDECODABLE: ...
```

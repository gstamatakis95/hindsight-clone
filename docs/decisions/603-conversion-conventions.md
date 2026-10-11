# 603 — proto ↔ domain conversion conventions

## Context
The card asks for `convert.go`: proto ↔ domain for every message, plus a round-trip test for every message
fuzzed with random values. The spec doesn't say:
- how map keys and scope strings are spelled;
- what happens when a domain type is richer or poorer than its message;
- which messages have no domain counterpart at all.

## Decision
- **Structural only.** No tag normalization, defaults or business validation; the engines own those.
  - proto → domain uses a `decoder` that records the first error. The only errors are an undefined enum
    number and an invalid Timestamp, both INVALID_ARGUMENT.
  - domain → proto never fails. An unknown domain enum string becomes `*_UNSPECIFIED`.
- **Enums** go through one descriptor-driven helper pair. The domain string is the lowercase value name
  without the enum prefix (decision 010). The mapping is derived from the descriptor, so
  no value is mapped by hand; `TestUnknownEnumRejected` pins the rejection of undefined numbers.
- **Map keys** named "fact type name" or "kind" (`BankStats`, `Document.units_by_fact_type`) are the domain
  enum strings, for example `world` and `retain_document`.
- **Scope strings** (`StrategyMatch.matched_scopes` / `shadowed_scopes`) use the canonical length-prefixed
  scope encoding (`domain.EncodeScopeTags`); decoding uses `DecodeScopeTags`.
- **Times.** A zero `time.Time` is a nil Timestamp.
- **Opaque payloads.**
  - `Operation.result` (Struct) ↔ `json.RawMessage` goes through protojson.
  - `ModelBinding.extra` ↔ `map[string]any`. nil and an empty Struct stay distinct.
- **Presence.**
  - `UpdateMemoryRequest.entities`: unset means unchanged; set but empty means clear.
  - `Bank.deleting` ↔ `DeletingAt != nil`.
- **Mental-model `Detail` trimming** happens in the server, following the proto's Detail comments:
  - METADATA is the spec plus status;
  - CONTENT adds `content_md` and `structured_output_json`;
  - FULL adds `source_ids` and `last_trace`.
  - Defaults: FULL for Get, METADATA for List, FULL for Create, Update and Clear.
- **Round-trip coverage.** Every message with a domain, engine or server counterpart round-trips
  proto → domain → proto, requests included: 96 messages × 200 random samples, plus `FuzzRoundTrip`.
  - Pure envelopes (ids only, or a wrapper around an already round-tripped message) are listed as exempt
    with a reason. The bufconn test covers them.
  - `TestRoundTripEveryMessage` fails when a memory.v1 message has neither a round trip nor an exemption.
  - Documented lossy spots are normalized per entry. Example: a nil message whose domain twin is a value
    becomes an empty message.
- **`RetainResponse.usage`** (CCR WP-07-1) is mapped through the server-side `retainResult`
  (IngestResult + Usage) and round-tripped. `retain.IngestResult` has no `Usage` field yet (WP-08 owns it).
  The Retain handler leaves it empty, marked `TODO(WP-08)`.

## Alternatives
- **Enum `String()` names as map keys** (`FACT_TYPE_WORLD`). Rejected: they are noisy, and the domain
  strings already match the DB and decision 010.
- **One-way converters for requests.** Rejected: the card asks for round trips of every message.

## Spec lines involved
gRPC surface (all messages), decision 010, `Detail` enum comments, CCR WP-07-1.

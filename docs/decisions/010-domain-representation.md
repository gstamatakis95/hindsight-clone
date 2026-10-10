# 010 — How domain types represent the proto

## Context
Engines may not import generated proto types, so `internal/domain` mirrors the proto. We had to choose
how enums, optional fields and lists are represented, and where types shared by two engines live.

## Decision
- **Enums are string types.** Their values are the lowercase proto suffix, for example
  `FactTypeWorld = "world"` and `OperationKindRetainDocument = "retain_document"`.
  - The empty string is `*_UNSPECIFIED`.
  - The strings match the CHECK-constrained text columns in the schema (`fact_type`, `kind`, `status`,
    link `kind`, history `kind`).
  - BankConfig stored as jsonb therefore reads well and survives enum renumbering.
- **`BankConfig` field representation** (WP-04's Merge relies on this):

  | Proto field | Go representation | Unset means |
  |---|---|---|
  | `optional` scalar | pointer | nil |
  | nested message | pointer | nil |
  | repeated / map | slice / map | nil (a non-nil empty slice is an explicit clear, as a FieldMask path would set) |
  | non-optional scalar (missions, enums) | plain value | zero value |

- **JSON tags** use the proto field names, because BankConfig is persisted as jsonb (bank config,
  document and operation overrides).
  - One Go field name is shortened to stay under 120 columns:
    `ConsolidationSourceTokensPerObs` ↔ `consolidation_source_facts_max_tokens_per_observation`.
- **Integer widths.** Integer knobs are `int` (proto int32), and counters and sequences are `int64`.
  `ModelBinding.Extra` (a `google.protobuf.Struct`) is `map[string]any`.
- **`TagGroup`'s oneof** is a `Kind` discriminator plus `Leaf`, `Groups` (all_of / any_of) and `Not`
  (none_of).
- **`ExtractedFact` and `Mention`** live in domain, not retain.
  - `timex.TimeNormalizer.Normalize` takes `*ExtractedFact`, and timex must not import retain (retain
    uses timex).
  - The retain package re-exports them as aliases.
  - `ExtractedFact` also carries `TimeExpression` and `Labels`, which extraction needs for TIME_PARSE
    and label resolution.
- **IDs** are string types (UUID text for server-generated ids), so fakes need no UUID library. The
  Postgres adapter parses them at the boundary.
- **Response-only fields** are filled by readers and are not persisted columns: `Unit.Entities`,
  `Unit.SourceIDs`, `Unit.State`, `Document.UnitCount`, `Document.UnitsByFactType` and
  `MentalModel.IsStale`.
- **Helpers in domain:**
  - `BankBlobPrefix` / `DocumentBlobKey` build the blob key layout.
  - `OperationKind.MaxAttempts` is 3, or unbounded for consolidate.
  - `OperationStatus.Terminal`.
  - `BankConfig.Binding` is a single-level lookup; the merge itself belongs to WP-04.

## Alternatives
- int enums mirroring proto numbers. Rejected: they're unreadable in jsonb and logs, and fragile.
- Generated proto types inside domain. Rejected: depguard forbids gen in engines.

## Spec lines involved
gRPC surface (BankConfig comments: "Scalars: set replaces unset. models: merged per slot. Other lists:
set replaces"), Engines (ExtractedFact, TimeNormalizer), Code standards (depguard).

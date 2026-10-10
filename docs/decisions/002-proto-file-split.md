# 002: Splitting the memory.v1 proto into files

Status: accepted (WP-00)

## Context

SPEC.md "gRPC surface" defines `memory.v1` as one proto file, listed in two blocks only for length. "File structure"
asks for twelve files under `api/memory/v1/` and gives each a one-line comment saying what goes in it. Those comments
leave many types unplaced, and following them literally produces at least one import cycle: "traces" in
`common.proto` would include RefreshTrace, which needs DeltaOp and RefreshOutcome from `mental_model.proto`, while
`mental_model.proto` needs common. The cross-file dependencies to resolve:

- `config.proto` (BankConfig) needs `ObservationScopes`, `TagSet`, `TagsMatch`, `Budget`, `RefreshMode` and
  `RefreshStrategy`. Those enums and messages sit in the spec's "tag filters & scopes" and "configuration" sections.
  They are also used by the service files, and `common.proto` cannot import `config.proto` either.
- `Operation` is returned by bank, document, observation and mental-model RPCs, but the File-structure comments
  only place `OperationService` in `operation.proto`.
- `BankTemplate` (bank) needs `MentalModelSpec` (mental_model). `ReflectRequest` (memory) needs `Directive` (bank).
- `RefreshTrace` (mental_model) needs `ToolCallTrace` and `SlotUsage`, which also serve reflect and operations.
- `ListEvents` is an rpc of `OperationService`, but its messages belong with the events in `events.proto`.

protoc rejects import cycles, and Go generates one package per directory, so every type has to live in exactly
one file and the import graph has to be a DAG.

## Decision

Shared leaf types move down into `common.proto`. `Operation` stays in `operation.proto`, and the service files
import it. Every name, field number, label, type, enum value, `optional`, map, oneof and nested enum is unchanged.
A descriptor comparison, run on spec.json against split.json with the scratch script cmp.py, finds the same 1005
elements in both: 8 services, 48 rpcs, 163 messages (map entries included), 636 fields, 25 enums and
125 enum values.

Import layering, lowest first. A file imports only from layers below it:

| Layer | Files | Imports (besides google/protobuf) |
|---|---|---|
| 0 | `models.proto`, `events.proto` | none |
| 1 | `common.proto` | models |
| 2 | `config.proto` | common, models |
| 3 | `operation.proto`, `tenant.proto`, `memory.proto`, `memories.proto` | common, config, events (operation only) |
| 4 | `mental_model.proto`, `document.proto`, `observation.proto` | common, config, operation |
| 5 | `bank.proto` | common, config, mental_model, operation |

Final file -> types mapping. This table was generated from `buf build -o split.json`. Nested enums are shown as
`Message.Enum`.

| File | Services | Enums | Messages |
|---|---|---|---|
| `models.proto` | - | ModelSlot | ModelBinding, SlotBinding |
| `common.proto` | - | FactType, Budget, TagsMatch, RefreshMode, RefreshStrategy, MemoryState, ObservationScopes.Mode, TagLeaf.Resolve | TagSet, ObservationScopes, TagGroup, TagGroupList, TagLeaf, MinScores, Scores, Memory, Chunk, Directive, SlotUsage, ArmTrace, RecallTrace, ToolCallTrace, ReflectTrace |
| `config.proto` | - | ExtractionMode, BudgetFunction, EntityLabel.Type | BankConfig, Disposition, EntityLabel, LabelValue, ScopePattern, ConsolidationStrategy, RetainTuning, RecallConfig, ReflectConfig, MentalModelConfig |
| `events.proto` | - | EventType | LifecycleEvent, ListEventsRequest, ListEventsResponse |
| `operation.proto` | OperationService | OperationStatus, OperationKind | Progress, Operation, Get/List/Cancel/RetryOperation Request + Response |
| `tenant.proto` | TenantService | - | Get/UpdateTenantConfig Request + Response |
| `memory.proto` | MemoryService | - | EntityHint, RetainItem, RetainRequest, RetainedDocument, RetainResponse, RecallInclude, RecallRequest, EntityState, RecallResponse, ReflectInclude, ReflectRequest, ReflectResponse, BasedOn |
| `memories.proto` | MemoryUnitService | TimeField | ListMemories/GetMemory/GetObservationHistory/UpdateMemory Request + Response, ObservationVersion, EntityList |
| `document.proto` | DocumentService | DocTimeField | Document, List/Get/Update/ReprocessDocument Request + Response |
| `observation.proto` | ObservationService | - | Consolidate/ListScopes/ClearObservations/ClearMemoryObservations Request + Response, ScopeInfo, PreviewConsolidationStrategiesRequest, StrategyMatch, PreviewConsolidationStrategiesResponse |
| `mental_model.proto` | MentalModelService | Detail, RefreshOutcome, HistoryKind, DeltaOp.Kind | RefreshTrigger, MentalModelSpec, MentalModel, DeltaOp, RefreshTrace, ResolvedScope, MentalModelHistoryEntry, PageHit, and the Request / Response of all 11 rpcs (DryRunRefresh has its own pair) |
| `bank.proto` | BankService | TagSource, PromptOperation, PromptRole, PromptSource | Bank, BankTemplate, BankStats, TagCount, PromptBlock, PromptMessage, PromptPreview, and the Request / Response of all 14 rpcs |

Deviations from the File-structure comments (SPEC.md lines 930-941):

1. Line 940 places **TagGroup and MinScores in `memories.proto`**. They live in `common.proto` instead, with
   TagGroupList and TagLeaf. Tag filters are shared by Recall, Reflect, RefreshTrigger and ResolvedScope. Putting
   them in the curation file would make `memory.proto` and `mental_model.proto` depend on `memories.proto` for a
   concept that has nothing to do with curation. This placement isn't forced by a cycle; we chose it for cohesion.
2. Enums used by config and by services (**FactType, Budget, TagsMatch, RefreshMode, RefreshStrategy,
   MemoryState**) and **TagSet and ObservationScopes** live in `common.proto`. `config.proto` needs them, and
   `common.proto` cannot import `config.proto`. ExtractionMode and BudgetFunction are used only by config, so they
   stay in `config.proto`.
3. **Directive** lives in `common.proto` rather than `bank.proto`, because `ReflectRequest.extra_directives` needs
   it. `memory.proto` could import `bank.proto` without a cycle, but that would pull the whole admin surface
   (mental_model, operation) into the data-plane file.
4. Line 930 puts "traces" in `common.proto`. That applies to ArmTrace, RecallTrace, ReflectTrace and
   ToolCallTrace. **RefreshTrace** stays in `mental_model.proto`, because it depends on DeltaOp and RefreshOutcome,
   which are mental-model types.
5. **Operation, Progress, OperationStatus and OperationKind** live in `operation.proto` next to OperationService.
   The four service files that return an Operation import `operation.proto`. Operation carries a BankConfig
   (`effective_overrides`), so it cannot move down into `common.proto`.
6. **ListEvents** stays an rpc of `OperationService`, as SPEC.md line 189 says. Its request and response messages
   live in `events.proto` with EventType and LifecycleEvent (line 941), and `operation.proto` imports
   `events.proto`.
7. **BankTemplate** stays in `bank.proto` (line 935), which imports `mental_model.proto` for MentalModelSpec.
   Nothing below bank imports it, so the graph stays acyclic.

Other changes made for buf COMMENTS and the 120-column limit:

- Every service, rpc, message, field, enum, enum value and oneof has a leading comment. The spec's trailing `//`
  notes became leading comments, and one-line messages were expanded so that each field has its own comment.
- `ObservationService.PreviewConsolidationStrategies` is 127 columns on one line, and `buf format` joins a plain
  line break. A trailing `// split: 120-column limit` comment after the request type keeps the break stable under
  `buf format`.

## Alternatives

- **One file (`memory.proto`) as in the spec listing.** This has no cycles and needs no deviations. It was rejected
  because File structure asks for per-service files, and a single file of more than 1500 commented lines is hard
  to review.
- **Follow the File-structure comments literally and break cycles with a new file (for example `types.proto`).**
  This would add a thirteenth file that the spec doesn't list. `common.proto` already exists for exactly this role.
- **Keep TagGroup / MinScores in `memories.proto` (line 940).** This is acyclic: memory and mental_model would
  import memories. It was rejected for cohesion; see deviation 1.
- **Move Operation into `common.proto`.** This is impossible without moving BankConfig there too, because Operation
  carries `effective_overrides`. That would collapse config into common.

`buf breaking` with the `FILE` category treats moving a type between files as a breaking change. Once this layout
is on main it is effectively frozen, and any later move needs a CCR.

## Spec lines involved

- SPEC.md 101-910: "gRPC surface" (the two proto blocks, 105-425 and 429-894), including services at 119-190,
  "tag filters & scopes" at 430-451, `Directive` at 372, `BankTemplate` at 380, `Operation` at 847-862 and
  `ListEvents` at 189 and 892-893.
- SPEC.md 912-1017: "File structure", especially the file comments at 927-942.
- SPEC.md 1019-1088: "Code standards": 120 columns, and `buf lint` STANDARD + COMMENTS with `buf breaking`
  against main (1085).
- SPEC.md 1896: the `api/AGENTS.md` contents.

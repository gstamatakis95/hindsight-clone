// Package domain holds memd's plain data types: banks and their configuration, memory units (facts and observations),
// documents and chunks, entities, links and evidence, observation scopes and tag filters, mental models, directives,
// templates, prompt previews, operations and lifecycle events, model slots and bindings, and the error values every
// layer shares.
//
// # Architecture
//
// domain is the innermost layer of memd's ports-and-adapters layout (domain -> port -> engine/adapter -> server/worker
// -> cmd/memd/wire.go). It imports nothing internal and nothing vendor-specific (depguard enforces both); its only
// dependency outside the standard library is golang.org/x/text for Unicode normalization of tags. Every other package
// may import it. Proto types never appear here: internal/grpcapi converts between memory.v1 messages and these types,
// and the Postgres adapter stores them in columns and JSON.
//
// # Representation
//
// Enums are string-valued so they read well in JSON columns and logs; the string is the stored column value (lower
// snake case of the proto enum without its prefix, e.g. [OperationKindRetainDocument] = "retain_document"), and the
// empty string is always the "unspecified" value, mirroring the proto's *_UNSPECIFIED = 0. Optional configuration
// scalars are pointers (nil = unset), nested messages are pointers (nil = unset) and lists are slices (nil = unset,
// non-nil empty = explicitly cleared).
//
// [BankConfig] is the one configuration shape used at every level, merged server < tenant < bank < strategy < request
// (< item). It is grouped by phase ([RetainConfig], [ObservationConfig], [RecallConfig], [ReflectConfig],
// [MentalModelConfig], [ModelBindings]): a set scalar replaces an unset one, groups merge field by field, a
// [ModelBinding] is atomic, and lists replace rather than append. [ModelBindings] binds models.default for every
// chat-model slot ([LLMSlots]) plus one optional binding per slot. The merging itself lives in internal/config;
// [BankConfig.Binding] and [ModelBindings.For] only read one level. New behaviour knobs go into BankConfig, in their
// phase group, so a level or a request override can set them.
//
// # Identity and scopes
//
// [BankRef] (tenant, bank) names a bank; every id ([UnitID], [DocumentID], [ChunkID], [EntityID], [OperationID],
// [MentalModelID], [DirectiveID]) is unique only within its bank. Tags are canonicalized by [NormalizeTags]: NFC,
// trimmed, lowercased, empty tags dropped, deduplicated and sorted; storage compares tags as plain arrays, so writers
// normalize first. A [ScopeKey] is one observation scope, a canonical tag set
// within a bank (no tags = the shared scope). Its String is a length-prefixed encoding ("3:foo,5:bar,b"), so no tag
// content can make two scopes collide, and [ParseScopeKey] accepts only the canonical form, so a scope has exactly one
// key.
//
//	k := domain.NewScopeKey(bank, []string{"Project-X ", "alice"}) // tags normalized and sorted
//	k.String()                                                   // "5:alice,9:project-x"
//
// # Tag filtering
//
// [MatchTags] is the flat filter with Hindsight's five modes (any, all, any_strict, all_strict, exact; unset behaves as
// any): any and all let untagged units pass, the strict modes do not, and exact with no tags selects only untagged
// units. [MatchGroup] and [MatchFilter] evaluate the boolean group tree; their semantics are the reference that
// store.SQLPredicate.Eval and the Postgres rendering agree with.
//
// # Operations
//
// An [Operation] is one row of the job queue; its [OperationKind] is also the job kind. [OperationKind.MaxAttempts] is
// 3 for every kind except consolidate, which is unbounded (0) and retries with capped backoff.
// [OperationStatus.Terminal] is true for completed, failed and cancelled. A [LifecycleEvent] carries the bank change
// seq of the transaction that wrote it.
//
// # Errors
//
// The error values are classes: wrap them with fmt.Errorf("%w: ...", ErrX) and test with errors.Is. The server maps
// each class to one gRPC code ([ErrNotFound] NOT_FOUND, [ErrAlreadyExists] ALREADY_EXISTS, [ErrInvalidArgument]
// INVALID_ARGUMENT, [ErrFailedPrecondition] FAILED_PRECONDITION, [ErrUnavailable] UNAVAILABLE, [ErrResourceExhausted]
// RESOURCE_EXHAUSTED, ...). Some wrap a broader class: [ErrBankDeleting] and [ErrModelUnbound] wrap
// ErrFailedPrecondition, [ErrContextOverflow] wraps ErrUnavailable and [ErrTooManyScopes] wraps ErrInvalidArgument.
// [ErrConflict] means a concurrent writer won a race and the caller may retry; [ErrCancelled] means the operation was
// cancelled or its bank deleted.
//
// # Testing
//
// scope_test.go and tags_test.go cover the scope encoding (round trips, no collisions, canonical-only parsing) and the
// tag normalization and matching rules. Governing documents: SPEC "Ports", "Behavioural rules" and "Decisions"
// (tenancy, embedding: [EmbeddingDim] is 768).
package domain

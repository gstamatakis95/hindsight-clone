// Package domain holds memd's plain data types: banks and their configuration, memory units (facts and observations),
// documents and chunks, entities and evidence, observation scopes, tag filters and predicates, seq ranges, mental
// models, directives, templates, prompt previews, operations and lifecycle events, model slots and bindings, and the
// error values every layer shares; plus the pure rules several packages share: [ExpandScopes], [SelectDirectives]
// and [RefreshDueAt].
//
// # Architecture
//
// domain is the innermost layer of memd's layout (domain -> boundaries and infrastructure -> libraries -> services ->
// maintenance and grpcapi -> cmd/memd). It imports nothing internal and nothing vendor-specific (depguard enforces
// both); its only dependency outside the standard library is golang.org/x/text for Unicode normalization of tags. Every
// other package may import it. Proto types never appear here: internal/grpcapi converts between memory.v1 messages and
// these types, and the Postgres adapter stores them in columns and JSON.
//
// # Representation
//
// Enums are string-valued so they read well in JSON columns and logs; the string is the stored column value (lower
// snake case of the proto enum without its prefix, e.g. [OperationKindRetainDocument] = "retain_document"), and the
// empty string is always the "unspecified" value, mirroring the proto's *_UNSPECIFIED = 0. Optional configuration
// scalars are pointers (nil = unset), nested messages are pointers (nil = unset) and lists are slices (nil = unset,
// non-nil empty = explicitly cleared).
//
// [BankConfig] is the one configuration shape used at every level, merged server < tenant < org < bank < strategy <
// request (< item). It is grouped by phase ([RetainConfig], [ObservationConfig], [RecallConfig], [ReflectConfig],
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
// key. [ExpandScopes] fans a unit's tags out into the scopes it consolidates under, by the observation scope mode.
//
//	k := domain.NewScopeKey(bank, []string{"Project-X ", "alice"}) // tags normalized and sorted
//	k.String()                                                   // "5:alice,9:project-x"
//
// # Tag filtering
//
// [MatchTags] is the flat filter with Hindsight's five modes (any, all, any_strict, all_strict, exact; unset behaves as
// any): any and all let untagged units pass, the strict modes do not, and exact with no tags selects only untagged
// units. [MatchGroup] and [MatchFilter] evaluate the boolean group tree; their semantics are the reference that
// [TagPredicate.Eval] and the Postgres rendering agree with. A [TagPredicate] is a compiled filter (fuzzy leaves
// resolved) that the searches and the graph take, and a [SeqRange] bounds them to a snapshot.
//
// [TagFilter] is the one filter shape every request uses (recall, reflect, the list RPCs, a mental model's scope,
// directives and consolidation strategy patterns): flat tags with a match mode, or groups, never both
// ([TagFilter.Validate]); list sites take the flat form only ([TagFilter.RequireFlat]). A mental model's effective
// scope is [MentalModelSpec.ScopeFilter].
//
// # Directives
//
// [SelectDirectives] is the one rule for which directives apply to a request: reflect, the mental-model synthesize
// prompt and the prompt preview all use it. Inactive directives never apply; untagged ones always do; a tagged one
// applies only when the request's tag filter is set and its tags satisfy it, or with apply_all_directives.
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
// The error values are classes ([Error]): wrap them with fmt.Errorf("%w: ...", ErrX) and test with errors.Is. The
// server maps each root class to one gRPC code ([ErrNotFound] NOT_FOUND, [ErrAlreadyExists] ALREADY_EXISTS,
// [ErrInvalidArgument] INVALID_ARGUMENT, [ErrFailedPrecondition] FAILED_PRECONDITION, [ErrUnavailable] UNAVAILABLE,
// [ErrResourceExhausted] RESOURCE_EXHAUSTED, ...) and sends the most specific class's stable reason ([ReasonOf]) as
// google.rpc.ErrorInfo. Refined classes keep their parent's code and add a reason: [ErrBankNotFound] (NOT_FOUND);
// [ErrTooManyScopes] and [ErrMemoryIsObservation] (INVALID_ARGUMENT); [ErrBankDeleting], [ErrModelUnbound],
// [ErrOperationNotCancellable] and [ErrOperationNotRetryable] (FAILED_PRECONDITION); [ErrContextOverflow] and
// [ErrRetrievalUnavailable] (UNAVAILABLE); [ErrMaxTotalTokens] (RESOURCE_EXHAUSTED). [ErrConflict] means a concurrent
// writer won a race and the caller may retry; [ErrCancelled] means the operation was cancelled or its bank deleted.
// [InvalidField] builds an ErrInvalidArgument that names the offending request field or config path;
// [FieldViolations] collects them, so the server can send a google.rpc.BadRequest.
//
// # Paging
//
// [PageBounds] states a listing's size rule once: [ListPage] (page_size: 0 = 100, at most 1000) for every List RPC
// and [SearchResults] (max_results: 0 = 10, at most 100) for page search; a negative size is an [InvalidField] error.
// [EncodePageToken] and [DecodePageToken] make a keyset position an opaque base64url JSON token.
//
// # Testing
//
// scope_test.go and tags_test.go cover the scope encoding (round trips, no collisions, canonical-only parsing) and the
// tag normalization and matching rules. [EmbeddingDim] is 768: one embedding model per
// deployment (docs/design/configuration.md#model-routing).
package domain

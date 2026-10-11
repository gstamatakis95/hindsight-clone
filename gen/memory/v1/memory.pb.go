// The memd API: agent memory over gRPC.
//
// This one file is the whole contract. It is ordered as follows:
//   1. this header: the conventions every service shares;
//   2. all service blocks;
//   3. one section per service, in the same order, with each RPC's request, response and helper messages,
//      then the service's resource messages;
//   4. shared types used by more than one service;
//   5. configuration: BankConfig, its sub-messages and the model bindings;
//   6. shared enums.
//
// Tenant. The tenant comes from the x-tenant-id metadata header, never from the request body.
//
// Banks. A bank is an isolated memory store within a tenant. A write to an unknown bank creates it with
// tenant defaults, and every read of an unknown bank returns NOT_FOUND.
//
// Configuration. BankConfig is the one config shape used at every stored level: server (the defaults block of
// the server config file) < tenant < bank, then a consolidation strategy and the request (< item, for retain).
// It is grouped by phase: retain, observations, recall, reflect, mental_models, models. Scalars: a set value
// replaces an unset one. Groups merge field by field. A ModelBinding is atomic. Lists: a set list replaces the
// inherited one (no append). Patch RPCs take a FieldMask, which is also how a list is cleared. Each request
// that does work takes its own *Overrides message holding only what that RPC can change; the server turns it
// into a BankConfig level. Model routing: see ModelBindings.
//
// Deadlines. A call that arrives without a deadline gets the server's default deadline (a server setting, so
// set your own for anything slower). A call that runs out of time fails with DEADLINE_EXCEEDED; work already
// committed stays. Recall, Reflect and PreviewMentalModelRefresh run within the call's deadline.
//
// Recall arms. A recall retrieves candidates with several named arms (built in: vector, text, graph, temporal)
// and fuses them. Arms are open-ended, so anything per arm is keyed by arm name, never a field per arm;
// ListRecallArms lists the names a server accepts. An unknown arm name is INVALID_ARGUMENT.
//
// Async work. Background work runs as operations. An RPC whose requested work runs in the background returns
// the Operation it enqueued, in a field named operation (or operations, batch_operation,
// mental_model_operations), never a bare id; OperationService reports its status and progress. Follow-up jobs
// an RPC triggers on its own (such as consolidation after a retain) are not returned. Retain is synchronous by
// default, returning once the memories are committed; run_async=true makes it return the enqueued operations instead.
// Recall and Reflect are synchronous.
//
// Events. State changes are recorded as lifecycle events numbered by the bank's change sequence; clients
// poll OperationService.ListEvents with after_seq, passing the previous response's next_after_seq.
//
// Paging. Every List RPC takes page_size and page_token and returns next_page_token (empty = no more pages).
// page_size 0 = 100, larger than 1000 = 1000, negative = INVALID_ARGUMENT. A page token is opaque and only
// valid for the same request; a malformed one is INVALID_ARGUMENT. ListEvents pages by sequence instead
// (page_size + after_seq / next_after_seq), and SearchMentalModels takes max_results (0 = 10, larger than
// 100 = 100, negative = INVALID_ARGUMENT).
//
// Tag filters. Every request that filters by tag takes one TagFilter: flat tags under a match mode, or
// boolean tag groups (setting both = INVALID_ARGUMENT). Each TagFilter field states its default match mode.
// List RPCs take only the flat form for now: groups there are INVALID_ARGUMENT.
//
// Errors. Every error carries a canonical gRPC code and a google.rpc.ErrorInfo detail with domain "memd" and
// a stable reason; INVALID_ARGUMENT also carries a google.rpc.BadRequest detail whose field violations name
// the offending request fields (for a config, the BankConfig path, for example reflect.disposition.empathy)
// when the server knows them. Clients branch on the code, then the reason; the message is for humans. The
// reasons, by code:
//   INVALID_ARGUMENT     INVALID_ARGUMENT (malformed request), TOO_MANY_SCOPES (ALL_COMBINATIONS fan-out
//                        over max_combinations), MEMORY_IS_OBSERVATION (curating an observation)
//   NOT_FOUND            NOT_FOUND (the named object does not exist), BANK_NOT_FOUND (unknown bank, or any
//                        read of a bank being deleted except GetOperation of its delete_bank operations)
//   ALREADY_EXISTS       ALREADY_EXISTS (a create collided with an existing id)
//   FAILED_PRECONDITION  FAILED_PRECONDITION (the state forbids the call), BANK_DELETING (a write to a bank
//                        being deleted), MODEL_UNBOUND (a model slot the call needs has no binding),
//                        OPERATION_NOT_CANCELLABLE, OPERATION_NOT_RETRYABLE (the operation's status or kind
//                        forbids it)
//   UNAVAILABLE          UNAVAILABLE (a dependency failed; retry with backoff), RETRIEVAL_UNAVAILABLE (a
//                        Reflect retrieval tool failed), CONTEXT_OVERFLOW (a prompt exceeded the model's context)
//   RESOURCE_EXHAUSTED   RESOURCE_EXHAUSTED, MAX_TOTAL_TOKENS_EXCEEDED (Reflect went over max_total_tokens)
//   ABORTED              OPERATION_CANCELLED (the operation was cancelled or its bank deleted), CONFLICT (a
//                        concurrent writer won; retry)
//   DEADLINE_EXCEEDED    DEADLINE_EXCEEDED
//   CANCELLED            CANCELLED (the client cancelled the call)
//   INTERNAL             INTERNAL (a server bug)
// The x-tenant-id header is checked first: missing or invalid = INVALID_ARGUMENT with field x-tenant-id.
//
// Naming. Every RPC has its own <Rpc>Request and <Rpc>Response. Every enum value is prefixed with its enum
// name, and value 0 is *_UNSPECIFIED. Patch RPCs take a google.protobuf.FieldMask update_mask.

// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.12
// 	protoc        (unknown)
// source: memory/v1/memory.proto

package memoryv1

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	structpb "google.golang.org/protobuf/types/known/structpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	reflect "reflect"
	sync "sync"
	unsafe "unsafe"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// MemoryState says whether a memory is live or invalidated by curation.
type MemoryState int32

const (
	// Not set. In ListMemories it applies no state filter.
	MemoryState_MEMORY_STATE_UNSPECIFIED MemoryState = 0
	// Live: visible to recall and consolidation.
	MemoryState_MEMORY_STATE_VALID MemoryState = 1
	// Invalidated (InvalidateMemory): invisible to recall and consolidation; RestoreMemory brings it back.
	MemoryState_MEMORY_STATE_INVALIDATED MemoryState = 2
)

// Enum value maps for MemoryState.
var (
	MemoryState_name = map[int32]string{
		0: "MEMORY_STATE_UNSPECIFIED",
		1: "MEMORY_STATE_VALID",
		2: "MEMORY_STATE_INVALIDATED",
	}
	MemoryState_value = map[string]int32{
		"MEMORY_STATE_UNSPECIFIED": 0,
		"MEMORY_STATE_VALID":       1,
		"MEMORY_STATE_INVALIDATED": 2,
	}
)

func (x MemoryState) Enum() *MemoryState {
	p := new(MemoryState)
	*p = x
	return p
}

func (x MemoryState) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (MemoryState) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[0].Descriptor()
}

func (MemoryState) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[0]
}

func (x MemoryState) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use MemoryState.Descriptor instead.
func (MemoryState) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{0}
}

// MentalModelView picks how much of a mental model a read returns.
type MentalModelView int32

const (
	// Not set; the RPC's default applies (GetMentalModel: FULL, ListMentalModels: BASIC).
	MentalModelView_MENTAL_MODEL_VIEW_UNSPECIFIED MentalModelView = 0
	// Spec and status fields only, no content.
	MentalModelView_MENTAL_MODEL_VIEW_BASIC MentalModelView = 1
	// BASIC plus content_md and structured_output_json.
	MentalModelView_MENTAL_MODEL_VIEW_CONTENT MentalModelView = 2
	// Everything, including stale, source_memory_ids and last_trace.
	MentalModelView_MENTAL_MODEL_VIEW_FULL MentalModelView = 3
)

// Enum value maps for MentalModelView.
var (
	MentalModelView_name = map[int32]string{
		0: "MENTAL_MODEL_VIEW_UNSPECIFIED",
		1: "MENTAL_MODEL_VIEW_BASIC",
		2: "MENTAL_MODEL_VIEW_CONTENT",
		3: "MENTAL_MODEL_VIEW_FULL",
	}
	MentalModelView_value = map[string]int32{
		"MENTAL_MODEL_VIEW_UNSPECIFIED": 0,
		"MENTAL_MODEL_VIEW_BASIC":       1,
		"MENTAL_MODEL_VIEW_CONTENT":     2,
		"MENTAL_MODEL_VIEW_FULL":        3,
	}
)

func (x MentalModelView) Enum() *MentalModelView {
	p := new(MentalModelView)
	*p = x
	return p
}

func (x MentalModelView) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (MentalModelView) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[1].Descriptor()
}

func (MentalModelView) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[1]
}

func (x MentalModelView) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use MentalModelView.Descriptor instead.
func (MentalModelView) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{1}
}

// RefreshOutcome is the result of one refresh.
type RefreshOutcome int32

const (
	// Not set; never stored.
	RefreshOutcome_REFRESH_OUTCOME_UNSPECIFIED RefreshOutcome = 0
	// A new version of the content was written.
	RefreshOutcome_REFRESH_OUTCOME_CONTENT_WRITTEN RefreshOutcome = 1
	// Nothing was in scope or in the window; no LLM call, only last_refreshed_at advanced.
	RefreshOutcome_REFRESH_OUTCOME_NO_NEW_FACTS RefreshOutcome = 2
	// The refresh failed; the stored content is unchanged.
	RefreshOutcome_REFRESH_OUTCOME_FAILED RefreshOutcome = 3
)

// Enum value maps for RefreshOutcome.
var (
	RefreshOutcome_name = map[int32]string{
		0: "REFRESH_OUTCOME_UNSPECIFIED",
		1: "REFRESH_OUTCOME_CONTENT_WRITTEN",
		2: "REFRESH_OUTCOME_NO_NEW_FACTS",
		3: "REFRESH_OUTCOME_FAILED",
	}
	RefreshOutcome_value = map[string]int32{
		"REFRESH_OUTCOME_UNSPECIFIED":     0,
		"REFRESH_OUTCOME_CONTENT_WRITTEN": 1,
		"REFRESH_OUTCOME_NO_NEW_FACTS":    2,
		"REFRESH_OUTCOME_FAILED":          3,
	}
)

func (x RefreshOutcome) Enum() *RefreshOutcome {
	p := new(RefreshOutcome)
	*p = x
	return p
}

func (x RefreshOutcome) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (RefreshOutcome) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[2].Descriptor()
}

func (RefreshOutcome) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[2]
}

func (x RefreshOutcome) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use RefreshOutcome.Descriptor instead.
func (RefreshOutcome) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{2}
}

// FactType classifies a memory.
type FactType int32

const (
	// Not set; never stored.
	FactType_FACT_TYPE_UNSPECIFIED FactType = 0
	// A fact about the world, extracted at retain.
	FactType_FACT_TYPE_WORLD FactType = 1
	// Something the agent or user experienced, extracted at retain.
	FactType_FACT_TYPE_EXPERIENCE FactType = 2
	// A derived observation written by consolidation from raw facts; never curated directly.
	FactType_FACT_TYPE_OBSERVATION FactType = 3
)

// Enum value maps for FactType.
var (
	FactType_name = map[int32]string{
		0: "FACT_TYPE_UNSPECIFIED",
		1: "FACT_TYPE_WORLD",
		2: "FACT_TYPE_EXPERIENCE",
		3: "FACT_TYPE_OBSERVATION",
	}
	FactType_value = map[string]int32{
		"FACT_TYPE_UNSPECIFIED": 0,
		"FACT_TYPE_WORLD":       1,
		"FACT_TYPE_EXPERIENCE":  2,
		"FACT_TYPE_OBSERVATION": 3,
	}
)

func (x FactType) Enum() *FactType {
	p := new(FactType)
	*p = x
	return p
}

func (x FactType) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (FactType) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[3].Descriptor()
}

func (FactType) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[3]
}

func (x FactType) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use FactType.Descriptor instead.
func (FactType) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{3}
}

// Budget picks a size class for recall and reflect work. Through RecallConfig (budget_low / budget_mid /
// budget_high, default 100 / 300 / 1000) it sets the per-arm candidate cap of recall, and it scales
// reflect iterations (LOW halves, HIGH doubles the MID value).
type Budget int32

const (
	// Not set; the RPC's documented default applies.
	Budget_BUDGET_UNSPECIFIED Budget = 0
	// Smallest budget.
	Budget_BUDGET_LOW Budget = 1
	// Medium budget.
	Budget_BUDGET_MID Budget = 2
	// Largest budget.
	Budget_BUDGET_HIGH Budget = 3
)

// Enum value maps for Budget.
var (
	Budget_name = map[int32]string{
		0: "BUDGET_UNSPECIFIED",
		1: "BUDGET_LOW",
		2: "BUDGET_MID",
		3: "BUDGET_HIGH",
	}
	Budget_value = map[string]int32{
		"BUDGET_UNSPECIFIED": 0,
		"BUDGET_LOW":         1,
		"BUDGET_MID":         2,
		"BUDGET_HIGH":        3,
	}
)

func (x Budget) Enum() *Budget {
	p := new(Budget)
	*p = x
	return p
}

func (x Budget) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (Budget) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[4].Descriptor()
}

func (Budget) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[4]
}

func (x Budget) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use Budget.Descriptor instead.
func (Budget) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{4}
}

// TagsMatch decides how a tag list filters memories. The STRICT variants exclude untagged memories; the
// plain variants also accept untagged (global) ones.
type TagsMatch int32

const (
	// Not set; the field's documented default applies (TAGS_MATCH_ANY unless it says otherwise: TagLeaf,
	// ListDocuments and ListDirectives use ANY_STRICT; MentalModelScope on a tagged model and strategy scope
	// patterns use ALL_STRICT).
	TagsMatch_TAGS_MATCH_UNSPECIFIED TagsMatch = 0
	// At least one of the tags, or untagged.
	TagsMatch_TAGS_MATCH_ANY TagsMatch = 1
	// Every one of the tags, or untagged.
	TagsMatch_TAGS_MATCH_ALL TagsMatch = 2
	// Exactly this tag set, no more and no fewer.
	TagsMatch_TAGS_MATCH_EXACT TagsMatch = 3
	// At least one of the tags; untagged memories are excluded.
	TagsMatch_TAGS_MATCH_ANY_STRICT TagsMatch = 4
	// Every one of the tags; untagged memories are excluded.
	TagsMatch_TAGS_MATCH_ALL_STRICT TagsMatch = 5
)

// Enum value maps for TagsMatch.
var (
	TagsMatch_name = map[int32]string{
		0: "TAGS_MATCH_UNSPECIFIED",
		1: "TAGS_MATCH_ANY",
		2: "TAGS_MATCH_ALL",
		3: "TAGS_MATCH_EXACT",
		4: "TAGS_MATCH_ANY_STRICT",
		5: "TAGS_MATCH_ALL_STRICT",
	}
	TagsMatch_value = map[string]int32{
		"TAGS_MATCH_UNSPECIFIED": 0,
		"TAGS_MATCH_ANY":         1,
		"TAGS_MATCH_ALL":         2,
		"TAGS_MATCH_EXACT":       3,
		"TAGS_MATCH_ANY_STRICT":  4,
		"TAGS_MATCH_ALL_STRICT":  5,
	}
)

func (x TagsMatch) Enum() *TagsMatch {
	p := new(TagsMatch)
	*p = x
	return p
}

func (x TagsMatch) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (TagsMatch) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[5].Descriptor()
}

func (TagsMatch) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[5]
}

func (x TagsMatch) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use TagsMatch.Descriptor instead.
func (TagsMatch) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{5}
}

// RefreshMode decides whether a mental-model refresh rewrites the page or edits it.
type RefreshMode int32

const (
	// Not set; inherit (MentalModelSpec.mode falls back to MentalModelConfig.default_mode).
	RefreshMode_REFRESH_MODE_UNSPECIFIED RefreshMode = 0
	// Rewrite the whole document from everything in scope; prunes source_memory_ids.
	RefreshMode_REFRESH_MODE_FULL RefreshMode = 1
	// Apply edit ops for what changed in scope since last_seq_seen; falls back to full when there is no
	// baseline content or the source query changed.
	RefreshMode_REFRESH_MODE_DELTA RefreshMode = 2
)

// Enum value maps for RefreshMode.
var (
	RefreshMode_name = map[int32]string{
		0: "REFRESH_MODE_UNSPECIFIED",
		1: "REFRESH_MODE_FULL",
		2: "REFRESH_MODE_DELTA",
	}
	RefreshMode_value = map[string]int32{
		"REFRESH_MODE_UNSPECIFIED": 0,
		"REFRESH_MODE_FULL":        1,
		"REFRESH_MODE_DELTA":       2,
	}
)

func (x RefreshMode) Enum() *RefreshMode {
	p := new(RefreshMode)
	*p = x
	return p
}

func (x RefreshMode) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (RefreshMode) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[6].Descriptor()
}

func (RefreshMode) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[6]
}

func (x RefreshMode) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use RefreshMode.Descriptor instead.
func (RefreshMode) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{6}
}

// RefreshStrategy picks how a mental-model refresh drafts content. Settable per bank, per model or per
// refresh call.
type RefreshStrategy int32

const (
	// Not set; inherit, ending at REFRESH_STRATEGY_AGENTIC.
	RefreshStrategy_REFRESH_STRATEGY_UNSPECIFIED RefreshStrategy = 0
	// The reflect loop with tools, answering as a document (Hindsight's behaviour, the default). Uses the
	// REFLECT slot.
	RefreshStrategy_REFRESH_STRATEGY_AGENTIC RefreshStrategy = 1
	// One recall over the model's scope, observations first, then one MENTAL_MODEL slot call (a full page
	// or delta edit ops); roughly 5-20x cheaper.
	RefreshStrategy_REFRESH_STRATEGY_SINGLE_SHOT RefreshStrategy = 2
)

// Enum value maps for RefreshStrategy.
var (
	RefreshStrategy_name = map[int32]string{
		0: "REFRESH_STRATEGY_UNSPECIFIED",
		1: "REFRESH_STRATEGY_AGENTIC",
		2: "REFRESH_STRATEGY_SINGLE_SHOT",
	}
	RefreshStrategy_value = map[string]int32{
		"REFRESH_STRATEGY_UNSPECIFIED": 0,
		"REFRESH_STRATEGY_AGENTIC":     1,
		"REFRESH_STRATEGY_SINGLE_SHOT": 2,
	}
)

func (x RefreshStrategy) Enum() *RefreshStrategy {
	p := new(RefreshStrategy)
	*p = x
	return p
}

func (x RefreshStrategy) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (RefreshStrategy) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[7].Descriptor()
}

func (RefreshStrategy) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[7]
}

func (x RefreshStrategy) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use RefreshStrategy.Descriptor instead.
func (RefreshStrategy) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{7}
}

// OperationKind is the job type of an operation.
type OperationKind int32

const (
	// Not set; in ListOperations it means no kind filter.
	OperationKind_OPERATION_KIND_UNSPECIFIED OperationKind = 0
	// Parent of one retain_document child per document; its status aggregates its children.
	OperationKind_OPERATION_KIND_RETAIN_BATCH OperationKind = 1
	// Extract, embed, resolve and link one document; resumes by chunk hash; enqueues consolidation.
	OperationKind_OPERATION_KIND_RETAIN_DOCUMENT OperationKind = 2
	// Consolidate the bank's oldest unconsolidated memories into observations; one at a time per bank.
	OperationKind_OPERATION_KIND_CONSOLIDATE OperationKind = 3
	// Re-embed, re-resolve and re-link a curated memory, then enqueue consolidation.
	OperationKind_OPERATION_KIND_REDERIVE_MEMORY OperationKind = 4
	// Rewrite a document's memory tags, clear their observations and enqueue consolidation.
	OperationKind_OPERATION_KIND_RETAG_DOCUMENT OperationKind = 5
	// Refresh one mental model (agentic or single-shot, delta or full) and write a version.
	OperationKind_OPERATION_KIND_REFRESH_MENTAL_MODEL OperationKind = 6
	// Refill links of neighbours of removed memories and prune orphaned entities; one at a time per bank.
	OperationKind_OPERATION_KIND_GRAPH_MAINTENANCE OperationKind = 7
	// Delete everything the bank holds, then the bank itself.
	OperationKind_OPERATION_KIND_DELETE_BANK OperationKind = 8
)

// Enum value maps for OperationKind.
var (
	OperationKind_name = map[int32]string{
		0: "OPERATION_KIND_UNSPECIFIED",
		1: "OPERATION_KIND_RETAIN_BATCH",
		2: "OPERATION_KIND_RETAIN_DOCUMENT",
		3: "OPERATION_KIND_CONSOLIDATE",
		4: "OPERATION_KIND_REDERIVE_MEMORY",
		5: "OPERATION_KIND_RETAG_DOCUMENT",
		6: "OPERATION_KIND_REFRESH_MENTAL_MODEL",
		7: "OPERATION_KIND_GRAPH_MAINTENANCE",
		8: "OPERATION_KIND_DELETE_BANK",
	}
	OperationKind_value = map[string]int32{
		"OPERATION_KIND_UNSPECIFIED":          0,
		"OPERATION_KIND_RETAIN_BATCH":         1,
		"OPERATION_KIND_RETAIN_DOCUMENT":      2,
		"OPERATION_KIND_CONSOLIDATE":          3,
		"OPERATION_KIND_REDERIVE_MEMORY":      4,
		"OPERATION_KIND_RETAG_DOCUMENT":       5,
		"OPERATION_KIND_REFRESH_MENTAL_MODEL": 6,
		"OPERATION_KIND_GRAPH_MAINTENANCE":    7,
		"OPERATION_KIND_DELETE_BANK":          8,
	}
)

func (x OperationKind) Enum() *OperationKind {
	p := new(OperationKind)
	*p = x
	return p
}

func (x OperationKind) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (OperationKind) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[8].Descriptor()
}

func (OperationKind) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[8]
}

func (x OperationKind) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use OperationKind.Descriptor instead.
func (OperationKind) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{8}
}

// OperationStatus is the lifecycle state of an operation.
type OperationStatus int32

const (
	// Not set; in ListOperations it means no status filter.
	OperationStatus_OPERATION_STATUS_UNSPECIFIED OperationStatus = 0
	// Queued, possibly parked until next_run_at.
	OperationStatus_OPERATION_STATUS_PENDING OperationStatus = 1
	// Running; if the server running it stops reporting progress, it goes back to PENDING.
	OperationStatus_OPERATION_STATUS_PROCESSING OperationStatus = 2
	// Finished successfully. Terminal; pruned after the retention period.
	OperationStatus_OPERATION_STATUS_COMPLETED OperationStatus = 3
	// Failed with error after its retries (or at once for deterministic errors). Terminal; RetryOperation
	// re-queues it.
	OperationStatus_OPERATION_STATUS_FAILED OperationStatus = 4
	// Cancelled by CancelOperation or DeleteBank. Terminal; RetryOperation re-queues it.
	OperationStatus_OPERATION_STATUS_CANCELLED OperationStatus = 5
)

// Enum value maps for OperationStatus.
var (
	OperationStatus_name = map[int32]string{
		0: "OPERATION_STATUS_UNSPECIFIED",
		1: "OPERATION_STATUS_PENDING",
		2: "OPERATION_STATUS_PROCESSING",
		3: "OPERATION_STATUS_COMPLETED",
		4: "OPERATION_STATUS_FAILED",
		5: "OPERATION_STATUS_CANCELLED",
	}
	OperationStatus_value = map[string]int32{
		"OPERATION_STATUS_UNSPECIFIED": 0,
		"OPERATION_STATUS_PENDING":     1,
		"OPERATION_STATUS_PROCESSING":  2,
		"OPERATION_STATUS_COMPLETED":   3,
		"OPERATION_STATUS_FAILED":      4,
		"OPERATION_STATUS_CANCELLED":   5,
	}
)

func (x OperationStatus) Enum() *OperationStatus {
	p := new(OperationStatus)
	*p = x
	return p
}

func (x OperationStatus) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (OperationStatus) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[9].Descriptor()
}

func (OperationStatus) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[9]
}

func (x OperationStatus) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use OperationStatus.Descriptor instead.
func (OperationStatus) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{9}
}

// EventType is the kind of state change a lifecycle event records.
type EventType int32

const (
	// Not set; never stored. In a ListEvents filter it is ignored.
	EventType_EVENT_TYPE_UNSPECIFIED EventType = 0
	// A document's retain finished and its memories are committed.
	EventType_EVENT_TYPE_RETAIN_COMPLETED EventType = 1
	// A consolidation run finished writing observations.
	EventType_EVENT_TYPE_CONSOLIDATION_COMPLETED EventType = 2
	// A mental-model refresh completed (a new version or a no-op advance).
	EventType_EVENT_TYPE_MENTAL_MODEL_REFRESHED EventType = 3
	// A mental-model refresh failed after its job retries and auto-refresh was paused.
	EventType_EVENT_TYPE_MENTAL_MODEL_REFRESH_FAILED EventType = 4
	// A memory was edited, invalidated or restored (UpdateMemory, InvalidateMemory, RestoreMemory).
	EventType_EVENT_TYPE_MEMORY_CURATED EventType = 5
	// An operation reached FAILED.
	EventType_EVENT_TYPE_OPERATION_FAILED EventType = 6
)

// Enum value maps for EventType.
var (
	EventType_name = map[int32]string{
		0: "EVENT_TYPE_UNSPECIFIED",
		1: "EVENT_TYPE_RETAIN_COMPLETED",
		2: "EVENT_TYPE_CONSOLIDATION_COMPLETED",
		3: "EVENT_TYPE_MENTAL_MODEL_REFRESHED",
		4: "EVENT_TYPE_MENTAL_MODEL_REFRESH_FAILED",
		5: "EVENT_TYPE_MEMORY_CURATED",
		6: "EVENT_TYPE_OPERATION_FAILED",
	}
	EventType_value = map[string]int32{
		"EVENT_TYPE_UNSPECIFIED":                 0,
		"EVENT_TYPE_RETAIN_COMPLETED":            1,
		"EVENT_TYPE_CONSOLIDATION_COMPLETED":     2,
		"EVENT_TYPE_MENTAL_MODEL_REFRESHED":      3,
		"EVENT_TYPE_MENTAL_MODEL_REFRESH_FAILED": 4,
		"EVENT_TYPE_MEMORY_CURATED":              5,
		"EVENT_TYPE_OPERATION_FAILED":            6,
	}
)

func (x EventType) Enum() *EventType {
	p := new(EventType)
	*p = x
	return p
}

func (x EventType) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (EventType) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[10].Descriptor()
}

func (EventType) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[10]
}

func (x EventType) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use EventType.Descriptor instead.
func (EventType) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{10}
}

// TimeField picks the memory timestamp the time range applies to.
type ListMemoriesRequest_TimeField int32

const (
	// Not set; same as TIME_FIELD_CREATED_AT.
	ListMemoriesRequest_TIME_FIELD_UNSPECIFIED ListMemoriesRequest_TimeField = 0
	// When the memory was written.
	ListMemoriesRequest_TIME_FIELD_CREATED_AT ListMemoriesRequest_TimeField = 1
	// When the memory last changed.
	ListMemoriesRequest_TIME_FIELD_UPDATED_AT ListMemoriesRequest_TimeField = 2
	// When the fact was mentioned.
	ListMemoriesRequest_TIME_FIELD_MENTIONED_AT ListMemoriesRequest_TimeField = 3
	// When the fact started happening.
	ListMemoriesRequest_TIME_FIELD_OCCURRED_START ListMemoriesRequest_TimeField = 4
	// When the fact stopped happening.
	ListMemoriesRequest_TIME_FIELD_OCCURRED_END ListMemoriesRequest_TimeField = 5
)

// Enum value maps for ListMemoriesRequest_TimeField.
var (
	ListMemoriesRequest_TimeField_name = map[int32]string{
		0: "TIME_FIELD_UNSPECIFIED",
		1: "TIME_FIELD_CREATED_AT",
		2: "TIME_FIELD_UPDATED_AT",
		3: "TIME_FIELD_MENTIONED_AT",
		4: "TIME_FIELD_OCCURRED_START",
		5: "TIME_FIELD_OCCURRED_END",
	}
	ListMemoriesRequest_TimeField_value = map[string]int32{
		"TIME_FIELD_UNSPECIFIED":    0,
		"TIME_FIELD_CREATED_AT":     1,
		"TIME_FIELD_UPDATED_AT":     2,
		"TIME_FIELD_MENTIONED_AT":   3,
		"TIME_FIELD_OCCURRED_START": 4,
		"TIME_FIELD_OCCURRED_END":   5,
	}
)

func (x ListMemoriesRequest_TimeField) Enum() *ListMemoriesRequest_TimeField {
	p := new(ListMemoriesRequest_TimeField)
	*p = x
	return p
}

func (x ListMemoriesRequest_TimeField) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (ListMemoriesRequest_TimeField) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[11].Descriptor()
}

func (ListMemoriesRequest_TimeField) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[11]
}

func (x ListMemoriesRequest_TimeField) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use ListMemoriesRequest_TimeField.Descriptor instead.
func (ListMemoriesRequest_TimeField) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{16, 0}
}

// TimeField picks the document timestamp the time range applies to.
type ListDocumentsRequest_TimeField int32

const (
	// Not set; same as TIME_FIELD_CREATED_AT.
	ListDocumentsRequest_TIME_FIELD_UNSPECIFIED ListDocumentsRequest_TimeField = 0
	// When the document was first retained.
	ListDocumentsRequest_TIME_FIELD_CREATED_AT ListDocumentsRequest_TimeField = 1
	// When the document was last upserted, retagged or reprocessed.
	ListDocumentsRequest_TIME_FIELD_UPDATED_AT ListDocumentsRequest_TimeField = 2
)

// Enum value maps for ListDocumentsRequest_TimeField.
var (
	ListDocumentsRequest_TimeField_name = map[int32]string{
		0: "TIME_FIELD_UNSPECIFIED",
		1: "TIME_FIELD_CREATED_AT",
		2: "TIME_FIELD_UPDATED_AT",
	}
	ListDocumentsRequest_TimeField_value = map[string]int32{
		"TIME_FIELD_UNSPECIFIED": 0,
		"TIME_FIELD_CREATED_AT":  1,
		"TIME_FIELD_UPDATED_AT":  2,
	}
)

func (x ListDocumentsRequest_TimeField) Enum() *ListDocumentsRequest_TimeField {
	p := new(ListDocumentsRequest_TimeField)
	*p = x
	return p
}

func (x ListDocumentsRequest_TimeField) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (ListDocumentsRequest_TimeField) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[12].Descriptor()
}

func (ListDocumentsRequest_TimeField) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[12]
}

func (x ListDocumentsRequest_TimeField) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use ListDocumentsRequest_TimeField.Descriptor instead.
func (ListDocumentsRequest_TimeField) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{28, 0}
}

// Kind separates content versions from refresh failures; each is capped separately
// (MentalModelConfig.history_max_entries).
type MentalModelHistoryEntry_Kind int32

const (
	// Not set; in ListMentalModelHistory it means both kinds.
	MentalModelHistoryEntry_KIND_UNSPECIFIED MentalModelHistoryEntry_Kind = 0
	// A previous content version.
	MentalModelHistoryEntry_KIND_VERSION MentalModelHistoryEntry_Kind = 1
	// A failed refresh.
	MentalModelHistoryEntry_KIND_REFRESH_FAILED MentalModelHistoryEntry_Kind = 2
)

// Enum value maps for MentalModelHistoryEntry_Kind.
var (
	MentalModelHistoryEntry_Kind_name = map[int32]string{
		0: "KIND_UNSPECIFIED",
		1: "KIND_VERSION",
		2: "KIND_REFRESH_FAILED",
	}
	MentalModelHistoryEntry_Kind_value = map[string]int32{
		"KIND_UNSPECIFIED":    0,
		"KIND_VERSION":        1,
		"KIND_REFRESH_FAILED": 2,
	}
)

func (x MentalModelHistoryEntry_Kind) Enum() *MentalModelHistoryEntry_Kind {
	p := new(MentalModelHistoryEntry_Kind)
	*p = x
	return p
}

func (x MentalModelHistoryEntry_Kind) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (MentalModelHistoryEntry_Kind) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[13].Descriptor()
}

func (MentalModelHistoryEntry_Kind) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[13]
}

func (x MentalModelHistoryEntry_Kind) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use MentalModelHistoryEntry_Kind.Descriptor instead.
func (MentalModelHistoryEntry_Kind) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{66, 0}
}

// Kind is the edit an op performs on the page's sections and blocks.
type DeltaOp_Kind int32

const (
	// Not set; rejected.
	DeltaOp_KIND_UNSPECIFIED DeltaOp_Kind = 0
	// Add a section with heading and blocks_md.
	DeltaOp_KIND_ADD_SECTION DeltaOp_Kind = 1
	// Remove section_id.
	DeltaOp_KIND_REMOVE_SECTION DeltaOp_Kind = 2
	// Rename section_id to heading.
	DeltaOp_KIND_RENAME_SECTION DeltaOp_Kind = 3
	// Replace every block of section_id with blocks_md.
	DeltaOp_KIND_REPLACE_SECTION_BLOCKS DeltaOp_Kind = 4
	// Append blocks_md to the end of section_id.
	DeltaOp_KIND_APPEND_BLOCK DeltaOp_Kind = 5
	// Insert blocks_md after after_block_id.
	DeltaOp_KIND_INSERT_BLOCK DeltaOp_Kind = 6
	// Replace block_id with blocks_md.
	DeltaOp_KIND_REPLACE_BLOCK DeltaOp_Kind = 7
	// Remove block_id.
	DeltaOp_KIND_REMOVE_BLOCK DeltaOp_Kind = 8
)

// Enum value maps for DeltaOp_Kind.
var (
	DeltaOp_Kind_name = map[int32]string{
		0: "KIND_UNSPECIFIED",
		1: "KIND_ADD_SECTION",
		2: "KIND_REMOVE_SECTION",
		3: "KIND_RENAME_SECTION",
		4: "KIND_REPLACE_SECTION_BLOCKS",
		5: "KIND_APPEND_BLOCK",
		6: "KIND_INSERT_BLOCK",
		7: "KIND_REPLACE_BLOCK",
		8: "KIND_REMOVE_BLOCK",
	}
	DeltaOp_Kind_value = map[string]int32{
		"KIND_UNSPECIFIED":            0,
		"KIND_ADD_SECTION":            1,
		"KIND_REMOVE_SECTION":         2,
		"KIND_RENAME_SECTION":         3,
		"KIND_REPLACE_SECTION_BLOCKS": 4,
		"KIND_APPEND_BLOCK":           5,
		"KIND_INSERT_BLOCK":           6,
		"KIND_REPLACE_BLOCK":          7,
		"KIND_REMOVE_BLOCK":           8,
	}
)

func (x DeltaOp_Kind) Enum() *DeltaOp_Kind {
	p := new(DeltaOp_Kind)
	*p = x
	return p
}

func (x DeltaOp_Kind) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (DeltaOp_Kind) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[14].Descriptor()
}

func (DeltaOp_Kind) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[14]
}

func (x DeltaOp_Kind) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use DeltaOp_Kind.Descriptor instead.
func (DeltaOp_Kind) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{79, 0}
}

// TagSource picks which objects ListTags counts tags on.
type ListTagsRequest_TagSource int32

const (
	// Not set; same as TAG_SOURCE_MEMORIES.
	ListTagsRequest_TAG_SOURCE_UNSPECIFIED ListTagsRequest_TagSource = 0
	// Tags of memories.
	ListTagsRequest_TAG_SOURCE_MEMORIES ListTagsRequest_TagSource = 1
	// Tags of mental models.
	ListTagsRequest_TAG_SOURCE_MENTAL_MODELS ListTagsRequest_TagSource = 2
	// Tags of documents.
	ListTagsRequest_TAG_SOURCE_DOCUMENTS ListTagsRequest_TagSource = 3
)

// Enum value maps for ListTagsRequest_TagSource.
var (
	ListTagsRequest_TagSource_name = map[int32]string{
		0: "TAG_SOURCE_UNSPECIFIED",
		1: "TAG_SOURCE_MEMORIES",
		2: "TAG_SOURCE_MENTAL_MODELS",
		3: "TAG_SOURCE_DOCUMENTS",
	}
	ListTagsRequest_TagSource_value = map[string]int32{
		"TAG_SOURCE_UNSPECIFIED":   0,
		"TAG_SOURCE_MEMORIES":      1,
		"TAG_SOURCE_MENTAL_MODELS": 2,
		"TAG_SOURCE_DOCUMENTS":     3,
	}
)

func (x ListTagsRequest_TagSource) Enum() *ListTagsRequest_TagSource {
	p := new(ListTagsRequest_TagSource)
	*p = x
	return p
}

func (x ListTagsRequest_TagSource) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (ListTagsRequest_TagSource) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[15].Descriptor()
}

func (ListTagsRequest_TagSource) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[15]
}

func (x ListTagsRequest_TagSource) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use ListTagsRequest_TagSource.Descriptor instead.
func (ListTagsRequest_TagSource) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{92, 0}
}

// PromptOperation picks which operation's prompt to preview.
type PreviewPromptRequest_PromptOperation int32

const (
	// Not set; rejected with INVALID_ARGUMENT.
	PreviewPromptRequest_PROMPT_OPERATION_UNSPECIFIED PreviewPromptRequest_PromptOperation = 0
	// Retain extraction.
	PreviewPromptRequest_PROMPT_OPERATION_RETAIN PreviewPromptRequest_PromptOperation = 1
	// Consolidation decisions.
	PreviewPromptRequest_PROMPT_OPERATION_CONSOLIDATION PreviewPromptRequest_PromptOperation = 2
	// Reflect system prompt.
	PreviewPromptRequest_PROMPT_OPERATION_REFLECT PreviewPromptRequest_PromptOperation = 3
	// Delta mental-model refresh.
	PreviewPromptRequest_PROMPT_OPERATION_MENTAL_MODEL_DELTA PreviewPromptRequest_PromptOperation = 4
	// Single-shot full mental-model synthesis.
	PreviewPromptRequest_PROMPT_OPERATION_SYNTHESIZE PreviewPromptRequest_PromptOperation = 5
)

// Enum value maps for PreviewPromptRequest_PromptOperation.
var (
	PreviewPromptRequest_PromptOperation_name = map[int32]string{
		0: "PROMPT_OPERATION_UNSPECIFIED",
		1: "PROMPT_OPERATION_RETAIN",
		2: "PROMPT_OPERATION_CONSOLIDATION",
		3: "PROMPT_OPERATION_REFLECT",
		4: "PROMPT_OPERATION_MENTAL_MODEL_DELTA",
		5: "PROMPT_OPERATION_SYNTHESIZE",
	}
	PreviewPromptRequest_PromptOperation_value = map[string]int32{
		"PROMPT_OPERATION_UNSPECIFIED":        0,
		"PROMPT_OPERATION_RETAIN":             1,
		"PROMPT_OPERATION_CONSOLIDATION":      2,
		"PROMPT_OPERATION_REFLECT":            3,
		"PROMPT_OPERATION_MENTAL_MODEL_DELTA": 4,
		"PROMPT_OPERATION_SYNTHESIZE":         5,
	}
)

func (x PreviewPromptRequest_PromptOperation) Enum() *PreviewPromptRequest_PromptOperation {
	p := new(PreviewPromptRequest_PromptOperation)
	*p = x
	return p
}

func (x PreviewPromptRequest_PromptOperation) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (PreviewPromptRequest_PromptOperation) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[16].Descriptor()
}

func (PreviewPromptRequest_PromptOperation) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[16]
}

func (x PreviewPromptRequest_PromptOperation) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use PreviewPromptRequest_PromptOperation.Descriptor instead.
func (PreviewPromptRequest_PromptOperation) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{110, 0}
}

// PromptRole is the chat role of a rendered message.
type PromptMessage_PromptRole int32

const (
	// Not set; never returned.
	PromptMessage_PROMPT_ROLE_UNSPECIFIED PromptMessage_PromptRole = 0
	// System message.
	PromptMessage_PROMPT_ROLE_SYSTEM PromptMessage_PromptRole = 1
	// User message.
	PromptMessage_PROMPT_ROLE_USER PromptMessage_PromptRole = 2
)

// Enum value maps for PromptMessage_PromptRole.
var (
	PromptMessage_PromptRole_name = map[int32]string{
		0: "PROMPT_ROLE_UNSPECIFIED",
		1: "PROMPT_ROLE_SYSTEM",
		2: "PROMPT_ROLE_USER",
	}
	PromptMessage_PromptRole_value = map[string]int32{
		"PROMPT_ROLE_UNSPECIFIED": 0,
		"PROMPT_ROLE_SYSTEM":      1,
		"PROMPT_ROLE_USER":        2,
	}
)

func (x PromptMessage_PromptRole) Enum() *PromptMessage_PromptRole {
	p := new(PromptMessage_PromptRole)
	*p = x
	return p
}

func (x PromptMessage_PromptRole) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (PromptMessage_PromptRole) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[17].Descriptor()
}

func (PromptMessage_PromptRole) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[17]
}

func (x PromptMessage_PromptRole) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use PromptMessage_PromptRole.Descriptor instead.
func (PromptMessage_PromptRole) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{113, 0}
}

// PromptSource says where a prompt block's text comes from.
type PromptBlock_PromptSource int32

const (
	// Not set; never returned.
	PromptBlock_PROMPT_SOURCE_UNSPECIFIED PromptBlock_PromptSource = 0
	// Built-in template text.
	PromptBlock_PROMPT_SOURCE_BUILTIN PromptBlock_PromptSource = 1
	// Text from the effective BankConfig (a mission, disposition, custom prompt, ...).
	PromptBlock_PROMPT_SOURCE_CONFIG PromptBlock_PromptSource = 2
)

// Enum value maps for PromptBlock_PromptSource.
var (
	PromptBlock_PromptSource_name = map[int32]string{
		0: "PROMPT_SOURCE_UNSPECIFIED",
		1: "PROMPT_SOURCE_BUILTIN",
		2: "PROMPT_SOURCE_CONFIG",
	}
	PromptBlock_PromptSource_value = map[string]int32{
		"PROMPT_SOURCE_UNSPECIFIED": 0,
		"PROMPT_SOURCE_BUILTIN":     1,
		"PROMPT_SOURCE_CONFIG":      2,
	}
)

func (x PromptBlock_PromptSource) Enum() *PromptBlock_PromptSource {
	p := new(PromptBlock_PromptSource)
	*p = x
	return p
}

func (x PromptBlock_PromptSource) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (PromptBlock_PromptSource) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[18].Descriptor()
}

func (PromptBlock_PromptSource) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[18]
}

func (x PromptBlock_PromptSource) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use PromptBlock_PromptSource.Descriptor instead.
func (PromptBlock_PromptSource) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{114, 0}
}

// Mode is how a memory's tags expand into observation scopes.
type ObservationScopeModes_Mode int32

const (
	// Not set; inherit the bank's default_observation_scope_modes.
	ObservationScopeModes_MODE_UNSPECIFIED ObservationScopeModes_Mode = 0
	// One scope: the memory's full tag set.
	ObservationScopeModes_MODE_COMBINED ObservationScopeModes_Mode = 1
	// The shared (untagged) scope only.
	ObservationScopeModes_MODE_SHARED ObservationScopeModes_Mode = 2
	// One scope per individual tag.
	ObservationScopeModes_MODE_PER_TAG ObservationScopeModes_Mode = 3
	// One scope per non-empty combination of the memory's tags, capped by max_combinations.
	ObservationScopeModes_MODE_ALL_COMBINATIONS ObservationScopeModes_Mode = 4
	// Exactly the scopes listed in custom.
	ObservationScopeModes_MODE_CUSTOM ObservationScopeModes_Mode = 5
)

// Enum value maps for ObservationScopeModes_Mode.
var (
	ObservationScopeModes_Mode_name = map[int32]string{
		0: "MODE_UNSPECIFIED",
		1: "MODE_COMBINED",
		2: "MODE_SHARED",
		3: "MODE_PER_TAG",
		4: "MODE_ALL_COMBINATIONS",
		5: "MODE_CUSTOM",
	}
	ObservationScopeModes_Mode_value = map[string]int32{
		"MODE_UNSPECIFIED":      0,
		"MODE_COMBINED":         1,
		"MODE_SHARED":           2,
		"MODE_PER_TAG":          3,
		"MODE_ALL_COMBINATIONS": 4,
		"MODE_CUSTOM":           5,
	}
)

func (x ObservationScopeModes_Mode) Enum() *ObservationScopeModes_Mode {
	p := new(ObservationScopeModes_Mode)
	*p = x
	return p
}

func (x ObservationScopeModes_Mode) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (ObservationScopeModes_Mode) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[19].Descriptor()
}

func (ObservationScopeModes_Mode) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[19]
}

func (x ObservationScopeModes_Mode) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use ObservationScopeModes_Mode.Descriptor instead.
func (ObservationScopeModes_Mode) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{139, 0}
}

// Resolve is how the leaf's tags map to the bank's stored tags.
type TagLeaf_Resolve int32

const (
	// Not set; same as RESOLVE_EXACT_NAMES.
	TagLeaf_RESOLVE_UNSPECIFIED TagLeaf_Resolve = 0
	// Tags are used verbatim.
	TagLeaf_RESOLVE_EXACT_NAMES TagLeaf_Resolve = 1
	// Each tag resolves against the bank's tags by trigram similarity (RecallConfig.fuzzy_tag_min_similarity)
	// before the filter compiles.
	TagLeaf_RESOLVE_FUZZY TagLeaf_Resolve = 2
)

// Enum value maps for TagLeaf_Resolve.
var (
	TagLeaf_Resolve_name = map[int32]string{
		0: "RESOLVE_UNSPECIFIED",
		1: "RESOLVE_EXACT_NAMES",
		2: "RESOLVE_FUZZY",
	}
	TagLeaf_Resolve_value = map[string]int32{
		"RESOLVE_UNSPECIFIED": 0,
		"RESOLVE_EXACT_NAMES": 1,
		"RESOLVE_FUZZY":       2,
	}
)

func (x TagLeaf_Resolve) Enum() *TagLeaf_Resolve {
	p := new(TagLeaf_Resolve)
	*p = x
	return p
}

func (x TagLeaf_Resolve) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (TagLeaf_Resolve) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[20].Descriptor()
}

func (TagLeaf_Resolve) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[20]
}

func (x TagLeaf_Resolve) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use TagLeaf_Resolve.Descriptor instead.
func (TagLeaf_Resolve) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{143, 0}
}

// ModelSlot names one model-backed step that can be routed to its own gateway model (see ModelBindings). Usage
// is recorded per slot.
type SlotUsage_ModelSlot int32

const (
	// Not set; never recorded in usage.
	SlotUsage_MODEL_SLOT_UNSPECIFIED SlotUsage_ModelSlot = 0
	// Retain fact, entity and label extraction. Overridable on Retain, ReprocessDocument, UpdateMemory and
	// RestoreMemory (re-deriving).
	SlotUsage_MODEL_SLOT_EXTRACT SlotUsage_ModelSlot = 1
	// Relative dates at retain, and query time windows at recall (only when a cue is found and the rules
	// fail). Overridable on Retain, Recall, ReprocessDocument, UpdateMemory and RestoreMemory.
	SlotUsage_MODEL_SLOT_TIME_PARSE SlotUsage_ModelSlot = 2
	// Create / update / none decisions per observation scope. Overridable on a manual Consolidate request
	// or the matching consolidation strategy, never by the retain that triggered the consolidation.
	SlotUsage_MODEL_SLOT_CONSOLIDATE SlotUsage_ModelSlot = 3
	// Merge-or-keep check for near-duplicate observations. Overridable like MODEL_SLOT_CONSOLIDATE.
	SlotUsage_MODEL_SLOT_DEDUP_CHECK SlotUsage_ModelSlot = 4
	// Tool-calling turns of the reflect loop and of agentic mental-model refresh. Overridable on Reflect and
	// RefreshMentalModel, or stored on the model (MentalModelSpec.overrides).
	SlotUsage_MODEL_SLOT_REFLECT SlotUsage_ModelSlot = 5
	// Second pass projecting prose into a JSON Schema (response_schema_json). Overridable on Reflect and
	// RefreshMentalModel, or stored on the model (MentalModelSpec.overrides).
	SlotUsage_MODEL_SLOT_STRUCTURED_OUTPUT SlotUsage_ModelSlot = 6
	// Single-shot mental-model refresh: the full page synthesis and the delta edit ops. Overridable on
	// RefreshMentalModel, or stored on the model (MentalModelSpec.overrides).
	SlotUsage_MODEL_SLOT_MENTAL_MODEL SlotUsage_ModelSlot = 7
	// Cross-encoder scores in recall. Overridable on Recall and Reflect.
	SlotUsage_MODEL_SLOT_RERANK SlotUsage_ModelSlot = 8
	// Embeddings of memories, observations, queries and mental models. Bound only in the server config file (one vector
	// space per database, 768 dimensions).
	SlotUsage_MODEL_SLOT_EMBED SlotUsage_ModelSlot = 9
)

// Enum value maps for SlotUsage_ModelSlot.
var (
	SlotUsage_ModelSlot_name = map[int32]string{
		0: "MODEL_SLOT_UNSPECIFIED",
		1: "MODEL_SLOT_EXTRACT",
		2: "MODEL_SLOT_TIME_PARSE",
		3: "MODEL_SLOT_CONSOLIDATE",
		4: "MODEL_SLOT_DEDUP_CHECK",
		5: "MODEL_SLOT_REFLECT",
		6: "MODEL_SLOT_STRUCTURED_OUTPUT",
		7: "MODEL_SLOT_MENTAL_MODEL",
		8: "MODEL_SLOT_RERANK",
		9: "MODEL_SLOT_EMBED",
	}
	SlotUsage_ModelSlot_value = map[string]int32{
		"MODEL_SLOT_UNSPECIFIED":       0,
		"MODEL_SLOT_EXTRACT":           1,
		"MODEL_SLOT_TIME_PARSE":        2,
		"MODEL_SLOT_CONSOLIDATE":       3,
		"MODEL_SLOT_DEDUP_CHECK":       4,
		"MODEL_SLOT_REFLECT":           5,
		"MODEL_SLOT_STRUCTURED_OUTPUT": 6,
		"MODEL_SLOT_MENTAL_MODEL":      7,
		"MODEL_SLOT_RERANK":            8,
		"MODEL_SLOT_EMBED":             9,
	}
)

func (x SlotUsage_ModelSlot) Enum() *SlotUsage_ModelSlot {
	p := new(SlotUsage_ModelSlot)
	*p = x
	return p
}

func (x SlotUsage_ModelSlot) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (SlotUsage_ModelSlot) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[21].Descriptor()
}

func (SlotUsage_ModelSlot) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[21]
}

func (x SlotUsage_ModelSlot) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use SlotUsage_ModelSlot.Descriptor instead.
func (SlotUsage_ModelSlot) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{147, 0}
}

// ExtractionMode picks how retain turns content into facts.
type RetainConfig_ExtractionMode int32

const (
	// Not set; inherit, ending at the server default.
	RetainConfig_EXTRACTION_MODE_UNSPECIFIED RetainConfig_ExtractionMode = 0
	// Short, deduplicated facts.
	RetainConfig_EXTRACTION_MODE_CONCISE RetainConfig_ExtractionMode = 1
	// Detailed facts keeping more of the source's wording and context.
	RetainConfig_EXTRACTION_MODE_VERBOSE RetainConfig_ExtractionMode = 2
	// Facts that quote the source verbatim.
	RetainConfig_EXTRACTION_MODE_VERBATIM RetainConfig_ExtractionMode = 3
	// No LLM call: chunks are stored as memories as they are.
	RetainConfig_EXTRACTION_MODE_CHUNKS RetainConfig_ExtractionMode = 4
	// Extraction driven by BankConfig.custom_extraction_prompt.
	RetainConfig_EXTRACTION_MODE_CUSTOM RetainConfig_ExtractionMode = 5
)

// Enum value maps for RetainConfig_ExtractionMode.
var (
	RetainConfig_ExtractionMode_name = map[int32]string{
		0: "EXTRACTION_MODE_UNSPECIFIED",
		1: "EXTRACTION_MODE_CONCISE",
		2: "EXTRACTION_MODE_VERBOSE",
		3: "EXTRACTION_MODE_VERBATIM",
		4: "EXTRACTION_MODE_CHUNKS",
		5: "EXTRACTION_MODE_CUSTOM",
	}
	RetainConfig_ExtractionMode_value = map[string]int32{
		"EXTRACTION_MODE_UNSPECIFIED": 0,
		"EXTRACTION_MODE_CONCISE":     1,
		"EXTRACTION_MODE_VERBOSE":     2,
		"EXTRACTION_MODE_VERBATIM":    3,
		"EXTRACTION_MODE_CHUNKS":      4,
		"EXTRACTION_MODE_CUSTOM":      5,
	}
)

func (x RetainConfig_ExtractionMode) Enum() *RetainConfig_ExtractionMode {
	p := new(RetainConfig_ExtractionMode)
	*p = x
	return p
}

func (x RetainConfig_ExtractionMode) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (RetainConfig_ExtractionMode) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[22].Descriptor()
}

func (RetainConfig_ExtractionMode) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[22]
}

func (x RetainConfig_ExtractionMode) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use RetainConfig_ExtractionMode.Descriptor instead.
func (RetainConfig_ExtractionMode) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{153, 0}
}

// Type is the shape of a label's value.
type EntityLabel_Type int32

const (
	// Not set; same as TYPE_VALUE.
	EntityLabel_TYPE_UNSPECIFIED EntityLabel_Type = 0
	// One value from the closed list in values.
	EntityLabel_TYPE_VALUE EntityLabel_Type = 1
	// Several values from the closed list in values.
	EntityLabel_TYPE_MULTI_VALUES EntityLabel_Type = 2
	// One free-text value.
	EntityLabel_TYPE_TEXT EntityLabel_Type = 3
	// Several free-text values.
	EntityLabel_TYPE_MULTI_TEXT EntityLabel_Type = 4
	// A nested object whose fields are labels themselves.
	EntityLabel_TYPE_MAP EntityLabel_Type = 5
)

// Enum value maps for EntityLabel_Type.
var (
	EntityLabel_Type_name = map[int32]string{
		0: "TYPE_UNSPECIFIED",
		1: "TYPE_VALUE",
		2: "TYPE_MULTI_VALUES",
		3: "TYPE_TEXT",
		4: "TYPE_MULTI_TEXT",
		5: "TYPE_MAP",
	}
	EntityLabel_Type_value = map[string]int32{
		"TYPE_UNSPECIFIED":  0,
		"TYPE_VALUE":        1,
		"TYPE_MULTI_VALUES": 2,
		"TYPE_TEXT":         3,
		"TYPE_MULTI_TEXT":   4,
		"TYPE_MAP":          5,
	}
)

func (x EntityLabel_Type) Enum() *EntityLabel_Type {
	p := new(EntityLabel_Type)
	*p = x
	return p
}

func (x EntityLabel_Type) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (EntityLabel_Type) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[23].Descriptor()
}

func (EntityLabel_Type) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[23]
}

func (x EntityLabel_Type) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use EntityLabel_Type.Descriptor instead.
func (EntityLabel_Type) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{156, 0}
}

// BudgetFunction picks how a recall Budget becomes the per-arm candidate cap.
type RecallConfig_BudgetFunction int32

const (
	// Not set; inherit, ending at the server default.
	RecallConfig_BUDGET_FUNCTION_UNSPECIFIED RecallConfig_BudgetFunction = 0
	// Use budget_low / budget_mid / budget_high as fixed candidate caps.
	RecallConfig_BUDGET_FUNCTION_FIXED RecallConfig_BudgetFunction = 1
	// Scale the cap with the request's max_tokens, clamped to [budget_min, budget_max].
	RecallConfig_BUDGET_FUNCTION_ADAPTIVE RecallConfig_BudgetFunction = 2
)

// Enum value maps for RecallConfig_BudgetFunction.
var (
	RecallConfig_BudgetFunction_name = map[int32]string{
		0: "BUDGET_FUNCTION_UNSPECIFIED",
		1: "BUDGET_FUNCTION_FIXED",
		2: "BUDGET_FUNCTION_ADAPTIVE",
	}
	RecallConfig_BudgetFunction_value = map[string]int32{
		"BUDGET_FUNCTION_UNSPECIFIED": 0,
		"BUDGET_FUNCTION_FIXED":       1,
		"BUDGET_FUNCTION_ADAPTIVE":    2,
	}
)

func (x RecallConfig_BudgetFunction) Enum() *RecallConfig_BudgetFunction {
	p := new(RecallConfig_BudgetFunction)
	*p = x
	return p
}

func (x RecallConfig_BudgetFunction) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (RecallConfig_BudgetFunction) Descriptor() protoreflect.EnumDescriptor {
	return file_memory_v1_memory_proto_enumTypes[24].Descriptor()
}

func (RecallConfig_BudgetFunction) Type() protoreflect.EnumType {
	return &file_memory_v1_memory_proto_enumTypes[24]
}

func (x RecallConfig_BudgetFunction) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use RecallConfig_BudgetFunction.Descriptor instead.
func (RecallConfig_BudgetFunction) EnumDescriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{159, 0}
}

// RetainRequest retains a batch of documents into one bank.
type RetainRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Target bank; created with tenant defaults if unknown.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Documents to retain.
	Items []*RetainItem `protobuf:"bytes,2,rep,name=items,proto3" json:"items,omitempty"`
	// Tags added to every item's memories.
	DocumentTags []string `protobuf:"bytes,3,rep,name=document_tags,json=documentTags,proto3" json:"document_tags,omitempty"`
	// Request-level overrides. Stored on each document (part of its no-op hash); never inherited by automatic
	// consolidation.
	Overrides *RetainOverrides `protobuf:"bytes,4,opt,name=overrides,proto3" json:"overrides,omitempty"`
	// Do not enqueue consolidation after this retain, so a manual Consolidate can run with its own settings.
	SkipConsolidation bool `protobuf:"varint,5,opt,name=skip_consolidation,json=skipConsolidation,proto3" json:"skip_consolidation,omitempty"`
	// false (default) = return after the memories commit; true = enqueue jobs and return their operations.
	RunAsync      bool `protobuf:"varint,6,opt,name=run_async,json=runAsync,proto3" json:"run_async,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RetainRequest) Reset() {
	*x = RetainRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RetainRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RetainRequest) ProtoMessage() {}

func (x *RetainRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RetainRequest.ProtoReflect.Descriptor instead.
func (*RetainRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{0}
}

func (x *RetainRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *RetainRequest) GetItems() []*RetainItem {
	if x != nil {
		return x.Items
	}
	return nil
}

func (x *RetainRequest) GetDocumentTags() []string {
	if x != nil {
		return x.DocumentTags
	}
	return nil
}

func (x *RetainRequest) GetOverrides() *RetainOverrides {
	if x != nil {
		return x.Overrides
	}
	return nil
}

func (x *RetainRequest) GetSkipConsolidation() bool {
	if x != nil {
		return x.SkipConsolidation
	}
	return false
}

func (x *RetainRequest) GetRunAsync() bool {
	if x != nil {
		return x.RunAsync
	}
	return false
}

// RetainResponse reports per-document outcomes.
type RetainResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// One entry per item. Sync: fully filled; async: document ids, unchanged flags and operations only.
	Documents []*RetainedDocument `protobuf:"bytes,1,rep,name=documents,proto3" json:"documents,omitempty"`
	// The retain_batch parent operation; set when run_async enqueued more than one document. Its status
	// aggregates the retain_document children.
	BatchOperation *Operation `protobuf:"bytes,2,opt,name=batch_operation,json=batchOperation,proto3" json:"batch_operation,omitempty"`
	// Token usage per slot and model of the LLM and embedding calls a synchronous retain made; empty when
	// run_async (the usage is then on the retain_document operations).
	Usage         []*SlotUsage `protobuf:"bytes,3,rep,name=usage,proto3" json:"usage,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RetainResponse) Reset() {
	*x = RetainResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RetainResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RetainResponse) ProtoMessage() {}

func (x *RetainResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RetainResponse.ProtoReflect.Descriptor instead.
func (*RetainResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{1}
}

func (x *RetainResponse) GetDocuments() []*RetainedDocument {
	if x != nil {
		return x.Documents
	}
	return nil
}

func (x *RetainResponse) GetBatchOperation() *Operation {
	if x != nil {
		return x.BatchOperation
	}
	return nil
}

func (x *RetainResponse) GetUsage() []*SlotUsage {
	if x != nil {
		return x.Usage
	}
	return nil
}

// RetainItem is one document to retain.
type RetainItem struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Content to extract memories from; the original text is kept in object storage.
	Content string `protobuf:"bytes,1,opt,name=content,proto3" json:"content,omitempty"`
	// Context for the extractor, for example who is speaking or where the content came from.
	Context string `protobuf:"bytes,2,opt,name=context,proto3" json:"context,omitempty"`
	// When the content was produced; omitted = now. Anchors relative dates.
	Timestamp *timestamppb.Timestamp `protobuf:"bytes,3,opt,name=timestamp,proto3" json:"timestamp,omitempty"`
	// Content that has no time; wins over timestamp.
	Timeless bool `protobuf:"varint,4,opt,name=timeless,proto3" json:"timeless,omitempty"`
	// Upsert key: an existing document with this id is replaced. Generated when empty.
	DocumentId string `protobuf:"bytes,5,opt,name=document_id,json=documentId,proto3" json:"document_id,omitempty"`
	// Tags of the document's memories, added to the request's document_tags.
	Tags []string `protobuf:"bytes,6,rep,name=tags,proto3" json:"tags,omitempty"`
	// Observation scopes for this item; unset = retain.default_observation_scope_modes of the config.
	ObservationScopeModes *ObservationScopeModes `protobuf:"bytes,7,opt,name=observation_scope_modes,json=observationScopeModes,proto3" json:"observation_scope_modes,omitempty"`
	// Metadata copied onto every memory of the document.
	Metadata map[string]string `protobuf:"bytes,8,rep,name=metadata,proto3" json:"metadata,omitempty" protobuf_key:"bytes,1,opt,name=key" protobuf_val:"bytes,2,opt,name=value"`
	// Entities the extractor should link.
	Entities []*EntityHint `protobuf:"bytes,9,rep,name=entities,proto3" json:"entities,omitempty"`
	// false = take entity names literally instead of resolving them to existing entities; unset = true.
	ResolveEntities *bool `protobuf:"varint,10,opt,name=resolve_entities,json=resolveEntities,proto3,oneof" json:"resolve_entities,omitempty"`
	// Item-level overrides, the most specific level (over the request's). Stored on the document and part of
	// its no-op hash, so ReprocessDocument replays them unless given new overrides.
	Overrides *RetainOverrides `protobuf:"bytes,11,opt,name=overrides,proto3" json:"overrides,omitempty"`
	// Re-extract even when the content hash and overrides hash are unchanged.
	ForceReextract bool `protobuf:"varint,12,opt,name=force_reextract,json=forceReextract,proto3" json:"force_reextract,omitempty"`
	unknownFields  protoimpl.UnknownFields
	sizeCache      protoimpl.SizeCache
}

func (x *RetainItem) Reset() {
	*x = RetainItem{}
	mi := &file_memory_v1_memory_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RetainItem) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RetainItem) ProtoMessage() {}

func (x *RetainItem) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RetainItem.ProtoReflect.Descriptor instead.
func (*RetainItem) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{2}
}

func (x *RetainItem) GetContent() string {
	if x != nil {
		return x.Content
	}
	return ""
}

func (x *RetainItem) GetContext() string {
	if x != nil {
		return x.Context
	}
	return ""
}

func (x *RetainItem) GetTimestamp() *timestamppb.Timestamp {
	if x != nil {
		return x.Timestamp
	}
	return nil
}

func (x *RetainItem) GetTimeless() bool {
	if x != nil {
		return x.Timeless
	}
	return false
}

func (x *RetainItem) GetDocumentId() string {
	if x != nil {
		return x.DocumentId
	}
	return ""
}

func (x *RetainItem) GetTags() []string {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *RetainItem) GetObservationScopeModes() *ObservationScopeModes {
	if x != nil {
		return x.ObservationScopeModes
	}
	return nil
}

func (x *RetainItem) GetMetadata() map[string]string {
	if x != nil {
		return x.Metadata
	}
	return nil
}

func (x *RetainItem) GetEntities() []*EntityHint {
	if x != nil {
		return x.Entities
	}
	return nil
}

func (x *RetainItem) GetResolveEntities() bool {
	if x != nil && x.ResolveEntities != nil {
		return *x.ResolveEntities
	}
	return false
}

func (x *RetainItem) GetOverrides() *RetainOverrides {
	if x != nil {
		return x.Overrides
	}
	return nil
}

func (x *RetainItem) GetForceReextract() bool {
	if x != nil {
		return x.ForceReextract
	}
	return false
}

// EntityHint names an entity the extractor should link, in addition to the ones it finds.
type EntityHint struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Entity name as it appears in the content.
	Text string `protobuf:"bytes,1,opt,name=text,proto3" json:"text,omitempty"`
	// Free-form entity type, for example person or project.
	Type          string `protobuf:"bytes,2,opt,name=type,proto3" json:"type,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *EntityHint) Reset() {
	*x = EntityHint{}
	mi := &file_memory_v1_memory_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *EntityHint) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*EntityHint) ProtoMessage() {}

func (x *EntityHint) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use EntityHint.ProtoReflect.Descriptor instead.
func (*EntityHint) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{3}
}

func (x *EntityHint) GetText() string {
	if x != nil {
		return x.Text
	}
	return ""
}

func (x *EntityHint) GetType() string {
	if x != nil {
		return x.Type
	}
	return ""
}

// RetainedDocument reports the outcome for one retained document.
type RetainedDocument struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Document id (given or generated).
	DocumentId string `protobuf:"bytes,1,opt,name=document_id,json=documentId,proto3" json:"document_id,omitempty"`
	// Memories created; 0 when unchanged or async.
	MemoriesCreated int32 `protobuf:"varint,2,opt,name=memories_created,json=memoriesCreated,proto3" json:"memories_created,omitempty"`
	// Same content and overrides hash as the last completed retain: nothing was re-extracted.
	Unchanged bool `protobuf:"varint,3,opt,name=unchanged,proto3" json:"unchanged,omitempty"`
	// The retain_document operation; set when run_async and the document changed.
	Operation     *Operation `protobuf:"bytes,4,opt,name=operation,proto3" json:"operation,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RetainedDocument) Reset() {
	*x = RetainedDocument{}
	mi := &file_memory_v1_memory_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RetainedDocument) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RetainedDocument) ProtoMessage() {}

func (x *RetainedDocument) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RetainedDocument.ProtoReflect.Descriptor instead.
func (*RetainedDocument) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{4}
}

func (x *RetainedDocument) GetDocumentId() string {
	if x != nil {
		return x.DocumentId
	}
	return ""
}

func (x *RetainedDocument) GetMemoriesCreated() int32 {
	if x != nil {
		return x.MemoriesCreated
	}
	return 0
}

func (x *RetainedDocument) GetUnchanged() bool {
	if x != nil {
		return x.Unchanged
	}
	return false
}

func (x *RetainedDocument) GetOperation() *Operation {
	if x != nil {
		return x.Operation
	}
	return nil
}

// RecallRequest retrieves memories for a query.
type RecallRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank to search; unknown bank = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Query text, at most 500 tokens.
	Query string `protobuf:"bytes,2,opt,name=query,proto3" json:"query,omitempty"`
	// Fact types to return; empty = all.
	FactTypes []FactType `protobuf:"varint,3,rep,packed,name=fact_types,json=factTypes,proto3,enum=memory.v1.FactType" json:"fact_types,omitempty"`
	// Tag filter on the memories' tags; unset = no filter. Flat tags or groups; tags.match unset =
	// TAGS_MATCH_ANY.
	Tags *TagFilter `protobuf:"bytes,4,opt,name=tags,proto3" json:"tags,omitempty"`
	// Size class: the per-arm candidate cap (budget_low / mid / high, default 100 / 300 / 1000); unset = MID.
	Budget Budget `protobuf:"varint,5,opt,name=budget,proto3,enum=memory.v1.Budget" json:"budget,omitempty"`
	// Token budget for packed results; 0 = the server's default packing budget (never derived from budget).
	MaxTokens int32 `protobuf:"varint,6,opt,name=max_tokens,json=maxTokens,proto3" json:"max_tokens,omitempty"`
	// Reference time for relative expressions and recency; unset = now.
	QueryTime *timestamppb.Timestamp `protobuf:"bytes,7,opt,name=query_time,json=queryTime,proto3" json:"query_time,omitempty"`
	// Explicit time window start; when set, the query is not parsed for a window.
	TimeWindowStart *timestamppb.Timestamp `protobuf:"bytes,8,opt,name=time_window_start,json=timeWindowStart,proto3" json:"time_window_start,omitempty"`
	// Explicit time window end.
	TimeWindowEnd *timestamppb.Timestamp `protobuf:"bytes,9,opt,name=time_window_end,json=timeWindowEnd,proto3" json:"time_window_end,omitempty"`
	// Prefer observations: drop raw facts covered by a returned observation and backfill the budget.
	PreferObservations bool `protobuf:"varint,10,opt,name=prefer_observations,json=preferObservations,proto3" json:"prefer_observations,omitempty"`
	// Score floors.
	MinScores *MinScores `protobuf:"bytes,11,opt,name=min_scores,json=minScores,proto3" json:"min_scores,omitempty"`
	// Extra material to attach.
	Include *RecallInclude `protobuf:"bytes,12,opt,name=include,proto3" json:"include,omitempty"`
	// Return a RecallTrace.
	Trace bool `protobuf:"varint,13,opt,name=trace,proto3" json:"trace,omitempty"`
	// Overrides for this call only.
	Overrides *RecallOverrides `protobuf:"bytes,14,opt,name=overrides,proto3" json:"overrides,omitempty"`
	// Recall arms to run, by name (ListRecallArms), e.g. ["graph", "vector"] runs only those two; empty = the
	// effective RecallConfig.arms (all registered arms by default). An unknown or repeated name =
	// INVALID_ARGUMENT.
	Arms          []string `protobuf:"bytes,15,rep,name=arms,proto3" json:"arms,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RecallRequest) Reset() {
	*x = RecallRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[5]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RecallRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RecallRequest) ProtoMessage() {}

func (x *RecallRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[5]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RecallRequest.ProtoReflect.Descriptor instead.
func (*RecallRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{5}
}

func (x *RecallRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *RecallRequest) GetQuery() string {
	if x != nil {
		return x.Query
	}
	return ""
}

func (x *RecallRequest) GetFactTypes() []FactType {
	if x != nil {
		return x.FactTypes
	}
	return nil
}

func (x *RecallRequest) GetTags() *TagFilter {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *RecallRequest) GetBudget() Budget {
	if x != nil {
		return x.Budget
	}
	return Budget_BUDGET_UNSPECIFIED
}

func (x *RecallRequest) GetMaxTokens() int32 {
	if x != nil {
		return x.MaxTokens
	}
	return 0
}

func (x *RecallRequest) GetQueryTime() *timestamppb.Timestamp {
	if x != nil {
		return x.QueryTime
	}
	return nil
}

func (x *RecallRequest) GetTimeWindowStart() *timestamppb.Timestamp {
	if x != nil {
		return x.TimeWindowStart
	}
	return nil
}

func (x *RecallRequest) GetTimeWindowEnd() *timestamppb.Timestamp {
	if x != nil {
		return x.TimeWindowEnd
	}
	return nil
}

func (x *RecallRequest) GetPreferObservations() bool {
	if x != nil {
		return x.PreferObservations
	}
	return false
}

func (x *RecallRequest) GetMinScores() *MinScores {
	if x != nil {
		return x.MinScores
	}
	return nil
}

func (x *RecallRequest) GetInclude() *RecallInclude {
	if x != nil {
		return x.Include
	}
	return nil
}

func (x *RecallRequest) GetTrace() bool {
	if x != nil {
		return x.Trace
	}
	return false
}

func (x *RecallRequest) GetOverrides() *RecallOverrides {
	if x != nil {
		return x.Overrides
	}
	return nil
}

func (x *RecallRequest) GetArms() []string {
	if x != nil {
		return x.Arms
	}
	return nil
}

// RecallResponse carries ranked memories and any requested attachments.
type RecallResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Memories, best first, packed to the token budget.
	Results []*Memory `protobuf:"bytes,1,rep,name=results,proto3" json:"results,omitempty"`
	// Source facts of returned observations by id; set when include.source_facts.
	SourceFacts map[string]*Memory `protobuf:"bytes,2,rep,name=source_facts,json=sourceFacts,proto3" json:"source_facts,omitempty" protobuf_key:"bytes,1,opt,name=key" protobuf_val:"bytes,2,opt,name=value"`
	// True when source facts were cut to their token budget.
	SourceFactsTruncated bool `protobuf:"varint,3,opt,name=source_facts_truncated,json=sourceFactsTruncated,proto3" json:"source_facts_truncated,omitempty"`
	// Chunks by id; set when include.chunks.
	Chunks map[string]*Chunk `protobuf:"bytes,4,rep,name=chunks,proto3" json:"chunks,omitempty" protobuf_key:"bytes,1,opt,name=key" protobuf_val:"bytes,2,opt,name=value"`
	// Entity state by entity id; set when include.entities.
	Entities map[string]*EntityState `protobuf:"bytes,5,rep,name=entities,proto3" json:"entities,omitempty" protobuf_key:"bytes,1,opt,name=key" protobuf_val:"bytes,2,opt,name=value"`
	// Pipeline trace; set when trace was requested.
	Trace         *RecallTrace `protobuf:"bytes,6,opt,name=trace,proto3" json:"trace,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RecallResponse) Reset() {
	*x = RecallResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[6]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RecallResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RecallResponse) ProtoMessage() {}

func (x *RecallResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[6]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RecallResponse.ProtoReflect.Descriptor instead.
func (*RecallResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{6}
}

func (x *RecallResponse) GetResults() []*Memory {
	if x != nil {
		return x.Results
	}
	return nil
}

func (x *RecallResponse) GetSourceFacts() map[string]*Memory {
	if x != nil {
		return x.SourceFacts
	}
	return nil
}

func (x *RecallResponse) GetSourceFactsTruncated() bool {
	if x != nil {
		return x.SourceFactsTruncated
	}
	return false
}

func (x *RecallResponse) GetChunks() map[string]*Chunk {
	if x != nil {
		return x.Chunks
	}
	return nil
}

func (x *RecallResponse) GetEntities() map[string]*EntityState {
	if x != nil {
		return x.Entities
	}
	return nil
}

func (x *RecallResponse) GetTrace() *RecallTrace {
	if x != nil {
		return x.Trace
	}
	return nil
}

// RecallInclude asks Recall to attach extra material, each within its own token budget.
type RecallInclude struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Attach the chunks results were extracted from.
	Chunks *bool `protobuf:"varint,1,opt,name=chunks,proto3,oneof" json:"chunks,omitempty"`
	// Token budget for attached chunks.
	ChunksMaxTokens *int32 `protobuf:"varint,2,opt,name=chunks_max_tokens,json=chunksMaxTokens,proto3,oneof" json:"chunks_max_tokens,omitempty"`
	// Attach the source facts of returned observations.
	SourceFacts *bool `protobuf:"varint,3,opt,name=source_facts,json=sourceFacts,proto3,oneof" json:"source_facts,omitempty"`
	// Token budget for attached source facts.
	SourceFactsMaxTokens *int32 `protobuf:"varint,4,opt,name=source_facts_max_tokens,json=sourceFactsMaxTokens,proto3,oneof" json:"source_facts_max_tokens,omitempty"`
	// Attach entity state for entities in the results.
	Entities      *bool `protobuf:"varint,5,opt,name=entities,proto3,oneof" json:"entities,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RecallInclude) Reset() {
	*x = RecallInclude{}
	mi := &file_memory_v1_memory_proto_msgTypes[7]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RecallInclude) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RecallInclude) ProtoMessage() {}

func (x *RecallInclude) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[7]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RecallInclude.ProtoReflect.Descriptor instead.
func (*RecallInclude) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{7}
}

func (x *RecallInclude) GetChunks() bool {
	if x != nil && x.Chunks != nil {
		return *x.Chunks
	}
	return false
}

func (x *RecallInclude) GetChunksMaxTokens() int32 {
	if x != nil && x.ChunksMaxTokens != nil {
		return *x.ChunksMaxTokens
	}
	return 0
}

func (x *RecallInclude) GetSourceFacts() bool {
	if x != nil && x.SourceFacts != nil {
		return *x.SourceFacts
	}
	return false
}

func (x *RecallInclude) GetSourceFactsMaxTokens() int32 {
	if x != nil && x.SourceFactsMaxTokens != nil {
		return *x.SourceFactsMaxTokens
	}
	return 0
}

func (x *RecallInclude) GetEntities() bool {
	if x != nil && x.Entities != nil {
		return *x.Entities
	}
	return false
}

// EntityState summarizes an entity mentioned in recall results.
type EntityState struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Entity id.
	EntityId string `protobuf:"bytes,1,opt,name=entity_id,json=entityId,proto3" json:"entity_id,omitempty"`
	// Resolved canonical name.
	CanonicalName string `protobuf:"bytes,2,opt,name=canonical_name,json=canonicalName,proto3" json:"canonical_name,omitempty"`
	// Observations about the entity.
	ObservationIds []string `protobuf:"bytes,3,rep,name=observation_ids,json=observationIds,proto3" json:"observation_ids,omitempty"`
	unknownFields  protoimpl.UnknownFields
	sizeCache      protoimpl.SizeCache
}

func (x *EntityState) Reset() {
	*x = EntityState{}
	mi := &file_memory_v1_memory_proto_msgTypes[8]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *EntityState) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*EntityState) ProtoMessage() {}

func (x *EntityState) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[8]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use EntityState.ProtoReflect.Descriptor instead.
func (*EntityState) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{8}
}

func (x *EntityState) GetEntityId() string {
	if x != nil {
		return x.EntityId
	}
	return ""
}

func (x *EntityState) GetCanonicalName() string {
	if x != nil {
		return x.CanonicalName
	}
	return ""
}

func (x *EntityState) GetObservationIds() []string {
	if x != nil {
		return x.ObservationIds
	}
	return nil
}

// ReflectRequest asks the reflect agent a question.
type ReflectRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank to reason over; unknown bank = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// The question to answer in natural language.
	Query string `protobuf:"bytes,2,opt,name=query,proto3" json:"query,omitempty"`
	// Tag filter for retrieval and directive selection; unset = no filter (only untagged directives apply).
	// Flat tags or groups; tags.match unset = TAGS_MATCH_ANY.
	Tags *TagFilter `protobuf:"bytes,3,opt,name=tags,proto3" json:"tags,omitempty"`
	// Size class scaling iterations and recall budgets, default BUDGET_LOW.
	Budget Budget `protobuf:"varint,4,opt,name=budget,proto3,enum=memory.v1.Budget" json:"budget,omitempty"`
	// Token cap on the final answer; overrides ReflectConfig.max_answer_tokens.
	MaxTokens *int32 `protobuf:"varint,5,opt,name=max_tokens,json=maxTokens,proto3,oneof" json:"max_tokens,omitempty"`
	// JSON Schema for a structured second pass (STRUCTURED_OUTPUT slot); empty = prose only.
	ResponseSchemaJson string `protobuf:"bytes,6,opt,name=response_schema_json,json=responseSchemaJson,proto3" json:"response_schema_json,omitempty"`
	// Apply every active directive, not only those matching the request tags.
	ApplyAllDirectives bool `protobuf:"varint,7,opt,name=apply_all_directives,json=applyAllDirectives,proto3" json:"apply_all_directives,omitempty"`
	// Directives added for this call only; stored directives can never be removed by a request.
	ExtraDirectives []*Directive `protobuf:"bytes,8,rep,name=extra_directives,json=extraDirectives,proto3" json:"extra_directives,omitempty"`
	// What to return beside the answer.
	Include *ReflectInclude `protobuf:"bytes,9,opt,name=include,proto3" json:"include,omitempty"`
	// Reference time for relative expressions; unset = now.
	QueryTime *timestamppb.Timestamp `protobuf:"bytes,10,opt,name=query_time,json=queryTime,proto3" json:"query_time,omitempty"`
	// Overrides for this call only. Extra directives come through extra_directives.
	Overrides *ReflectOverrides `protobuf:"bytes,11,opt,name=overrides,proto3" json:"overrides,omitempty"`
	// Recall arms the agent's recall and search_observations tools run, by name (ListRecallArms); empty = the
	// effective RecallConfig.arms (all registered arms by default). An unknown or repeated name =
	// INVALID_ARGUMENT.
	Arms          []string `protobuf:"bytes,12,rep,name=arms,proto3" json:"arms,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ReflectRequest) Reset() {
	*x = ReflectRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[9]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ReflectRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ReflectRequest) ProtoMessage() {}

func (x *ReflectRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[9]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ReflectRequest.ProtoReflect.Descriptor instead.
func (*ReflectRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{9}
}

func (x *ReflectRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *ReflectRequest) GetQuery() string {
	if x != nil {
		return x.Query
	}
	return ""
}

func (x *ReflectRequest) GetTags() *TagFilter {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *ReflectRequest) GetBudget() Budget {
	if x != nil {
		return x.Budget
	}
	return Budget_BUDGET_UNSPECIFIED
}

func (x *ReflectRequest) GetMaxTokens() int32 {
	if x != nil && x.MaxTokens != nil {
		return *x.MaxTokens
	}
	return 0
}

func (x *ReflectRequest) GetResponseSchemaJson() string {
	if x != nil {
		return x.ResponseSchemaJson
	}
	return ""
}

func (x *ReflectRequest) GetApplyAllDirectives() bool {
	if x != nil {
		return x.ApplyAllDirectives
	}
	return false
}

func (x *ReflectRequest) GetExtraDirectives() []*Directive {
	if x != nil {
		return x.ExtraDirectives
	}
	return nil
}

func (x *ReflectRequest) GetInclude() *ReflectInclude {
	if x != nil {
		return x.Include
	}
	return nil
}

func (x *ReflectRequest) GetQueryTime() *timestamppb.Timestamp {
	if x != nil {
		return x.QueryTime
	}
	return nil
}

func (x *ReflectRequest) GetOverrides() *ReflectOverrides {
	if x != nil {
		return x.Overrides
	}
	return nil
}

func (x *ReflectRequest) GetArms() []string {
	if x != nil {
		return x.Arms
	}
	return nil
}

// ReflectResponse carries the answer and its provenance.
type ReflectResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The answer in prose; never empty on success.
	Text string `protobuf:"bytes,1,opt,name=text,proto3" json:"text,omitempty"`
	// Structured output matching response_schema_json; empty when not requested or when projection failed.
	StructuredOutputJson string `protobuf:"bytes,2,opt,name=structured_output_json,json=structuredOutputJson,proto3" json:"structured_output_json,omitempty"`
	// Why the structured pass failed; empty on success.
	StructuredOutputError string `protobuf:"bytes,3,opt,name=structured_output_error,json=structuredOutputError,proto3" json:"structured_output_error,omitempty"`
	// Evidence the answer used; set unless include.based_on is false.
	BasedOn *BasedOn `protobuf:"bytes,4,opt,name=based_on,json=basedOn,proto3" json:"based_on,omitempty"`
	// Loop trace; set when include.trace.
	Trace *ReflectTrace `protobuf:"bytes,5,opt,name=trace,proto3" json:"trace,omitempty"`
	// Token usage per slot and model.
	Usage         []*SlotUsage `protobuf:"bytes,6,rep,name=usage,proto3" json:"usage,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ReflectResponse) Reset() {
	*x = ReflectResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[10]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ReflectResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ReflectResponse) ProtoMessage() {}

func (x *ReflectResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[10]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ReflectResponse.ProtoReflect.Descriptor instead.
func (*ReflectResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{10}
}

func (x *ReflectResponse) GetText() string {
	if x != nil {
		return x.Text
	}
	return ""
}

func (x *ReflectResponse) GetStructuredOutputJson() string {
	if x != nil {
		return x.StructuredOutputJson
	}
	return ""
}

func (x *ReflectResponse) GetStructuredOutputError() string {
	if x != nil {
		return x.StructuredOutputError
	}
	return ""
}

func (x *ReflectResponse) GetBasedOn() *BasedOn {
	if x != nil {
		return x.BasedOn
	}
	return nil
}

func (x *ReflectResponse) GetTrace() *ReflectTrace {
	if x != nil {
		return x.Trace
	}
	return nil
}

func (x *ReflectResponse) GetUsage() []*SlotUsage {
	if x != nil {
		return x.Usage
	}
	return nil
}

// ReflectInclude picks what Reflect returns beside the answer.
type ReflectInclude struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Return based_on (the memories, models and directives used); unset = true.
	BasedOn *bool `protobuf:"varint,1,opt,name=based_on,json=basedOn,proto3,oneof" json:"based_on,omitempty"`
	// Return the trace with tool calls.
	Trace bool `protobuf:"varint,2,opt,name=trace,proto3" json:"trace,omitempty"`
	// Include tool outputs in the trace; unset = true when trace.
	ToolOutputs   *bool `protobuf:"varint,3,opt,name=tool_outputs,json=toolOutputs,proto3,oneof" json:"tool_outputs,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ReflectInclude) Reset() {
	*x = ReflectInclude{}
	mi := &file_memory_v1_memory_proto_msgTypes[11]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ReflectInclude) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ReflectInclude) ProtoMessage() {}

func (x *ReflectInclude) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[11]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ReflectInclude.ProtoReflect.Descriptor instead.
func (*ReflectInclude) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{11}
}

func (x *ReflectInclude) GetBasedOn() bool {
	if x != nil && x.BasedOn != nil {
		return *x.BasedOn
	}
	return false
}

func (x *ReflectInclude) GetTrace() bool {
	if x != nil {
		return x.Trace
	}
	return false
}

func (x *ReflectInclude) GetToolOutputs() bool {
	if x != nil && x.ToolOutputs != nil {
		return *x.ToolOutputs
	}
	return false
}

// BasedOn lists what a reflect answer cites; citations the agent never retrieved are dropped.
type BasedOn struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Memories cited.
	Memories []*Memory `protobuf:"bytes,1,rep,name=memories,proto3" json:"memories,omitempty"`
	// Mental models cited.
	MentalModelIds []string `protobuf:"bytes,2,rep,name=mental_model_ids,json=mentalModelIds,proto3" json:"mental_model_ids,omitempty"`
	// Directives applied.
	DirectiveIds  []string `protobuf:"bytes,3,rep,name=directive_ids,json=directiveIds,proto3" json:"directive_ids,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *BasedOn) Reset() {
	*x = BasedOn{}
	mi := &file_memory_v1_memory_proto_msgTypes[12]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *BasedOn) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*BasedOn) ProtoMessage() {}

func (x *BasedOn) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[12]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use BasedOn.ProtoReflect.Descriptor instead.
func (*BasedOn) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{12}
}

func (x *BasedOn) GetMemories() []*Memory {
	if x != nil {
		return x.Memories
	}
	return nil
}

func (x *BasedOn) GetMentalModelIds() []string {
	if x != nil {
		return x.MentalModelIds
	}
	return nil
}

func (x *BasedOn) GetDirectiveIds() []string {
	if x != nil {
		return x.DirectiveIds
	}
	return nil
}

// ListRecallArmsRequest takes no parameters: the arms are the server's, the same for every tenant and bank.
type ListRecallArmsRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListRecallArmsRequest) Reset() {
	*x = ListRecallArmsRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[13]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListRecallArmsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListRecallArmsRequest) ProtoMessage() {}

func (x *ListRecallArmsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[13]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListRecallArmsRequest.ProtoReflect.Descriptor instead.
func (*ListRecallArmsRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{13}
}

// ListRecallArmsResponse lists the registered recall arms.
type ListRecallArmsResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The registered arms, in fusion order.
	Arms          []*RecallArm `protobuf:"bytes,1,rep,name=arms,proto3" json:"arms,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListRecallArmsResponse) Reset() {
	*x = ListRecallArmsResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[14]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListRecallArmsResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListRecallArmsResponse) ProtoMessage() {}

func (x *ListRecallArmsResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[14]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListRecallArmsResponse.ProtoReflect.Descriptor instead.
func (*ListRecallArmsResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{14}
}

func (x *ListRecallArmsResponse) GetArms() []*RecallArm {
	if x != nil {
		return x.Arms
	}
	return nil
}

// RecallArm describes one registered recall arm.
type RecallArm struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Arm name, as RecallRequest.arms, RecallConfig.arms, RecallConfig.arm_boosts and MinScores.arms take it.
	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	// One-line description of what the arm retrieves.
	Description   string `protobuf:"bytes,2,opt,name=description,proto3" json:"description,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RecallArm) Reset() {
	*x = RecallArm{}
	mi := &file_memory_v1_memory_proto_msgTypes[15]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RecallArm) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RecallArm) ProtoMessage() {}

func (x *RecallArm) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[15]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RecallArm.ProtoReflect.Descriptor instead.
func (*RecallArm) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{15}
}

func (x *RecallArm) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *RecallArm) GetDescription() string {
	if x != nil {
		return x.Description
	}
	return ""
}

// ListMemoriesRequest filters and pages the bank's memories.
type ListMemoriesRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank to list; unknown bank = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Fact types; empty = all.
	FactTypes []FactType `protobuf:"varint,2,rep,packed,name=fact_types,json=factTypes,proto3,enum=memory.v1.FactType" json:"fact_types,omitempty"`
	// Case-insensitive substring the memory text must contain; empty = no filter.
	TextContains string `protobuf:"bytes,3,opt,name=text_contains,json=textContains,proto3" json:"text_contains,omitempty"`
	// Only memories of this document.
	DocumentId string `protobuf:"bytes,4,opt,name=document_id,json=documentId,proto3" json:"document_id,omitempty"`
	// Only live memories linked to this entity.
	EntityId string `protobuf:"bytes,5,opt,name=entity_id,json=entityId,proto3" json:"entity_id,omitempty"`
	// Live or invalidated memories; unspecified = no state filter.
	State MemoryState `protobuf:"varint,6,opt,name=state,proto3,enum=memory.v1.MemoryState" json:"state,omitempty"`
	// Inclusive start of the time range on time_field.
	Start *timestamppb.Timestamp `protobuf:"bytes,7,opt,name=start,proto3" json:"start,omitempty"`
	// Exclusive end of the time range on time_field.
	End *timestamppb.Timestamp `protobuf:"bytes,8,opt,name=end,proto3" json:"end,omitempty"`
	// Timestamp the range applies to.
	TimeField ListMemoriesRequest_TimeField `protobuf:"varint,9,opt,name=time_field,json=timeField,proto3,enum=memory.v1.ListMemoriesRequest_TimeField" json:"time_field,omitempty"`
	// Tag filter on the memories' tags; unset or empty tags = no filter. Flat form only; tags.match unset =
	// TAGS_MATCH_ANY.
	Tags *TagFilter `protobuf:"bytes,10,opt,name=tags,proto3" json:"tags,omitempty"`
	// Maximum memories per page; 0 = 100, at most 1000.
	PageSize int32 `protobuf:"varint,11,opt,name=page_size,json=pageSize,proto3" json:"page_size,omitempty"`
	// Token from a previous response; empty = first page.
	PageToken     string `protobuf:"bytes,12,opt,name=page_token,json=pageToken,proto3" json:"page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListMemoriesRequest) Reset() {
	*x = ListMemoriesRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[16]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListMemoriesRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListMemoriesRequest) ProtoMessage() {}

func (x *ListMemoriesRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[16]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListMemoriesRequest.ProtoReflect.Descriptor instead.
func (*ListMemoriesRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{16}
}

func (x *ListMemoriesRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *ListMemoriesRequest) GetFactTypes() []FactType {
	if x != nil {
		return x.FactTypes
	}
	return nil
}

func (x *ListMemoriesRequest) GetTextContains() string {
	if x != nil {
		return x.TextContains
	}
	return ""
}

func (x *ListMemoriesRequest) GetDocumentId() string {
	if x != nil {
		return x.DocumentId
	}
	return ""
}

func (x *ListMemoriesRequest) GetEntityId() string {
	if x != nil {
		return x.EntityId
	}
	return ""
}

func (x *ListMemoriesRequest) GetState() MemoryState {
	if x != nil {
		return x.State
	}
	return MemoryState_MEMORY_STATE_UNSPECIFIED
}

func (x *ListMemoriesRequest) GetStart() *timestamppb.Timestamp {
	if x != nil {
		return x.Start
	}
	return nil
}

func (x *ListMemoriesRequest) GetEnd() *timestamppb.Timestamp {
	if x != nil {
		return x.End
	}
	return nil
}

func (x *ListMemoriesRequest) GetTimeField() ListMemoriesRequest_TimeField {
	if x != nil {
		return x.TimeField
	}
	return ListMemoriesRequest_TIME_FIELD_UNSPECIFIED
}

func (x *ListMemoriesRequest) GetTags() *TagFilter {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *ListMemoriesRequest) GetPageSize() int32 {
	if x != nil {
		return x.PageSize
	}
	return 0
}

func (x *ListMemoriesRequest) GetPageToken() string {
	if x != nil {
		return x.PageToken
	}
	return ""
}

// ListMemoriesResponse is one page of memories.
type ListMemoriesResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Memories on this page, newest first.
	Memories []*Memory `protobuf:"bytes,1,rep,name=memories,proto3" json:"memories,omitempty"`
	// Token for the next page; empty = no more.
	NextPageToken string `protobuf:"bytes,2,opt,name=next_page_token,json=nextPageToken,proto3" json:"next_page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListMemoriesResponse) Reset() {
	*x = ListMemoriesResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[17]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListMemoriesResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListMemoriesResponse) ProtoMessage() {}

func (x *ListMemoriesResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[17]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListMemoriesResponse.ProtoReflect.Descriptor instead.
func (*ListMemoriesResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{17}
}

func (x *ListMemoriesResponse) GetMemories() []*Memory {
	if x != nil {
		return x.Memories
	}
	return nil
}

func (x *ListMemoriesResponse) GetNextPageToken() string {
	if x != nil {
		return x.NextPageToken
	}
	return ""
}

// GetMemoryRequest names one memory.
type GetMemoryRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the memory; unknown bank = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Memory id.
	MemoryId      string `protobuf:"bytes,2,opt,name=memory_id,json=memoryId,proto3" json:"memory_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetMemoryRequest) Reset() {
	*x = GetMemoryRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[18]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetMemoryRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetMemoryRequest) ProtoMessage() {}

func (x *GetMemoryRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[18]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetMemoryRequest.ProtoReflect.Descriptor instead.
func (*GetMemoryRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{18}
}

func (x *GetMemoryRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *GetMemoryRequest) GetMemoryId() string {
	if x != nil {
		return x.MemoryId
	}
	return ""
}

// GetMemoryResponse carries the memory.
type GetMemoryResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The memory, live or invalidated.
	Memory        *Memory `protobuf:"bytes,1,opt,name=memory,proto3" json:"memory,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetMemoryResponse) Reset() {
	*x = GetMemoryResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[19]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetMemoryResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetMemoryResponse) ProtoMessage() {}

func (x *GetMemoryResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[19]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetMemoryResponse.ProtoReflect.Descriptor instead.
func (*GetMemoryResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{19}
}

func (x *GetMemoryResponse) GetMemory() *Memory {
	if x != nil {
		return x.Memory
	}
	return nil
}

// UpdateMemoryRequest edits one live raw fact. The edit is recorded in the memory's history, clears the embedding until
// the rederive_memory job writes a new one, deletes the observations citing the fact and queues refreshes of
// the mental models that cite one of those observations.
type UpdateMemoryRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the memory.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Memory id; must be a live raw fact. An observation = INVALID_ARGUMENT (reason MEMORY_IS_OBSERVATION); an
	// invalidated or unknown memory = NOT_FOUND.
	MemoryId string `protobuf:"bytes,2,opt,name=memory_id,json=memoryId,proto3" json:"memory_id,omitempty"`
	// The new values; only the fields update_mask names are applied.
	Memory *MemoryEdit `protobuf:"bytes,3,opt,name=memory,proto3" json:"memory,omitempty"`
	// Fields of memory to apply: text, context, occurred_start, occurred_end, fact_type, entities. Empty = the
	// populated fields of memory. A named field left unset clears it, so clearing a field (the entities, or
	// both occurred bounds to make the fact timeless) needs it named here. Any other path, or nothing to
	// apply, = INVALID_ARGUMENT.
	UpdateMask *fieldmaskpb.FieldMask `protobuf:"bytes,4,opt,name=update_mask,json=updateMask,proto3" json:"update_mask,omitempty"`
	// false = take the new entity names literally instead of resolving them to existing entities; unset = true.
	ResolveEntities *bool `protobuf:"varint,5,opt,name=resolve_entities,json=resolveEntities,proto3,oneof" json:"resolve_entities,omitempty"`
	// Why the change was made; recorded with the edit.
	Reason string `protobuf:"bytes,6,opt,name=reason,proto3" json:"reason,omitempty"`
	// Overrides for re-deriving; kept on the rederive operation.
	Overrides     *RederiveOverrides `protobuf:"bytes,7,opt,name=overrides,proto3" json:"overrides,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *UpdateMemoryRequest) Reset() {
	*x = UpdateMemoryRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[20]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *UpdateMemoryRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdateMemoryRequest) ProtoMessage() {}

func (x *UpdateMemoryRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[20]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdateMemoryRequest.ProtoReflect.Descriptor instead.
func (*UpdateMemoryRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{20}
}

func (x *UpdateMemoryRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *UpdateMemoryRequest) GetMemoryId() string {
	if x != nil {
		return x.MemoryId
	}
	return ""
}

func (x *UpdateMemoryRequest) GetMemory() *MemoryEdit {
	if x != nil {
		return x.Memory
	}
	return nil
}

func (x *UpdateMemoryRequest) GetUpdateMask() *fieldmaskpb.FieldMask {
	if x != nil {
		return x.UpdateMask
	}
	return nil
}

func (x *UpdateMemoryRequest) GetResolveEntities() bool {
	if x != nil && x.ResolveEntities != nil {
		return *x.ResolveEntities
	}
	return false
}

func (x *UpdateMemoryRequest) GetReason() string {
	if x != nil {
		return x.Reason
	}
	return ""
}

func (x *UpdateMemoryRequest) GetOverrides() *RederiveOverrides {
	if x != nil {
		return x.Overrides
	}
	return nil
}

// UpdateMemoryResponse carries the edited memory and its rederive operation.
type UpdateMemoryResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The memory after the edit.
	Memory *Memory `protobuf:"bytes,1,opt,name=memory,proto3" json:"memory,omitempty"`
	// The rederive_memory operation, which re-embeds and re-links the fact.
	Operation     *Operation `protobuf:"bytes,2,opt,name=operation,proto3" json:"operation,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *UpdateMemoryResponse) Reset() {
	*x = UpdateMemoryResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[21]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *UpdateMemoryResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdateMemoryResponse) ProtoMessage() {}

func (x *UpdateMemoryResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[21]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdateMemoryResponse.ProtoReflect.Descriptor instead.
func (*UpdateMemoryResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{21}
}

func (x *UpdateMemoryResponse) GetMemory() *Memory {
	if x != nil {
		return x.Memory
	}
	return nil
}

func (x *UpdateMemoryResponse) GetOperation() *Operation {
	if x != nil {
		return x.Operation
	}
	return nil
}

// MemoryEdit holds the curated fields of a raw fact that UpdateMemory can change.
type MemoryEdit struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Fact text; named but empty = INVALID_ARGUMENT.
	Text string `protobuf:"bytes,1,opt,name=text,proto3" json:"text,omitempty"`
	// Context of the fact; empty clears it.
	Context string `protobuf:"bytes,2,opt,name=context,proto3" json:"context,omitempty"`
	// When the fact started happening; unset clears it.
	OccurredStart *timestamppb.Timestamp `protobuf:"bytes,3,opt,name=occurred_start,json=occurredStart,proto3" json:"occurred_start,omitempty"`
	// When the fact stopped happening; unset clears it.
	OccurredEnd *timestamppb.Timestamp `protobuf:"bytes,4,opt,name=occurred_end,json=occurredEnd,proto3" json:"occurred_end,omitempty"`
	// FACT_TYPE_WORLD or FACT_TYPE_EXPERIENCE; any other value, unspecified included, = INVALID_ARGUMENT.
	FactType FactType `protobuf:"varint,5,opt,name=fact_type,json=factType,proto3,enum=memory.v1.FactType" json:"fact_type,omitempty"`
	// Entity names linked to the fact; empty removes every entity.
	Entities      []string `protobuf:"bytes,6,rep,name=entities,proto3" json:"entities,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *MemoryEdit) Reset() {
	*x = MemoryEdit{}
	mi := &file_memory_v1_memory_proto_msgTypes[22]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *MemoryEdit) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*MemoryEdit) ProtoMessage() {}

func (x *MemoryEdit) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[22]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use MemoryEdit.ProtoReflect.Descriptor instead.
func (*MemoryEdit) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{22}
}

func (x *MemoryEdit) GetText() string {
	if x != nil {
		return x.Text
	}
	return ""
}

func (x *MemoryEdit) GetContext() string {
	if x != nil {
		return x.Context
	}
	return ""
}

func (x *MemoryEdit) GetOccurredStart() *timestamppb.Timestamp {
	if x != nil {
		return x.OccurredStart
	}
	return nil
}

func (x *MemoryEdit) GetOccurredEnd() *timestamppb.Timestamp {
	if x != nil {
		return x.OccurredEnd
	}
	return nil
}

func (x *MemoryEdit) GetFactType() FactType {
	if x != nil {
		return x.FactType
	}
	return FactType_FACT_TYPE_UNSPECIFIED
}

func (x *MemoryEdit) GetEntities() []string {
	if x != nil {
		return x.Entities
	}
	return nil
}

// InvalidateMemoryRequest names the raw fact to archive. The call also deletes the observations citing the
// fact, queues consolidation and graph maintenance, and queues refreshes of the mental models that cite the
// fact or one of those observations.
type InvalidateMemoryRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the memory.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Memory id; must be a live raw fact. An observation = INVALID_ARGUMENT (reason MEMORY_IS_OBSERVATION); an
	// invalidated or unknown memory = NOT_FOUND.
	MemoryId string `protobuf:"bytes,2,opt,name=memory_id,json=memoryId,proto3" json:"memory_id,omitempty"`
	// Why the fact is invalidated; recorded with the change.
	Reason        string `protobuf:"bytes,3,opt,name=reason,proto3" json:"reason,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *InvalidateMemoryRequest) Reset() {
	*x = InvalidateMemoryRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[23]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *InvalidateMemoryRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*InvalidateMemoryRequest) ProtoMessage() {}

func (x *InvalidateMemoryRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[23]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use InvalidateMemoryRequest.ProtoReflect.Descriptor instead.
func (*InvalidateMemoryRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{23}
}

func (x *InvalidateMemoryRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *InvalidateMemoryRequest) GetMemoryId() string {
	if x != nil {
		return x.MemoryId
	}
	return ""
}

func (x *InvalidateMemoryRequest) GetReason() string {
	if x != nil {
		return x.Reason
	}
	return ""
}

// InvalidateMemoryResponse carries the invalidated memory.
type InvalidateMemoryResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The memory, now MEMORY_STATE_INVALIDATED.
	Memory        *Memory `protobuf:"bytes,1,opt,name=memory,proto3" json:"memory,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *InvalidateMemoryResponse) Reset() {
	*x = InvalidateMemoryResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[24]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *InvalidateMemoryResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*InvalidateMemoryResponse) ProtoMessage() {}

func (x *InvalidateMemoryResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[24]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use InvalidateMemoryResponse.ProtoReflect.Descriptor instead.
func (*InvalidateMemoryResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{24}
}

func (x *InvalidateMemoryResponse) GetMemory() *Memory {
	if x != nil {
		return x.Memory
	}
	return nil
}

// RestoreMemoryRequest names the invalidated raw fact to bring back. The restore re-attaches the entities the
// fact had when it was invalidated; its rederive_memory job re-embeds and re-links it.
type RestoreMemoryRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the memory.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Memory id. An unknown memory = NOT_FOUND; an observation = INVALID_ARGUMENT (reason
	// MEMORY_IS_OBSERVATION); a live fact is left unchanged.
	MemoryId string `protobuf:"bytes,2,opt,name=memory_id,json=memoryId,proto3" json:"memory_id,omitempty"`
	// Why the fact is restored; recorded with the change.
	Reason string `protobuf:"bytes,3,opt,name=reason,proto3" json:"reason,omitempty"`
	// false = re-attach the fact's entity names literally instead of resolving them to existing entities;
	// unset = true.
	ResolveEntities *bool `protobuf:"varint,4,opt,name=resolve_entities,json=resolveEntities,proto3,oneof" json:"resolve_entities,omitempty"`
	// Overrides for re-deriving; kept on the rederive operation.
	Overrides     *RederiveOverrides `protobuf:"bytes,5,opt,name=overrides,proto3" json:"overrides,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RestoreMemoryRequest) Reset() {
	*x = RestoreMemoryRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[25]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RestoreMemoryRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RestoreMemoryRequest) ProtoMessage() {}

func (x *RestoreMemoryRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[25]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RestoreMemoryRequest.ProtoReflect.Descriptor instead.
func (*RestoreMemoryRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{25}
}

func (x *RestoreMemoryRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *RestoreMemoryRequest) GetMemoryId() string {
	if x != nil {
		return x.MemoryId
	}
	return ""
}

func (x *RestoreMemoryRequest) GetReason() string {
	if x != nil {
		return x.Reason
	}
	return ""
}

func (x *RestoreMemoryRequest) GetResolveEntities() bool {
	if x != nil && x.ResolveEntities != nil {
		return *x.ResolveEntities
	}
	return false
}

func (x *RestoreMemoryRequest) GetOverrides() *RederiveOverrides {
	if x != nil {
		return x.Overrides
	}
	return nil
}

// RestoreMemoryResponse carries the restored memory and its rederive operation.
type RestoreMemoryResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The memory, now MEMORY_STATE_VALID.
	Memory *Memory `protobuf:"bytes,1,opt,name=memory,proto3" json:"memory,omitempty"`
	// The rederive_memory operation; unset when the memory was already live.
	Operation     *Operation `protobuf:"bytes,2,opt,name=operation,proto3" json:"operation,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RestoreMemoryResponse) Reset() {
	*x = RestoreMemoryResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[26]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RestoreMemoryResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RestoreMemoryResponse) ProtoMessage() {}

func (x *RestoreMemoryResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[26]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RestoreMemoryResponse.ProtoReflect.Descriptor instead.
func (*RestoreMemoryResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{26}
}

func (x *RestoreMemoryResponse) GetMemory() *Memory {
	if x != nil {
		return x.Memory
	}
	return nil
}

func (x *RestoreMemoryResponse) GetOperation() *Operation {
	if x != nil {
		return x.Operation
	}
	return nil
}

// Memory is the read model of one memory: a raw fact or an observation.
type Memory struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Memory id.
	Id string `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	// World, experience or observation.
	FactType FactType `protobuf:"varint,2,opt,name=fact_type,json=factType,proto3,enum=memory.v1.FactType" json:"fact_type,omitempty"`
	// The fact or observation text.
	Text string `protobuf:"bytes,3,opt,name=text,proto3" json:"text,omitempty"`
	// Context given at retain (or by curation).
	Context string `protobuf:"bytes,4,opt,name=context,proto3" json:"context,omitempty"`
	// Metadata copied from the retained item.
	Metadata map[string]string `protobuf:"bytes,5,rep,name=metadata,proto3" json:"metadata,omitempty" protobuf_key:"bytes,1,opt,name=key" protobuf_val:"bytes,2,opt,name=value"`
	// Tags of the memory.
	Tags []string `protobuf:"bytes,6,rep,name=tags,proto3" json:"tags,omitempty"`
	// Canonical names of the entities linked to the memory.
	Entities []string `protobuf:"bytes,7,rep,name=entities,proto3" json:"entities,omitempty"`
	// Start of when the fact happened; unset for timeless facts.
	OccurredStart *timestamppb.Timestamp `protobuf:"bytes,8,opt,name=occurred_start,json=occurredStart,proto3" json:"occurred_start,omitempty"`
	// End of when the fact happened; unset for timeless or instantaneous facts.
	OccurredEnd *timestamppb.Timestamp `protobuf:"bytes,9,opt,name=occurred_end,json=occurredEnd,proto3" json:"occurred_end,omitempty"`
	// When the fact was mentioned (the retained item's timestamp).
	MentionedAt *timestamppb.Timestamp `protobuf:"bytes,10,opt,name=mentioned_at,json=mentionedAt,proto3" json:"mentioned_at,omitempty"`
	// Document the memory was extracted from; empty for observations.
	DocumentId string `protobuf:"bytes,11,opt,name=document_id,json=documentId,proto3" json:"document_id,omitempty"`
	// Chunk the memory was extracted from; empty for observations.
	ChunkId string `protobuf:"bytes,12,opt,name=chunk_id,json=chunkId,proto3" json:"chunk_id,omitempty"`
	// Number of source facts supporting an observation; feeds the proof boost.
	ProofCount int32 `protobuf:"varint,13,opt,name=proof_count,json=proofCount,proto3" json:"proof_count,omitempty"`
	// Raw fact ids an observation was derived from; empty for raw facts.
	SourceFactIds []string `protobuf:"bytes,14,rep,name=source_fact_ids,json=sourceFactIds,proto3" json:"source_fact_ids,omitempty"`
	// Score breakdown; set only in recall results.
	Scores *Scores `protobuf:"bytes,15,opt,name=scores,proto3" json:"scores,omitempty"`
	// Live or invalidated.
	State MemoryState `protobuf:"varint,16,opt,name=state,proto3,enum=memory.v1.MemoryState" json:"state,omitempty"`
	// Time of the last curation edit; unset if never edited.
	EditedAt *timestamppb.Timestamp `protobuf:"bytes,17,opt,name=edited_at,json=editedAt,proto3" json:"edited_at,omitempty"`
	// True for an observation whose scope has unconsolidated facts, so it may be out of date.
	Stale         bool `protobuf:"varint,18,opt,name=stale,proto3" json:"stale,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Memory) Reset() {
	*x = Memory{}
	mi := &file_memory_v1_memory_proto_msgTypes[27]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Memory) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Memory) ProtoMessage() {}

func (x *Memory) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[27]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Memory.ProtoReflect.Descriptor instead.
func (*Memory) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{27}
}

func (x *Memory) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *Memory) GetFactType() FactType {
	if x != nil {
		return x.FactType
	}
	return FactType_FACT_TYPE_UNSPECIFIED
}

func (x *Memory) GetText() string {
	if x != nil {
		return x.Text
	}
	return ""
}

func (x *Memory) GetContext() string {
	if x != nil {
		return x.Context
	}
	return ""
}

func (x *Memory) GetMetadata() map[string]string {
	if x != nil {
		return x.Metadata
	}
	return nil
}

func (x *Memory) GetTags() []string {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *Memory) GetEntities() []string {
	if x != nil {
		return x.Entities
	}
	return nil
}

func (x *Memory) GetOccurredStart() *timestamppb.Timestamp {
	if x != nil {
		return x.OccurredStart
	}
	return nil
}

func (x *Memory) GetOccurredEnd() *timestamppb.Timestamp {
	if x != nil {
		return x.OccurredEnd
	}
	return nil
}

func (x *Memory) GetMentionedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.MentionedAt
	}
	return nil
}

func (x *Memory) GetDocumentId() string {
	if x != nil {
		return x.DocumentId
	}
	return ""
}

func (x *Memory) GetChunkId() string {
	if x != nil {
		return x.ChunkId
	}
	return ""
}

func (x *Memory) GetProofCount() int32 {
	if x != nil {
		return x.ProofCount
	}
	return 0
}

func (x *Memory) GetSourceFactIds() []string {
	if x != nil {
		return x.SourceFactIds
	}
	return nil
}

func (x *Memory) GetScores() *Scores {
	if x != nil {
		return x.Scores
	}
	return nil
}

func (x *Memory) GetState() MemoryState {
	if x != nil {
		return x.State
	}
	return MemoryState_MEMORY_STATE_UNSPECIFIED
}

func (x *Memory) GetEditedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.EditedAt
	}
	return nil
}

func (x *Memory) GetStale() bool {
	if x != nil {
		return x.Stale
	}
	return false
}

// ListDocumentsRequest filters and pages the bank's documents.
type ListDocumentsRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank to list; unknown bank = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Case-insensitive substring the document id must contain; empty = no filter.
	DocumentIdContains string `protobuf:"bytes,2,opt,name=document_id_contains,json=documentIdContains,proto3" json:"document_id_contains,omitempty"`
	// Tag filter on the documents' tags; unset or empty tags = no filter. Flat form only; tags.match unset =
	// TAGS_MATCH_ANY_STRICT.
	Tags *TagFilter `protobuf:"bytes,3,opt,name=tags,proto3" json:"tags,omitempty"`
	// Inclusive start of the time range on time_field.
	Start *timestamppb.Timestamp `protobuf:"bytes,4,opt,name=start,proto3" json:"start,omitempty"`
	// Exclusive end of the time range on time_field.
	End *timestamppb.Timestamp `protobuf:"bytes,5,opt,name=end,proto3" json:"end,omitempty"`
	// Timestamp the range applies to.
	TimeField ListDocumentsRequest_TimeField `protobuf:"varint,6,opt,name=time_field,json=timeField,proto3,enum=memory.v1.ListDocumentsRequest_TimeField" json:"time_field,omitempty"`
	// Maximum documents per page; 0 = 100, at most 1000.
	PageSize int32 `protobuf:"varint,7,opt,name=page_size,json=pageSize,proto3" json:"page_size,omitempty"`
	// Token from a previous response; empty = first page.
	PageToken     string `protobuf:"bytes,8,opt,name=page_token,json=pageToken,proto3" json:"page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListDocumentsRequest) Reset() {
	*x = ListDocumentsRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[28]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListDocumentsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListDocumentsRequest) ProtoMessage() {}

func (x *ListDocumentsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[28]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListDocumentsRequest.ProtoReflect.Descriptor instead.
func (*ListDocumentsRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{28}
}

func (x *ListDocumentsRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *ListDocumentsRequest) GetDocumentIdContains() string {
	if x != nil {
		return x.DocumentIdContains
	}
	return ""
}

func (x *ListDocumentsRequest) GetTags() *TagFilter {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *ListDocumentsRequest) GetStart() *timestamppb.Timestamp {
	if x != nil {
		return x.Start
	}
	return nil
}

func (x *ListDocumentsRequest) GetEnd() *timestamppb.Timestamp {
	if x != nil {
		return x.End
	}
	return nil
}

func (x *ListDocumentsRequest) GetTimeField() ListDocumentsRequest_TimeField {
	if x != nil {
		return x.TimeField
	}
	return ListDocumentsRequest_TIME_FIELD_UNSPECIFIED
}

func (x *ListDocumentsRequest) GetPageSize() int32 {
	if x != nil {
		return x.PageSize
	}
	return 0
}

func (x *ListDocumentsRequest) GetPageToken() string {
	if x != nil {
		return x.PageToken
	}
	return ""
}

// ListDocumentsResponse is one page of documents.
type ListDocumentsResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Documents on this page, without original text.
	Documents []*Document `protobuf:"bytes,1,rep,name=documents,proto3" json:"documents,omitempty"`
	// Token for the next page; empty = no more.
	NextPageToken string `protobuf:"bytes,2,opt,name=next_page_token,json=nextPageToken,proto3" json:"next_page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListDocumentsResponse) Reset() {
	*x = ListDocumentsResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[29]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListDocumentsResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListDocumentsResponse) ProtoMessage() {}

func (x *ListDocumentsResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[29]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListDocumentsResponse.ProtoReflect.Descriptor instead.
func (*ListDocumentsResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{29}
}

func (x *ListDocumentsResponse) GetDocuments() []*Document {
	if x != nil {
		return x.Documents
	}
	return nil
}

func (x *ListDocumentsResponse) GetNextPageToken() string {
	if x != nil {
		return x.NextPageToken
	}
	return ""
}

// GetDocumentRequest names one document.
type GetDocumentRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the document; unknown bank = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Document id.
	DocumentId string `protobuf:"bytes,2,opt,name=document_id,json=documentId,proto3" json:"document_id,omitempty"`
	// Read and return the original text from the blob store.
	IncludeText   bool `protobuf:"varint,3,opt,name=include_text,json=includeText,proto3" json:"include_text,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetDocumentRequest) Reset() {
	*x = GetDocumentRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[30]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetDocumentRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetDocumentRequest) ProtoMessage() {}

func (x *GetDocumentRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[30]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetDocumentRequest.ProtoReflect.Descriptor instead.
func (*GetDocumentRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{30}
}

func (x *GetDocumentRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *GetDocumentRequest) GetDocumentId() string {
	if x != nil {
		return x.DocumentId
	}
	return ""
}

func (x *GetDocumentRequest) GetIncludeText() bool {
	if x != nil {
		return x.IncludeText
	}
	return false
}

// GetDocumentResponse carries the document.
type GetDocumentResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The document; original_text is filled only when include_text was set.
	Document      *Document `protobuf:"bytes,1,opt,name=document,proto3" json:"document,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetDocumentResponse) Reset() {
	*x = GetDocumentResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[31]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetDocumentResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetDocumentResponse) ProtoMessage() {}

func (x *GetDocumentResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[31]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetDocumentResponse.ProtoReflect.Descriptor instead.
func (*GetDocumentResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{31}
}

func (x *GetDocumentResponse) GetDocument() *Document {
	if x != nil {
		return x.Document
	}
	return nil
}

// RetagDocumentRequest replaces a document's tag set.
type RetagDocumentRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the document.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Document id.
	DocumentId string `protobuf:"bytes,2,opt,name=document_id,json=documentId,proto3" json:"document_id,omitempty"`
	// The new tag set; replaces the old one entirely. Required: unset = INVALID_ARGUMENT; a set but empty
	// TagSet removes every document tag.
	Tags          *TagSet `protobuf:"bytes,3,opt,name=tags,proto3" json:"tags,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RetagDocumentRequest) Reset() {
	*x = RetagDocumentRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[32]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RetagDocumentRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RetagDocumentRequest) ProtoMessage() {}

func (x *RetagDocumentRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[32]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RetagDocumentRequest.ProtoReflect.Descriptor instead.
func (*RetagDocumentRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{32}
}

func (x *RetagDocumentRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *RetagDocumentRequest) GetDocumentId() string {
	if x != nil {
		return x.DocumentId
	}
	return ""
}

func (x *RetagDocumentRequest) GetTags() *TagSet {
	if x != nil {
		return x.Tags
	}
	return nil
}

// RetagDocumentResponse carries the retag job.
type RetagDocumentResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The retag_document operation, which clears the observations citing the document's memories and
	// re-consolidates them; unset when the retag was a no-op.
	Operation     *Operation `protobuf:"bytes,1,opt,name=operation,proto3" json:"operation,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RetagDocumentResponse) Reset() {
	*x = RetagDocumentResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[33]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RetagDocumentResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RetagDocumentResponse) ProtoMessage() {}

func (x *RetagDocumentResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[33]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RetagDocumentResponse.ProtoReflect.Descriptor instead.
func (*RetagDocumentResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{33}
}

func (x *RetagDocumentResponse) GetOperation() *Operation {
	if x != nil {
		return x.Operation
	}
	return nil
}

// ReprocessDocumentRequest re-extracts a document.
type ReprocessDocumentRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the document.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Document id.
	DocumentId string `protobuf:"bytes,2,opt,name=document_id,json=documentId,proto3" json:"document_id,omitempty"`
	// New retain overrides; they replace the document's stored overrides. Unset = replay the stored overrides.
	Overrides     *RetainOverrides `protobuf:"bytes,3,opt,name=overrides,proto3" json:"overrides,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ReprocessDocumentRequest) Reset() {
	*x = ReprocessDocumentRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[34]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ReprocessDocumentRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ReprocessDocumentRequest) ProtoMessage() {}

func (x *ReprocessDocumentRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[34]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ReprocessDocumentRequest.ProtoReflect.Descriptor instead.
func (*ReprocessDocumentRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{34}
}

func (x *ReprocessDocumentRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *ReprocessDocumentRequest) GetDocumentId() string {
	if x != nil {
		return x.DocumentId
	}
	return ""
}

func (x *ReprocessDocumentRequest) GetOverrides() *RetainOverrides {
	if x != nil {
		return x.Overrides
	}
	return nil
}

// ReprocessDocumentResponse carries the reprocess job.
type ReprocessDocumentResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The retain_document operation.
	Operation     *Operation `protobuf:"bytes,1,opt,name=operation,proto3" json:"operation,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ReprocessDocumentResponse) Reset() {
	*x = ReprocessDocumentResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[35]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ReprocessDocumentResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ReprocessDocumentResponse) ProtoMessage() {}

func (x *ReprocessDocumentResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[35]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ReprocessDocumentResponse.ProtoReflect.Descriptor instead.
func (*ReprocessDocumentResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{35}
}

func (x *ReprocessDocumentResponse) GetOperation() *Operation {
	if x != nil {
		return x.Operation
	}
	return nil
}

// Document is the read model of one retained document.
type Document struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Document id (the upsert key).
	Id string `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	// Hash of the original content; with the overrides hash, decides whether a retain is a no-op.
	ContentHash string `protobuf:"bytes,2,opt,name=content_hash,json=contentHash,proto3" json:"content_hash,omitempty"`
	// Document tags, applied to its memories.
	Tags []string `protobuf:"bytes,3,rep,name=tags,proto3" json:"tags,omitempty"`
	// Live memories extracted from the document (count).
	MemoryCount int64 `protobuf:"varint,4,opt,name=memory_count,json=memoryCount,proto3" json:"memory_count,omitempty"`
	// Live memory counts keyed by lowercase fact type: "world" and "experience" (a document has no
	// observations). A type with no memories is absent.
	MemoriesByFactType map[string]int64 `protobuf:"bytes,5,rep,name=memories_by_fact_type,json=memoriesByFactType,proto3" json:"memories_by_fact_type,omitempty" protobuf_key:"bytes,1,opt,name=key" protobuf_val:"varint,2,opt,name=value"`
	// Retain overrides stored with the document (the request's merged with the item's); ReprocessDocument
	// replays them.
	RetainOverrides *RetainOverrides `protobuf:"bytes,6,opt,name=retain_overrides,json=retainOverrides,proto3" json:"retain_overrides,omitempty"`
	// When the document was first retained.
	CreatedAt *timestamppb.Timestamp `protobuf:"bytes,7,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`
	// When the document last changed.
	UpdatedAt *timestamppb.Timestamp `protobuf:"bytes,8,opt,name=updated_at,json=updatedAt,proto3" json:"updated_at,omitempty"`
	// Original text read from the blob store; GetDocument with include_text only.
	OriginalText string `protobuf:"bytes,9,opt,name=original_text,json=originalText,proto3" json:"original_text,omitempty"`
	// When the last retain completed; unset while a retain is in flight or after it failed.
	ExtractedAt   *timestamppb.Timestamp `protobuf:"bytes,10,opt,name=extracted_at,json=extractedAt,proto3" json:"extracted_at,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Document) Reset() {
	*x = Document{}
	mi := &file_memory_v1_memory_proto_msgTypes[36]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Document) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Document) ProtoMessage() {}

func (x *Document) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[36]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Document.ProtoReflect.Descriptor instead.
func (*Document) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{36}
}

func (x *Document) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *Document) GetContentHash() string {
	if x != nil {
		return x.ContentHash
	}
	return ""
}

func (x *Document) GetTags() []string {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *Document) GetMemoryCount() int64 {
	if x != nil {
		return x.MemoryCount
	}
	return 0
}

func (x *Document) GetMemoriesByFactType() map[string]int64 {
	if x != nil {
		return x.MemoriesByFactType
	}
	return nil
}

func (x *Document) GetRetainOverrides() *RetainOverrides {
	if x != nil {
		return x.RetainOverrides
	}
	return nil
}

func (x *Document) GetCreatedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.CreatedAt
	}
	return nil
}

func (x *Document) GetUpdatedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.UpdatedAt
	}
	return nil
}

func (x *Document) GetOriginalText() string {
	if x != nil {
		return x.OriginalText
	}
	return ""
}

func (x *Document) GetExtractedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.ExtractedAt
	}
	return nil
}

// ConsolidateRequest asks for a manual consolidation run.
type ConsolidateRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank to consolidate.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Scopes to consolidate; empty = every scope.
	Scopes []*TagSet `protobuf:"bytes,2,rep,name=scopes,proto3" json:"scopes,omitempty"`
	// Overrides for this run only; kept on the operation.
	Overrides     *ConsolidateOverrides `protobuf:"bytes,3,opt,name=overrides,proto3" json:"overrides,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ConsolidateRequest) Reset() {
	*x = ConsolidateRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[37]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ConsolidateRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ConsolidateRequest) ProtoMessage() {}

func (x *ConsolidateRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[37]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ConsolidateRequest.ProtoReflect.Descriptor instead.
func (*ConsolidateRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{37}
}

func (x *ConsolidateRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *ConsolidateRequest) GetScopes() []*TagSet {
	if x != nil {
		return x.Scopes
	}
	return nil
}

func (x *ConsolidateRequest) GetOverrides() *ConsolidateOverrides {
	if x != nil {
		return x.Overrides
	}
	return nil
}

// ConsolidateResponse carries the consolidation job.
type ConsolidateResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The consolidate operation.
	Operation     *Operation `protobuf:"bytes,1,opt,name=operation,proto3" json:"operation,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ConsolidateResponse) Reset() {
	*x = ConsolidateResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[38]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ConsolidateResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ConsolidateResponse) ProtoMessage() {}

func (x *ConsolidateResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[38]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ConsolidateResponse.ProtoReflect.Descriptor instead.
func (*ConsolidateResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{38}
}

func (x *ConsolidateResponse) GetOperation() *Operation {
	if x != nil {
		return x.Operation
	}
	return nil
}

// ListObservationScopesRequest pages the bank's observation scopes.
type ListObservationScopesRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank to list; unknown bank = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Maximum scopes per page; 0 = 100, at most 1000.
	PageSize int32 `protobuf:"varint,2,opt,name=page_size,json=pageSize,proto3" json:"page_size,omitempty"`
	// Token from a previous response; empty = first page.
	PageToken     string `protobuf:"bytes,3,opt,name=page_token,json=pageToken,proto3" json:"page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListObservationScopesRequest) Reset() {
	*x = ListObservationScopesRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[39]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListObservationScopesRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListObservationScopesRequest) ProtoMessage() {}

func (x *ListObservationScopesRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[39]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListObservationScopesRequest.ProtoReflect.Descriptor instead.
func (*ListObservationScopesRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{39}
}

func (x *ListObservationScopesRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *ListObservationScopesRequest) GetPageSize() int32 {
	if x != nil {
		return x.PageSize
	}
	return 0
}

func (x *ListObservationScopesRequest) GetPageToken() string {
	if x != nil {
		return x.PageToken
	}
	return ""
}

// ListObservationScopesResponse is one page of observation scopes, ordered by the scope's tags.
type ListObservationScopesResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Scopes on this page.
	Scopes []*ObservationScope `protobuf:"bytes,1,rep,name=scopes,proto3" json:"scopes,omitempty"`
	// Token for the next page; empty = no more.
	NextPageToken string `protobuf:"bytes,2,opt,name=next_page_token,json=nextPageToken,proto3" json:"next_page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListObservationScopesResponse) Reset() {
	*x = ListObservationScopesResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[40]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListObservationScopesResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListObservationScopesResponse) ProtoMessage() {}

func (x *ListObservationScopesResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[40]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListObservationScopesResponse.ProtoReflect.Descriptor instead.
func (*ListObservationScopesResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{40}
}

func (x *ListObservationScopesResponse) GetScopes() []*ObservationScope {
	if x != nil {
		return x.Scopes
	}
	return nil
}

func (x *ListObservationScopesResponse) GetNextPageToken() string {
	if x != nil {
		return x.NextPageToken
	}
	return ""
}

// ObservationScope describes one observation scope: a tag set that has held an observation.
type ObservationScope struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The scope's tag set; empty = the shared scope.
	Scope *TagSet `protobuf:"bytes,1,opt,name=scope,proto3" json:"scope,omitempty"`
	// Observations in the scope (count).
	Observations int64 `protobuf:"varint,2,opt,name=observations,proto3" json:"observations,omitempty"`
	// Memories in the scope not yet consolidated (count).
	UnconsolidatedMemories int64 `protobuf:"varint,3,opt,name=unconsolidated_memories,json=unconsolidatedMemories,proto3" json:"unconsolidated_memories,omitempty"`
	unknownFields          protoimpl.UnknownFields
	sizeCache              protoimpl.SizeCache
}

func (x *ObservationScope) Reset() {
	*x = ObservationScope{}
	mi := &file_memory_v1_memory_proto_msgTypes[41]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ObservationScope) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ObservationScope) ProtoMessage() {}

func (x *ObservationScope) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[41]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ObservationScope.ProtoReflect.Descriptor instead.
func (*ObservationScope) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{41}
}

func (x *ObservationScope) GetScope() *TagSet {
	if x != nil {
		return x.Scope
	}
	return nil
}

func (x *ObservationScope) GetObservations() int64 {
	if x != nil {
		return x.Observations
	}
	return 0
}

func (x *ObservationScope) GetUnconsolidatedMemories() int64 {
	if x != nil {
		return x.UnconsolidatedMemories
	}
	return 0
}

// ListObservationHistoryRequest pages one observation's versions.
type ListObservationHistoryRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the observation; unknown bank = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Observation id; an id with no history (unknown, or not an observation) = NOT_FOUND.
	ObservationId string `protobuf:"bytes,2,opt,name=observation_id,json=observationId,proto3" json:"observation_id,omitempty"`
	// Maximum versions per page; 0 = 100, at most 1000.
	PageSize int32 `protobuf:"varint,3,opt,name=page_size,json=pageSize,proto3" json:"page_size,omitempty"`
	// Token from a previous response; empty = first page.
	PageToken     string `protobuf:"bytes,4,opt,name=page_token,json=pageToken,proto3" json:"page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListObservationHistoryRequest) Reset() {
	*x = ListObservationHistoryRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[42]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListObservationHistoryRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListObservationHistoryRequest) ProtoMessage() {}

func (x *ListObservationHistoryRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[42]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListObservationHistoryRequest.ProtoReflect.Descriptor instead.
func (*ListObservationHistoryRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{42}
}

func (x *ListObservationHistoryRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *ListObservationHistoryRequest) GetObservationId() string {
	if x != nil {
		return x.ObservationId
	}
	return ""
}

func (x *ListObservationHistoryRequest) GetPageSize() int32 {
	if x != nil {
		return x.PageSize
	}
	return 0
}

func (x *ListObservationHistoryRequest) GetPageToken() string {
	if x != nil {
		return x.PageToken
	}
	return ""
}

// ListObservationHistoryResponse is one page of an observation's versions.
type ListObservationHistoryResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Versions on this page, newest first.
	Versions []*ObservationVersion `protobuf:"bytes,1,rep,name=versions,proto3" json:"versions,omitempty"`
	// Token for the next page; empty = no more.
	NextPageToken string `protobuf:"bytes,2,opt,name=next_page_token,json=nextPageToken,proto3" json:"next_page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListObservationHistoryResponse) Reset() {
	*x = ListObservationHistoryResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[43]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListObservationHistoryResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListObservationHistoryResponse) ProtoMessage() {}

func (x *ListObservationHistoryResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[43]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListObservationHistoryResponse.ProtoReflect.Descriptor instead.
func (*ListObservationHistoryResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{43}
}

func (x *ListObservationHistoryResponse) GetVersions() []*ObservationVersion {
	if x != nil {
		return x.Versions
	}
	return nil
}

func (x *ListObservationHistoryResponse) GetNextPageToken() string {
	if x != nil {
		return x.NextPageToken
	}
	return ""
}

// ObservationVersion is one version of an observation, written on every create and update.
type ObservationVersion struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Version number, increasing per observation.
	Version int64 `protobuf:"varint,1,opt,name=version,proto3" json:"version,omitempty"`
	// Observation text at this version.
	Text string `protobuf:"bytes,2,opt,name=text,proto3" json:"text,omitempty"`
	// Ids of the raw facts the observation cited at this version.
	SourceFactIds []string `protobuf:"bytes,3,rep,name=source_fact_ids,json=sourceFactIds,proto3" json:"source_fact_ids,omitempty"`
	// When the version was written.
	CreatedAt     *timestamppb.Timestamp `protobuf:"bytes,4,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ObservationVersion) Reset() {
	*x = ObservationVersion{}
	mi := &file_memory_v1_memory_proto_msgTypes[44]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ObservationVersion) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ObservationVersion) ProtoMessage() {}

func (x *ObservationVersion) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[44]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ObservationVersion.ProtoReflect.Descriptor instead.
func (*ObservationVersion) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{44}
}

func (x *ObservationVersion) GetVersion() int64 {
	if x != nil {
		return x.Version
	}
	return 0
}

func (x *ObservationVersion) GetText() string {
	if x != nil {
		return x.Text
	}
	return ""
}

func (x *ObservationVersion) GetSourceFactIds() []string {
	if x != nil {
		return x.SourceFactIds
	}
	return nil
}

func (x *ObservationVersion) GetCreatedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.CreatedAt
	}
	return nil
}

// ClearObservationsRequest selects the observations to delete: exactly one target.
type ClearObservationsRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank to clear.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// What to clear. Required: no target, an empty scope list or all = false is INVALID_ARGUMENT.
	//
	// Types that are valid to be assigned to Target:
	//
	//	*ClearObservationsRequest_Scopes
	//	*ClearObservationsRequest_MemoryId
	//	*ClearObservationsRequest_All
	Target isClearObservationsRequest_Target `protobuf_oneof:"target"`
	// Enqueue a consolidation of what was cleared afterwards: the same scopes, or every scope for memory_id
	// and all.
	Reconsolidate bool `protobuf:"varint,5,opt,name=reconsolidate,proto3" json:"reconsolidate,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ClearObservationsRequest) Reset() {
	*x = ClearObservationsRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[45]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ClearObservationsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ClearObservationsRequest) ProtoMessage() {}

func (x *ClearObservationsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[45]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ClearObservationsRequest.ProtoReflect.Descriptor instead.
func (*ClearObservationsRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{45}
}

func (x *ClearObservationsRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *ClearObservationsRequest) GetTarget() isClearObservationsRequest_Target {
	if x != nil {
		return x.Target
	}
	return nil
}

func (x *ClearObservationsRequest) GetScopes() *TagSetList {
	if x != nil {
		if x, ok := x.Target.(*ClearObservationsRequest_Scopes); ok {
			return x.Scopes
		}
	}
	return nil
}

func (x *ClearObservationsRequest) GetMemoryId() string {
	if x != nil {
		if x, ok := x.Target.(*ClearObservationsRequest_MemoryId); ok {
			return x.MemoryId
		}
	}
	return ""
}

func (x *ClearObservationsRequest) GetAll() bool {
	if x != nil {
		if x, ok := x.Target.(*ClearObservationsRequest_All); ok {
			return x.All
		}
	}
	return false
}

func (x *ClearObservationsRequest) GetReconsolidate() bool {
	if x != nil {
		return x.Reconsolidate
	}
	return false
}

type isClearObservationsRequest_Target interface {
	isClearObservationsRequest_Target()
}

type ClearObservationsRequest_Scopes struct {
	// Every observation of exactly these scopes (tag sets, compared as sets); other scopes, supersets and
	// subsets included, are kept.
	Scopes *TagSetList `protobuf:"bytes,2,opt,name=scopes,proto3,oneof"`
}

type ClearObservationsRequest_MemoryId struct {
	// Every observation citing this memory (unknown = NOT_FOUND); a raw fact then consolidates again.
	MemoryId string `protobuf:"bytes,3,opt,name=memory_id,json=memoryId,proto3,oneof"`
}

type ClearObservationsRequest_All struct {
	// true clears every observation of the bank.
	All bool `protobuf:"varint,4,opt,name=all,proto3,oneof"`
}

func (*ClearObservationsRequest_Scopes) isClearObservationsRequest_Target() {}

func (*ClearObservationsRequest_MemoryId) isClearObservationsRequest_Target() {}

func (*ClearObservationsRequest_All) isClearObservationsRequest_Target() {}

// TagSetList is a list of tag sets, so that a oneof can carry several scopes.
type TagSetList struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The tag sets; at least one.
	TagSets       []*TagSet `protobuf:"bytes,1,rep,name=tag_sets,json=tagSets,proto3" json:"tag_sets,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *TagSetList) Reset() {
	*x = TagSetList{}
	mi := &file_memory_v1_memory_proto_msgTypes[46]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *TagSetList) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TagSetList) ProtoMessage() {}

func (x *TagSetList) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[46]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TagSetList.ProtoReflect.Descriptor instead.
func (*TagSetList) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{46}
}

func (x *TagSetList) GetTagSets() []*TagSet {
	if x != nil {
		return x.TagSets
	}
	return nil
}

// ClearObservationsResponse reports what was cleared.
type ClearObservationsResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Observations deleted.
	Deleted int64 `protobuf:"varint,1,opt,name=deleted,proto3" json:"deleted,omitempty"`
	// The consolidate operation; set only when reconsolidate was requested.
	Operation     *Operation `protobuf:"bytes,2,opt,name=operation,proto3" json:"operation,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ClearObservationsResponse) Reset() {
	*x = ClearObservationsResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[47]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ClearObservationsResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ClearObservationsResponse) ProtoMessage() {}

func (x *ClearObservationsResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[47]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ClearObservationsResponse.ProtoReflect.Descriptor instead.
func (*ClearObservationsResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{47}
}

func (x *ClearObservationsResponse) GetDeleted() int64 {
	if x != nil {
		return x.Deleted
	}
	return 0
}

func (x *ClearObservationsResponse) GetOperation() *Operation {
	if x != nil {
		return x.Operation
	}
	return nil
}

// PreviewConsolidationStrategiesRequest carries draft strategies to evaluate.
type PreviewConsolidationStrategiesRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank whose existing scopes are matched.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Draft strategies, in priority order (first match wins).
	Strategies    []*ConsolidationStrategy `protobuf:"bytes,2,rep,name=strategies,proto3" json:"strategies,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PreviewConsolidationStrategiesRequest) Reset() {
	*x = PreviewConsolidationStrategiesRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[48]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PreviewConsolidationStrategiesRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PreviewConsolidationStrategiesRequest) ProtoMessage() {}

func (x *PreviewConsolidationStrategiesRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[48]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PreviewConsolidationStrategiesRequest.ProtoReflect.Descriptor instead.
func (*PreviewConsolidationStrategiesRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{48}
}

func (x *PreviewConsolidationStrategiesRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *PreviewConsolidationStrategiesRequest) GetStrategies() []*ConsolidationStrategy {
	if x != nil {
		return x.Strategies
	}
	return nil
}

// PreviewConsolidationStrategiesResponse reports the matches of every draft strategy.
type PreviewConsolidationStrategiesResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// One entry per draft strategy, in request order.
	Matches []*StrategyMatch `protobuf:"bytes,1,rep,name=matches,proto3" json:"matches,omitempty"`
	// False when the preview stopped before checking every scope of the bank.
	Complete      bool `protobuf:"varint,2,opt,name=complete,proto3" json:"complete,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PreviewConsolidationStrategiesResponse) Reset() {
	*x = PreviewConsolidationStrategiesResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[49]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PreviewConsolidationStrategiesResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PreviewConsolidationStrategiesResponse) ProtoMessage() {}

func (x *PreviewConsolidationStrategiesResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[49]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PreviewConsolidationStrategiesResponse.ProtoReflect.Descriptor instead.
func (*PreviewConsolidationStrategiesResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{49}
}

func (x *PreviewConsolidationStrategiesResponse) GetMatches() []*StrategyMatch {
	if x != nil {
		return x.Matches
	}
	return nil
}

func (x *PreviewConsolidationStrategiesResponse) GetComplete() bool {
	if x != nil {
		return x.Complete
	}
	return false
}

// StrategyMatch reports which scopes one draft strategy would take.
type StrategyMatch struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Strategy name.
	Strategy string `protobuf:"bytes,1,opt,name=strategy,proto3" json:"strategy,omitempty"`
	// Scopes the strategy would apply to.
	MatchedScopes []string `protobuf:"bytes,2,rep,name=matched_scopes,json=matchedScopes,proto3" json:"matched_scopes,omitempty"`
	// Scopes its patterns match but an earlier strategy takes first.
	ShadowedScopes []string `protobuf:"bytes,3,rep,name=shadowed_scopes,json=shadowedScopes,proto3" json:"shadowed_scopes,omitempty"`
	unknownFields  protoimpl.UnknownFields
	sizeCache      protoimpl.SizeCache
}

func (x *StrategyMatch) Reset() {
	*x = StrategyMatch{}
	mi := &file_memory_v1_memory_proto_msgTypes[50]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *StrategyMatch) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*StrategyMatch) ProtoMessage() {}

func (x *StrategyMatch) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[50]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use StrategyMatch.ProtoReflect.Descriptor instead.
func (*StrategyMatch) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{50}
}

func (x *StrategyMatch) GetStrategy() string {
	if x != nil {
		return x.Strategy
	}
	return ""
}

func (x *StrategyMatch) GetMatchedScopes() []string {
	if x != nil {
		return x.MatchedScopes
	}
	return nil
}

func (x *StrategyMatch) GetShadowedScopes() []string {
	if x != nil {
		return x.ShadowedScopes
	}
	return nil
}

// CreateMentalModelRequest creates a model.
type CreateMentalModelRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank to create it in.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// The model's spec; spec.id must be unique in the bank.
	Spec          *MentalModelSpec `protobuf:"bytes,2,opt,name=spec,proto3" json:"spec,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *CreateMentalModelRequest) Reset() {
	*x = CreateMentalModelRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[51]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CreateMentalModelRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CreateMentalModelRequest) ProtoMessage() {}

func (x *CreateMentalModelRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[51]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CreateMentalModelRequest.ProtoReflect.Descriptor instead.
func (*CreateMentalModelRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{51}
}

func (x *CreateMentalModelRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *CreateMentalModelRequest) GetSpec() *MentalModelSpec {
	if x != nil {
		return x.Spec
	}
	return nil
}

// CreateMentalModelResponse carries the new model and its first build.
type CreateMentalModelResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The model, with no content yet.
	MentalModel *MentalModel `protobuf:"bytes,1,opt,name=mental_model,json=mentalModel,proto3" json:"mental_model,omitempty"`
	// The refresh_mental_model operation building the first version.
	Operation     *Operation `protobuf:"bytes,2,opt,name=operation,proto3" json:"operation,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *CreateMentalModelResponse) Reset() {
	*x = CreateMentalModelResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[52]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CreateMentalModelResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CreateMentalModelResponse) ProtoMessage() {}

func (x *CreateMentalModelResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[52]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CreateMentalModelResponse.ProtoReflect.Descriptor instead.
func (*CreateMentalModelResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{52}
}

func (x *CreateMentalModelResponse) GetMentalModel() *MentalModel {
	if x != nil {
		return x.MentalModel
	}
	return nil
}

func (x *CreateMentalModelResponse) GetOperation() *Operation {
	if x != nil {
		return x.Operation
	}
	return nil
}

// GetMentalModelRequest names one model.
type GetMentalModelRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the model; unknown bank = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Model id.
	MentalModelId string `protobuf:"bytes,2,opt,name=mental_model_id,json=mentalModelId,proto3" json:"mental_model_id,omitempty"`
	// How much to return; unset = MENTAL_MODEL_VIEW_FULL.
	View          MentalModelView `protobuf:"varint,3,opt,name=view,proto3,enum=memory.v1.MentalModelView" json:"view,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetMentalModelRequest) Reset() {
	*x = GetMentalModelRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[53]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetMentalModelRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetMentalModelRequest) ProtoMessage() {}

func (x *GetMentalModelRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[53]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetMentalModelRequest.ProtoReflect.Descriptor instead.
func (*GetMentalModelRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{53}
}

func (x *GetMentalModelRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *GetMentalModelRequest) GetMentalModelId() string {
	if x != nil {
		return x.MentalModelId
	}
	return ""
}

func (x *GetMentalModelRequest) GetView() MentalModelView {
	if x != nil {
		return x.View
	}
	return MentalModelView_MENTAL_MODEL_VIEW_UNSPECIFIED
}

// GetMentalModelResponse carries the model.
type GetMentalModelResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The model in the requested view; content is the latest version.
	MentalModel   *MentalModel `protobuf:"bytes,1,opt,name=mental_model,json=mentalModel,proto3" json:"mental_model,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetMentalModelResponse) Reset() {
	*x = GetMentalModelResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[54]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetMentalModelResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetMentalModelResponse) ProtoMessage() {}

func (x *GetMentalModelResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[54]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetMentalModelResponse.ProtoReflect.Descriptor instead.
func (*GetMentalModelResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{54}
}

func (x *GetMentalModelResponse) GetMentalModel() *MentalModel {
	if x != nil {
		return x.MentalModel
	}
	return nil
}

// ListMentalModelsRequest filters and pages the bank's models.
type ListMentalModelsRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank to list; unknown bank = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Only models whose folder path starts with this; empty = all.
	FolderPrefix string `protobuf:"bytes,2,opt,name=folder_prefix,json=folderPrefix,proto3" json:"folder_prefix,omitempty"`
	// How much of each model to return; unset = MENTAL_MODEL_VIEW_BASIC.
	View MentalModelView `protobuf:"varint,3,opt,name=view,proto3,enum=memory.v1.MentalModelView" json:"view,omitempty"`
	// Tag filter on the models' own tags (MentalModelSpec.tags); unset or empty tags = no filter. Flat form
	// only; tags.match unset = TAGS_MATCH_ANY.
	Tags *TagFilter `protobuf:"bytes,4,opt,name=tags,proto3" json:"tags,omitempty"`
	// Maximum models per page; 0 = 100, at most 1000.
	PageSize int32 `protobuf:"varint,5,opt,name=page_size,json=pageSize,proto3" json:"page_size,omitempty"`
	// Token from a previous response; empty = first page.
	PageToken     string `protobuf:"bytes,6,opt,name=page_token,json=pageToken,proto3" json:"page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListMentalModelsRequest) Reset() {
	*x = ListMentalModelsRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[55]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListMentalModelsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListMentalModelsRequest) ProtoMessage() {}

func (x *ListMentalModelsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[55]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListMentalModelsRequest.ProtoReflect.Descriptor instead.
func (*ListMentalModelsRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{55}
}

func (x *ListMentalModelsRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *ListMentalModelsRequest) GetFolderPrefix() string {
	if x != nil {
		return x.FolderPrefix
	}
	return ""
}

func (x *ListMentalModelsRequest) GetView() MentalModelView {
	if x != nil {
		return x.View
	}
	return MentalModelView_MENTAL_MODEL_VIEW_UNSPECIFIED
}

func (x *ListMentalModelsRequest) GetTags() *TagFilter {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *ListMentalModelsRequest) GetPageSize() int32 {
	if x != nil {
		return x.PageSize
	}
	return 0
}

func (x *ListMentalModelsRequest) GetPageToken() string {
	if x != nil {
		return x.PageToken
	}
	return ""
}

// ListMentalModelsResponse is one page of models.
type ListMentalModelsResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Models on this page.
	MentalModels []*MentalModel `protobuf:"bytes,1,rep,name=mental_models,json=mentalModels,proto3" json:"mental_models,omitempty"`
	// Token for the next page; empty = no more.
	NextPageToken string `protobuf:"bytes,2,opt,name=next_page_token,json=nextPageToken,proto3" json:"next_page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListMentalModelsResponse) Reset() {
	*x = ListMentalModelsResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[56]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListMentalModelsResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListMentalModelsResponse) ProtoMessage() {}

func (x *ListMentalModelsResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[56]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListMentalModelsResponse.ProtoReflect.Descriptor instead.
func (*ListMentalModelsResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{56}
}

func (x *ListMentalModelsResponse) GetMentalModels() []*MentalModel {
	if x != nil {
		return x.MentalModels
	}
	return nil
}

func (x *ListMentalModelsResponse) GetNextPageToken() string {
	if x != nil {
		return x.NextPageToken
	}
	return ""
}

// UpdateMentalModelRequest patches a model's spec.
type UpdateMentalModelRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the model.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Model id.
	MentalModelId string `protobuf:"bytes,2,opt,name=mental_model_id,json=mentalModelId,proto3" json:"mental_model_id,omitempty"`
	// New values for the fields named in update_mask.
	Spec *MentalModelSpec `protobuf:"bytes,3,opt,name=spec,proto3" json:"spec,omitempty"`
	// MentalModelSpec paths to replace (for example schedule.cron, scope.fact_types, strategy or
	// overrides.reflect.mission); a path set to an empty value clears it. Setting either member of
	// schedule.when clears the other one. Unset = every populated field of spec; id cannot be updated.
	UpdateMask    *fieldmaskpb.FieldMask `protobuf:"bytes,4,opt,name=update_mask,json=updateMask,proto3" json:"update_mask,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *UpdateMentalModelRequest) Reset() {
	*x = UpdateMentalModelRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[57]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *UpdateMentalModelRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdateMentalModelRequest) ProtoMessage() {}

func (x *UpdateMentalModelRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[57]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdateMentalModelRequest.ProtoReflect.Descriptor instead.
func (*UpdateMentalModelRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{57}
}

func (x *UpdateMentalModelRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *UpdateMentalModelRequest) GetMentalModelId() string {
	if x != nil {
		return x.MentalModelId
	}
	return ""
}

func (x *UpdateMentalModelRequest) GetSpec() *MentalModelSpec {
	if x != nil {
		return x.Spec
	}
	return nil
}

func (x *UpdateMentalModelRequest) GetUpdateMask() *fieldmaskpb.FieldMask {
	if x != nil {
		return x.UpdateMask
	}
	return nil
}

// UpdateMentalModelResponse carries the patched model.
type UpdateMentalModelResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The model after the patch.
	MentalModel   *MentalModel `protobuf:"bytes,1,opt,name=mental_model,json=mentalModel,proto3" json:"mental_model,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *UpdateMentalModelResponse) Reset() {
	*x = UpdateMentalModelResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[58]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *UpdateMentalModelResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdateMentalModelResponse) ProtoMessage() {}

func (x *UpdateMentalModelResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[58]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdateMentalModelResponse.ProtoReflect.Descriptor instead.
func (*UpdateMentalModelResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{58}
}

func (x *UpdateMentalModelResponse) GetMentalModel() *MentalModel {
	if x != nil {
		return x.MentalModel
	}
	return nil
}

// RefreshMentalModelRequest asks for a manual refresh.
type RefreshMentalModelRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the model.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Model id.
	MentalModelId string `protobuf:"bytes,2,opt,name=mental_model_id,json=mentalModelId,proto3" json:"mental_model_id,omitempty"`
	// Force a full refresh; OR-ed into a queued refresh this joins.
	ForceFull bool `protobuf:"varint,3,opt,name=force_full,json=forceFull,proto3" json:"force_full,omitempty"`
	// Overrides for this refresh only, over the model's stored ones. Kept on the operation; they replace a
	// queued refresh's overrides.
	Overrides *RefreshOverrides `protobuf:"bytes,4,opt,name=overrides,proto3" json:"overrides,omitempty"`
	// Strategy for this refresh only; unset = MentalModelSpec.strategy.
	Strategy      RefreshStrategy `protobuf:"varint,5,opt,name=strategy,proto3,enum=memory.v1.RefreshStrategy" json:"strategy,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RefreshMentalModelRequest) Reset() {
	*x = RefreshMentalModelRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[59]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RefreshMentalModelRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RefreshMentalModelRequest) ProtoMessage() {}

func (x *RefreshMentalModelRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[59]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RefreshMentalModelRequest.ProtoReflect.Descriptor instead.
func (*RefreshMentalModelRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{59}
}

func (x *RefreshMentalModelRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *RefreshMentalModelRequest) GetMentalModelId() string {
	if x != nil {
		return x.MentalModelId
	}
	return ""
}

func (x *RefreshMentalModelRequest) GetForceFull() bool {
	if x != nil {
		return x.ForceFull
	}
	return false
}

func (x *RefreshMentalModelRequest) GetOverrides() *RefreshOverrides {
	if x != nil {
		return x.Overrides
	}
	return nil
}

func (x *RefreshMentalModelRequest) GetStrategy() RefreshStrategy {
	if x != nil {
		return x.Strategy
	}
	return RefreshStrategy_REFRESH_STRATEGY_UNSPECIFIED
}

// RefreshMentalModelResponse carries the refresh job.
type RefreshMentalModelResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The refresh_mental_model operation (new, or the queued one that was joined).
	Operation *Operation `protobuf:"bytes,1,opt,name=operation,proto3" json:"operation,omitempty"`
	// True when the request joined and upgraded an already queued refresh.
	JoinedQueued  bool `protobuf:"varint,2,opt,name=joined_queued,json=joinedQueued,proto3" json:"joined_queued,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RefreshMentalModelResponse) Reset() {
	*x = RefreshMentalModelResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[60]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RefreshMentalModelResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RefreshMentalModelResponse) ProtoMessage() {}

func (x *RefreshMentalModelResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[60]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RefreshMentalModelResponse.ProtoReflect.Descriptor instead.
func (*RefreshMentalModelResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{60}
}

func (x *RefreshMentalModelResponse) GetOperation() *Operation {
	if x != nil {
		return x.Operation
	}
	return nil
}

func (x *RefreshMentalModelResponse) GetJoinedQueued() bool {
	if x != nil {
		return x.JoinedQueued
	}
	return false
}

// PreviewMentalModelRefreshRequest asks for a refresh that writes nothing.
type PreviewMentalModelRefreshRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the model.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Model id.
	MentalModelId string `protobuf:"bytes,2,opt,name=mental_model_id,json=mentalModelId,proto3" json:"mental_model_id,omitempty"`
	// Strategy to preview; unset = MentalModelSpec.strategy.
	Strategy      RefreshStrategy `protobuf:"varint,3,opt,name=strategy,proto3,enum=memory.v1.RefreshStrategy" json:"strategy,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PreviewMentalModelRefreshRequest) Reset() {
	*x = PreviewMentalModelRefreshRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[61]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PreviewMentalModelRefreshRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PreviewMentalModelRefreshRequest) ProtoMessage() {}

func (x *PreviewMentalModelRefreshRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[61]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PreviewMentalModelRefreshRequest.ProtoReflect.Descriptor instead.
func (*PreviewMentalModelRefreshRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{61}
}

func (x *PreviewMentalModelRefreshRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *PreviewMentalModelRefreshRequest) GetMentalModelId() string {
	if x != nil {
		return x.MentalModelId
	}
	return ""
}

func (x *PreviewMentalModelRefreshRequest) GetStrategy() RefreshStrategy {
	if x != nil {
		return x.Strategy
	}
	return RefreshStrategy_REFRESH_STRATEGY_UNSPECIFIED
}

// PreviewMentalModelRefreshResponse reports what a refresh would do.
type PreviewMentalModelRefreshResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Mode the spec and state ask for, before any fallback.
	RequestedMode RefreshMode `protobuf:"varint,1,opt,name=requested_mode,json=requestedMode,proto3,enum=memory.v1.RefreshMode" json:"requested_mode,omitempty"`
	// Trace of the preview, including the effective mode and outcome.
	Trace *RefreshTrace `protobuf:"bytes,2,opt,name=trace,proto3" json:"trace,omitempty"`
	// Scope the refresh read with.
	ResolvedScope *ResolvedScope `protobuf:"bytes,3,opt,name=resolved_scope,json=resolvedScope,proto3" json:"resolved_scope,omitempty"`
	// Lower bound of the change window (exclusive): the model's last_seq_seen.
	WindowAfterSeq int64 `protobuf:"varint,4,opt,name=window_after_seq,json=windowAfterSeq,proto3" json:"window_after_seq,omitempty"`
	// Upper bound of the change window (inclusive): the bank change_seq read at the start.
	WindowBeforeSeq int64 `protobuf:"varint,5,opt,name=window_before_seq,json=windowBeforeSeq,proto3" json:"window_before_seq,omitempty"`
	// Facts the retrieval returned.
	FactsRetrieved int32 `protobuf:"varint,6,opt,name=facts_retrieved,json=factsRetrieved,proto3" json:"facts_retrieved,omitempty"`
	// Facts the draft cites.
	FactsUsed int32 `protobuf:"varint,7,opt,name=facts_used,json=factsUsed,proto3" json:"facts_used,omitempty"`
	// Unified diff of the draft against the stored content.
	Diff string `protobuf:"bytes,8,opt,name=diff,proto3" json:"diff,omitempty"`
	// Whether a real refresh would write a new version.
	WouldPersist  bool `protobuf:"varint,9,opt,name=would_persist,json=wouldPersist,proto3" json:"would_persist,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PreviewMentalModelRefreshResponse) Reset() {
	*x = PreviewMentalModelRefreshResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[62]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PreviewMentalModelRefreshResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PreviewMentalModelRefreshResponse) ProtoMessage() {}

func (x *PreviewMentalModelRefreshResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[62]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PreviewMentalModelRefreshResponse.ProtoReflect.Descriptor instead.
func (*PreviewMentalModelRefreshResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{62}
}

func (x *PreviewMentalModelRefreshResponse) GetRequestedMode() RefreshMode {
	if x != nil {
		return x.RequestedMode
	}
	return RefreshMode_REFRESH_MODE_UNSPECIFIED
}

func (x *PreviewMentalModelRefreshResponse) GetTrace() *RefreshTrace {
	if x != nil {
		return x.Trace
	}
	return nil
}

func (x *PreviewMentalModelRefreshResponse) GetResolvedScope() *ResolvedScope {
	if x != nil {
		return x.ResolvedScope
	}
	return nil
}

func (x *PreviewMentalModelRefreshResponse) GetWindowAfterSeq() int64 {
	if x != nil {
		return x.WindowAfterSeq
	}
	return 0
}

func (x *PreviewMentalModelRefreshResponse) GetWindowBeforeSeq() int64 {
	if x != nil {
		return x.WindowBeforeSeq
	}
	return 0
}

func (x *PreviewMentalModelRefreshResponse) GetFactsRetrieved() int32 {
	if x != nil {
		return x.FactsRetrieved
	}
	return 0
}

func (x *PreviewMentalModelRefreshResponse) GetFactsUsed() int32 {
	if x != nil {
		return x.FactsUsed
	}
	return 0
}

func (x *PreviewMentalModelRefreshResponse) GetDiff() string {
	if x != nil {
		return x.Diff
	}
	return ""
}

func (x *PreviewMentalModelRefreshResponse) GetWouldPersist() bool {
	if x != nil {
		return x.WouldPersist
	}
	return false
}

// ResolvedScope is the effective filter a refresh reads with, after defaults.
type ResolvedScope struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Effective tag filter: the scope's groups, or the model's tags under the effective match mode.
	Tags *TagFilter `protobuf:"bytes,1,opt,name=tags,proto3" json:"tags,omitempty"`
	// Effective fact types; empty = all.
	FactTypes     []FactType `protobuf:"varint,2,rep,packed,name=fact_types,json=factTypes,proto3,enum=memory.v1.FactType" json:"fact_types,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ResolvedScope) Reset() {
	*x = ResolvedScope{}
	mi := &file_memory_v1_memory_proto_msgTypes[63]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ResolvedScope) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ResolvedScope) ProtoMessage() {}

func (x *ResolvedScope) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[63]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ResolvedScope.ProtoReflect.Descriptor instead.
func (*ResolvedScope) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{63}
}

func (x *ResolvedScope) GetTags() *TagFilter {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *ResolvedScope) GetFactTypes() []FactType {
	if x != nil {
		return x.FactTypes
	}
	return nil
}

// ListMentalModelHistoryRequest pages a model's history.
type ListMentalModelHistoryRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the model; unknown bank = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Model id.
	MentalModelId string `protobuf:"bytes,2,opt,name=mental_model_id,json=mentalModelId,proto3" json:"mental_model_id,omitempty"`
	// Only this kind; unspecified = both.
	Kind MentalModelHistoryEntry_Kind `protobuf:"varint,3,opt,name=kind,proto3,enum=memory.v1.MentalModelHistoryEntry_Kind" json:"kind,omitempty"`
	// Maximum entries per page; 0 = 100, at most 1000.
	PageSize int32 `protobuf:"varint,4,opt,name=page_size,json=pageSize,proto3" json:"page_size,omitempty"`
	// Token from a previous response; empty = first page.
	PageToken     string `protobuf:"bytes,5,opt,name=page_token,json=pageToken,proto3" json:"page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListMentalModelHistoryRequest) Reset() {
	*x = ListMentalModelHistoryRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[64]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListMentalModelHistoryRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListMentalModelHistoryRequest) ProtoMessage() {}

func (x *ListMentalModelHistoryRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[64]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListMentalModelHistoryRequest.ProtoReflect.Descriptor instead.
func (*ListMentalModelHistoryRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{64}
}

func (x *ListMentalModelHistoryRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *ListMentalModelHistoryRequest) GetMentalModelId() string {
	if x != nil {
		return x.MentalModelId
	}
	return ""
}

func (x *ListMentalModelHistoryRequest) GetKind() MentalModelHistoryEntry_Kind {
	if x != nil {
		return x.Kind
	}
	return MentalModelHistoryEntry_KIND_UNSPECIFIED
}

func (x *ListMentalModelHistoryRequest) GetPageSize() int32 {
	if x != nil {
		return x.PageSize
	}
	return 0
}

func (x *ListMentalModelHistoryRequest) GetPageToken() string {
	if x != nil {
		return x.PageToken
	}
	return ""
}

// ListMentalModelHistoryResponse is one page of history, newest first.
type ListMentalModelHistoryResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Entries on this page.
	Entries []*MentalModelHistoryEntry `protobuf:"bytes,1,rep,name=entries,proto3" json:"entries,omitempty"`
	// Token for the next page; empty = no more.
	NextPageToken string `protobuf:"bytes,2,opt,name=next_page_token,json=nextPageToken,proto3" json:"next_page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListMentalModelHistoryResponse) Reset() {
	*x = ListMentalModelHistoryResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[65]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListMentalModelHistoryResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListMentalModelHistoryResponse) ProtoMessage() {}

func (x *ListMentalModelHistoryResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[65]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListMentalModelHistoryResponse.ProtoReflect.Descriptor instead.
func (*ListMentalModelHistoryResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{65}
}

func (x *ListMentalModelHistoryResponse) GetEntries() []*MentalModelHistoryEntry {
	if x != nil {
		return x.Entries
	}
	return nil
}

func (x *ListMentalModelHistoryResponse) GetNextPageToken() string {
	if x != nil {
		return x.NextPageToken
	}
	return ""
}

// MentalModelHistoryEntry is one history row of a model.
type MentalModelHistoryEntry struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Version or failure.
	Kind MentalModelHistoryEntry_Kind `protobuf:"varint,1,opt,name=kind,proto3,enum=memory.v1.MentalModelHistoryEntry_Kind" json:"kind,omitempty"`
	// Content before the change (KIND_VERSION).
	PreviousContent string `protobuf:"bytes,2,opt,name=previous_content,json=previousContent,proto3" json:"previous_content,omitempty"`
	// When the entry was recorded.
	CreatedAt *timestamppb.Timestamp `protobuf:"bytes,3,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`
	// Outcome of the refresh behind the entry.
	Outcome RefreshOutcome `protobuf:"varint,4,opt,name=outcome,proto3,enum=memory.v1.RefreshOutcome" json:"outcome,omitempty"`
	// Machine-readable failure reason (KIND_REFRESH_FAILED).
	FailureReason string `protobuf:"bytes,5,opt,name=failure_reason,json=failureReason,proto3" json:"failure_reason,omitempty"`
	// Error message of the failure (KIND_REFRESH_FAILED).
	ErrorMessage  string `protobuf:"bytes,6,opt,name=error_message,json=errorMessage,proto3" json:"error_message,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *MentalModelHistoryEntry) Reset() {
	*x = MentalModelHistoryEntry{}
	mi := &file_memory_v1_memory_proto_msgTypes[66]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *MentalModelHistoryEntry) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*MentalModelHistoryEntry) ProtoMessage() {}

func (x *MentalModelHistoryEntry) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[66]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use MentalModelHistoryEntry.ProtoReflect.Descriptor instead.
func (*MentalModelHistoryEntry) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{66}
}

func (x *MentalModelHistoryEntry) GetKind() MentalModelHistoryEntry_Kind {
	if x != nil {
		return x.Kind
	}
	return MentalModelHistoryEntry_KIND_UNSPECIFIED
}

func (x *MentalModelHistoryEntry) GetPreviousContent() string {
	if x != nil {
		return x.PreviousContent
	}
	return ""
}

func (x *MentalModelHistoryEntry) GetCreatedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.CreatedAt
	}
	return nil
}

func (x *MentalModelHistoryEntry) GetOutcome() RefreshOutcome {
	if x != nil {
		return x.Outcome
	}
	return RefreshOutcome_REFRESH_OUTCOME_UNSPECIFIED
}

func (x *MentalModelHistoryEntry) GetFailureReason() string {
	if x != nil {
		return x.FailureReason
	}
	return ""
}

func (x *MentalModelHistoryEntry) GetErrorMessage() string {
	if x != nil {
		return x.ErrorMessage
	}
	return ""
}

// DeleteMentalModelRequest names the model to delete.
type DeleteMentalModelRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the model.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Model id.
	MentalModelId string `protobuf:"bytes,2,opt,name=mental_model_id,json=mentalModelId,proto3" json:"mental_model_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DeleteMentalModelRequest) Reset() {
	*x = DeleteMentalModelRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[67]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DeleteMentalModelRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeleteMentalModelRequest) ProtoMessage() {}

func (x *DeleteMentalModelRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[67]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeleteMentalModelRequest.ProtoReflect.Descriptor instead.
func (*DeleteMentalModelRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{67}
}

func (x *DeleteMentalModelRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *DeleteMentalModelRequest) GetMentalModelId() string {
	if x != nil {
		return x.MentalModelId
	}
	return ""
}

// DeleteMentalModelResponse is empty on success.
type DeleteMentalModelResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DeleteMentalModelResponse) Reset() {
	*x = DeleteMentalModelResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[68]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DeleteMentalModelResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeleteMentalModelResponse) ProtoMessage() {}

func (x *DeleteMentalModelResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[68]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeleteMentalModelResponse.ProtoReflect.Descriptor instead.
func (*DeleteMentalModelResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{68}
}

// DeleteMentalModelFolderRequest names the folder to delete.
type DeleteMentalModelFolderRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the folder.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Folder path; every model under it is deleted, recursively. Empty = INVALID_ARGUMENT.
	FolderPath    string `protobuf:"bytes,2,opt,name=folder_path,json=folderPath,proto3" json:"folder_path,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DeleteMentalModelFolderRequest) Reset() {
	*x = DeleteMentalModelFolderRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[69]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DeleteMentalModelFolderRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeleteMentalModelFolderRequest) ProtoMessage() {}

func (x *DeleteMentalModelFolderRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[69]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeleteMentalModelFolderRequest.ProtoReflect.Descriptor instead.
func (*DeleteMentalModelFolderRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{69}
}

func (x *DeleteMentalModelFolderRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *DeleteMentalModelFolderRequest) GetFolderPath() string {
	if x != nil {
		return x.FolderPath
	}
	return ""
}

// DeleteMentalModelFolderResponse reports how many models were deleted.
type DeleteMentalModelFolderResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Models deleted.
	Deleted       int32 `protobuf:"varint,1,opt,name=deleted,proto3" json:"deleted,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DeleteMentalModelFolderResponse) Reset() {
	*x = DeleteMentalModelFolderResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[70]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DeleteMentalModelFolderResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeleteMentalModelFolderResponse) ProtoMessage() {}

func (x *DeleteMentalModelFolderResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[70]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeleteMentalModelFolderResponse.ProtoReflect.Descriptor instead.
func (*DeleteMentalModelFolderResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{70}
}

func (x *DeleteMentalModelFolderResponse) GetDeleted() int32 {
	if x != nil {
		return x.Deleted
	}
	return 0
}

// SearchMentalModelsRequest searches the bank's mental models by content.
type SearchMentalModelsRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank to search; unknown bank = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Search text.
	Query string `protobuf:"bytes,2,opt,name=query,proto3" json:"query,omitempty"`
	// Only models whose folder path starts with this; empty = all.
	FolderPrefix string `protobuf:"bytes,3,opt,name=folder_prefix,json=folderPrefix,proto3" json:"folder_prefix,omitempty"`
	// Maximum hits; 0 = 10, at most 100, negative = INVALID_ARGUMENT.
	MaxResults    int32 `protobuf:"varint,4,opt,name=max_results,json=maxResults,proto3" json:"max_results,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *SearchMentalModelsRequest) Reset() {
	*x = SearchMentalModelsRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[71]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SearchMentalModelsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SearchMentalModelsRequest) ProtoMessage() {}

func (x *SearchMentalModelsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[71]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SearchMentalModelsRequest.ProtoReflect.Descriptor instead.
func (*SearchMentalModelsRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{71}
}

func (x *SearchMentalModelsRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *SearchMentalModelsRequest) GetQuery() string {
	if x != nil {
		return x.Query
	}
	return ""
}

func (x *SearchMentalModelsRequest) GetFolderPrefix() string {
	if x != nil {
		return x.FolderPrefix
	}
	return ""
}

func (x *SearchMentalModelsRequest) GetMaxResults() int32 {
	if x != nil {
		return x.MaxResults
	}
	return 0
}

// SearchMentalModelsResponse lists matching models.
type SearchMentalModelsResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Hits, best first.
	Hits          []*MentalModelHit `protobuf:"bytes,1,rep,name=hits,proto3" json:"hits,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *SearchMentalModelsResponse) Reset() {
	*x = SearchMentalModelsResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[72]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SearchMentalModelsResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SearchMentalModelsResponse) ProtoMessage() {}

func (x *SearchMentalModelsResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[72]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SearchMentalModelsResponse.ProtoReflect.Descriptor instead.
func (*SearchMentalModelsResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{72}
}

func (x *SearchMentalModelsResponse) GetHits() []*MentalModelHit {
	if x != nil {
		return x.Hits
	}
	return nil
}

// MentalModelHit is one mental model matching a search.
type MentalModelHit struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Model id.
	MentalModelId string `protobuf:"bytes,1,opt,name=mental_model_id,json=mentalModelId,proto3" json:"mental_model_id,omitempty"`
	// Folder the model lives in.
	FolderPath string `protobuf:"bytes,2,opt,name=folder_path,json=folderPath,proto3" json:"folder_path,omitempty"`
	// Model name.
	Name string `protobuf:"bytes,3,opt,name=name,proto3" json:"name,omitempty"`
	// Matching excerpt of the content.
	Snippet string `protobuf:"bytes,4,opt,name=snippet,proto3" json:"snippet,omitempty"`
	// Fused full-text and semantic score; hits are ordered by it.
	Score         float64 `protobuf:"fixed64,5,opt,name=score,proto3" json:"score,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *MentalModelHit) Reset() {
	*x = MentalModelHit{}
	mi := &file_memory_v1_memory_proto_msgTypes[73]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *MentalModelHit) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*MentalModelHit) ProtoMessage() {}

func (x *MentalModelHit) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[73]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use MentalModelHit.ProtoReflect.Descriptor instead.
func (*MentalModelHit) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{73}
}

func (x *MentalModelHit) GetMentalModelId() string {
	if x != nil {
		return x.MentalModelId
	}
	return ""
}

func (x *MentalModelHit) GetFolderPath() string {
	if x != nil {
		return x.FolderPath
	}
	return ""
}

func (x *MentalModelHit) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *MentalModelHit) GetSnippet() string {
	if x != nil {
		return x.Snippet
	}
	return ""
}

func (x *MentalModelHit) GetScore() float64 {
	if x != nil {
		return x.Score
	}
	return 0
}

// MentalModel is a stored mental model with its content and refresh state.
type MentalModel struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The writable spec.
	Spec *MentalModelSpec `protobuf:"bytes,1,opt,name=spec,proto3" json:"spec,omitempty"`
	// Page content in Markdown.
	ContentMd string `protobuf:"bytes,2,opt,name=content_md,json=contentMd,proto3" json:"content_md,omitempty"`
	// Structured output matching spec.response_schema_json; empty when none.
	StructuredOutputJson string `protobuf:"bytes,3,opt,name=structured_output_json,json=structuredOutputJson,proto3" json:"structured_output_json,omitempty"`
	// Content version, incremented on every write.
	Version int64 `protobuf:"varint,4,opt,name=version,proto3" json:"version,omitempty"`
	// True when an in-scope memory or observation changed after last_seq_seen, or a source was retracted;
	// computed in MENTAL_MODEL_VIEW_FULL only (false otherwise).
	Stale bool `protobuf:"varint,5,opt,name=stale,proto3" json:"stale,omitempty"`
	// Wall clock of the last completed refresh, including no-op refreshes.
	LastRefreshedAt *timestamppb.Timestamp `protobuf:"bytes,6,opt,name=last_refreshed_at,json=lastRefreshedAt,proto3" json:"last_refreshed_at,omitempty"`
	// The bank change_seq the last successful refresh read up to (its watermark): the same counter as
	// LifecycleEvent.seq and BankStats.change_seq.
	LastSeqSeen int64 `protobuf:"varint,7,opt,name=last_seq_seen,json=lastSeqSeen,proto3" json:"last_seq_seen,omitempty"`
	// When the last refresh failed; unset if none has.
	LastRefreshFailedAt *timestamppb.Timestamp `protobuf:"bytes,8,opt,name=last_refresh_failed_at,json=lastRefreshFailedAt,proto3" json:"last_refresh_failed_at,omitempty"`
	// Set by a failed refresh; automatic refreshes skip the model until a manual refresh succeeds.
	AutoRefreshPaused bool `protobuf:"varint,9,opt,name=auto_refresh_paused,json=autoRefreshPaused,proto3" json:"auto_refresh_paused,omitempty"`
	// Ids of the memories the content cites, accumulated across refreshes; pruned on full refresh and on
	// invalidate. MENTAL_MODEL_VIEW_FULL only.
	SourceMemoryIds []string `protobuf:"bytes,10,rep,name=source_memory_ids,json=sourceMemoryIds,proto3" json:"source_memory_ids,omitempty"`
	// Trace of the last refresh when spec.keep_trace; MENTAL_MODEL_VIEW_FULL only.
	LastTrace *RefreshTrace `protobuf:"bytes,11,opt,name=last_trace,json=lastTrace,proto3" json:"last_trace,omitempty"`
	// When the model was created.
	CreatedAt     *timestamppb.Timestamp `protobuf:"bytes,12,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *MentalModel) Reset() {
	*x = MentalModel{}
	mi := &file_memory_v1_memory_proto_msgTypes[74]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *MentalModel) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*MentalModel) ProtoMessage() {}

func (x *MentalModel) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[74]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use MentalModel.ProtoReflect.Descriptor instead.
func (*MentalModel) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{74}
}

func (x *MentalModel) GetSpec() *MentalModelSpec {
	if x != nil {
		return x.Spec
	}
	return nil
}

func (x *MentalModel) GetContentMd() string {
	if x != nil {
		return x.ContentMd
	}
	return ""
}

func (x *MentalModel) GetStructuredOutputJson() string {
	if x != nil {
		return x.StructuredOutputJson
	}
	return ""
}

func (x *MentalModel) GetVersion() int64 {
	if x != nil {
		return x.Version
	}
	return 0
}

func (x *MentalModel) GetStale() bool {
	if x != nil {
		return x.Stale
	}
	return false
}

func (x *MentalModel) GetLastRefreshedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.LastRefreshedAt
	}
	return nil
}

func (x *MentalModel) GetLastSeqSeen() int64 {
	if x != nil {
		return x.LastSeqSeen
	}
	return 0
}

func (x *MentalModel) GetLastRefreshFailedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.LastRefreshFailedAt
	}
	return nil
}

func (x *MentalModel) GetAutoRefreshPaused() bool {
	if x != nil {
		return x.AutoRefreshPaused
	}
	return false
}

func (x *MentalModel) GetSourceMemoryIds() []string {
	if x != nil {
		return x.SourceMemoryIds
	}
	return nil
}

func (x *MentalModel) GetLastTrace() *RefreshTrace {
	if x != nil {
		return x.LastTrace
	}
	return nil
}

func (x *MentalModel) GetCreatedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.CreatedAt
	}
	return nil
}

// MentalModelSpec is the writable part of a mental model: what it answers, when it refreshes (schedule),
// what it reads (scope), how a refresh runs, and its stored overrides. Unset run settings fall back to
// BankConfig.mental_models.
type MentalModelSpec struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Model id: lowercase alphanumerics and hyphens, unique per (tenant, bank).
	Id string `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	// Display name.
	Name string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	// The question the page answers; changing it forces the next refresh to be full.
	SourceQuery string `protobuf:"bytes,3,opt,name=source_query,json=sourceQuery,proto3" json:"source_query,omitempty"`
	// Folder the page lives in, for example team/projects.
	FolderPath string `protobuf:"bytes,4,opt,name=folder_path,json=folderPath,proto3" json:"folder_path,omitempty"`
	// Tags of the model: ListMentalModels and ListTags filter on them, and they scope what the model reads
	// and when it goes stale (see MentalModelScope.tags).
	Tags []string `protobuf:"bytes,5,rep,name=tags,proto3" json:"tags,omitempty"`
	// When the model refreshes on its own; unset = manual refreshes only.
	Schedule *RefreshSchedule `protobuf:"bytes,6,opt,name=schedule,proto3" json:"schedule,omitempty"`
	// What the model reads; unset = everything its tags select.
	Scope *MentalModelScope `protobuf:"bytes,7,opt,name=scope,proto3" json:"scope,omitempty"`
	// Full or delta; unset = MentalModelConfig.default_mode.
	Mode RefreshMode `protobuf:"varint,8,opt,name=mode,proto3,enum=memory.v1.RefreshMode" json:"mode,omitempty"`
	// Agentic or single-shot; unset = MentalModelConfig.default_strategy. RefreshMentalModelRequest.strategy
	// wins for one call.
	Strategy RefreshStrategy `protobuf:"varint,9,opt,name=strategy,proto3,enum=memory.v1.RefreshStrategy" json:"strategy,omitempty"`
	// Size class of a refresh; unset = BUDGET_MID.
	Budget Budget `protobuf:"varint,10,opt,name=budget,proto3,enum=memory.v1.Budget" json:"budget,omitempty"`
	// Token cap on the content; unset = MentalModelConfig.max_content_tokens.
	MaxTokens *int32 `protobuf:"varint,11,opt,name=max_tokens,json=maxTokens,proto3,oneof" json:"max_tokens,omitempty"`
	// Include source chunks in the refresh's recall results; unset = no chunks.
	IncludeChunks *bool `protobuf:"varint,12,opt,name=include_chunks,json=includeChunks,proto3,oneof" json:"include_chunks,omitempty"`
	// Token cap on chunks attached to a recall during refresh; unset = the recall default.
	RecallChunksMaxTokens *int32 `protobuf:"varint,13,opt,name=recall_chunks_max_tokens,json=recallChunksMaxTokens,proto3,oneof" json:"recall_chunks_max_tokens,omitempty"`
	// Do not read other mental models while refreshing (agentic refresh).
	ExcludeMentalModels bool `protobuf:"varint,14,opt,name=exclude_mental_models,json=excludeMentalModels,proto3" json:"exclude_mental_models,omitempty"`
	// Mental models never read while refreshing (agentic refresh); the model itself is always excluded.
	ExcludeMentalModelIds []string `protobuf:"bytes,15,rep,name=exclude_mental_model_ids,json=excludeMentalModelIds,proto3" json:"exclude_mental_model_ids,omitempty"`
	// JSON Schema for structured output projected from the page (MentalModel.structured_output_json); empty =
	// none.
	ResponseSchemaJson string `protobuf:"bytes,16,opt,name=response_schema_json,json=responseSchemaJson,proto3" json:"response_schema_json,omitempty"`
	// Keep the last refresh trace on the model (MentalModel.last_trace).
	KeepTrace bool `protobuf:"varint,17,opt,name=keep_trace,json=keepTrace,proto3" json:"keep_trace,omitempty"`
	// Overrides stored on the model and applied to every refresh; the reflect loop's caps
	// (reflect.recall_max_tokens, reflect.search_observations_max_tokens,
	// reflect.search_observations_include_entities) are set here. In an update_mask, paths go inside it
	// (overrides.reflect.mission, overrides.mental_model_model).
	Overrides     *RefreshOverrides `protobuf:"bytes,18,opt,name=overrides,proto3" json:"overrides,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *MentalModelSpec) Reset() {
	*x = MentalModelSpec{}
	mi := &file_memory_v1_memory_proto_msgTypes[75]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *MentalModelSpec) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*MentalModelSpec) ProtoMessage() {}

func (x *MentalModelSpec) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[75]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use MentalModelSpec.ProtoReflect.Descriptor instead.
func (*MentalModelSpec) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{75}
}

func (x *MentalModelSpec) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *MentalModelSpec) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *MentalModelSpec) GetSourceQuery() string {
	if x != nil {
		return x.SourceQuery
	}
	return ""
}

func (x *MentalModelSpec) GetFolderPath() string {
	if x != nil {
		return x.FolderPath
	}
	return ""
}

func (x *MentalModelSpec) GetTags() []string {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *MentalModelSpec) GetSchedule() *RefreshSchedule {
	if x != nil {
		return x.Schedule
	}
	return nil
}

func (x *MentalModelSpec) GetScope() *MentalModelScope {
	if x != nil {
		return x.Scope
	}
	return nil
}

func (x *MentalModelSpec) GetMode() RefreshMode {
	if x != nil {
		return x.Mode
	}
	return RefreshMode_REFRESH_MODE_UNSPECIFIED
}

func (x *MentalModelSpec) GetStrategy() RefreshStrategy {
	if x != nil {
		return x.Strategy
	}
	return RefreshStrategy_REFRESH_STRATEGY_UNSPECIFIED
}

func (x *MentalModelSpec) GetBudget() Budget {
	if x != nil {
		return x.Budget
	}
	return Budget_BUDGET_UNSPECIFIED
}

func (x *MentalModelSpec) GetMaxTokens() int32 {
	if x != nil && x.MaxTokens != nil {
		return *x.MaxTokens
	}
	return 0
}

func (x *MentalModelSpec) GetIncludeChunks() bool {
	if x != nil && x.IncludeChunks != nil {
		return *x.IncludeChunks
	}
	return false
}

func (x *MentalModelSpec) GetRecallChunksMaxTokens() int32 {
	if x != nil && x.RecallChunksMaxTokens != nil {
		return *x.RecallChunksMaxTokens
	}
	return 0
}

func (x *MentalModelSpec) GetExcludeMentalModels() bool {
	if x != nil {
		return x.ExcludeMentalModels
	}
	return false
}

func (x *MentalModelSpec) GetExcludeMentalModelIds() []string {
	if x != nil {
		return x.ExcludeMentalModelIds
	}
	return nil
}

func (x *MentalModelSpec) GetResponseSchemaJson() string {
	if x != nil {
		return x.ResponseSchemaJson
	}
	return ""
}

func (x *MentalModelSpec) GetKeepTrace() bool {
	if x != nil {
		return x.KeepTrace
	}
	return false
}

func (x *MentalModelSpec) GetOverrides() *RefreshOverrides {
	if x != nil {
		return x.Overrides
	}
	return nil
}

// RefreshSchedule decides when a model refreshes on its own. Manual refreshes (RefreshMentalModel) are always
// allowed.
type RefreshSchedule struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// When is the automatic trigger: at most one. Unset (or after_consolidation=false, or an empty cron) = no
	// automatic refresh.
	//
	// Types that are valid to be assigned to When:
	//
	//	*RefreshSchedule_AfterConsolidation
	//	*RefreshSchedule_Cron
	When isRefreshSchedule_When `protobuf_oneof:"when"`
	// Minimum seconds between automatic refreshes; unset = MentalModelConfig.min_refresh_interval_seconds.
	MinIntervalSeconds *int32 `protobuf:"varint,3,opt,name=min_interval_seconds,json=minIntervalSeconds,proto3,oneof" json:"min_interval_seconds,omitempty"`
	unknownFields      protoimpl.UnknownFields
	sizeCache          protoimpl.SizeCache
}

func (x *RefreshSchedule) Reset() {
	*x = RefreshSchedule{}
	mi := &file_memory_v1_memory_proto_msgTypes[76]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RefreshSchedule) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RefreshSchedule) ProtoMessage() {}

func (x *RefreshSchedule) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[76]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RefreshSchedule.ProtoReflect.Descriptor instead.
func (*RefreshSchedule) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{76}
}

func (x *RefreshSchedule) GetWhen() isRefreshSchedule_When {
	if x != nil {
		return x.When
	}
	return nil
}

func (x *RefreshSchedule) GetAfterConsolidation() bool {
	if x != nil {
		if x, ok := x.When.(*RefreshSchedule_AfterConsolidation); ok {
			return x.AfterConsolidation
		}
	}
	return false
}

func (x *RefreshSchedule) GetCron() string {
	if x != nil {
		if x, ok := x.When.(*RefreshSchedule_Cron); ok {
			return x.Cron
		}
	}
	return ""
}

func (x *RefreshSchedule) GetMinIntervalSeconds() int32 {
	if x != nil && x.MinIntervalSeconds != nil {
		return *x.MinIntervalSeconds
	}
	return 0
}

type isRefreshSchedule_When interface {
	isRefreshSchedule_When()
}

type RefreshSchedule_AfterConsolidation struct {
	// Refresh after a consolidation changes an in-scope memory or observation (debounced by
	// MentalModelConfig.refresh_debounce_seconds).
	AfterConsolidation bool `protobuf:"varint,1,opt,name=after_consolidation,json=afterConsolidation,proto3,oneof"`
}

type RefreshSchedule_Cron struct {
	// Cron schedule, UTC, 5 fields; malformed = INVALID_ARGUMENT.
	Cron string `protobuf:"bytes,2,opt,name=cron,proto3,oneof"`
}

func (*RefreshSchedule_AfterConsolidation) isRefreshSchedule_When() {}

func (*RefreshSchedule_Cron) isRefreshSchedule_When() {}

// MentalModelScope is what a model reads and goes stale by, beyond its own tags.
type MentalModelScope struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Tag filter. The flat tags always come from MentalModelSpec.tags, so tags.tags must stay empty
	// (INVALID_ARGUMENT otherwise); tags.match is how the model's tags filter memories, unset =
	// TAGS_MATCH_ALL_STRICT when the model is tagged (TAGS_MATCH_ANY otherwise). tags.groups, when set,
	// replace the model's tags and the match mode.
	Tags *TagFilter `protobuf:"bytes,1,opt,name=tags,proto3" json:"tags,omitempty"`
	// Fact types in scope; empty = all.
	FactTypes     []FactType `protobuf:"varint,2,rep,packed,name=fact_types,json=factTypes,proto3,enum=memory.v1.FactType" json:"fact_types,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *MentalModelScope) Reset() {
	*x = MentalModelScope{}
	mi := &file_memory_v1_memory_proto_msgTypes[77]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *MentalModelScope) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*MentalModelScope) ProtoMessage() {}

func (x *MentalModelScope) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[77]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use MentalModelScope.ProtoReflect.Descriptor instead.
func (*MentalModelScope) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{77}
}

func (x *MentalModelScope) GetTags() *TagFilter {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *MentalModelScope) GetFactTypes() []FactType {
	if x != nil {
		return x.FactTypes
	}
	return nil
}

// RefreshTrace explains one refresh or preview.
type RefreshTrace struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Mode actually used.
	EffectiveMode RefreshMode `protobuf:"varint,1,opt,name=effective_mode,json=effectiveMode,proto3,enum=memory.v1.RefreshMode" json:"effective_mode,omitempty"`
	// Why a delta refresh ran full: no_baseline_content, source_query_changed, ...; empty if it did not.
	ModeFallbackReason string `protobuf:"bytes,2,opt,name=mode_fallback_reason,json=modeFallbackReason,proto3" json:"mode_fallback_reason,omitempty"`
	// Result of the refresh.
	Outcome RefreshOutcome `protobuf:"varint,3,opt,name=outcome,proto3,enum=memory.v1.RefreshOutcome" json:"outcome,omitempty"`
	// Tool calls of the reflect loop; REFRESH_STRATEGY_AGENTIC only.
	ToolCalls []*ToolCallTrace `protobuf:"bytes,4,rep,name=tool_calls,json=toolCalls,proto3" json:"tool_calls,omitempty"`
	// Edit ops of a delta refresh, with whether each applied.
	DeltaOperations []*DeltaOp `protobuf:"bytes,5,rep,name=delta_operations,json=deltaOperations,proto3" json:"delta_operations,omitempty"`
	// Token usage per slot and model.
	Usage []*SlotUsage `protobuf:"bytes,6,rep,name=usage,proto3" json:"usage,omitempty"`
	// When the trace was recorded.
	RecordedAt *timestamppb.Timestamp `protobuf:"bytes,7,opt,name=recorded_at,json=recordedAt,proto3" json:"recorded_at,omitempty"`
	// Total refresh wall time in milliseconds.
	DurationMs int64 `protobuf:"varint,8,opt,name=duration_ms,json=durationMs,proto3" json:"duration_ms,omitempty"`
	// Non-fatal problems, for example dropped citations or truncated content.
	Warnings []string `protobuf:"bytes,9,rep,name=warnings,proto3" json:"warnings,omitempty"`
	// Strategy actually used.
	EffectiveStrategy RefreshStrategy `protobuf:"varint,10,opt,name=effective_strategy,json=effectiveStrategy,proto3,enum=memory.v1.RefreshStrategy" json:"effective_strategy,omitempty"`
	unknownFields     protoimpl.UnknownFields
	sizeCache         protoimpl.SizeCache
}

func (x *RefreshTrace) Reset() {
	*x = RefreshTrace{}
	mi := &file_memory_v1_memory_proto_msgTypes[78]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RefreshTrace) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RefreshTrace) ProtoMessage() {}

func (x *RefreshTrace) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[78]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RefreshTrace.ProtoReflect.Descriptor instead.
func (*RefreshTrace) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{78}
}

func (x *RefreshTrace) GetEffectiveMode() RefreshMode {
	if x != nil {
		return x.EffectiveMode
	}
	return RefreshMode_REFRESH_MODE_UNSPECIFIED
}

func (x *RefreshTrace) GetModeFallbackReason() string {
	if x != nil {
		return x.ModeFallbackReason
	}
	return ""
}

func (x *RefreshTrace) GetOutcome() RefreshOutcome {
	if x != nil {
		return x.Outcome
	}
	return RefreshOutcome_REFRESH_OUTCOME_UNSPECIFIED
}

func (x *RefreshTrace) GetToolCalls() []*ToolCallTrace {
	if x != nil {
		return x.ToolCalls
	}
	return nil
}

func (x *RefreshTrace) GetDeltaOperations() []*DeltaOp {
	if x != nil {
		return x.DeltaOperations
	}
	return nil
}

func (x *RefreshTrace) GetUsage() []*SlotUsage {
	if x != nil {
		return x.Usage
	}
	return nil
}

func (x *RefreshTrace) GetRecordedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.RecordedAt
	}
	return nil
}

func (x *RefreshTrace) GetDurationMs() int64 {
	if x != nil {
		return x.DurationMs
	}
	return 0
}

func (x *RefreshTrace) GetWarnings() []string {
	if x != nil {
		return x.Warnings
	}
	return nil
}

func (x *RefreshTrace) GetEffectiveStrategy() RefreshStrategy {
	if x != nil {
		return x.EffectiveStrategy
	}
	return RefreshStrategy_REFRESH_STRATEGY_UNSPECIFIED
}

// DeltaOp is one edit operation of a delta refresh.
type DeltaOp struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The edit to perform; decides which of the other fields apply.
	Kind DeltaOp_Kind `protobuf:"varint,1,opt,name=kind,proto3,enum=memory.v1.DeltaOp_Kind" json:"kind,omitempty"`
	// Target section id.
	SectionId string `protobuf:"bytes,2,opt,name=section_id,json=sectionId,proto3" json:"section_id,omitempty"`
	// Target block id.
	BlockId string `protobuf:"bytes,3,opt,name=block_id,json=blockId,proto3" json:"block_id,omitempty"`
	// Block to insert after (KIND_INSERT_BLOCK).
	AfterBlockId string `protobuf:"bytes,4,opt,name=after_block_id,json=afterBlockId,proto3" json:"after_block_id,omitempty"`
	// Section heading (KIND_ADD_SECTION, KIND_RENAME_SECTION).
	Heading string `protobuf:"bytes,5,opt,name=heading,proto3" json:"heading,omitempty"`
	// New Markdown blocks.
	BlocksMd []string `protobuf:"bytes,6,rep,name=blocks_md,json=blocksMd,proto3" json:"blocks_md,omitempty"`
	// Whether the op applied cleanly.
	Applied bool `protobuf:"varint,7,opt,name=applied,proto3" json:"applied,omitempty"`
	// Why the op was rejected, for example an unknown id; empty when applied.
	SkipReason    string `protobuf:"bytes,8,opt,name=skip_reason,json=skipReason,proto3" json:"skip_reason,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DeltaOp) Reset() {
	*x = DeltaOp{}
	mi := &file_memory_v1_memory_proto_msgTypes[79]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DeltaOp) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeltaOp) ProtoMessage() {}

func (x *DeltaOp) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[79]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeltaOp.ProtoReflect.Descriptor instead.
func (*DeltaOp) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{79}
}

func (x *DeltaOp) GetKind() DeltaOp_Kind {
	if x != nil {
		return x.Kind
	}
	return DeltaOp_KIND_UNSPECIFIED
}

func (x *DeltaOp) GetSectionId() string {
	if x != nil {
		return x.SectionId
	}
	return ""
}

func (x *DeltaOp) GetBlockId() string {
	if x != nil {
		return x.BlockId
	}
	return ""
}

func (x *DeltaOp) GetAfterBlockId() string {
	if x != nil {
		return x.AfterBlockId
	}
	return ""
}

func (x *DeltaOp) GetHeading() string {
	if x != nil {
		return x.Heading
	}
	return ""
}

func (x *DeltaOp) GetBlocksMd() []string {
	if x != nil {
		return x.BlocksMd
	}
	return nil
}

func (x *DeltaOp) GetApplied() bool {
	if x != nil {
		return x.Applied
	}
	return false
}

func (x *DeltaOp) GetSkipReason() string {
	if x != nil {
		return x.SkipReason
	}
	return ""
}

// CreateBankRequest creates a bank.
type CreateBankRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank id, unique within the tenant.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Display name.
	Name string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	// What the bank starts with; unset = no own config (everything inherits tenant and server defaults).
	//
	// Types that are valid to be assigned to Initial:
	//
	//	*CreateBankRequest_Config
	//	*CreateBankRequest_Template
	Initial       isCreateBankRequest_Initial `protobuf_oneof:"initial"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *CreateBankRequest) Reset() {
	*x = CreateBankRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[80]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CreateBankRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CreateBankRequest) ProtoMessage() {}

func (x *CreateBankRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[80]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CreateBankRequest.ProtoReflect.Descriptor instead.
func (*CreateBankRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{80}
}

func (x *CreateBankRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *CreateBankRequest) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *CreateBankRequest) GetInitial() isCreateBankRequest_Initial {
	if x != nil {
		return x.Initial
	}
	return nil
}

func (x *CreateBankRequest) GetConfig() *BankConfig {
	if x != nil {
		if x, ok := x.Initial.(*CreateBankRequest_Config); ok {
			return x.Config
		}
	}
	return nil
}

func (x *CreateBankRequest) GetTemplate() *BankTemplate {
	if x != nil {
		if x, ok := x.Initial.(*CreateBankRequest_Template); ok {
			return x.Template
		}
	}
	return nil
}

type isCreateBankRequest_Initial interface {
	isCreateBankRequest_Initial()
}

type CreateBankRequest_Config struct {
	// Initial config overrides; unset fields inherit tenant and server defaults.
	Config *BankConfig `protobuf:"bytes,3,opt,name=config,proto3,oneof"`
}

type CreateBankRequest_Template struct {
	// A template (ExportBankTemplate): its config becomes the bank's own overrides, its directives are created
	// with new ids, and its mental models are created with their first builds enqueued.
	Template *BankTemplate `protobuf:"bytes,4,opt,name=template,proto3,oneof"`
}

func (*CreateBankRequest_Config) isCreateBankRequest_Initial() {}

func (*CreateBankRequest_Template) isCreateBankRequest_Initial() {}

// CreateBankResponse carries the new bank and, for a template, its mental-model builds.
type CreateBankResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The newly created bank.
	Bank *Bank `protobuf:"bytes,1,opt,name=bank,proto3" json:"bank,omitempty"`
	// One refresh_mental_model operation per template mental model, building its first version, in template
	// order; empty without a template.
	MentalModelOperations []*Operation `protobuf:"bytes,2,rep,name=mental_model_operations,json=mentalModelOperations,proto3" json:"mental_model_operations,omitempty"`
	unknownFields         protoimpl.UnknownFields
	sizeCache             protoimpl.SizeCache
}

func (x *CreateBankResponse) Reset() {
	*x = CreateBankResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[81]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CreateBankResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CreateBankResponse) ProtoMessage() {}

func (x *CreateBankResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[81]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CreateBankResponse.ProtoReflect.Descriptor instead.
func (*CreateBankResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{81}
}

func (x *CreateBankResponse) GetBank() *Bank {
	if x != nil {
		return x.Bank
	}
	return nil
}

func (x *CreateBankResponse) GetMentalModelOperations() []*Operation {
	if x != nil {
		return x.MentalModelOperations
	}
	return nil
}

// BankTemplate is a portable bank definition: config, directives and mental-model specs (no memories).
type BankTemplate struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Config overrides of the bank.
	Config *BankConfig `protobuf:"bytes,1,opt,name=config,proto3" json:"config,omitempty"`
	// Directives of the bank (ids are not carried: CreateBank assigns new ones). Names must be set and unique.
	Directives []*Directive `protobuf:"bytes,2,rep,name=directives,proto3" json:"directives,omitempty"`
	// Mental-model specs; content is rebuilt, not copied.
	MentalModels  []*MentalModelSpec `protobuf:"bytes,3,rep,name=mental_models,json=mentalModels,proto3" json:"mental_models,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *BankTemplate) Reset() {
	*x = BankTemplate{}
	mi := &file_memory_v1_memory_proto_msgTypes[82]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *BankTemplate) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*BankTemplate) ProtoMessage() {}

func (x *BankTemplate) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[82]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use BankTemplate.ProtoReflect.Descriptor instead.
func (*BankTemplate) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{82}
}

func (x *BankTemplate) GetConfig() *BankConfig {
	if x != nil {
		return x.Config
	}
	return nil
}

func (x *BankTemplate) GetDirectives() []*Directive {
	if x != nil {
		return x.Directives
	}
	return nil
}

func (x *BankTemplate) GetMentalModels() []*MentalModelSpec {
	if x != nil {
		return x.MentalModels
	}
	return nil
}

// GetBankRequest names one bank.
type GetBankRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank id; unknown, or being deleted = NOT_FOUND (BANK_NOT_FOUND).
	BankId        string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetBankRequest) Reset() {
	*x = GetBankRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[83]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetBankRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetBankRequest) ProtoMessage() {}

func (x *GetBankRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[83]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetBankRequest.ProtoReflect.Descriptor instead.
func (*GetBankRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{83}
}

func (x *GetBankRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

// GetBankResponse carries the bank.
type GetBankResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The bank; its config is ConfigService.GetBankConfig.
	Bank          *Bank `protobuf:"bytes,1,opt,name=bank,proto3" json:"bank,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetBankResponse) Reset() {
	*x = GetBankResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[84]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetBankResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetBankResponse) ProtoMessage() {}

func (x *GetBankResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[84]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetBankResponse.ProtoReflect.Descriptor instead.
func (*GetBankResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{84}
}

func (x *GetBankResponse) GetBank() *Bank {
	if x != nil {
		return x.Bank
	}
	return nil
}

// ListBanksRequest pages the tenant's banks.
type ListBanksRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Maximum banks per page; 0 = 100, at most 1000.
	PageSize int32 `protobuf:"varint,1,opt,name=page_size,json=pageSize,proto3" json:"page_size,omitempty"`
	// Token from a previous response; empty = first page.
	PageToken string `protobuf:"bytes,2,opt,name=page_token,json=pageToken,proto3" json:"page_token,omitempty"`
	// Only banks whose id starts with this; empty = all.
	IdPrefix      string `protobuf:"bytes,3,opt,name=id_prefix,json=idPrefix,proto3" json:"id_prefix,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListBanksRequest) Reset() {
	*x = ListBanksRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[85]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListBanksRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListBanksRequest) ProtoMessage() {}

func (x *ListBanksRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[85]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListBanksRequest.ProtoReflect.Descriptor instead.
func (*ListBanksRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{85}
}

func (x *ListBanksRequest) GetPageSize() int32 {
	if x != nil {
		return x.PageSize
	}
	return 0
}

func (x *ListBanksRequest) GetPageToken() string {
	if x != nil {
		return x.PageToken
	}
	return ""
}

func (x *ListBanksRequest) GetIdPrefix() string {
	if x != nil {
		return x.IdPrefix
	}
	return ""
}

// ListBanksResponse is one page of banks, in id order.
type ListBanksResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Banks on this page; banks being deleted are left out.
	Banks []*Bank `protobuf:"bytes,1,rep,name=banks,proto3" json:"banks,omitempty"`
	// Token for the next page; empty = no more.
	NextPageToken string `protobuf:"bytes,2,opt,name=next_page_token,json=nextPageToken,proto3" json:"next_page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListBanksResponse) Reset() {
	*x = ListBanksResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[86]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListBanksResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListBanksResponse) ProtoMessage() {}

func (x *ListBanksResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[86]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListBanksResponse.ProtoReflect.Descriptor instead.
func (*ListBanksResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{86}
}

func (x *ListBanksResponse) GetBanks() []*Bank {
	if x != nil {
		return x.Banks
	}
	return nil
}

func (x *ListBanksResponse) GetNextPageToken() string {
	if x != nil {
		return x.NextPageToken
	}
	return ""
}

// DeleteBankRequest names the bank to delete.
type DeleteBankRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank id; unknown = NOT_FOUND.
	BankId        string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DeleteBankRequest) Reset() {
	*x = DeleteBankRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[87]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DeleteBankRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeleteBankRequest) ProtoMessage() {}

func (x *DeleteBankRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[87]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeleteBankRequest.ProtoReflect.Descriptor instead.
func (*DeleteBankRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{87}
}

func (x *DeleteBankRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

// DeleteBankResponse carries the delete job.
type DeleteBankResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The delete_bank operation. Poll GetOperation with its id: NOT_FOUND means the bank is gone.
	Operation     *Operation `protobuf:"bytes,1,opt,name=operation,proto3" json:"operation,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DeleteBankResponse) Reset() {
	*x = DeleteBankResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[88]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DeleteBankResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeleteBankResponse) ProtoMessage() {}

func (x *DeleteBankResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[88]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeleteBankResponse.ProtoReflect.Descriptor instead.
func (*DeleteBankResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{88}
}

func (x *DeleteBankResponse) GetOperation() *Operation {
	if x != nil {
		return x.Operation
	}
	return nil
}

// GetBankStatsRequest names one bank.
type GetBankStatsRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank id; unknown = NOT_FOUND.
	BankId        string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetBankStatsRequest) Reset() {
	*x = GetBankStatsRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[89]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetBankStatsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetBankStatsRequest) ProtoMessage() {}

func (x *GetBankStatsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[89]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetBankStatsRequest.ProtoReflect.Descriptor instead.
func (*GetBankStatsRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{89}
}

func (x *GetBankStatsRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

// GetBankStatsResponse carries the stats.
type GetBankStatsResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Counts and change counter of the bank.
	Stats         *BankStats `protobuf:"bytes,1,opt,name=stats,proto3" json:"stats,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetBankStatsResponse) Reset() {
	*x = GetBankStatsResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[90]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetBankStatsResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetBankStatsResponse) ProtoMessage() {}

func (x *GetBankStatsResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[90]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetBankStatsResponse.ProtoReflect.Descriptor instead.
func (*GetBankStatsResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{90}
}

func (x *GetBankStatsResponse) GetStats() *BankStats {
	if x != nil {
		return x.Stats
	}
	return nil
}

// BankStats summarizes a bank, as a snapshot of its counts.
type BankStats struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Live memory counts keyed by lowercase fact type: "world", "experience" and "observation". A type with no
	// memories is absent.
	MemoriesByFactType map[string]int64 `protobuf:"bytes,1,rep,name=memories_by_fact_type,json=memoriesByFactType,proto3" json:"memories_by_fact_type,omitempty" protobuf_key:"bytes,1,opt,name=key" protobuf_val:"varint,2,opt,name=value"`
	// Memories archived by invalidation (count).
	InvalidatedMemories int64 `protobuf:"varint,2,opt,name=invalidated_memories,json=invalidatedMemories,proto3" json:"invalidated_memories,omitempty"`
	// Documents retained in the bank (count).
	Documents int64 `protobuf:"varint,3,opt,name=documents,proto3" json:"documents,omitempty"`
	// Entities (free-form and label) in the bank (count).
	Entities int64 `protobuf:"varint,4,opt,name=entities,proto3" json:"entities,omitempty"`
	// Mental models in the bank (count).
	MentalModels int64 `protobuf:"varint,5,opt,name=mental_models,json=mentalModels,proto3" json:"mental_models,omitempty"`
	// Pending and processing operation counts keyed by lowercase operation kind (for example "consolidate",
	// "retain_document"); a kind with none is absent. Eval barrier: poll until empty.
	PendingOperationsByKind map[string]int64 `protobuf:"bytes,6,rep,name=pending_operations_by_kind,json=pendingOperationsByKind,proto3" json:"pending_operations_by_kind,omitempty" protobuf_key:"bytes,1,opt,name=key" protobuf_val:"varint,2,opt,name=value"`
	// Live facts not yet consolidated (count).
	UnconsolidatedMemories int64 `protobuf:"varint,7,opt,name=unconsolidated_memories,json=unconsolidatedMemories,proto3" json:"unconsolidated_memories,omitempty"`
	// Time of the last write to memory content.
	LastMemoryWriteAt *timestamppb.Timestamp `protobuf:"bytes,8,opt,name=last_memory_write_at,json=lastMemoryWriteAt,proto3" json:"last_memory_write_at,omitempty"`
	// The bank's commit-ordered change counter: the same counter as LifecycleEvent.seq and
	// MentalModel.last_seq_seen.
	ChangeSeq     int64 `protobuf:"varint,9,opt,name=change_seq,json=changeSeq,proto3" json:"change_seq,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *BankStats) Reset() {
	*x = BankStats{}
	mi := &file_memory_v1_memory_proto_msgTypes[91]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *BankStats) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*BankStats) ProtoMessage() {}

func (x *BankStats) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[91]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use BankStats.ProtoReflect.Descriptor instead.
func (*BankStats) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{91}
}

func (x *BankStats) GetMemoriesByFactType() map[string]int64 {
	if x != nil {
		return x.MemoriesByFactType
	}
	return nil
}

func (x *BankStats) GetInvalidatedMemories() int64 {
	if x != nil {
		return x.InvalidatedMemories
	}
	return 0
}

func (x *BankStats) GetDocuments() int64 {
	if x != nil {
		return x.Documents
	}
	return 0
}

func (x *BankStats) GetEntities() int64 {
	if x != nil {
		return x.Entities
	}
	return 0
}

func (x *BankStats) GetMentalModels() int64 {
	if x != nil {
		return x.MentalModels
	}
	return 0
}

func (x *BankStats) GetPendingOperationsByKind() map[string]int64 {
	if x != nil {
		return x.PendingOperationsByKind
	}
	return nil
}

func (x *BankStats) GetUnconsolidatedMemories() int64 {
	if x != nil {
		return x.UnconsolidatedMemories
	}
	return 0
}

func (x *BankStats) GetLastMemoryWriteAt() *timestamppb.Timestamp {
	if x != nil {
		return x.LastMemoryWriteAt
	}
	return nil
}

func (x *BankStats) GetChangeSeq() int64 {
	if x != nil {
		return x.ChangeSeq
	}
	return 0
}

// ListTagsRequest pages the bank's tags.
type ListTagsRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank id; unknown = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Which objects to count tags on.
	Source ListTagsRequest_TagSource `protobuf:"varint,2,opt,name=source,proto3,enum=memory.v1.ListTagsRequest_TagSource" json:"source,omitempty"`
	// Only tags starting with this; empty = all.
	Prefix string `protobuf:"bytes,3,opt,name=prefix,proto3" json:"prefix,omitempty"`
	// Maximum tags per page; 0 = 100, at most 1000.
	PageSize int32 `protobuf:"varint,4,opt,name=page_size,json=pageSize,proto3" json:"page_size,omitempty"`
	// Token from a previous response; empty = first page.
	PageToken     string `protobuf:"bytes,5,opt,name=page_token,json=pageToken,proto3" json:"page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListTagsRequest) Reset() {
	*x = ListTagsRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[92]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListTagsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListTagsRequest) ProtoMessage() {}

func (x *ListTagsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[92]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListTagsRequest.ProtoReflect.Descriptor instead.
func (*ListTagsRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{92}
}

func (x *ListTagsRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *ListTagsRequest) GetSource() ListTagsRequest_TagSource {
	if x != nil {
		return x.Source
	}
	return ListTagsRequest_TAG_SOURCE_UNSPECIFIED
}

func (x *ListTagsRequest) GetPrefix() string {
	if x != nil {
		return x.Prefix
	}
	return ""
}

func (x *ListTagsRequest) GetPageSize() int32 {
	if x != nil {
		return x.PageSize
	}
	return 0
}

func (x *ListTagsRequest) GetPageToken() string {
	if x != nil {
		return x.PageToken
	}
	return ""
}

// ListTagsResponse is one page of tags.
type ListTagsResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Tags on this page.
	Tags []*TagCount `protobuf:"bytes,1,rep,name=tags,proto3" json:"tags,omitempty"`
	// Token for the next page; empty = no more.
	NextPageToken string `protobuf:"bytes,2,opt,name=next_page_token,json=nextPageToken,proto3" json:"next_page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListTagsResponse) Reset() {
	*x = ListTagsResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[93]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListTagsResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListTagsResponse) ProtoMessage() {}

func (x *ListTagsResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[93]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListTagsResponse.ProtoReflect.Descriptor instead.
func (*ListTagsResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{93}
}

func (x *ListTagsResponse) GetTags() []*TagCount {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *ListTagsResponse) GetNextPageToken() string {
	if x != nil {
		return x.NextPageToken
	}
	return ""
}

// TagCount is one tag with its usage count.
type TagCount struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The tag string as stored.
	Tag string `protobuf:"bytes,1,opt,name=tag,proto3" json:"tag,omitempty"`
	// Objects of the requested source carrying it.
	Count         int64 `protobuf:"varint,2,opt,name=count,proto3" json:"count,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *TagCount) Reset() {
	*x = TagCount{}
	mi := &file_memory_v1_memory_proto_msgTypes[94]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *TagCount) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TagCount) ProtoMessage() {}

func (x *TagCount) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[94]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TagCount.ProtoReflect.Descriptor instead.
func (*TagCount) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{94}
}

func (x *TagCount) GetTag() string {
	if x != nil {
		return x.Tag
	}
	return ""
}

func (x *TagCount) GetCount() int64 {
	if x != nil {
		return x.Count
	}
	return 0
}

// ExportBankTemplateRequest names the bank to export.
type ExportBankTemplateRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank id; unknown = NOT_FOUND.
	BankId        string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ExportBankTemplateRequest) Reset() {
	*x = ExportBankTemplateRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[95]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ExportBankTemplateRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ExportBankTemplateRequest) ProtoMessage() {}

func (x *ExportBankTemplateRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[95]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ExportBankTemplateRequest.ProtoReflect.Descriptor instead.
func (*ExportBankTemplateRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{95}
}

func (x *ExportBankTemplateRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

// ExportBankTemplateResponse carries the template.
type ExportBankTemplateResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Config overrides, directives and mental-model specs of the bank; pass it to CreateBank.template.
	Template      *BankTemplate `protobuf:"bytes,1,opt,name=template,proto3" json:"template,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ExportBankTemplateResponse) Reset() {
	*x = ExportBankTemplateResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[96]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ExportBankTemplateResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ExportBankTemplateResponse) ProtoMessage() {}

func (x *ExportBankTemplateResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[96]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ExportBankTemplateResponse.ProtoReflect.Descriptor instead.
func (*ExportBankTemplateResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{96}
}

func (x *ExportBankTemplateResponse) GetTemplate() *BankTemplate {
	if x != nil {
		return x.Template
	}
	return nil
}

// Bank is a memory bank. Its config is read and written through ConfigService.
type Bank struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank id, unique within the tenant.
	Id string `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	// Display name.
	Name string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	// When the bank was created.
	CreatedAt     *timestamppb.Timestamp `protobuf:"bytes,3,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Bank) Reset() {
	*x = Bank{}
	mi := &file_memory_v1_memory_proto_msgTypes[97]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Bank) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Bank) ProtoMessage() {}

func (x *Bank) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[97]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Bank.ProtoReflect.Descriptor instead.
func (*Bank) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{97}
}

func (x *Bank) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *Bank) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Bank) GetCreatedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.CreatedAt
	}
	return nil
}

// GetTenantConfigRequest is empty: the tenant comes from request metadata.
type GetTenantConfigRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetTenantConfigRequest) Reset() {
	*x = GetTenantConfigRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[98]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetTenantConfigRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetTenantConfigRequest) ProtoMessage() {}

func (x *GetTenantConfigRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[98]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetTenantConfigRequest.ProtoReflect.Descriptor instead.
func (*GetTenantConfigRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{98}
}

// GetTenantConfigResponse carries the tenant's config.
type GetTenantConfigResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// own: the tenant's overrides (unset fields inherit server defaults); effective: server < tenant.
	Config        *ConfigView `protobuf:"bytes,1,opt,name=config,proto3" json:"config,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetTenantConfigResponse) Reset() {
	*x = GetTenantConfigResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[99]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetTenantConfigResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetTenantConfigResponse) ProtoMessage() {}

func (x *GetTenantConfigResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[99]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetTenantConfigResponse.ProtoReflect.Descriptor instead.
func (*GetTenantConfigResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{99}
}

func (x *GetTenantConfigResponse) GetConfig() *ConfigView {
	if x != nil {
		return x.Config
	}
	return nil
}

// UpdateTenantConfigRequest patches the tenant config.
type UpdateTenantConfigRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// New values for the fields named in update_mask.
	Config *BankConfig `protobuf:"bytes,1,opt,name=config,proto3" json:"config,omitempty"`
	// BankConfig paths to replace; a path set to an empty value clears it (this is how a list is cleared).
	// Unset = every populated field of config.
	UpdateMask    *fieldmaskpb.FieldMask `protobuf:"bytes,2,opt,name=update_mask,json=updateMask,proto3" json:"update_mask,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *UpdateTenantConfigRequest) Reset() {
	*x = UpdateTenantConfigRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[100]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *UpdateTenantConfigRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdateTenantConfigRequest) ProtoMessage() {}

func (x *UpdateTenantConfigRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[100]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdateTenantConfigRequest.ProtoReflect.Descriptor instead.
func (*UpdateTenantConfigRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{100}
}

func (x *UpdateTenantConfigRequest) GetConfig() *BankConfig {
	if x != nil {
		return x.Config
	}
	return nil
}

func (x *UpdateTenantConfigRequest) GetUpdateMask() *fieldmaskpb.FieldMask {
	if x != nil {
		return x.UpdateMask
	}
	return nil
}

// UpdateTenantConfigResponse carries the tenant config after the patch.
type UpdateTenantConfigResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The tenant's config after the patch.
	Config        *ConfigView `protobuf:"bytes,1,opt,name=config,proto3" json:"config,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *UpdateTenantConfigResponse) Reset() {
	*x = UpdateTenantConfigResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[101]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *UpdateTenantConfigResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdateTenantConfigResponse) ProtoMessage() {}

func (x *UpdateTenantConfigResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[101]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdateTenantConfigResponse.ProtoReflect.Descriptor instead.
func (*UpdateTenantConfigResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{101}
}

func (x *UpdateTenantConfigResponse) GetConfig() *ConfigView {
	if x != nil {
		return x.Config
	}
	return nil
}

// ResetTenantConfigRequest removes some or all of the tenant's own config.
type ResetTenantConfigRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// BankConfig paths to reset, so they inherit the server defaults again; the single path "*" resets
	// everything. Empty = INVALID_ARGUMENT.
	ResetMask     *fieldmaskpb.FieldMask `protobuf:"bytes,1,opt,name=reset_mask,json=resetMask,proto3" json:"reset_mask,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ResetTenantConfigRequest) Reset() {
	*x = ResetTenantConfigRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[102]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ResetTenantConfigRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ResetTenantConfigRequest) ProtoMessage() {}

func (x *ResetTenantConfigRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[102]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ResetTenantConfigRequest.ProtoReflect.Descriptor instead.
func (*ResetTenantConfigRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{102}
}

func (x *ResetTenantConfigRequest) GetResetMask() *fieldmaskpb.FieldMask {
	if x != nil {
		return x.ResetMask
	}
	return nil
}

// ResetTenantConfigResponse carries the tenant config after the reset.
type ResetTenantConfigResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The tenant's config after the reset.
	Config        *ConfigView `protobuf:"bytes,1,opt,name=config,proto3" json:"config,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ResetTenantConfigResponse) Reset() {
	*x = ResetTenantConfigResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[103]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ResetTenantConfigResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ResetTenantConfigResponse) ProtoMessage() {}

func (x *ResetTenantConfigResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[103]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ResetTenantConfigResponse.ProtoReflect.Descriptor instead.
func (*ResetTenantConfigResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{103}
}

func (x *ResetTenantConfigResponse) GetConfig() *ConfigView {
	if x != nil {
		return x.Config
	}
	return nil
}

// GetBankConfigRequest names one bank.
type GetBankConfigRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank id; unknown, or being deleted = NOT_FOUND.
	BankId        string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetBankConfigRequest) Reset() {
	*x = GetBankConfigRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[104]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetBankConfigRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetBankConfigRequest) ProtoMessage() {}

func (x *GetBankConfigRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[104]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetBankConfigRequest.ProtoReflect.Descriptor instead.
func (*GetBankConfigRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{104}
}

func (x *GetBankConfigRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

// GetBankConfigResponse carries the bank's config.
type GetBankConfigResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// own: only what the bank itself sets; effective: server < tenant < bank, merged.
	Config        *ConfigView `protobuf:"bytes,1,opt,name=config,proto3" json:"config,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetBankConfigResponse) Reset() {
	*x = GetBankConfigResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[105]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetBankConfigResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetBankConfigResponse) ProtoMessage() {}

func (x *GetBankConfigResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[105]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetBankConfigResponse.ProtoReflect.Descriptor instead.
func (*GetBankConfigResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{105}
}

func (x *GetBankConfigResponse) GetConfig() *ConfigView {
	if x != nil {
		return x.Config
	}
	return nil
}

// UpdateBankConfigRequest patches the bank's own overrides. An unknown bank is created with tenant defaults.
type UpdateBankConfigRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank id.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// New values for the fields named in update_mask.
	Config *BankConfig `protobuf:"bytes,2,opt,name=config,proto3" json:"config,omitempty"`
	// BankConfig paths to replace (for example observations.dedup_threshold or models.extract; a model binding
	// is replaced whole). A path set to an empty value clears it (this is how a list is cleared). Unset = every
	// populated field of config.
	UpdateMask    *fieldmaskpb.FieldMask `protobuf:"bytes,3,opt,name=update_mask,json=updateMask,proto3" json:"update_mask,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *UpdateBankConfigRequest) Reset() {
	*x = UpdateBankConfigRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[106]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *UpdateBankConfigRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdateBankConfigRequest) ProtoMessage() {}

func (x *UpdateBankConfigRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[106]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdateBankConfigRequest.ProtoReflect.Descriptor instead.
func (*UpdateBankConfigRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{106}
}

func (x *UpdateBankConfigRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *UpdateBankConfigRequest) GetConfig() *BankConfig {
	if x != nil {
		return x.Config
	}
	return nil
}

func (x *UpdateBankConfigRequest) GetUpdateMask() *fieldmaskpb.FieldMask {
	if x != nil {
		return x.UpdateMask
	}
	return nil
}

// UpdateBankConfigResponse carries the bank's config after the patch.
type UpdateBankConfigResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The bank's config after the patch.
	Config        *ConfigView `protobuf:"bytes,1,opt,name=config,proto3" json:"config,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *UpdateBankConfigResponse) Reset() {
	*x = UpdateBankConfigResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[107]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *UpdateBankConfigResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdateBankConfigResponse) ProtoMessage() {}

func (x *UpdateBankConfigResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[107]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdateBankConfigResponse.ProtoReflect.Descriptor instead.
func (*UpdateBankConfigResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{107}
}

func (x *UpdateBankConfigResponse) GetConfig() *ConfigView {
	if x != nil {
		return x.Config
	}
	return nil
}

// ResetBankConfigRequest removes some or all of the bank's own overrides.
type ResetBankConfigRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank id.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// BankConfig paths to reset, so they inherit again; the single path "*" resets everything. Empty =
	// INVALID_ARGUMENT.
	ResetMask     *fieldmaskpb.FieldMask `protobuf:"bytes,2,opt,name=reset_mask,json=resetMask,proto3" json:"reset_mask,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ResetBankConfigRequest) Reset() {
	*x = ResetBankConfigRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[108]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ResetBankConfigRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ResetBankConfigRequest) ProtoMessage() {}

func (x *ResetBankConfigRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[108]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ResetBankConfigRequest.ProtoReflect.Descriptor instead.
func (*ResetBankConfigRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{108}
}

func (x *ResetBankConfigRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *ResetBankConfigRequest) GetResetMask() *fieldmaskpb.FieldMask {
	if x != nil {
		return x.ResetMask
	}
	return nil
}

// ResetBankConfigResponse carries the bank's config after the reset.
type ResetBankConfigResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The bank's config after the reset.
	Config        *ConfigView `protobuf:"bytes,1,opt,name=config,proto3" json:"config,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ResetBankConfigResponse) Reset() {
	*x = ResetBankConfigResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[109]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ResetBankConfigResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ResetBankConfigResponse) ProtoMessage() {}

func (x *ResetBankConfigResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[109]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ResetBankConfigResponse.ProtoReflect.Descriptor instead.
func (*ResetBankConfigResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{109}
}

func (x *ResetBankConfigResponse) GetConfig() *ConfigView {
	if x != nil {
		return x.Config
	}
	return nil
}

// PreviewPromptRequest asks for a prompt rendering.
type PreviewPromptRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank whose effective config is used.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Operation to render.
	Operation PreviewPromptRequest_PromptOperation `protobuf:"varint,2,opt,name=operation,proto3,enum=memory.v1.PreviewPromptRequest_PromptOperation" json:"operation,omitempty"`
	// Request-level overrides to render with, as a real request would. The kind must match operation (retain
	// for RETAIN, consolidation for CONSOLIDATION, reflect for REFLECT, mental_model for MENTAL_MODEL_DELTA and
	// SYNTHESIZE); a mismatch is INVALID_ARGUMENT. Unset = none.
	//
	// Types that are valid to be assigned to Overrides:
	//
	//	*PreviewPromptRequest_Retain
	//	*PreviewPromptRequest_Consolidation
	//	*PreviewPromptRequest_Reflect
	//	*PreviewPromptRequest_MentalModel
	Overrides     isPreviewPromptRequest_Overrides `protobuf_oneof:"overrides"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PreviewPromptRequest) Reset() {
	*x = PreviewPromptRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[110]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PreviewPromptRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PreviewPromptRequest) ProtoMessage() {}

func (x *PreviewPromptRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[110]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PreviewPromptRequest.ProtoReflect.Descriptor instead.
func (*PreviewPromptRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{110}
}

func (x *PreviewPromptRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *PreviewPromptRequest) GetOperation() PreviewPromptRequest_PromptOperation {
	if x != nil {
		return x.Operation
	}
	return PreviewPromptRequest_PROMPT_OPERATION_UNSPECIFIED
}

func (x *PreviewPromptRequest) GetOverrides() isPreviewPromptRequest_Overrides {
	if x != nil {
		return x.Overrides
	}
	return nil
}

func (x *PreviewPromptRequest) GetRetain() *RetainOverrides {
	if x != nil {
		if x, ok := x.Overrides.(*PreviewPromptRequest_Retain); ok {
			return x.Retain
		}
	}
	return nil
}

func (x *PreviewPromptRequest) GetConsolidation() *ConsolidateOverrides {
	if x != nil {
		if x, ok := x.Overrides.(*PreviewPromptRequest_Consolidation); ok {
			return x.Consolidation
		}
	}
	return nil
}

func (x *PreviewPromptRequest) GetReflect() *ReflectOverrides {
	if x != nil {
		if x, ok := x.Overrides.(*PreviewPromptRequest_Reflect); ok {
			return x.Reflect
		}
	}
	return nil
}

func (x *PreviewPromptRequest) GetMentalModel() *RefreshOverrides {
	if x != nil {
		if x, ok := x.Overrides.(*PreviewPromptRequest_MentalModel); ok {
			return x.MentalModel
		}
	}
	return nil
}

type isPreviewPromptRequest_Overrides interface {
	isPreviewPromptRequest_Overrides()
}

type PreviewPromptRequest_Retain struct {
	// Overrides of a Retain.
	Retain *RetainOverrides `protobuf:"bytes,3,opt,name=retain,proto3,oneof"`
}

type PreviewPromptRequest_Consolidation struct {
	// Overrides of a Consolidate.
	Consolidation *ConsolidateOverrides `protobuf:"bytes,4,opt,name=consolidation,proto3,oneof"`
}

type PreviewPromptRequest_Reflect struct {
	// Overrides of a Reflect.
	Reflect *ReflectOverrides `protobuf:"bytes,5,opt,name=reflect,proto3,oneof"`
}

type PreviewPromptRequest_MentalModel struct {
	// Overrides of a RefreshMentalModel.
	MentalModel *RefreshOverrides `protobuf:"bytes,6,opt,name=mental_model,json=mentalModel,proto3,oneof"`
}

func (*PreviewPromptRequest_Retain) isPreviewPromptRequest_Overrides() {}

func (*PreviewPromptRequest_Consolidation) isPreviewPromptRequest_Overrides() {}

func (*PreviewPromptRequest_Reflect) isPreviewPromptRequest_Overrides() {}

func (*PreviewPromptRequest_MentalModel) isPreviewPromptRequest_Overrides() {}

// PreviewPromptResponse carries the rendering.
type PreviewPromptResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The rendered messages and response schema.
	Preview       *PromptPreview `protobuf:"bytes,1,opt,name=preview,proto3" json:"preview,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PreviewPromptResponse) Reset() {
	*x = PreviewPromptResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[111]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PreviewPromptResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PreviewPromptResponse) ProtoMessage() {}

func (x *PreviewPromptResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[111]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PreviewPromptResponse.ProtoReflect.Descriptor instead.
func (*PreviewPromptResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{111}
}

func (x *PreviewPromptResponse) GetPreview() *PromptPreview {
	if x != nil {
		return x.Preview
	}
	return nil
}

// PromptPreview is the full rendering for one operation.
type PromptPreview struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Messages in send order.
	Messages []*PromptMessage `protobuf:"bytes,1,rep,name=messages,proto3" json:"messages,omitempty"`
	// JSON Schema the response must match, if the operation uses one.
	ResponseSchemaJson string `protobuf:"bytes,2,opt,name=response_schema_json,json=responseSchemaJson,proto3" json:"response_schema_json,omitempty"`
	// Why nothing would be sent (for example extraction mode chunks needs no LLM); empty otherwise.
	SkippedReason string `protobuf:"bytes,3,opt,name=skipped_reason,json=skippedReason,proto3" json:"skipped_reason,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PromptPreview) Reset() {
	*x = PromptPreview{}
	mi := &file_memory_v1_memory_proto_msgTypes[112]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PromptPreview) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PromptPreview) ProtoMessage() {}

func (x *PromptPreview) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[112]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PromptPreview.ProtoReflect.Descriptor instead.
func (*PromptPreview) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{112}
}

func (x *PromptPreview) GetMessages() []*PromptMessage {
	if x != nil {
		return x.Messages
	}
	return nil
}

func (x *PromptPreview) GetResponseSchemaJson() string {
	if x != nil {
		return x.ResponseSchemaJson
	}
	return ""
}

func (x *PromptPreview) GetSkippedReason() string {
	if x != nil {
		return x.SkippedReason
	}
	return ""
}

// PromptMessage is one rendered chat message.
type PromptMessage struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Chat role.
	Role PromptMessage_PromptRole `protobuf:"varint,1,opt,name=role,proto3,enum=memory.v1.PromptMessage_PromptRole" json:"role,omitempty"`
	// Blocks in order; their texts concatenate into the message.
	Blocks        []*PromptBlock `protobuf:"bytes,2,rep,name=blocks,proto3" json:"blocks,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PromptMessage) Reset() {
	*x = PromptMessage{}
	mi := &file_memory_v1_memory_proto_msgTypes[113]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PromptMessage) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PromptMessage) ProtoMessage() {}

func (x *PromptMessage) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[113]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PromptMessage.ProtoReflect.Descriptor instead.
func (*PromptMessage) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{113}
}

func (x *PromptMessage) GetRole() PromptMessage_PromptRole {
	if x != nil {
		return x.Role
	}
	return PromptMessage_PROMPT_ROLE_UNSPECIFIED
}

func (x *PromptMessage) GetBlocks() []*PromptBlock {
	if x != nil {
		return x.Blocks
	}
	return nil
}

// PromptBlock is one block of a rendered message, attributed to its source.
type PromptBlock struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Rendered text; empty when the block is inactive.
	Text string `protobuf:"bytes,1,opt,name=text,proto3" json:"text,omitempty"`
	// Built-in or config text.
	Source PromptBlock_PromptSource `protobuf:"varint,2,opt,name=source,proto3,enum=memory.v1.PromptBlock_PromptSource" json:"source,omitempty"`
	// BankConfig field path behind the block (for example retain.mission), if any.
	Field string `protobuf:"bytes,3,opt,name=field,proto3" json:"field,omitempty"`
	// Prompt section the block belongs to, for example disposition or directives.
	Section string `protobuf:"bytes,4,opt,name=section,proto3" json:"section,omitempty"`
	// Whether the block is rendered with the current config.
	Active        bool `protobuf:"varint,5,opt,name=active,proto3" json:"active,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PromptBlock) Reset() {
	*x = PromptBlock{}
	mi := &file_memory_v1_memory_proto_msgTypes[114]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PromptBlock) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PromptBlock) ProtoMessage() {}

func (x *PromptBlock) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[114]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PromptBlock.ProtoReflect.Descriptor instead.
func (*PromptBlock) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{114}
}

func (x *PromptBlock) GetText() string {
	if x != nil {
		return x.Text
	}
	return ""
}

func (x *PromptBlock) GetSource() PromptBlock_PromptSource {
	if x != nil {
		return x.Source
	}
	return PromptBlock_PROMPT_SOURCE_UNSPECIFIED
}

func (x *PromptBlock) GetField() string {
	if x != nil {
		return x.Field
	}
	return ""
}

func (x *PromptBlock) GetSection() string {
	if x != nil {
		return x.Section
	}
	return ""
}

func (x *PromptBlock) GetActive() bool {
	if x != nil {
		return x.Active
	}
	return false
}

// CreateDirectiveRequest adds a directive to a bank. An unknown bank is created with tenant defaults.
type CreateDirectiveRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the directive.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// The directive. Its id is assigned by the server (a set id is ignored); its name is required and must be
	// new in the bank (taken = ALREADY_EXISTS).
	Directive     *Directive `protobuf:"bytes,2,opt,name=directive,proto3" json:"directive,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *CreateDirectiveRequest) Reset() {
	*x = CreateDirectiveRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[115]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CreateDirectiveRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CreateDirectiveRequest) ProtoMessage() {}

func (x *CreateDirectiveRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[115]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CreateDirectiveRequest.ProtoReflect.Descriptor instead.
func (*CreateDirectiveRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{115}
}

func (x *CreateDirectiveRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *CreateDirectiveRequest) GetDirective() *Directive {
	if x != nil {
		return x.Directive
	}
	return nil
}

// CreateDirectiveResponse carries the stored directive.
type CreateDirectiveResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The directive, with its id set and its tags normalized.
	Directive     *Directive `protobuf:"bytes,1,opt,name=directive,proto3" json:"directive,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *CreateDirectiveResponse) Reset() {
	*x = CreateDirectiveResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[116]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CreateDirectiveResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CreateDirectiveResponse) ProtoMessage() {}

func (x *CreateDirectiveResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[116]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CreateDirectiveResponse.ProtoReflect.Descriptor instead.
func (*CreateDirectiveResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{116}
}

func (x *CreateDirectiveResponse) GetDirective() *Directive {
	if x != nil {
		return x.Directive
	}
	return nil
}

// UpdateDirectiveRequest patches one directive.
type UpdateDirectiveRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the directive; unknown = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Directive id; unknown = NOT_FOUND.
	DirectiveId string `protobuf:"bytes,2,opt,name=directive_id,json=directiveId,proto3" json:"directive_id,omitempty"`
	// New values for the fields named in update_mask.
	Directive *Directive `protobuf:"bytes,3,opt,name=directive,proto3" json:"directive,omitempty"`
	// Directive paths to replace: name, rules, tags, active (id cannot be changed). A masked field left unset
	// clears it (so active=false needs an explicit mask); the name must stay set and unique in the bank (taken =
	// ALREADY_EXISTS). Unset = every populated field of directive.
	UpdateMask    *fieldmaskpb.FieldMask `protobuf:"bytes,4,opt,name=update_mask,json=updateMask,proto3" json:"update_mask,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *UpdateDirectiveRequest) Reset() {
	*x = UpdateDirectiveRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[117]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *UpdateDirectiveRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdateDirectiveRequest) ProtoMessage() {}

func (x *UpdateDirectiveRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[117]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdateDirectiveRequest.ProtoReflect.Descriptor instead.
func (*UpdateDirectiveRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{117}
}

func (x *UpdateDirectiveRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *UpdateDirectiveRequest) GetDirectiveId() string {
	if x != nil {
		return x.DirectiveId
	}
	return ""
}

func (x *UpdateDirectiveRequest) GetDirective() *Directive {
	if x != nil {
		return x.Directive
	}
	return nil
}

func (x *UpdateDirectiveRequest) GetUpdateMask() *fieldmaskpb.FieldMask {
	if x != nil {
		return x.UpdateMask
	}
	return nil
}

// UpdateDirectiveResponse carries the directive after the patch.
type UpdateDirectiveResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The directive, with its tags normalized.
	Directive     *Directive `protobuf:"bytes,1,opt,name=directive,proto3" json:"directive,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *UpdateDirectiveResponse) Reset() {
	*x = UpdateDirectiveResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[118]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *UpdateDirectiveResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdateDirectiveResponse) ProtoMessage() {}

func (x *UpdateDirectiveResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[118]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdateDirectiveResponse.ProtoReflect.Descriptor instead.
func (*UpdateDirectiveResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{118}
}

func (x *UpdateDirectiveResponse) GetDirective() *Directive {
	if x != nil {
		return x.Directive
	}
	return nil
}

// ListDirectivesRequest filters and pages the bank's directives.
type ListDirectivesRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank id; unknown = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Tag filter on the directives' tags; unset or empty tags = no filter. Flat form only; tags.match unset =
	// TAGS_MATCH_ANY_STRICT (directives sharing a tag; untagged ones are left out).
	Tags *TagFilter `protobuf:"bytes,2,opt,name=tags,proto3" json:"tags,omitempty"`
	// Only active directives.
	ActiveOnly bool `protobuf:"varint,3,opt,name=active_only,json=activeOnly,proto3" json:"active_only,omitempty"`
	// Maximum directives per page; 0 = 100, at most 1000.
	PageSize int32 `protobuf:"varint,4,opt,name=page_size,json=pageSize,proto3" json:"page_size,omitempty"`
	// Token from a previous response; empty = first page.
	PageToken     string `protobuf:"bytes,5,opt,name=page_token,json=pageToken,proto3" json:"page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListDirectivesRequest) Reset() {
	*x = ListDirectivesRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[119]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListDirectivesRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListDirectivesRequest) ProtoMessage() {}

func (x *ListDirectivesRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[119]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListDirectivesRequest.ProtoReflect.Descriptor instead.
func (*ListDirectivesRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{119}
}

func (x *ListDirectivesRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *ListDirectivesRequest) GetTags() *TagFilter {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *ListDirectivesRequest) GetActiveOnly() bool {
	if x != nil {
		return x.ActiveOnly
	}
	return false
}

func (x *ListDirectivesRequest) GetPageSize() int32 {
	if x != nil {
		return x.PageSize
	}
	return 0
}

func (x *ListDirectivesRequest) GetPageToken() string {
	if x != nil {
		return x.PageToken
	}
	return ""
}

// ListDirectivesResponse is one page of directives, oldest first.
type ListDirectivesResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Directives on this page.
	Directives []*Directive `protobuf:"bytes,1,rep,name=directives,proto3" json:"directives,omitempty"`
	// Token for the next page; empty = no more.
	NextPageToken string `protobuf:"bytes,2,opt,name=next_page_token,json=nextPageToken,proto3" json:"next_page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListDirectivesResponse) Reset() {
	*x = ListDirectivesResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[120]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListDirectivesResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListDirectivesResponse) ProtoMessage() {}

func (x *ListDirectivesResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[120]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListDirectivesResponse.ProtoReflect.Descriptor instead.
func (*ListDirectivesResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{120}
}

func (x *ListDirectivesResponse) GetDirectives() []*Directive {
	if x != nil {
		return x.Directives
	}
	return nil
}

func (x *ListDirectivesResponse) GetNextPageToken() string {
	if x != nil {
		return x.NextPageToken
	}
	return ""
}

// DeleteDirectiveRequest names the directive to delete.
type DeleteDirectiveRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the directive.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Directive id.
	DirectiveId   string `protobuf:"bytes,2,opt,name=directive_id,json=directiveId,proto3" json:"directive_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DeleteDirectiveRequest) Reset() {
	*x = DeleteDirectiveRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[121]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DeleteDirectiveRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeleteDirectiveRequest) ProtoMessage() {}

func (x *DeleteDirectiveRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[121]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeleteDirectiveRequest.ProtoReflect.Descriptor instead.
func (*DeleteDirectiveRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{121}
}

func (x *DeleteDirectiveRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *DeleteDirectiveRequest) GetDirectiveId() string {
	if x != nil {
		return x.DirectiveId
	}
	return ""
}

// DeleteDirectiveResponse is empty on success.
type DeleteDirectiveResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DeleteDirectiveResponse) Reset() {
	*x = DeleteDirectiveResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[122]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DeleteDirectiveResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeleteDirectiveResponse) ProtoMessage() {}

func (x *DeleteDirectiveResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[122]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeleteDirectiveResponse.ProtoReflect.Descriptor instead.
func (*DeleteDirectiveResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{122}
}

// ConfigView is one config level as every config RPC returns it.
type ConfigView struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Only what this level itself sets (the tenant's config, or the bank's own overrides).
	Own *BankConfig `protobuf:"bytes,1,opt,name=own,proto3" json:"own,omitempty"`
	// The config this level resolves to: server < tenant for a tenant, server < tenant < bank for a bank.
	Effective     *BankConfig `protobuf:"bytes,2,opt,name=effective,proto3" json:"effective,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ConfigView) Reset() {
	*x = ConfigView{}
	mi := &file_memory_v1_memory_proto_msgTypes[123]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ConfigView) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ConfigView) ProtoMessage() {}

func (x *ConfigView) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[123]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ConfigView.ProtoReflect.Descriptor instead.
func (*ConfigView) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{123}
}

func (x *ConfigView) GetOwn() *BankConfig {
	if x != nil {
		return x.Own
	}
	return nil
}

func (x *ConfigView) GetEffective() *BankConfig {
	if x != nil {
		return x.Effective
	}
	return nil
}

// Directive is a standing instruction injected into reflect prompts. Active directives whose tags match
// the request apply; untagged directives are global.
type Directive struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Directive id, assigned by CreateDirective.
	Id string `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	// Name, unique within the bank; required.
	Name string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	// Rules, rendered into the reflect system prompt in order.
	Rules []string `protobuf:"bytes,3,rep,name=rules,proto3" json:"rules,omitempty"`
	// Tags the directive applies to; empty = every request.
	Tags []string `protobuf:"bytes,4,rep,name=tags,proto3" json:"tags,omitempty"`
	// Inactive directives are stored but never applied.
	Active        bool `protobuf:"varint,5,opt,name=active,proto3" json:"active,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Directive) Reset() {
	*x = Directive{}
	mi := &file_memory_v1_memory_proto_msgTypes[124]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Directive) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Directive) ProtoMessage() {}

func (x *Directive) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[124]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Directive.ProtoReflect.Descriptor instead.
func (*Directive) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{124}
}

func (x *Directive) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *Directive) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Directive) GetRules() []string {
	if x != nil {
		return x.Rules
	}
	return nil
}

func (x *Directive) GetTags() []string {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *Directive) GetActive() bool {
	if x != nil {
		return x.Active
	}
	return false
}

// GetOperationRequest names one operation.
type GetOperationRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the operation; unknown bank = NOT_FOUND. A bank being deleted serves only its delete_bank
	// operations.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Operation id.
	OperationId   string `protobuf:"bytes,2,opt,name=operation_id,json=operationId,proto3" json:"operation_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetOperationRequest) Reset() {
	*x = GetOperationRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[125]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetOperationRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetOperationRequest) ProtoMessage() {}

func (x *GetOperationRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[125]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetOperationRequest.ProtoReflect.Descriptor instead.
func (*GetOperationRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{125}
}

func (x *GetOperationRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *GetOperationRequest) GetOperationId() string {
	if x != nil {
		return x.OperationId
	}
	return ""
}

// GetOperationResponse carries the operation.
type GetOperationResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The operation, with its current status, progress and usage.
	Operation     *Operation `protobuf:"bytes,1,opt,name=operation,proto3" json:"operation,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetOperationResponse) Reset() {
	*x = GetOperationResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[126]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetOperationResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetOperationResponse) ProtoMessage() {}

func (x *GetOperationResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[126]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetOperationResponse.ProtoReflect.Descriptor instead.
func (*GetOperationResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{126}
}

func (x *GetOperationResponse) GetOperation() *Operation {
	if x != nil {
		return x.Operation
	}
	return nil
}

// ListOperationsRequest filters and pages the bank's operations.
type ListOperationsRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank to list; unknown bank = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Only this status; unspecified = any.
	Status OperationStatus `protobuf:"varint,2,opt,name=status,proto3,enum=memory.v1.OperationStatus" json:"status,omitempty"`
	// Only this kind; unspecified = any.
	Kind OperationKind `protobuf:"varint,3,opt,name=kind,proto3,enum=memory.v1.OperationKind" json:"kind,omitempty"`
	// Omit retain_batch parents, listing only operations that do work.
	ExcludeParents bool `protobuf:"varint,4,opt,name=exclude_parents,json=excludeParents,proto3" json:"exclude_parents,omitempty"`
	// Maximum operations per page; 0 = 100, at most 1000.
	PageSize int32 `protobuf:"varint,5,opt,name=page_size,json=pageSize,proto3" json:"page_size,omitempty"`
	// Token from a previous response; empty = first page.
	PageToken     string `protobuf:"bytes,6,opt,name=page_token,json=pageToken,proto3" json:"page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListOperationsRequest) Reset() {
	*x = ListOperationsRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[127]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListOperationsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListOperationsRequest) ProtoMessage() {}

func (x *ListOperationsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[127]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListOperationsRequest.ProtoReflect.Descriptor instead.
func (*ListOperationsRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{127}
}

func (x *ListOperationsRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *ListOperationsRequest) GetStatus() OperationStatus {
	if x != nil {
		return x.Status
	}
	return OperationStatus_OPERATION_STATUS_UNSPECIFIED
}

func (x *ListOperationsRequest) GetKind() OperationKind {
	if x != nil {
		return x.Kind
	}
	return OperationKind_OPERATION_KIND_UNSPECIFIED
}

func (x *ListOperationsRequest) GetExcludeParents() bool {
	if x != nil {
		return x.ExcludeParents
	}
	return false
}

func (x *ListOperationsRequest) GetPageSize() int32 {
	if x != nil {
		return x.PageSize
	}
	return 0
}

func (x *ListOperationsRequest) GetPageToken() string {
	if x != nil {
		return x.PageToken
	}
	return ""
}

// ListOperationsResponse is one page of operations.
type ListOperationsResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Operations on this page.
	Operations []*Operation `protobuf:"bytes,1,rep,name=operations,proto3" json:"operations,omitempty"`
	// Token for the next page; empty = no more.
	NextPageToken string `protobuf:"bytes,2,opt,name=next_page_token,json=nextPageToken,proto3" json:"next_page_token,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListOperationsResponse) Reset() {
	*x = ListOperationsResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[128]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListOperationsResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListOperationsResponse) ProtoMessage() {}

func (x *ListOperationsResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[128]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListOperationsResponse.ProtoReflect.Descriptor instead.
func (*ListOperationsResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{128}
}

func (x *ListOperationsResponse) GetOperations() []*Operation {
	if x != nil {
		return x.Operations
	}
	return nil
}

func (x *ListOperationsResponse) GetNextPageToken() string {
	if x != nil {
		return x.NextPageToken
	}
	return ""
}

// CancelOperationRequest names the operation to cancel.
type CancelOperationRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the operation.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Operation id; must be PENDING or PROCESSING.
	OperationId   string `protobuf:"bytes,2,opt,name=operation_id,json=operationId,proto3" json:"operation_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *CancelOperationRequest) Reset() {
	*x = CancelOperationRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[129]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CancelOperationRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CancelOperationRequest) ProtoMessage() {}

func (x *CancelOperationRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[129]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CancelOperationRequest.ProtoReflect.Descriptor instead.
func (*CancelOperationRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{129}
}

func (x *CancelOperationRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *CancelOperationRequest) GetOperationId() string {
	if x != nil {
		return x.OperationId
	}
	return ""
}

// CancelOperationResponse carries the cancelled operation.
type CancelOperationResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The operation, now CANCELLED.
	Operation     *Operation `protobuf:"bytes,1,opt,name=operation,proto3" json:"operation,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *CancelOperationResponse) Reset() {
	*x = CancelOperationResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[130]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CancelOperationResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CancelOperationResponse) ProtoMessage() {}

func (x *CancelOperationResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[130]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CancelOperationResponse.ProtoReflect.Descriptor instead.
func (*CancelOperationResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{130}
}

func (x *CancelOperationResponse) GetOperation() *Operation {
	if x != nil {
		return x.Operation
	}
	return nil
}

// RetryOperationRequest names the operation to retry.
type RetryOperationRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank owning the operation.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Operation id; must be FAILED or CANCELLED (a cancel-requested PROCESSING operation is refused).
	OperationId   string `protobuf:"bytes,2,opt,name=operation_id,json=operationId,proto3" json:"operation_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RetryOperationRequest) Reset() {
	*x = RetryOperationRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[131]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RetryOperationRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RetryOperationRequest) ProtoMessage() {}

func (x *RetryOperationRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[131]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RetryOperationRequest.ProtoReflect.Descriptor instead.
func (*RetryOperationRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{131}
}

func (x *RetryOperationRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *RetryOperationRequest) GetOperationId() string {
	if x != nil {
		return x.OperationId
	}
	return ""
}

// RetryOperationResponse carries the re-queued operation.
type RetryOperationResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The operation, now PENDING with next_run_at = now.
	Operation     *Operation `protobuf:"bytes,1,opt,name=operation,proto3" json:"operation,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RetryOperationResponse) Reset() {
	*x = RetryOperationResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[132]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RetryOperationResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RetryOperationResponse) ProtoMessage() {}

func (x *RetryOperationResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[132]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RetryOperationResponse.ProtoReflect.Descriptor instead.
func (*RetryOperationResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{132}
}

func (x *RetryOperationResponse) GetOperation() *Operation {
	if x != nil {
		return x.Operation
	}
	return nil
}

// ListEventsRequest polls a bank's lifecycle events after a known sequence number.
type ListEventsRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Bank to read; unknown bank = NOT_FOUND.
	BankId string `protobuf:"bytes,1,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Return only events with seq greater than this (the previous response's next_after_seq); 0 = from the
	// oldest retained event.
	AfterSeq int64 `protobuf:"varint,2,opt,name=after_seq,json=afterSeq,proto3" json:"after_seq,omitempty"`
	// Soft maximum of events per page; 0 = 100, at most 1000, negative = INVALID_ARGUMENT. A page never
	// splits a seq group: it is extended past page_size to finish the last seq it includes.
	PageSize int32 `protobuf:"varint,3,opt,name=page_size,json=pageSize,proto3" json:"page_size,omitempty"`
	// Only these event types; empty = all types.
	Types         []EventType `protobuf:"varint,4,rep,packed,name=types,proto3,enum=memory.v1.EventType" json:"types,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListEventsRequest) Reset() {
	*x = ListEventsRequest{}
	mi := &file_memory_v1_memory_proto_msgTypes[133]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListEventsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListEventsRequest) ProtoMessage() {}

func (x *ListEventsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[133]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListEventsRequest.ProtoReflect.Descriptor instead.
func (*ListEventsRequest) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{133}
}

func (x *ListEventsRequest) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *ListEventsRequest) GetAfterSeq() int64 {
	if x != nil {
		return x.AfterSeq
	}
	return 0
}

func (x *ListEventsRequest) GetPageSize() int32 {
	if x != nil {
		return x.PageSize
	}
	return 0
}

func (x *ListEventsRequest) GetTypes() []EventType {
	if x != nil {
		return x.Types
	}
	return nil
}

// ListEventsResponse returns events in seq order.
type ListEventsResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Events with seq > after_seq, ascending.
	Events []*LifecycleEvent `protobuf:"bytes,1,rep,name=events,proto3" json:"events,omitempty"`
	// Seq to pass as after_seq on the next poll: the highest seq scanned for this page, including events
	// the types filter dropped, and never below the request's after_seq (so a filter over non-matching
	// events still advances). An integer cursor a consumer can persist.
	NextAfterSeq  int64 `protobuf:"varint,2,opt,name=next_after_seq,json=nextAfterSeq,proto3" json:"next_after_seq,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListEventsResponse) Reset() {
	*x = ListEventsResponse{}
	mi := &file_memory_v1_memory_proto_msgTypes[134]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListEventsResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListEventsResponse) ProtoMessage() {}

func (x *ListEventsResponse) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[134]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListEventsResponse.ProtoReflect.Descriptor instead.
func (*ListEventsResponse) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{134}
}

func (x *ListEventsResponse) GetEvents() []*LifecycleEvent {
	if x != nil {
		return x.Events
	}
	return nil
}

func (x *ListEventsResponse) GetNextAfterSeq() int64 {
	if x != nil {
		return x.NextAfterSeq
	}
	return 0
}

// LifecycleEvent records one state change of a bank.
type LifecycleEvent struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The bank's change_seq for the change: commit-ordered within the bank, so polling by seq never skips.
	// All events written by one transaction share its seq.
	Seq int64 `protobuf:"varint,1,opt,name=seq,proto3" json:"seq,omitempty"`
	// What happened.
	Type EventType `protobuf:"varint,2,opt,name=type,proto3,enum=memory.v1.EventType" json:"type,omitempty"`
	// Bank the event belongs to.
	BankId string `protobuf:"bytes,3,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Commit time of the change.
	CreatedAt *timestamppb.Timestamp `protobuf:"bytes,4,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`
	// Operation that caused the change, if any.
	OperationId string `protobuf:"bytes,5,opt,name=operation_id,json=operationId,proto3" json:"operation_id,omitempty"`
	// Document, memory or mental-model id the event is about.
	SubjectId     string `protobuf:"bytes,6,opt,name=subject_id,json=subjectId,proto3" json:"subject_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *LifecycleEvent) Reset() {
	*x = LifecycleEvent{}
	mi := &file_memory_v1_memory_proto_msgTypes[135]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *LifecycleEvent) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*LifecycleEvent) ProtoMessage() {}

func (x *LifecycleEvent) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[135]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use LifecycleEvent.ProtoReflect.Descriptor instead.
func (*LifecycleEvent) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{135}
}

func (x *LifecycleEvent) GetSeq() int64 {
	if x != nil {
		return x.Seq
	}
	return 0
}

func (x *LifecycleEvent) GetType() EventType {
	if x != nil {
		return x.Type
	}
	return EventType_EVENT_TYPE_UNSPECIFIED
}

func (x *LifecycleEvent) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *LifecycleEvent) GetCreatedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.CreatedAt
	}
	return nil
}

func (x *LifecycleEvent) GetOperationId() string {
	if x != nil {
		return x.OperationId
	}
	return ""
}

func (x *LifecycleEvent) GetSubjectId() string {
	if x != nil {
		return x.SubjectId
	}
	return ""
}

// Operation is one background job: what it does, where it is, and what it used.
type Operation struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Operation (job) id.
	Id string `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	// Bank the operation belongs to; GetOperation, CancelOperation and RetryOperation take it with id.
	BankId string `protobuf:"bytes,2,opt,name=bank_id,json=bankId,proto3" json:"bank_id,omitempty"`
	// Job type.
	Kind OperationKind `protobuf:"varint,3,opt,name=kind,proto3,enum=memory.v1.OperationKind" json:"kind,omitempty"`
	// Lifecycle state.
	Status OperationStatus `protobuf:"varint,4,opt,name=status,proto3,enum=memory.v1.OperationStatus" json:"status,omitempty"`
	// Last error message; set when FAILED.
	Error string `protobuf:"bytes,5,opt,name=error,proto3" json:"error,omitempty"`
	// A kind-specific summary of the outcome as a JSON object, for people and logs. It is NOT a stable
	// contract: keys may change in any release, so never parse it in client logic.
	DebugSummary string `protobuf:"bytes,6,opt,name=debug_summary,json=debugSummary,proto3" json:"debug_summary,omitempty"`
	// Token usage per slot and model of every LLM call the job made.
	Usage []*SlotUsage `protobuf:"bytes,7,rep,name=usage,proto3" json:"usage,omitempty"`
	// The overrides the run actually used (request, strategy and stored overrides merged).
	EffectiveOverrides *BankConfig `protobuf:"bytes,8,opt,name=effective_overrides,json=effectiveOverrides,proto3" json:"effective_overrides,omitempty"`
	// Parent operation (a retain_batch); empty for top-level operations.
	ParentId string `protobuf:"bytes,9,opt,name=parent_id,json=parentId,proto3" json:"parent_id,omitempty"`
	// Last reported checkpoint.
	Progress *Progress `protobuf:"bytes,10,opt,name=progress,proto3" json:"progress,omitempty"`
	// When the operation was enqueued.
	CreatedAt *timestamppb.Timestamp `protobuf:"bytes,11,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`
	// Last change to the operation.
	UpdatedAt *timestamppb.Timestamp `protobuf:"bytes,12,opt,name=updated_at,json=updatedAt,proto3" json:"updated_at,omitempty"`
	// When a parked operation becomes runnable: backoff, debounce or refresh interval floor.
	NextRunAt *timestamppb.Timestamp `protobuf:"bytes,13,opt,name=next_run_at,json=nextRunAt,proto3" json:"next_run_at,omitempty"`
	// Earlier attempts that failed with a retry or were reclaimed (0 on the first run); transient failures
	// retry with backoff up to the kind's limit (consolidate: unbounded). With next_run_at it tells a
	// retrying operation from a parked one.
	Attempts int32 `protobuf:"varint,14,opt,name=attempts,proto3" json:"attempts,omitempty"`
	// Last sign of life from the server while PROCESSING; an old value means the operation is stalled and
	// will be picked up again.
	HeartbeatAt   *timestamppb.Timestamp `protobuf:"bytes,15,opt,name=heartbeat_at,json=heartbeatAt,proto3" json:"heartbeat_at,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Operation) Reset() {
	*x = Operation{}
	mi := &file_memory_v1_memory_proto_msgTypes[136]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Operation) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Operation) ProtoMessage() {}

func (x *Operation) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[136]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Operation.ProtoReflect.Descriptor instead.
func (*Operation) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{136}
}

func (x *Operation) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *Operation) GetBankId() string {
	if x != nil {
		return x.BankId
	}
	return ""
}

func (x *Operation) GetKind() OperationKind {
	if x != nil {
		return x.Kind
	}
	return OperationKind_OPERATION_KIND_UNSPECIFIED
}

func (x *Operation) GetStatus() OperationStatus {
	if x != nil {
		return x.Status
	}
	return OperationStatus_OPERATION_STATUS_UNSPECIFIED
}

func (x *Operation) GetError() string {
	if x != nil {
		return x.Error
	}
	return ""
}

func (x *Operation) GetDebugSummary() string {
	if x != nil {
		return x.DebugSummary
	}
	return ""
}

func (x *Operation) GetUsage() []*SlotUsage {
	if x != nil {
		return x.Usage
	}
	return nil
}

func (x *Operation) GetEffectiveOverrides() *BankConfig {
	if x != nil {
		return x.EffectiveOverrides
	}
	return nil
}

func (x *Operation) GetParentId() string {
	if x != nil {
		return x.ParentId
	}
	return ""
}

func (x *Operation) GetProgress() *Progress {
	if x != nil {
		return x.Progress
	}
	return nil
}

func (x *Operation) GetCreatedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.CreatedAt
	}
	return nil
}

func (x *Operation) GetUpdatedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.UpdatedAt
	}
	return nil
}

func (x *Operation) GetNextRunAt() *timestamppb.Timestamp {
	if x != nil {
		return x.NextRunAt
	}
	return nil
}

func (x *Operation) GetAttempts() int32 {
	if x != nil {
		return x.Attempts
	}
	return 0
}

func (x *Operation) GetHeartbeatAt() *timestamppb.Timestamp {
	if x != nil {
		return x.HeartbeatAt
	}
	return nil
}

// Progress is the last checkpoint an operation reported.
type Progress struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Stage name; its values depend on the operation kind.
	Stage string `protobuf:"bytes,1,opt,name=stage,proto3" json:"stage,omitempty"`
	// When the checkpoint was written.
	RecordedAt *timestamppb.Timestamp `protobuf:"bytes,2,opt,name=recorded_at,json=recordedAt,proto3" json:"recorded_at,omitempty"`
	// Items done so far in this stage.
	Processed int64 `protobuf:"varint,3,opt,name=processed,proto3" json:"processed,omitempty"`
	// Items in this stage, if known; 0 = unknown.
	Total         int64 `protobuf:"varint,4,opt,name=total,proto3" json:"total,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Progress) Reset() {
	*x = Progress{}
	mi := &file_memory_v1_memory_proto_msgTypes[137]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Progress) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Progress) ProtoMessage() {}

func (x *Progress) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[137]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Progress.ProtoReflect.Descriptor instead.
func (*Progress) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{137}
}

func (x *Progress) GetStage() string {
	if x != nil {
		return x.Stage
	}
	return ""
}

func (x *Progress) GetRecordedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.RecordedAt
	}
	return nil
}

func (x *Progress) GetProcessed() int64 {
	if x != nil {
		return x.Processed
	}
	return 0
}

func (x *Progress) GetTotal() int64 {
	if x != nil {
		return x.Total
	}
	return 0
}

// TagSet is an unordered set of tags, used as an observation scope or a document's tag set.
type TagSet struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The tags; order is not significant. Empty = the shared (untagged) scope.
	Tags          []string `protobuf:"bytes,1,rep,name=tags,proto3" json:"tags,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *TagSet) Reset() {
	*x = TagSet{}
	mi := &file_memory_v1_memory_proto_msgTypes[138]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *TagSet) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TagSet) ProtoMessage() {}

func (x *TagSet) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[138]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TagSet.ProtoReflect.Descriptor instead.
func (*TagSet) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{138}
}

func (x *TagSet) GetTags() []string {
	if x != nil {
		return x.Tags
	}
	return nil
}

// ObservationScopeModes decides which tag scopes a memory consolidates under.
type ObservationScopeModes struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Expansion mode.
	Mode ObservationScopeModes_Mode `protobuf:"varint,1,opt,name=mode,proto3,enum=memory.v1.ObservationScopeModes_Mode" json:"mode,omitempty"`
	// Explicit scopes; MODE_CUSTOM only.
	Custom []*TagSet `protobuf:"bytes,2,rep,name=custom,proto3" json:"custom,omitempty"`
	// Cap on MODE_ALL_COMBINATIONS fan-out, default 15. Checked synchronously at Retain: exceeding it
	// fails the RPC with INVALID_ARGUMENT before anything is stored.
	MaxCombinations *int32 `protobuf:"varint,3,opt,name=max_combinations,json=maxCombinations,proto3,oneof" json:"max_combinations,omitempty"`
	unknownFields   protoimpl.UnknownFields
	sizeCache       protoimpl.SizeCache
}

func (x *ObservationScopeModes) Reset() {
	*x = ObservationScopeModes{}
	mi := &file_memory_v1_memory_proto_msgTypes[139]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ObservationScopeModes) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ObservationScopeModes) ProtoMessage() {}

func (x *ObservationScopeModes) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[139]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ObservationScopeModes.ProtoReflect.Descriptor instead.
func (*ObservationScopeModes) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{139}
}

func (x *ObservationScopeModes) GetMode() ObservationScopeModes_Mode {
	if x != nil {
		return x.Mode
	}
	return ObservationScopeModes_MODE_UNSPECIFIED
}

func (x *ObservationScopeModes) GetCustom() []*TagSet {
	if x != nil {
		return x.Custom
	}
	return nil
}

func (x *ObservationScopeModes) GetMaxCombinations() int32 {
	if x != nil && x.MaxCombinations != nil {
		return *x.MaxCombinations
	}
	return 0
}

// TagFilter filters by tag: flat tags under a match mode, or boolean tag groups. tags/match and groups are
// mutually exclusive (setting both = INVALID_ARGUMENT). An unset TagFilter, or one with no tags and no groups
// and a match other than TAGS_MATCH_EXACT, filters nothing. List RPCs accept only the flat form (groups =
// INVALID_ARGUMENT).
type TagFilter struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Flat filter tags, compared under match.
	Tags []string `protobuf:"bytes,1,rep,name=tags,proto3" json:"tags,omitempty"`
	// How tags must match; unset = the default the TagFilter field documents.
	Match TagsMatch `protobuf:"varint,2,opt,name=match,proto3,enum=memory.v1.TagsMatch" json:"match,omitempty"`
	// Boolean tag groups; top-level groups are AND-ed.
	Groups        []*TagGroup `protobuf:"bytes,3,rep,name=groups,proto3" json:"groups,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *TagFilter) Reset() {
	*x = TagFilter{}
	mi := &file_memory_v1_memory_proto_msgTypes[140]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *TagFilter) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TagFilter) ProtoMessage() {}

func (x *TagFilter) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[140]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TagFilter.ProtoReflect.Descriptor instead.
func (*TagFilter) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{140}
}

func (x *TagFilter) GetTags() []string {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *TagFilter) GetMatch() TagsMatch {
	if x != nil {
		return x.Match
	}
	return TagsMatch_TAGS_MATCH_UNSPECIFIED
}

func (x *TagFilter) GetGroups() []*TagGroup {
	if x != nil {
		return x.Groups
	}
	return nil
}

// TagGroup is one node of a boolean tag-filter tree; TagFilter.groups AND-s its top-level groups.
type TagGroup struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The node: a leaf test or a boolean combination of child groups.
	//
	// Types that are valid to be assigned to Node:
	//
	//	*TagGroup_Leaf
	//	*TagGroup_AllOf
	//	*TagGroup_AnyOf
	//	*TagGroup_NoneOf
	Node          isTagGroup_Node `protobuf_oneof:"node"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *TagGroup) Reset() {
	*x = TagGroup{}
	mi := &file_memory_v1_memory_proto_msgTypes[141]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *TagGroup) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TagGroup) ProtoMessage() {}

func (x *TagGroup) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[141]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TagGroup.ProtoReflect.Descriptor instead.
func (*TagGroup) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{141}
}

func (x *TagGroup) GetNode() isTagGroup_Node {
	if x != nil {
		return x.Node
	}
	return nil
}

func (x *TagGroup) GetLeaf() *TagLeaf {
	if x != nil {
		if x, ok := x.Node.(*TagGroup_Leaf); ok {
			return x.Leaf
		}
	}
	return nil
}

func (x *TagGroup) GetAllOf() *TagGroupList {
	if x != nil {
		if x, ok := x.Node.(*TagGroup_AllOf); ok {
			return x.AllOf
		}
	}
	return nil
}

func (x *TagGroup) GetAnyOf() *TagGroupList {
	if x != nil {
		if x, ok := x.Node.(*TagGroup_AnyOf); ok {
			return x.AnyOf
		}
	}
	return nil
}

func (x *TagGroup) GetNoneOf() *TagGroup {
	if x != nil {
		if x, ok := x.Node.(*TagGroup_NoneOf); ok {
			return x.NoneOf
		}
	}
	return nil
}

type isTagGroup_Node interface {
	isTagGroup_Node()
}

type TagGroup_Leaf struct {
	// A single tag test.
	Leaf *TagLeaf `protobuf:"bytes,1,opt,name=leaf,proto3,oneof"`
}

type TagGroup_AllOf struct {
	// Matches when every child group matches.
	AllOf *TagGroupList `protobuf:"bytes,2,opt,name=all_of,json=allOf,proto3,oneof"`
}

type TagGroup_AnyOf struct {
	// Matches when at least one child group matches.
	AnyOf *TagGroupList `protobuf:"bytes,3,opt,name=any_of,json=anyOf,proto3,oneof"`
}

type TagGroup_NoneOf struct {
	// Matches when the child group does not match.
	NoneOf *TagGroup `protobuf:"bytes,4,opt,name=none_of,json=noneOf,proto3,oneof"`
}

func (*TagGroup_Leaf) isTagGroup_Node() {}

func (*TagGroup_AllOf) isTagGroup_Node() {}

func (*TagGroup_AnyOf) isTagGroup_Node() {}

func (*TagGroup_NoneOf) isTagGroup_Node() {}

// TagGroupList is the child list of an all_of / any_of node.
type TagGroupList struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Child groups.
	Groups        []*TagGroup `protobuf:"bytes,1,rep,name=groups,proto3" json:"groups,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *TagGroupList) Reset() {
	*x = TagGroupList{}
	mi := &file_memory_v1_memory_proto_msgTypes[142]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *TagGroupList) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TagGroupList) ProtoMessage() {}

func (x *TagGroupList) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[142]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TagGroupList.ProtoReflect.Descriptor instead.
func (*TagGroupList) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{142}
}

func (x *TagGroupList) GetGroups() []*TagGroup {
	if x != nil {
		return x.Groups
	}
	return nil
}

// TagLeaf tests a memory's tags against a tag list.
type TagLeaf struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Tags to test.
	Tags []string `protobuf:"bytes,1,rep,name=tags,proto3" json:"tags,omitempty"`
	// How the tags must match; default TAGS_MATCH_ANY_STRICT.
	Match TagsMatch `protobuf:"varint,2,opt,name=match,proto3,enum=memory.v1.TagsMatch" json:"match,omitempty"`
	// How tags resolve to stored tags.
	Resolve       TagLeaf_Resolve `protobuf:"varint,3,opt,name=resolve,proto3,enum=memory.v1.TagLeaf_Resolve" json:"resolve,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *TagLeaf) Reset() {
	*x = TagLeaf{}
	mi := &file_memory_v1_memory_proto_msgTypes[143]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *TagLeaf) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TagLeaf) ProtoMessage() {}

func (x *TagLeaf) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[143]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TagLeaf.ProtoReflect.Descriptor instead.
func (*TagLeaf) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{143}
}

func (x *TagLeaf) GetTags() []string {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *TagLeaf) GetMatch() TagsMatch {
	if x != nil {
		return x.Match
	}
	return TagsMatch_TAGS_MATCH_UNSPECIFIED
}

func (x *TagLeaf) GetResolve() TagLeaf_Resolve {
	if x != nil {
		return x.Resolve
	}
	return TagLeaf_RESOLVE_UNSPECIFIED
}

// Scores is the score breakdown behind a recalled memory.
type Scores struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Final score after rerank and recency / temporal / proof boosts; results are ordered by it.
	Final float64 `protobuf:"fixed64,1,opt,name=final,proto3" json:"final,omitempty"`
	// Cross-encoder score; unset when rerank did not run.
	Reranker *float64 `protobuf:"fixed64,2,opt,name=reranker,proto3,oneof" json:"reranker,omitempty"`
	// Raw score per arm name from each arm that returned the memory (vector: cosine similarity, text: BM25,
	// graph: path weight, temporal: proximity); an arm that did not return it has no entry.
	Arms          map[string]float64 `protobuf:"bytes,3,rep,name=arms,proto3" json:"arms,omitempty" protobuf_key:"bytes,1,opt,name=key" protobuf_val:"fixed64,2,opt,name=value"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Scores) Reset() {
	*x = Scores{}
	mi := &file_memory_v1_memory_proto_msgTypes[144]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Scores) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Scores) ProtoMessage() {}

func (x *Scores) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[144]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Scores.ProtoReflect.Descriptor instead.
func (*Scores) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{144}
}

func (x *Scores) GetFinal() float64 {
	if x != nil {
		return x.Final
	}
	return 0
}

func (x *Scores) GetReranker() float64 {
	if x != nil && x.Reranker != nil {
		return *x.Reranker
	}
	return 0
}

func (x *Scores) GetArms() map[string]float64 {
	if x != nil {
		return x.Arms
	}
	return nil
}

// MinScores sets score floors for recall. Arm floors apply inside retrieval; result floors (reranker, final)
// apply after scoring. Unset = no floor.
type MinScores struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Retrieval floor per arm name (ListRecallArms); an unknown name = INVALID_ARGUMENT. Each arm reads its own
	// entry: vector takes a cosine-similarity floor (absent = 0.3, an explicit 0 disables it), text a BM25
	// floor (absent = none); graph and temporal ignore theirs.
	Arms map[string]float64 `protobuf:"bytes,1,rep,name=arms,proto3" json:"arms,omitempty" protobuf_key:"bytes,1,opt,name=key" protobuf_val:"fixed64,2,opt,name=value"`
	// Minimum cross-encoder score after rerank.
	Reranker *float64 `protobuf:"fixed64,2,opt,name=reranker,proto3,oneof" json:"reranker,omitempty"`
	// Minimum final score after boosts.
	Final         *float64 `protobuf:"fixed64,3,opt,name=final,proto3,oneof" json:"final,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *MinScores) Reset() {
	*x = MinScores{}
	mi := &file_memory_v1_memory_proto_msgTypes[145]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *MinScores) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*MinScores) ProtoMessage() {}

func (x *MinScores) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[145]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use MinScores.ProtoReflect.Descriptor instead.
func (*MinScores) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{145}
}

func (x *MinScores) GetArms() map[string]float64 {
	if x != nil {
		return x.Arms
	}
	return nil
}

func (x *MinScores) GetReranker() float64 {
	if x != nil && x.Reranker != nil {
		return *x.Reranker
	}
	return 0
}

func (x *MinScores) GetFinal() float64 {
	if x != nil && x.Final != nil {
		return *x.Final
	}
	return 0
}

// Chunk is a slice of a retained document that memories were extracted from.
type Chunk struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Chunk id.
	Id string `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	// Chunk text, possibly cut to the request's chunk token budget.
	Text string `protobuf:"bytes,2,opt,name=text,proto3" json:"text,omitempty"`
	// Zero-based position of the chunk within its document.
	ChunkIndex int32 `protobuf:"varint,3,opt,name=chunk_index,json=chunkIndex,proto3" json:"chunk_index,omitempty"`
	// True when text was cut to fit the budget.
	Truncated     bool `protobuf:"varint,4,opt,name=truncated,proto3" json:"truncated,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Chunk) Reset() {
	*x = Chunk{}
	mi := &file_memory_v1_memory_proto_msgTypes[146]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Chunk) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Chunk) ProtoMessage() {}

func (x *Chunk) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[146]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Chunk.ProtoReflect.Descriptor instead.
func (*Chunk) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{146}
}

func (x *Chunk) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *Chunk) GetText() string {
	if x != nil {
		return x.Text
	}
	return ""
}

func (x *Chunk) GetChunkIndex() int32 {
	if x != nil {
		return x.ChunkIndex
	}
	return 0
}

func (x *Chunk) GetTruncated() bool {
	if x != nil {
		return x.Truncated
	}
	return false
}

// SlotUsage is token usage for one (slot, model) pair, recorded on every LLM call.
type SlotUsage struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Slot the calls were made for.
	Slot SlotUsage_ModelSlot `protobuf:"varint,1,opt,name=slot,proto3,enum=memory.v1.SlotUsage_ModelSlot" json:"slot,omitempty"`
	// Gateway model that served them.
	Model string `protobuf:"bytes,2,opt,name=model,proto3" json:"model,omitempty"`
	// Prompt tokens.
	InputTokens int64 `protobuf:"varint,3,opt,name=input_tokens,json=inputTokens,proto3" json:"input_tokens,omitempty"`
	// Completion tokens.
	OutputTokens  int64 `protobuf:"varint,4,opt,name=output_tokens,json=outputTokens,proto3" json:"output_tokens,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *SlotUsage) Reset() {
	*x = SlotUsage{}
	mi := &file_memory_v1_memory_proto_msgTypes[147]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SlotUsage) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SlotUsage) ProtoMessage() {}

func (x *SlotUsage) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[147]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SlotUsage.ProtoReflect.Descriptor instead.
func (*SlotUsage) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{147}
}

func (x *SlotUsage) GetSlot() SlotUsage_ModelSlot {
	if x != nil {
		return x.Slot
	}
	return SlotUsage_MODEL_SLOT_UNSPECIFIED
}

func (x *SlotUsage) GetModel() string {
	if x != nil {
		return x.Model
	}
	return ""
}

func (x *SlotUsage) GetInputTokens() int64 {
	if x != nil {
		return x.InputTokens
	}
	return 0
}

func (x *SlotUsage) GetOutputTokens() int64 {
	if x != nil {
		return x.OutputTokens
	}
	return 0
}

// RecallTrace explains a recall: per-arm results, fusion, rerank and packing. Returned when
// RecallRequest.trace is set.
type RecallTrace struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// One entry per arm that ran, in fusion order; an arm that does not apply to the query (temporal without
	// a time window) has none.
	Arms []*ArmTrace `protobuf:"bytes,1,rep,name=arms,proto3" json:"arms,omitempty"`
	// Candidates after RRF fusion.
	Fused int32 `protobuf:"varint,2,opt,name=fused,proto3" json:"fused,omitempty"`
	// Candidates sent to the cross-encoder.
	Reranked int32 `protobuf:"varint,3,opt,name=reranked,proto3" json:"reranked,omitempty"`
	// Results packed into the response.
	Packed int32 `protobuf:"varint,4,opt,name=packed,proto3" json:"packed,omitempty"`
	// Token budget the packer worked with.
	TokenBudget int32 `protobuf:"varint,5,opt,name=token_budget,json=tokenBudget,proto3" json:"token_budget,omitempty"`
	// Tokens the packed results use.
	TokensUsed int32 `protobuf:"varint,6,opt,name=tokens_used,json=tokensUsed,proto3" json:"tokens_used,omitempty"`
	// Start of the time window TIME_PARSE resolved, if anything.
	TimeWindowStart *timestamppb.Timestamp `protobuf:"bytes,7,opt,name=time_window_start,json=timeWindowStart,proto3" json:"time_window_start,omitempty"`
	// End of the time window TIME_PARSE resolved, if anything.
	TimeWindowEnd *timestamppb.Timestamp `protobuf:"bytes,8,opt,name=time_window_end,json=timeWindowEnd,proto3" json:"time_window_end,omitempty"`
	// Total recall wall time in milliseconds.
	DurationMs    int64 `protobuf:"varint,9,opt,name=duration_ms,json=durationMs,proto3" json:"duration_ms,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RecallTrace) Reset() {
	*x = RecallTrace{}
	mi := &file_memory_v1_memory_proto_msgTypes[148]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RecallTrace) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RecallTrace) ProtoMessage() {}

func (x *RecallTrace) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[148]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RecallTrace.ProtoReflect.Descriptor instead.
func (*RecallTrace) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{148}
}

func (x *RecallTrace) GetArms() []*ArmTrace {
	if x != nil {
		return x.Arms
	}
	return nil
}

func (x *RecallTrace) GetFused() int32 {
	if x != nil {
		return x.Fused
	}
	return 0
}

func (x *RecallTrace) GetReranked() int32 {
	if x != nil {
		return x.Reranked
	}
	return 0
}

func (x *RecallTrace) GetPacked() int32 {
	if x != nil {
		return x.Packed
	}
	return 0
}

func (x *RecallTrace) GetTokenBudget() int32 {
	if x != nil {
		return x.TokenBudget
	}
	return 0
}

func (x *RecallTrace) GetTokensUsed() int32 {
	if x != nil {
		return x.TokensUsed
	}
	return 0
}

func (x *RecallTrace) GetTimeWindowStart() *timestamppb.Timestamp {
	if x != nil {
		return x.TimeWindowStart
	}
	return nil
}

func (x *RecallTrace) GetTimeWindowEnd() *timestamppb.Timestamp {
	if x != nil {
		return x.TimeWindowEnd
	}
	return nil
}

func (x *RecallTrace) GetDurationMs() int64 {
	if x != nil {
		return x.DurationMs
	}
	return 0
}

// ArmTrace describes one recall arm's contribution.
type ArmTrace struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Arm name (ListRecallArms), e.g. vector, text, graph or temporal.
	Arm string `protobuf:"bytes,1,opt,name=arm,proto3" json:"arm,omitempty"`
	// Candidates the arm returned.
	Candidates int32 `protobuf:"varint,2,opt,name=candidates,proto3" json:"candidates,omitempty"`
	// Wall time of the arm's query in milliseconds.
	DurationMs int64 `protobuf:"varint,3,opt,name=duration_ms,json=durationMs,proto3" json:"duration_ms,omitempty"`
	// Ids of the arm's top candidates, best first.
	TopIds        []string `protobuf:"bytes,4,rep,name=top_ids,json=topIds,proto3" json:"top_ids,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ArmTrace) Reset() {
	*x = ArmTrace{}
	mi := &file_memory_v1_memory_proto_msgTypes[149]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ArmTrace) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ArmTrace) ProtoMessage() {}

func (x *ArmTrace) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[149]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ArmTrace.ProtoReflect.Descriptor instead.
func (*ArmTrace) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{149}
}

func (x *ArmTrace) GetArm() string {
	if x != nil {
		return x.Arm
	}
	return ""
}

func (x *ArmTrace) GetCandidates() int32 {
	if x != nil {
		return x.Candidates
	}
	return 0
}

func (x *ArmTrace) GetDurationMs() int64 {
	if x != nil {
		return x.DurationMs
	}
	return 0
}

func (x *ArmTrace) GetTopIds() []string {
	if x != nil {
		return x.TopIds
	}
	return nil
}

// ToolCallTrace records one tool call of a reflect loop or an agentic refresh.
type ToolCallTrace struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Reflect-loop iteration the call happened in.
	Iteration int32 `protobuf:"varint,1,opt,name=iteration,proto3" json:"iteration,omitempty"`
	// Tool name: recall, search_observations, search_mental_models, read_mental_models, expand or done.
	Tool string `protobuf:"bytes,2,opt,name=tool,proto3" json:"tool,omitempty"`
	// Arguments the model passed, as JSON.
	ArgumentsJson string `protobuf:"bytes,3,opt,name=arguments_json,json=argumentsJson,proto3" json:"arguments_json,omitempty"`
	// Tool output as JSON; empty unless tool outputs were requested.
	OutputJson string `protobuf:"bytes,4,opt,name=output_json,json=outputJson,proto3" json:"output_json,omitempty"`
	// Error returned to the model for a malformed call; empty on success.
	Error string `protobuf:"bytes,5,opt,name=error,proto3" json:"error,omitempty"`
	// Wall time of the call in milliseconds.
	DurationMs    int64 `protobuf:"varint,6,opt,name=duration_ms,json=durationMs,proto3" json:"duration_ms,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ToolCallTrace) Reset() {
	*x = ToolCallTrace{}
	mi := &file_memory_v1_memory_proto_msgTypes[150]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ToolCallTrace) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ToolCallTrace) ProtoMessage() {}

func (x *ToolCallTrace) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[150]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ToolCallTrace.ProtoReflect.Descriptor instead.
func (*ToolCallTrace) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{150}
}

func (x *ToolCallTrace) GetIteration() int32 {
	if x != nil {
		return x.Iteration
	}
	return 0
}

func (x *ToolCallTrace) GetTool() string {
	if x != nil {
		return x.Tool
	}
	return ""
}

func (x *ToolCallTrace) GetArgumentsJson() string {
	if x != nil {
		return x.ArgumentsJson
	}
	return ""
}

func (x *ToolCallTrace) GetOutputJson() string {
	if x != nil {
		return x.OutputJson
	}
	return ""
}

func (x *ToolCallTrace) GetError() string {
	if x != nil {
		return x.Error
	}
	return ""
}

func (x *ToolCallTrace) GetDurationMs() int64 {
	if x != nil {
		return x.DurationMs
	}
	return 0
}

// ReflectTrace explains a reflect call. Returned when ReflectInclude.trace is set.
type ReflectTrace struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Tool calls in order.
	ToolCalls []*ToolCallTrace `protobuf:"bytes,1,rep,name=tool_calls,json=toolCalls,proto3" json:"tool_calls,omitempty"`
	// Loop iterations used.
	Iterations int32 `protobuf:"varint,2,opt,name=iterations,proto3" json:"iterations,omitempty"`
	// Why the loop stopped: answered, max_iterations or context_full.
	StopReason string `protobuf:"bytes,3,opt,name=stop_reason,json=stopReason,proto3" json:"stop_reason,omitempty"`
	// Directives applied to the prompt.
	DirectiveIds []string `protobuf:"bytes,4,rep,name=directive_ids,json=directiveIds,proto3" json:"directive_ids,omitempty"`
	// Total reflect wall time in milliseconds.
	DurationMs    int64 `protobuf:"varint,5,opt,name=duration_ms,json=durationMs,proto3" json:"duration_ms,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ReflectTrace) Reset() {
	*x = ReflectTrace{}
	mi := &file_memory_v1_memory_proto_msgTypes[151]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ReflectTrace) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ReflectTrace) ProtoMessage() {}

func (x *ReflectTrace) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[151]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ReflectTrace.ProtoReflect.Descriptor instead.
func (*ReflectTrace) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{151}
}

func (x *ReflectTrace) GetToolCalls() []*ToolCallTrace {
	if x != nil {
		return x.ToolCalls
	}
	return nil
}

func (x *ReflectTrace) GetIterations() int32 {
	if x != nil {
		return x.Iterations
	}
	return 0
}

func (x *ReflectTrace) GetStopReason() string {
	if x != nil {
		return x.StopReason
	}
	return ""
}

func (x *ReflectTrace) GetDirectiveIds() []string {
	if x != nil {
		return x.DirectiveIds
	}
	return nil
}

func (x *ReflectTrace) GetDurationMs() int64 {
	if x != nil {
		return x.DurationMs
	}
	return 0
}

// BankConfig is the full set of behaviour knobs, grouped by phase and identical at every config level. Unset
// fields inherit from the next less specific level.
type BankConfig struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Retain: mission, extraction, labels, default scopes, chunking, entity matching and linking.
	Retain *RetainConfig `protobuf:"bytes,1,opt,name=retain,proto3" json:"retain,omitempty"`
	// Observations: whether and how consolidation runs, its strategies.
	Observations *ObservationConfig `protobuf:"bytes,2,opt,name=observations,proto3" json:"observations,omitempty"`
	// Recall tuning, also used by reflect, consolidation and single-shot refresh.
	Recall *RecallConfig `protobuf:"bytes,3,opt,name=recall,proto3" json:"recall,omitempty"`
	// Reflect: mission, disposition and loop tuning.
	Reflect *ReflectConfig `protobuf:"bytes,4,opt,name=reflect,proto3" json:"reflect,omitempty"`
	// Bank-level mental-model defaults.
	MentalModels *MentalModelConfig `protobuf:"bytes,5,opt,name=mental_models,json=mentalModels,proto3" json:"mental_models,omitempty"`
	// Model bindings; see "Model routing" above.
	Models        *ModelBindings `protobuf:"bytes,6,opt,name=models,proto3" json:"models,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *BankConfig) Reset() {
	*x = BankConfig{}
	mi := &file_memory_v1_memory_proto_msgTypes[152]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *BankConfig) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*BankConfig) ProtoMessage() {}

func (x *BankConfig) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[152]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use BankConfig.ProtoReflect.Descriptor instead.
func (*BankConfig) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{152}
}

func (x *BankConfig) GetRetain() *RetainConfig {
	if x != nil {
		return x.Retain
	}
	return nil
}

func (x *BankConfig) GetObservations() *ObservationConfig {
	if x != nil {
		return x.Observations
	}
	return nil
}

func (x *BankConfig) GetRecall() *RecallConfig {
	if x != nil {
		return x.Recall
	}
	return nil
}

func (x *BankConfig) GetReflect() *ReflectConfig {
	if x != nil {
		return x.Reflect
	}
	return nil
}

func (x *BankConfig) GetMentalModels() *MentalModelConfig {
	if x != nil {
		return x.MentalModels
	}
	return nil
}

func (x *BankConfig) GetModels() *ModelBindings {
	if x != nil {
		return x.Models
	}
	return nil
}

// RetainConfig tunes retain: what it extracts, how it chunks, resolves entities and builds links.
type RetainConfig struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Maximum tokens per chunk.
	ChunkMaxTokens *int32 `protobuf:"varint,1,opt,name=chunk_max_tokens,json=chunkMaxTokens,proto3,oneof" json:"chunk_max_tokens,omitempty"`
	// Tokens shared by consecutive chunks.
	ChunkOverlapTokens *int32 `protobuf:"varint,2,opt,name=chunk_overlap_tokens,json=chunkOverlapTokens,proto3,oneof" json:"chunk_overlap_tokens,omitempty"`
	// Minimum resolver score (name similarity + co-occurrence + temporal proximity) to reuse an entity.
	EntityMatchThreshold *float64 `protobuf:"fixed64,3,opt,name=entity_match_threshold,json=entityMatchThreshold,proto3,oneof" json:"entity_match_threshold,omitempty"`
	// Window in hours within which memories get temporal links.
	TemporalLinkWindowHours *int32 `protobuf:"varint,4,opt,name=temporal_link_window_hours,json=temporalLinkWindowHours,proto3,oneof" json:"temporal_link_window_hours,omitempty"`
	// Nearest neighbours considered for semantic links; also the semantic link cap per memory.
	SemanticLinkK *int32 `protobuf:"varint,5,opt,name=semantic_link_k,json=semanticLinkK,proto3,oneof" json:"semantic_link_k,omitempty"`
	// Minimum cosine similarity for a semantic link.
	SemanticLinkMinSimilarity *float64 `protobuf:"fixed64,6,opt,name=semantic_link_min_similarity,json=semanticLinkMinSimilarity,proto3,oneof" json:"semantic_link_min_similarity,omitempty"`
	// Ask extraction for causal links between facts of the same batch.
	ExtractCausalLinks *bool `protobuf:"varint,7,opt,name=extract_causal_links,json=extractCausalLinks,proto3,oneof" json:"extract_causal_links,omitempty"`
	// Keep a JSONL line or chat turn whole in one chunk up to this many tokens.
	StructuredChunkMaxTokens *int32 `protobuf:"varint,8,opt,name=structured_chunk_max_tokens,json=structuredChunkMaxTokens,proto3,oneof" json:"structured_chunk_max_tokens,omitempty"`
	// Temporal link cap per memory, default 20.
	TemporalLinkCap *int32 `protobuf:"varint,9,opt,name=temporal_link_cap,json=temporalLinkCap,proto3,oneof" json:"temporal_link_cap,omitempty"`
	// Mission injected into retain extraction prompts.
	Mission string `protobuf:"bytes,10,opt,name=mission,proto3" json:"mission,omitempty"`
	// How retain extracts facts.
	ExtractionMode RetainConfig_ExtractionMode `protobuf:"varint,11,opt,name=extraction_mode,json=extractionMode,proto3,enum=memory.v1.RetainConfig_ExtractionMode" json:"extraction_mode,omitempty"`
	// Extraction prompt used with EXTRACTION_MODE_CUSTOM.
	CustomExtractionPrompt string `protobuf:"bytes,12,opt,name=custom_extraction_prompt,json=customExtractionPrompt,proto3" json:"custom_extraction_prompt,omitempty"`
	// Typed entity labels extracted at retain as key:value label entities.
	EntityLabels []*EntityLabel `protobuf:"bytes,13,rep,name=entity_labels,json=entityLabels,proto3" json:"entity_labels,omitempty"`
	// false = only label entities are created (no free-form entities); unset = inherited.
	EntitiesAllowFreeForm *bool `protobuf:"varint,14,opt,name=entities_allow_free_form,json=entitiesAllowFreeForm,proto3,oneof" json:"entities_allow_free_form,omitempty"`
	// Observation scopes used when a retained item does not set its own.
	DefaultObservationScopeModes *ObservationScopeModes `protobuf:"bytes,15,opt,name=default_observation_scope_modes,json=defaultObservationScopeModes,proto3" json:"default_observation_scope_modes,omitempty"`
	unknownFields                protoimpl.UnknownFields
	sizeCache                    protoimpl.SizeCache
}

func (x *RetainConfig) Reset() {
	*x = RetainConfig{}
	mi := &file_memory_v1_memory_proto_msgTypes[153]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RetainConfig) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RetainConfig) ProtoMessage() {}

func (x *RetainConfig) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[153]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RetainConfig.ProtoReflect.Descriptor instead.
func (*RetainConfig) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{153}
}

func (x *RetainConfig) GetChunkMaxTokens() int32 {
	if x != nil && x.ChunkMaxTokens != nil {
		return *x.ChunkMaxTokens
	}
	return 0
}

func (x *RetainConfig) GetChunkOverlapTokens() int32 {
	if x != nil && x.ChunkOverlapTokens != nil {
		return *x.ChunkOverlapTokens
	}
	return 0
}

func (x *RetainConfig) GetEntityMatchThreshold() float64 {
	if x != nil && x.EntityMatchThreshold != nil {
		return *x.EntityMatchThreshold
	}
	return 0
}

func (x *RetainConfig) GetTemporalLinkWindowHours() int32 {
	if x != nil && x.TemporalLinkWindowHours != nil {
		return *x.TemporalLinkWindowHours
	}
	return 0
}

func (x *RetainConfig) GetSemanticLinkK() int32 {
	if x != nil && x.SemanticLinkK != nil {
		return *x.SemanticLinkK
	}
	return 0
}

func (x *RetainConfig) GetSemanticLinkMinSimilarity() float64 {
	if x != nil && x.SemanticLinkMinSimilarity != nil {
		return *x.SemanticLinkMinSimilarity
	}
	return 0
}

func (x *RetainConfig) GetExtractCausalLinks() bool {
	if x != nil && x.ExtractCausalLinks != nil {
		return *x.ExtractCausalLinks
	}
	return false
}

func (x *RetainConfig) GetStructuredChunkMaxTokens() int32 {
	if x != nil && x.StructuredChunkMaxTokens != nil {
		return *x.StructuredChunkMaxTokens
	}
	return 0
}

func (x *RetainConfig) GetTemporalLinkCap() int32 {
	if x != nil && x.TemporalLinkCap != nil {
		return *x.TemporalLinkCap
	}
	return 0
}

func (x *RetainConfig) GetMission() string {
	if x != nil {
		return x.Mission
	}
	return ""
}

func (x *RetainConfig) GetExtractionMode() RetainConfig_ExtractionMode {
	if x != nil {
		return x.ExtractionMode
	}
	return RetainConfig_EXTRACTION_MODE_UNSPECIFIED
}

func (x *RetainConfig) GetCustomExtractionPrompt() string {
	if x != nil {
		return x.CustomExtractionPrompt
	}
	return ""
}

func (x *RetainConfig) GetEntityLabels() []*EntityLabel {
	if x != nil {
		return x.EntityLabels
	}
	return nil
}

func (x *RetainConfig) GetEntitiesAllowFreeForm() bool {
	if x != nil && x.EntitiesAllowFreeForm != nil {
		return *x.EntitiesAllowFreeForm
	}
	return false
}

func (x *RetainConfig) GetDefaultObservationScopeModes() *ObservationScopeModes {
	if x != nil {
		return x.DefaultObservationScopeModes
	}
	return nil
}

// ObservationConfig tunes consolidation, which turns raw facts into observations per tag scope.
type ObservationConfig struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// false = no consolidation at all; unset = inherited.
	Enabled *bool `protobuf:"varint,1,opt,name=enabled,proto3,oneof" json:"enabled,omitempty"`
	// Enqueue consolidation automatically after retain, and let periodic maintenance catch up on facts left
	// behind; unset = inherited.
	AutoConsolidation *bool `protobuf:"varint,2,opt,name=auto_consolidation,json=autoConsolidation,proto3,oneof" json:"auto_consolidation,omitempty"`
	// Mission injected into consolidation prompts; a matching strategy's mission wins.
	Mission string `protobuf:"bytes,3,opt,name=mission,proto3" json:"mission,omitempty"`
	// Cosine similarity at or above which observations in one scope get a DEDUP_CHECK, default 0.97.
	DedupThreshold *float64 `protobuf:"fixed64,4,opt,name=dedup_threshold,json=dedupThreshold,proto3,oneof" json:"dedup_threshold,omitempty"`
	// Cap on observations per scope.
	MaxObservationsPerScope *int32 `protobuf:"varint,5,opt,name=max_observations_per_scope,json=maxObservationsPerScope,proto3,oneof" json:"max_observations_per_scope,omitempty"`
	// Token budget for source facts shown to one consolidation call.
	SourceFactsMaxTokens *int32 `protobuf:"varint,6,opt,name=source_facts_max_tokens,json=sourceFactsMaxTokens,proto3,oneof" json:"source_facts_max_tokens,omitempty"`
	// Token budget for the source facts shown per existing observation, default 256.
	SourceFactsMaxTokensPerObservation *int32 `protobuf:"varint,7,opt,name=source_facts_max_tokens_per_observation,json=sourceFactsMaxTokensPerObservation,proto3,oneof" json:"source_facts_max_tokens_per_observation,omitempty"`
	// Facts per consolidation LLM call, default 8. A failing batch is halved and retried.
	LlmBatchSize *int32 `protobuf:"varint,8,opt,name=llm_batch_size,json=llmBatchSize,proto3,oneof" json:"llm_batch_size,omitempty"`
	// Memories one consolidation run takes before it re-enqueues itself, default 100.
	MaxUnitsPerRun *int32 `protobuf:"varint,9,opt,name=max_units_per_run,json=maxUnitsPerRun,proto3,oneof" json:"max_units_per_run,omitempty"`
	// Per-scope consolidation strategies; the first whose patterns match a scope wins.
	Strategies []*ConsolidationStrategy `protobuf:"bytes,10,rep,name=strategies,proto3" json:"strategies,omitempty"`
	// Seconds a fact may wait unconsolidated before periodic maintenance consolidates its bank, default 600.
	// Keeps maintenance from pre-empting a manual Consolidate after a retain with skip_consolidation.
	ReconcileGraceSeconds *int32 `protobuf:"varint,11,opt,name=reconcile_grace_seconds,json=reconcileGraceSeconds,proto3,oneof" json:"reconcile_grace_seconds,omitempty"`
	unknownFields         protoimpl.UnknownFields
	sizeCache             protoimpl.SizeCache
}

func (x *ObservationConfig) Reset() {
	*x = ObservationConfig{}
	mi := &file_memory_v1_memory_proto_msgTypes[154]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ObservationConfig) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ObservationConfig) ProtoMessage() {}

func (x *ObservationConfig) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[154]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ObservationConfig.ProtoReflect.Descriptor instead.
func (*ObservationConfig) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{154}
}

func (x *ObservationConfig) GetEnabled() bool {
	if x != nil && x.Enabled != nil {
		return *x.Enabled
	}
	return false
}

func (x *ObservationConfig) GetAutoConsolidation() bool {
	if x != nil && x.AutoConsolidation != nil {
		return *x.AutoConsolidation
	}
	return false
}

func (x *ObservationConfig) GetMission() string {
	if x != nil {
		return x.Mission
	}
	return ""
}

func (x *ObservationConfig) GetDedupThreshold() float64 {
	if x != nil && x.DedupThreshold != nil {
		return *x.DedupThreshold
	}
	return 0
}

func (x *ObservationConfig) GetMaxObservationsPerScope() int32 {
	if x != nil && x.MaxObservationsPerScope != nil {
		return *x.MaxObservationsPerScope
	}
	return 0
}

func (x *ObservationConfig) GetSourceFactsMaxTokens() int32 {
	if x != nil && x.SourceFactsMaxTokens != nil {
		return *x.SourceFactsMaxTokens
	}
	return 0
}

func (x *ObservationConfig) GetSourceFactsMaxTokensPerObservation() int32 {
	if x != nil && x.SourceFactsMaxTokensPerObservation != nil {
		return *x.SourceFactsMaxTokensPerObservation
	}
	return 0
}

func (x *ObservationConfig) GetLlmBatchSize() int32 {
	if x != nil && x.LlmBatchSize != nil {
		return *x.LlmBatchSize
	}
	return 0
}

func (x *ObservationConfig) GetMaxUnitsPerRun() int32 {
	if x != nil && x.MaxUnitsPerRun != nil {
		return *x.MaxUnitsPerRun
	}
	return 0
}

func (x *ObservationConfig) GetStrategies() []*ConsolidationStrategy {
	if x != nil {
		return x.Strategies
	}
	return nil
}

func (x *ObservationConfig) GetReconcileGraceSeconds() int32 {
	if x != nil && x.ReconcileGraceSeconds != nil {
		return *x.ReconcileGraceSeconds
	}
	return 0
}

// Disposition sets reflect personality traits, each on a 1-5 scale, default 3. Rendered into the reflect
// system prompt as instructions.
type Disposition struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// How strongly to doubt weakly supported claims, 1-5.
	Skepticism int32 `protobuf:"varint,1,opt,name=skepticism,proto3" json:"skepticism,omitempty"`
	// How literally to read the question and the evidence, 1-5.
	Literalism int32 `protobuf:"varint,2,opt,name=literalism,proto3" json:"literalism,omitempty"`
	// How much weight to give feelings and the user's perspective, 1-5.
	Empathy       int32 `protobuf:"varint,3,opt,name=empathy,proto3" json:"empathy,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Disposition) Reset() {
	*x = Disposition{}
	mi := &file_memory_v1_memory_proto_msgTypes[155]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Disposition) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Disposition) ProtoMessage() {}

func (x *Disposition) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[155]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Disposition.ProtoReflect.Descriptor instead.
func (*Disposition) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{155}
}

func (x *Disposition) GetSkepticism() int32 {
	if x != nil {
		return x.Skepticism
	}
	return 0
}

func (x *Disposition) GetLiteralism() int32 {
	if x != nil {
		return x.Literalism
	}
	return 0
}

func (x *Disposition) GetEmpathy() int32 {
	if x != nil {
		return x.Empathy
	}
	return 0
}

// EntityLabel defines a typed label that retain extracts and resolves to exact-match label entities.
type EntityLabel struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Label key, used as the key of key:value label entities.
	Key string `protobuf:"bytes,1,opt,name=key,proto3" json:"key,omitempty"`
	// What the label means; shown to the extraction model.
	Description string `protobuf:"bytes,2,opt,name=description,proto3" json:"description,omitempty"`
	// Value shape, default TYPE_VALUE.
	Type EntityLabel_Type `protobuf:"varint,3,opt,name=type,proto3,enum=memory.v1.EntityLabel_Type" json:"type,omitempty"`
	// Allowed values; TYPE_VALUE and TYPE_MULTI_VALUES only.
	Values []*LabelValue `protobuf:"bytes,4,rep,name=values,proto3" json:"values,omitempty"`
	// Sub-labels by field name; TYPE_MAP only. Stored as key:field:value.
	Fields map[string]*EntityLabel `protobuf:"bytes,5,rep,name=fields,proto3" json:"fields,omitempty" protobuf_key:"bytes,1,opt,name=key" protobuf_val:"bytes,2,opt,name=value"`
	// Whether a fact may omit the label, default true.
	Optional *bool `protobuf:"varint,6,opt,name=optional,proto3,oneof" json:"optional,omitempty"`
	// Also copy extracted values into the memory's tags.
	WriteToTags   bool `protobuf:"varint,7,opt,name=write_to_tags,json=writeToTags,proto3" json:"write_to_tags,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *EntityLabel) Reset() {
	*x = EntityLabel{}
	mi := &file_memory_v1_memory_proto_msgTypes[156]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *EntityLabel) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*EntityLabel) ProtoMessage() {}

func (x *EntityLabel) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[156]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use EntityLabel.ProtoReflect.Descriptor instead.
func (*EntityLabel) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{156}
}

func (x *EntityLabel) GetKey() string {
	if x != nil {
		return x.Key
	}
	return ""
}

func (x *EntityLabel) GetDescription() string {
	if x != nil {
		return x.Description
	}
	return ""
}

func (x *EntityLabel) GetType() EntityLabel_Type {
	if x != nil {
		return x.Type
	}
	return EntityLabel_TYPE_UNSPECIFIED
}

func (x *EntityLabel) GetValues() []*LabelValue {
	if x != nil {
		return x.Values
	}
	return nil
}

func (x *EntityLabel) GetFields() map[string]*EntityLabel {
	if x != nil {
		return x.Fields
	}
	return nil
}

func (x *EntityLabel) GetOptional() bool {
	if x != nil && x.Optional != nil {
		return *x.Optional
	}
	return false
}

func (x *EntityLabel) GetWriteToTags() bool {
	if x != nil {
		return x.WriteToTags
	}
	return false
}

// LabelValue is one allowed value of a closed-list label.
type LabelValue struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The value as stored.
	Value string `protobuf:"bytes,1,opt,name=value,proto3" json:"value,omitempty"`
	// What the value means; shown to the extraction model.
	Description   string `protobuf:"bytes,2,opt,name=description,proto3" json:"description,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *LabelValue) Reset() {
	*x = LabelValue{}
	mi := &file_memory_v1_memory_proto_msgTypes[157]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *LabelValue) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*LabelValue) ProtoMessage() {}

func (x *LabelValue) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[157]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use LabelValue.ProtoReflect.Descriptor instead.
func (*LabelValue) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{157}
}

func (x *LabelValue) GetValue() string {
	if x != nil {
		return x.Value
	}
	return ""
}

func (x *LabelValue) GetDescription() string {
	if x != nil {
		return x.Description
	}
	return ""
}

// ConsolidationStrategy overrides consolidation settings for the scopes it matches. Its settings sit
// between bank and request in the config hierarchy, and it is the only source of CONSOLIDATE /
// DEDUP_CHECK overrides for automatic consolidation.
type ConsolidationStrategy struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Strategy name, unique within the list.
	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	// Scope patterns, each a tag filter on the scope's tag set; the strategy applies to a scope matching any
	// of them. A pattern's match unset = TAGS_MATCH_ALL_STRICT (the scope holds every pattern tag; the shared
	// scope never matches). A pattern with no tags and no groups matches only under TAGS_MATCH_EXACT (the
	// shared scope); otherwise it is ignored. Groups are allowed.
	Scopes []*TagFilter `protobuf:"bytes,2,rep,name=scopes,proto3" json:"scopes,omitempty"`
	// Observations mission for matched scopes; empty = the bank's.
	Mission string `protobuf:"bytes,3,opt,name=mission,proto3" json:"mission,omitempty"`
	// Cap on observations per matched scope.
	MaxObservationsPerScope *int32 `protobuf:"varint,4,opt,name=max_observations_per_scope,json=maxObservationsPerScope,proto3,oneof" json:"max_observations_per_scope,omitempty"`
	// Token budget for source facts per consolidation call.
	SourceFactsMaxTokens *int32 `protobuf:"varint,5,opt,name=source_facts_max_tokens,json=sourceFactsMaxTokens,proto3,oneof" json:"source_facts_max_tokens,omitempty"`
	// Token budget for the source facts shown per existing observation.
	SourceFactsMaxTokensPerObservation *int32 `protobuf:"varint,6,opt,name=source_facts_max_tokens_per_observation,json=sourceFactsMaxTokensPerObservation,proto3,oneof" json:"source_facts_max_tokens_per_observation,omitempty"`
	// CONSOLIDATE model for matched scopes; unset = the bank's.
	ConsolidateModel *ModelBinding `protobuf:"bytes,7,opt,name=consolidate_model,json=consolidateModel,proto3" json:"consolidate_model,omitempty"`
	// DEDUP_CHECK model for matched scopes; unset = the bank's.
	DedupCheckModel *ModelBinding `protobuf:"bytes,8,opt,name=dedup_check_model,json=dedupCheckModel,proto3" json:"dedup_check_model,omitempty"`
	unknownFields   protoimpl.UnknownFields
	sizeCache       protoimpl.SizeCache
}

func (x *ConsolidationStrategy) Reset() {
	*x = ConsolidationStrategy{}
	mi := &file_memory_v1_memory_proto_msgTypes[158]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ConsolidationStrategy) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ConsolidationStrategy) ProtoMessage() {}

func (x *ConsolidationStrategy) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[158]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ConsolidationStrategy.ProtoReflect.Descriptor instead.
func (*ConsolidationStrategy) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{158}
}

func (x *ConsolidationStrategy) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *ConsolidationStrategy) GetScopes() []*TagFilter {
	if x != nil {
		return x.Scopes
	}
	return nil
}

func (x *ConsolidationStrategy) GetMission() string {
	if x != nil {
		return x.Mission
	}
	return ""
}

func (x *ConsolidationStrategy) GetMaxObservationsPerScope() int32 {
	if x != nil && x.MaxObservationsPerScope != nil {
		return *x.MaxObservationsPerScope
	}
	return 0
}

func (x *ConsolidationStrategy) GetSourceFactsMaxTokens() int32 {
	if x != nil && x.SourceFactsMaxTokens != nil {
		return *x.SourceFactsMaxTokens
	}
	return 0
}

func (x *ConsolidationStrategy) GetSourceFactsMaxTokensPerObservation() int32 {
	if x != nil && x.SourceFactsMaxTokensPerObservation != nil {
		return *x.SourceFactsMaxTokensPerObservation
	}
	return 0
}

func (x *ConsolidationStrategy) GetConsolidateModel() *ModelBinding {
	if x != nil {
		return x.ConsolidateModel
	}
	return nil
}

func (x *ConsolidationStrategy) GetDedupCheckModel() *ModelBinding {
	if x != nil {
		return x.DedupCheckModel
	}
	return nil
}

// RecallConfig tunes the recall pipeline: the arms, RRF fusion, rerank, boosts and packing.
type RecallConfig struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// How a Budget becomes the per-arm candidate cap.
	BudgetFunction RecallConfig_BudgetFunction `protobuf:"varint,1,opt,name=budget_function,json=budgetFunction,proto3,enum=memory.v1.RecallConfig_BudgetFunction" json:"budget_function,omitempty"`
	// Per-arm candidate cap for BUDGET_LOW, default 100.
	BudgetLow *int32 `protobuf:"varint,2,opt,name=budget_low,json=budgetLow,proto3,oneof" json:"budget_low,omitempty"`
	// Per-arm candidate cap for BUDGET_MID, default 300.
	BudgetMid *int32 `protobuf:"varint,3,opt,name=budget_mid,json=budgetMid,proto3,oneof" json:"budget_mid,omitempty"`
	// Per-arm candidate cap for BUDGET_HIGH, default 1000.
	BudgetHigh *int32 `protobuf:"varint,4,opt,name=budget_high,json=budgetHigh,proto3,oneof" json:"budget_high,omitempty"`
	// Fusion weight per arm name (ListRecallArms); missing = 1. It decides which candidates survive the
	// reranker_max_candidates cut. An unknown name = INVALID_ARGUMENT.
	ArmBoosts map[string]float64 `protobuf:"bytes,5,rep,name=arm_boosts,json=armBoosts,proto3" json:"arm_boosts,omitempty" protobuf_key:"bytes,1,opt,name=key" protobuf_val:"fixed64,2,opt,name=value"`
	// Arms to run, by name (ListRecallArms); empty = every registered arm. RecallRequest.arms and
	// ReflectRequest.arms win over it. An unknown name = INVALID_ARGUMENT.
	Arms []string `protobuf:"bytes,6,rep,name=arms,proto3" json:"arms,omitempty"`
	// Candidates sent to the cross-encoder, default 300.
	RerankerMaxCandidates *int32 `protobuf:"varint,7,opt,name=reranker_max_candidates,json=rerankerMaxCandidates,proto3,oneof" json:"reranker_max_candidates,omitempty"`
	// Whether to run the cross-encoder (needs a models.rerank binding).
	Rerank *bool `protobuf:"varint,8,opt,name=rerank,proto3,oneof" json:"rerank,omitempty"`
	// RRF constant k, default 60.
	RrfK *int32 `protobuf:"varint,9,opt,name=rrf_k,json=rrfK,proto3,oneof" json:"rrf_k,omitempty"`
	// Weight of the recency boost.
	RecencyAlpha *float64 `protobuf:"fixed64,10,opt,name=recency_alpha,json=recencyAlpha,proto3,oneof" json:"recency_alpha,omitempty"`
	// Weight of the boost for matching the query's time window.
	TemporalAlpha *float64 `protobuf:"fixed64,11,opt,name=temporal_alpha,json=temporalAlpha,proto3,oneof" json:"temporal_alpha,omitempty"`
	// Weight of the proof-count boost for observations.
	ProofAlpha *float64 `protobuf:"fixed64,12,opt,name=proof_alpha,json=proofAlpha,proto3,oneof" json:"proof_alpha,omitempty"`
	// Age in days over which the recency boost decays.
	RecencyHorizonDays *int32 `protobuf:"varint,13,opt,name=recency_horizon_days,json=recencyHorizonDays,proto3,oneof" json:"recency_horizon_days,omitempty"`
	// Hops the graph arm follows over links, 1-2.
	GraphMaxHops *int32 `protobuf:"varint,14,opt,name=graph_max_hops,json=graphMaxHops,proto3,oneof" json:"graph_max_hops,omitempty"`
	// Buckets the temporal arm spreads its window over.
	TemporalBuckets *int32 `protobuf:"varint,15,opt,name=temporal_buckets,json=temporalBuckets,proto3,oneof" json:"temporal_buckets,omitempty"`
	// Trigram similarity a fuzzy tag-leaf tag needs to match a stored tag.
	FuzzyTagMinSimilarity *float64 `protobuf:"fixed64,16,opt,name=fuzzy_tag_min_similarity,json=fuzzyTagMinSimilarity,proto3,oneof" json:"fuzzy_tag_min_similarity,omitempty"`
	// Lower clamp of the adaptive candidate cap, default 20.
	BudgetMin *int32 `protobuf:"varint,17,opt,name=budget_min,json=budgetMin,proto3,oneof" json:"budget_min,omitempty"`
	// Upper clamp of the adaptive candidate cap, default 2000.
	BudgetMax     *int32 `protobuf:"varint,18,opt,name=budget_max,json=budgetMax,proto3,oneof" json:"budget_max,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RecallConfig) Reset() {
	*x = RecallConfig{}
	mi := &file_memory_v1_memory_proto_msgTypes[159]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RecallConfig) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RecallConfig) ProtoMessage() {}

func (x *RecallConfig) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[159]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RecallConfig.ProtoReflect.Descriptor instead.
func (*RecallConfig) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{159}
}

func (x *RecallConfig) GetBudgetFunction() RecallConfig_BudgetFunction {
	if x != nil {
		return x.BudgetFunction
	}
	return RecallConfig_BUDGET_FUNCTION_UNSPECIFIED
}

func (x *RecallConfig) GetBudgetLow() int32 {
	if x != nil && x.BudgetLow != nil {
		return *x.BudgetLow
	}
	return 0
}

func (x *RecallConfig) GetBudgetMid() int32 {
	if x != nil && x.BudgetMid != nil {
		return *x.BudgetMid
	}
	return 0
}

func (x *RecallConfig) GetBudgetHigh() int32 {
	if x != nil && x.BudgetHigh != nil {
		return *x.BudgetHigh
	}
	return 0
}

func (x *RecallConfig) GetArmBoosts() map[string]float64 {
	if x != nil {
		return x.ArmBoosts
	}
	return nil
}

func (x *RecallConfig) GetArms() []string {
	if x != nil {
		return x.Arms
	}
	return nil
}

func (x *RecallConfig) GetRerankerMaxCandidates() int32 {
	if x != nil && x.RerankerMaxCandidates != nil {
		return *x.RerankerMaxCandidates
	}
	return 0
}

func (x *RecallConfig) GetRerank() bool {
	if x != nil && x.Rerank != nil {
		return *x.Rerank
	}
	return false
}

func (x *RecallConfig) GetRrfK() int32 {
	if x != nil && x.RrfK != nil {
		return *x.RrfK
	}
	return 0
}

func (x *RecallConfig) GetRecencyAlpha() float64 {
	if x != nil && x.RecencyAlpha != nil {
		return *x.RecencyAlpha
	}
	return 0
}

func (x *RecallConfig) GetTemporalAlpha() float64 {
	if x != nil && x.TemporalAlpha != nil {
		return *x.TemporalAlpha
	}
	return 0
}

func (x *RecallConfig) GetProofAlpha() float64 {
	if x != nil && x.ProofAlpha != nil {
		return *x.ProofAlpha
	}
	return 0
}

func (x *RecallConfig) GetRecencyHorizonDays() int32 {
	if x != nil && x.RecencyHorizonDays != nil {
		return *x.RecencyHorizonDays
	}
	return 0
}

func (x *RecallConfig) GetGraphMaxHops() int32 {
	if x != nil && x.GraphMaxHops != nil {
		return *x.GraphMaxHops
	}
	return 0
}

func (x *RecallConfig) GetTemporalBuckets() int32 {
	if x != nil && x.TemporalBuckets != nil {
		return *x.TemporalBuckets
	}
	return 0
}

func (x *RecallConfig) GetFuzzyTagMinSimilarity() float64 {
	if x != nil && x.FuzzyTagMinSimilarity != nil {
		return *x.FuzzyTagMinSimilarity
	}
	return 0
}

func (x *RecallConfig) GetBudgetMin() int32 {
	if x != nil && x.BudgetMin != nil {
		return *x.BudgetMin
	}
	return 0
}

func (x *RecallConfig) GetBudgetMax() int32 {
	if x != nil && x.BudgetMax != nil {
		return *x.BudgetMax
	}
	return 0
}

// ReflectConfig tunes reflect: its mission and disposition, and the agent loop.
type ReflectConfig struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Loop iterations at BUDGET_MID, default 10; BUDGET_LOW halves it, BUDGET_HIGH doubles it.
	MaxIterations *int32 `protobuf:"varint,1,opt,name=max_iterations,json=maxIterations,proto3,oneof" json:"max_iterations,omitempty"`
	// Tools the agent may call; empty = all.
	EnabledTools []string `protobuf:"bytes,2,rep,name=enabled_tools,json=enabledTools,proto3" json:"enabled_tools,omitempty"`
	// Require at least one retrieval before the agent may answer.
	RequireEvidence *bool `protobuf:"varint,3,opt,name=require_evidence,json=requireEvidence,proto3,oneof" json:"require_evidence,omitempty"`
	// Budget for the recall tool.
	RecallBudget Budget `protobuf:"varint,4,opt,name=recall_budget,json=recallBudget,proto3,enum=memory.v1.Budget" json:"recall_budget,omitempty"`
	// Token cap on one recall tool result.
	RecallMaxTokens *int32 `protobuf:"varint,5,opt,name=recall_max_tokens,json=recallMaxTokens,proto3,oneof" json:"recall_max_tokens,omitempty"`
	// Token cap on one search_observations tool result, default 5000.
	SearchObservationsMaxTokens *int32 `protobuf:"varint,6,opt,name=search_observations_max_tokens,json=searchObservationsMaxTokens,proto3,oneof" json:"search_observations_max_tokens,omitempty"`
	// Include entity state in search_observations results, default true.
	SearchObservationsIncludeEntities *bool `protobuf:"varint,7,opt,name=search_observations_include_entities,json=searchObservationsIncludeEntities,proto3,oneof" json:"search_observations_include_entities,omitempty"`
	// Token cap on the final answer only, default 4096.
	MaxAnswerTokens *int32 `protobuf:"varint,8,opt,name=max_answer_tokens,json=maxAnswerTokens,proto3,oneof" json:"max_answer_tokens,omitempty"`
	// Token budget for the whole call; exceeding it fails the call with RESOURCE_EXHAUSTED.
	MaxTotalTokens *int64 `protobuf:"varint,9,opt,name=max_total_tokens,json=maxTotalTokens,proto3,oneof" json:"max_total_tokens,omitempty"`
	// Mission injected into the reflect system prompt (also used by mental-model refresh).
	Mission string `protobuf:"bytes,10,opt,name=mission,proto3" json:"mission,omitempty"`
	// Reflect disposition traits.
	Disposition   *Disposition `protobuf:"bytes,11,opt,name=disposition,proto3" json:"disposition,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ReflectConfig) Reset() {
	*x = ReflectConfig{}
	mi := &file_memory_v1_memory_proto_msgTypes[160]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ReflectConfig) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ReflectConfig) ProtoMessage() {}

func (x *ReflectConfig) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[160]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ReflectConfig.ProtoReflect.Descriptor instead.
func (*ReflectConfig) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{160}
}

func (x *ReflectConfig) GetMaxIterations() int32 {
	if x != nil && x.MaxIterations != nil {
		return *x.MaxIterations
	}
	return 0
}

func (x *ReflectConfig) GetEnabledTools() []string {
	if x != nil {
		return x.EnabledTools
	}
	return nil
}

func (x *ReflectConfig) GetRequireEvidence() bool {
	if x != nil && x.RequireEvidence != nil {
		return *x.RequireEvidence
	}
	return false
}

func (x *ReflectConfig) GetRecallBudget() Budget {
	if x != nil {
		return x.RecallBudget
	}
	return Budget_BUDGET_UNSPECIFIED
}

func (x *ReflectConfig) GetRecallMaxTokens() int32 {
	if x != nil && x.RecallMaxTokens != nil {
		return *x.RecallMaxTokens
	}
	return 0
}

func (x *ReflectConfig) GetSearchObservationsMaxTokens() int32 {
	if x != nil && x.SearchObservationsMaxTokens != nil {
		return *x.SearchObservationsMaxTokens
	}
	return 0
}

func (x *ReflectConfig) GetSearchObservationsIncludeEntities() bool {
	if x != nil && x.SearchObservationsIncludeEntities != nil {
		return *x.SearchObservationsIncludeEntities
	}
	return false
}

func (x *ReflectConfig) GetMaxAnswerTokens() int32 {
	if x != nil && x.MaxAnswerTokens != nil {
		return *x.MaxAnswerTokens
	}
	return 0
}

func (x *ReflectConfig) GetMaxTotalTokens() int64 {
	if x != nil && x.MaxTotalTokens != nil {
		return *x.MaxTotalTokens
	}
	return 0
}

func (x *ReflectConfig) GetMission() string {
	if x != nil {
		return x.Mission
	}
	return ""
}

func (x *ReflectConfig) GetDisposition() *Disposition {
	if x != nil {
		return x.Disposition
	}
	return nil
}

// MentalModelConfig holds bank-level mental-model defaults; a model's MentalModelSpec wins over them.
type MentalModelConfig struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Refresh mode when MentalModelSpec.mode is unset.
	DefaultMode RefreshMode `protobuf:"varint,1,opt,name=default_mode,json=defaultMode,proto3,enum=memory.v1.RefreshMode" json:"default_mode,omitempty"`
	// Largest share of the page a delta refresh may change before it falls back to a full refresh.
	MaxDeltaRatio *float64 `protobuf:"fixed64,2,opt,name=max_delta_ratio,json=maxDeltaRatio,proto3,oneof" json:"max_delta_ratio,omitempty"`
	// Delay before an automatic refresh runs, so a burst of consolidations joins one refresh, default 30.
	RefreshDebounceSeconds *int32 `protobuf:"varint,3,opt,name=refresh_debounce_seconds,json=refreshDebounceSeconds,proto3,oneof" json:"refresh_debounce_seconds,omitempty"`
	// Token cap on a model's content; MentalModelSpec.max_tokens wins.
	MaxContentTokens *int32 `protobuf:"varint,4,opt,name=max_content_tokens,json=maxContentTokens,proto3,oneof" json:"max_content_tokens,omitempty"`
	// Minimum seconds between automatic refreshes of one model, default 0 = no floor; RefreshSchedule's
	// min_interval_seconds wins. Manual refreshes ignore it.
	MinRefreshIntervalSeconds *int32 `protobuf:"varint,5,opt,name=min_refresh_interval_seconds,json=minRefreshIntervalSeconds,proto3,oneof" json:"min_refresh_interval_seconds,omitempty"`
	// History entries kept per kind (versions, failures), default 50.
	HistoryMaxEntries *int32 `protobuf:"varint,6,opt,name=history_max_entries,json=historyMaxEntries,proto3,oneof" json:"history_max_entries,omitempty"`
	// Refresh strategy when neither the spec nor the call sets one, default REFRESH_STRATEGY_AGENTIC.
	DefaultStrategy RefreshStrategy `protobuf:"varint,7,opt,name=default_strategy,json=defaultStrategy,proto3,enum=memory.v1.RefreshStrategy" json:"default_strategy,omitempty"`
	unknownFields   protoimpl.UnknownFields
	sizeCache       protoimpl.SizeCache
}

func (x *MentalModelConfig) Reset() {
	*x = MentalModelConfig{}
	mi := &file_memory_v1_memory_proto_msgTypes[161]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *MentalModelConfig) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*MentalModelConfig) ProtoMessage() {}

func (x *MentalModelConfig) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[161]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use MentalModelConfig.ProtoReflect.Descriptor instead.
func (*MentalModelConfig) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{161}
}

func (x *MentalModelConfig) GetDefaultMode() RefreshMode {
	if x != nil {
		return x.DefaultMode
	}
	return RefreshMode_REFRESH_MODE_UNSPECIFIED
}

func (x *MentalModelConfig) GetMaxDeltaRatio() float64 {
	if x != nil && x.MaxDeltaRatio != nil {
		return *x.MaxDeltaRatio
	}
	return 0
}

func (x *MentalModelConfig) GetRefreshDebounceSeconds() int32 {
	if x != nil && x.RefreshDebounceSeconds != nil {
		return *x.RefreshDebounceSeconds
	}
	return 0
}

func (x *MentalModelConfig) GetMaxContentTokens() int32 {
	if x != nil && x.MaxContentTokens != nil {
		return *x.MaxContentTokens
	}
	return 0
}

func (x *MentalModelConfig) GetMinRefreshIntervalSeconds() int32 {
	if x != nil && x.MinRefreshIntervalSeconds != nil {
		return *x.MinRefreshIntervalSeconds
	}
	return 0
}

func (x *MentalModelConfig) GetHistoryMaxEntries() int32 {
	if x != nil && x.HistoryMaxEntries != nil {
		return *x.HistoryMaxEntries
	}
	return 0
}

func (x *MentalModelConfig) GetDefaultStrategy() RefreshStrategy {
	if x != nil {
		return x.DefaultStrategy
	}
	return RefreshStrategy_REFRESH_STRATEGY_UNSPECIFIED
}

// ModelBindings binds model slots at one config level: default for every chat-model slot the level leaves
// unset, plus one optional binding per slot (see "Model routing" above). In a FieldMask each binding is one
// path (models.default, models.extract, ...): it is set or reset whole.
type ModelBindings struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Binds every chat-model slot (all but rerank and embed) this level leaves unset.
	Default *ModelBinding `protobuf:"bytes,1,opt,name=default,proto3" json:"default,omitempty"`
	// MODEL_SLOT_EXTRACT.
	Extract *ModelBinding `protobuf:"bytes,2,opt,name=extract,proto3" json:"extract,omitempty"`
	// MODEL_SLOT_TIME_PARSE.
	TimeParse *ModelBinding `protobuf:"bytes,3,opt,name=time_parse,json=timeParse,proto3" json:"time_parse,omitempty"`
	// MODEL_SLOT_CONSOLIDATE.
	Consolidate *ModelBinding `protobuf:"bytes,4,opt,name=consolidate,proto3" json:"consolidate,omitempty"`
	// MODEL_SLOT_DEDUP_CHECK.
	DedupCheck *ModelBinding `protobuf:"bytes,5,opt,name=dedup_check,json=dedupCheck,proto3" json:"dedup_check,omitempty"`
	// MODEL_SLOT_REFLECT.
	Reflect *ModelBinding `protobuf:"bytes,6,opt,name=reflect,proto3" json:"reflect,omitempty"`
	// MODEL_SLOT_STRUCTURED_OUTPUT.
	StructuredOutput *ModelBinding `protobuf:"bytes,7,opt,name=structured_output,json=structuredOutput,proto3" json:"structured_output,omitempty"`
	// MODEL_SLOT_MENTAL_MODEL.
	MentalModel *ModelBinding `protobuf:"bytes,8,opt,name=mental_model,json=mentalModel,proto3" json:"mental_model,omitempty"`
	// MODEL_SLOT_RERANK; never filled by default. Unbound = recall skips the cross-encoder.
	Rerank *ModelBinding `protobuf:"bytes,9,opt,name=rerank,proto3" json:"rerank,omitempty"`
	// MODEL_SLOT_EMBED; server config file only (INVALID_ARGUMENT anywhere else); only its model is used, which
	// must return 768 dimensions (checked at startup). Never filled by default.
	Embed         *ModelBinding `protobuf:"bytes,10,opt,name=embed,proto3" json:"embed,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ModelBindings) Reset() {
	*x = ModelBindings{}
	mi := &file_memory_v1_memory_proto_msgTypes[162]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ModelBindings) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ModelBindings) ProtoMessage() {}

func (x *ModelBindings) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[162]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ModelBindings.ProtoReflect.Descriptor instead.
func (*ModelBindings) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{162}
}

func (x *ModelBindings) GetDefault() *ModelBinding {
	if x != nil {
		return x.Default
	}
	return nil
}

func (x *ModelBindings) GetExtract() *ModelBinding {
	if x != nil {
		return x.Extract
	}
	return nil
}

func (x *ModelBindings) GetTimeParse() *ModelBinding {
	if x != nil {
		return x.TimeParse
	}
	return nil
}

func (x *ModelBindings) GetConsolidate() *ModelBinding {
	if x != nil {
		return x.Consolidate
	}
	return nil
}

func (x *ModelBindings) GetDedupCheck() *ModelBinding {
	if x != nil {
		return x.DedupCheck
	}
	return nil
}

func (x *ModelBindings) GetReflect() *ModelBinding {
	if x != nil {
		return x.Reflect
	}
	return nil
}

func (x *ModelBindings) GetStructuredOutput() *ModelBinding {
	if x != nil {
		return x.StructuredOutput
	}
	return nil
}

func (x *ModelBindings) GetMentalModel() *ModelBinding {
	if x != nil {
		return x.MentalModel
	}
	return nil
}

func (x *ModelBindings) GetRerank() *ModelBinding {
	if x != nil {
		return x.Rerank
	}
	return nil
}

func (x *ModelBindings) GetEmbed() *ModelBinding {
	if x != nil {
		return x.Embed
	}
	return nil
}

// ModelBinding is the gateway model and call parameters used for one slot. A binding is atomic: the
// level that supplies the slot supplies the whole binding, and any field it leaves unset means the gateway /
// adapter default. Unset fields are never inherited from a less specific level's binding of the same slot.
type ModelBinding struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Gateway model name, as sent in the OpenAI-compatible request; required.
	Model string `protobuf:"bytes,1,opt,name=model,proto3" json:"model,omitempty"`
	// Sampling temperature; unset = the gateway / adapter default.
	Temperature *float64 `protobuf:"fixed64,2,opt,name=temperature,proto3,oneof" json:"temperature,omitempty"`
	// Nucleus sampling cutoff; unset = the gateway / adapter default.
	TopP *float64 `protobuf:"fixed64,3,opt,name=top_p,json=topP,proto3,oneof" json:"top_p,omitempty"`
	// Maximum output tokens per call; unset = the gateway / adapter default.
	MaxOutputTokens *int32 `protobuf:"varint,4,opt,name=max_output_tokens,json=maxOutputTokens,proto3,oneof" json:"max_output_tokens,omitempty"`
	// Reasoning effort hint (for example low, medium, high); empty = the gateway / adapter default.
	ReasoningEffort string `protobuf:"bytes,5,opt,name=reasoning_effort,json=reasoningEffort,proto3" json:"reasoning_effort,omitempty"`
	// Per-call timeout in milliseconds; unset = the gateway / adapter default.
	TimeoutMs *int32 `protobuf:"varint,6,opt,name=timeout_ms,json=timeoutMs,proto3,oneof" json:"timeout_ms,omitempty"`
	// Gateway-call retries on transient errors; unset = the gateway / adapter default.
	MaxRetries *int32 `protobuf:"varint,7,opt,name=max_retries,json=maxRetries,proto3,oneof" json:"max_retries,omitempty"`
	// Free-form object passed through to the OpenAI-compatible request body untouched; unset = nothing
	// extra. Never merged with a less specific level's extra.
	Extra         *structpb.Struct `protobuf:"bytes,8,opt,name=extra,proto3" json:"extra,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ModelBinding) Reset() {
	*x = ModelBinding{}
	mi := &file_memory_v1_memory_proto_msgTypes[163]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ModelBinding) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ModelBinding) ProtoMessage() {}

func (x *ModelBinding) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[163]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ModelBinding.ProtoReflect.Descriptor instead.
func (*ModelBinding) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{163}
}

func (x *ModelBinding) GetModel() string {
	if x != nil {
		return x.Model
	}
	return ""
}

func (x *ModelBinding) GetTemperature() float64 {
	if x != nil && x.Temperature != nil {
		return *x.Temperature
	}
	return 0
}

func (x *ModelBinding) GetTopP() float64 {
	if x != nil && x.TopP != nil {
		return *x.TopP
	}
	return 0
}

func (x *ModelBinding) GetMaxOutputTokens() int32 {
	if x != nil && x.MaxOutputTokens != nil {
		return *x.MaxOutputTokens
	}
	return 0
}

func (x *ModelBinding) GetReasoningEffort() string {
	if x != nil {
		return x.ReasoningEffort
	}
	return ""
}

func (x *ModelBinding) GetTimeoutMs() int32 {
	if x != nil && x.TimeoutMs != nil {
		return *x.TimeoutMs
	}
	return 0
}

func (x *ModelBinding) GetMaxRetries() int32 {
	if x != nil && x.MaxRetries != nil {
		return *x.MaxRetries
	}
	return 0
}

func (x *ModelBinding) GetExtra() *structpb.Struct {
	if x != nil {
		return x.Extra
	}
	return nil
}

// RetainOverrides is what a Retain (request or item) and ReprocessDocument may override: the whole retain group
// and the EXTRACT and TIME_PARSE models.
type RetainOverrides struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Retain settings for this call.
	Retain *RetainConfig `protobuf:"bytes,1,opt,name=retain,proto3" json:"retain,omitempty"`
	// MODEL_SLOT_EXTRACT for this call.
	ExtractModel *ModelBinding `protobuf:"bytes,2,opt,name=extract_model,json=extractModel,proto3" json:"extract_model,omitempty"`
	// MODEL_SLOT_TIME_PARSE for this call.
	TimeParseModel *ModelBinding `protobuf:"bytes,3,opt,name=time_parse_model,json=timeParseModel,proto3" json:"time_parse_model,omitempty"`
	unknownFields  protoimpl.UnknownFields
	sizeCache      protoimpl.SizeCache
}

func (x *RetainOverrides) Reset() {
	*x = RetainOverrides{}
	mi := &file_memory_v1_memory_proto_msgTypes[164]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RetainOverrides) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RetainOverrides) ProtoMessage() {}

func (x *RetainOverrides) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[164]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RetainOverrides.ProtoReflect.Descriptor instead.
func (*RetainOverrides) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{164}
}

func (x *RetainOverrides) GetRetain() *RetainConfig {
	if x != nil {
		return x.Retain
	}
	return nil
}

func (x *RetainOverrides) GetExtractModel() *ModelBinding {
	if x != nil {
		return x.ExtractModel
	}
	return nil
}

func (x *RetainOverrides) GetTimeParseModel() *ModelBinding {
	if x != nil {
		return x.TimeParseModel
	}
	return nil
}

// RecallOverrides is what a Recall may override: the recall group and the TIME_PARSE and RERANK models.
type RecallOverrides struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Recall settings for this call. recall.arms must stay empty (INVALID_ARGUMENT otherwise): pick the arms
	// with RecallRequest.arms.
	Recall *RecallConfig `protobuf:"bytes,1,opt,name=recall,proto3" json:"recall,omitempty"`
	// MODEL_SLOT_TIME_PARSE for this call (query time windows).
	TimeParseModel *ModelBinding `protobuf:"bytes,2,opt,name=time_parse_model,json=timeParseModel,proto3" json:"time_parse_model,omitempty"`
	// MODEL_SLOT_RERANK for this call.
	RerankModel   *ModelBinding `protobuf:"bytes,3,opt,name=rerank_model,json=rerankModel,proto3" json:"rerank_model,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RecallOverrides) Reset() {
	*x = RecallOverrides{}
	mi := &file_memory_v1_memory_proto_msgTypes[165]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RecallOverrides) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RecallOverrides) ProtoMessage() {}

func (x *RecallOverrides) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[165]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RecallOverrides.ProtoReflect.Descriptor instead.
func (*RecallOverrides) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{165}
}

func (x *RecallOverrides) GetRecall() *RecallConfig {
	if x != nil {
		return x.Recall
	}
	return nil
}

func (x *RecallOverrides) GetTimeParseModel() *ModelBinding {
	if x != nil {
		return x.TimeParseModel
	}
	return nil
}

func (x *RecallOverrides) GetRerankModel() *ModelBinding {
	if x != nil {
		return x.RerankModel
	}
	return nil
}

// ReflectOverrides is what a Reflect may override: the reflect and recall groups and the REFLECT,
// STRUCTURED_OUTPUT and RERANK models.
type ReflectOverrides struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Reflect settings for this call.
	Reflect *ReflectConfig `protobuf:"bytes,1,opt,name=reflect,proto3" json:"reflect,omitempty"`
	// Recall settings of the agent's retrieval tools for this call. recall.arms must stay empty
	// (INVALID_ARGUMENT otherwise): pick the arms with ReflectRequest.arms.
	Recall *RecallConfig `protobuf:"bytes,2,opt,name=recall,proto3" json:"recall,omitempty"`
	// MODEL_SLOT_REFLECT for this call.
	ReflectModel *ModelBinding `protobuf:"bytes,3,opt,name=reflect_model,json=reflectModel,proto3" json:"reflect_model,omitempty"`
	// MODEL_SLOT_STRUCTURED_OUTPUT for this call.
	StructuredOutputModel *ModelBinding `protobuf:"bytes,4,opt,name=structured_output_model,json=structuredOutputModel,proto3" json:"structured_output_model,omitempty"`
	// MODEL_SLOT_RERANK for this call.
	RerankModel   *ModelBinding `protobuf:"bytes,5,opt,name=rerank_model,json=rerankModel,proto3" json:"rerank_model,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ReflectOverrides) Reset() {
	*x = ReflectOverrides{}
	mi := &file_memory_v1_memory_proto_msgTypes[166]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ReflectOverrides) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ReflectOverrides) ProtoMessage() {}

func (x *ReflectOverrides) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[166]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ReflectOverrides.ProtoReflect.Descriptor instead.
func (*ReflectOverrides) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{166}
}

func (x *ReflectOverrides) GetReflect() *ReflectConfig {
	if x != nil {
		return x.Reflect
	}
	return nil
}

func (x *ReflectOverrides) GetRecall() *RecallConfig {
	if x != nil {
		return x.Recall
	}
	return nil
}

func (x *ReflectOverrides) GetReflectModel() *ModelBinding {
	if x != nil {
		return x.ReflectModel
	}
	return nil
}

func (x *ReflectOverrides) GetStructuredOutputModel() *ModelBinding {
	if x != nil {
		return x.StructuredOutputModel
	}
	return nil
}

func (x *ReflectOverrides) GetRerankModel() *ModelBinding {
	if x != nil {
		return x.RerankModel
	}
	return nil
}

// ConsolidateOverrides is what a manual Consolidate may override: the observations group except enabled,
// auto_consolidation and reconcile_grace_seconds, and the CONSOLIDATE and DEDUP_CHECK models.
type ConsolidateOverrides struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Observations mission for this run.
	Mission string `protobuf:"bytes,1,opt,name=mission,proto3" json:"mission,omitempty"`
	// Cap on observations per scope.
	MaxObservationsPerScope *int32 `protobuf:"varint,2,opt,name=max_observations_per_scope,json=maxObservationsPerScope,proto3,oneof" json:"max_observations_per_scope,omitempty"`
	// Token budget for source facts shown to one consolidation call.
	SourceFactsMaxTokens *int32 `protobuf:"varint,3,opt,name=source_facts_max_tokens,json=sourceFactsMaxTokens,proto3,oneof" json:"source_facts_max_tokens,omitempty"`
	// Token budget for the source facts shown per existing observation.
	SourceFactsMaxTokensPerObservation *int32 `protobuf:"varint,4,opt,name=source_facts_max_tokens_per_observation,json=sourceFactsMaxTokensPerObservation,proto3,oneof" json:"source_facts_max_tokens_per_observation,omitempty"`
	// Facts per consolidation LLM call.
	LlmBatchSize *int32 `protobuf:"varint,5,opt,name=llm_batch_size,json=llmBatchSize,proto3,oneof" json:"llm_batch_size,omitempty"`
	// Memories one consolidation run takes before it re-enqueues itself.
	MaxUnitsPerRun *int32 `protobuf:"varint,6,opt,name=max_units_per_run,json=maxUnitsPerRun,proto3,oneof" json:"max_units_per_run,omitempty"`
	// Near-duplicate cosine threshold.
	DedupThreshold *float64 `protobuf:"fixed64,7,opt,name=dedup_threshold,json=dedupThreshold,proto3,oneof" json:"dedup_threshold,omitempty"`
	// Strategies for this run; they replace the bank's list.
	Strategies []*ConsolidationStrategy `protobuf:"bytes,8,rep,name=strategies,proto3" json:"strategies,omitempty"`
	// MODEL_SLOT_CONSOLIDATE for this run.
	ConsolidateModel *ModelBinding `protobuf:"bytes,9,opt,name=consolidate_model,json=consolidateModel,proto3" json:"consolidate_model,omitempty"`
	// MODEL_SLOT_DEDUP_CHECK for this run.
	DedupCheckModel *ModelBinding `protobuf:"bytes,10,opt,name=dedup_check_model,json=dedupCheckModel,proto3" json:"dedup_check_model,omitempty"`
	unknownFields   protoimpl.UnknownFields
	sizeCache       protoimpl.SizeCache
}

func (x *ConsolidateOverrides) Reset() {
	*x = ConsolidateOverrides{}
	mi := &file_memory_v1_memory_proto_msgTypes[167]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ConsolidateOverrides) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ConsolidateOverrides) ProtoMessage() {}

func (x *ConsolidateOverrides) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[167]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ConsolidateOverrides.ProtoReflect.Descriptor instead.
func (*ConsolidateOverrides) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{167}
}

func (x *ConsolidateOverrides) GetMission() string {
	if x != nil {
		return x.Mission
	}
	return ""
}

func (x *ConsolidateOverrides) GetMaxObservationsPerScope() int32 {
	if x != nil && x.MaxObservationsPerScope != nil {
		return *x.MaxObservationsPerScope
	}
	return 0
}

func (x *ConsolidateOverrides) GetSourceFactsMaxTokens() int32 {
	if x != nil && x.SourceFactsMaxTokens != nil {
		return *x.SourceFactsMaxTokens
	}
	return 0
}

func (x *ConsolidateOverrides) GetSourceFactsMaxTokensPerObservation() int32 {
	if x != nil && x.SourceFactsMaxTokensPerObservation != nil {
		return *x.SourceFactsMaxTokensPerObservation
	}
	return 0
}

func (x *ConsolidateOverrides) GetLlmBatchSize() int32 {
	if x != nil && x.LlmBatchSize != nil {
		return *x.LlmBatchSize
	}
	return 0
}

func (x *ConsolidateOverrides) GetMaxUnitsPerRun() int32 {
	if x != nil && x.MaxUnitsPerRun != nil {
		return *x.MaxUnitsPerRun
	}
	return 0
}

func (x *ConsolidateOverrides) GetDedupThreshold() float64 {
	if x != nil && x.DedupThreshold != nil {
		return *x.DedupThreshold
	}
	return 0
}

func (x *ConsolidateOverrides) GetStrategies() []*ConsolidationStrategy {
	if x != nil {
		return x.Strategies
	}
	return nil
}

func (x *ConsolidateOverrides) GetConsolidateModel() *ModelBinding {
	if x != nil {
		return x.ConsolidateModel
	}
	return nil
}

func (x *ConsolidateOverrides) GetDedupCheckModel() *ModelBinding {
	if x != nil {
		return x.DedupCheckModel
	}
	return nil
}

// RederiveOverrides is what re-deriving an edited or restored memory may override: entity matching and the
// EXTRACT and TIME_PARSE models.
type RederiveOverrides struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Minimum resolver score to reuse an entity.
	EntityMatchThreshold *float64 `protobuf:"fixed64,1,opt,name=entity_match_threshold,json=entityMatchThreshold,proto3,oneof" json:"entity_match_threshold,omitempty"`
	// MODEL_SLOT_EXTRACT for the rederive.
	ExtractModel *ModelBinding `protobuf:"bytes,2,opt,name=extract_model,json=extractModel,proto3" json:"extract_model,omitempty"`
	// MODEL_SLOT_TIME_PARSE for the rederive.
	TimeParseModel *ModelBinding `protobuf:"bytes,3,opt,name=time_parse_model,json=timeParseModel,proto3" json:"time_parse_model,omitempty"`
	unknownFields  protoimpl.UnknownFields
	sizeCache      protoimpl.SizeCache
}

func (x *RederiveOverrides) Reset() {
	*x = RederiveOverrides{}
	mi := &file_memory_v1_memory_proto_msgTypes[168]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RederiveOverrides) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RederiveOverrides) ProtoMessage() {}

func (x *RederiveOverrides) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[168]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RederiveOverrides.ProtoReflect.Descriptor instead.
func (*RederiveOverrides) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{168}
}

func (x *RederiveOverrides) GetEntityMatchThreshold() float64 {
	if x != nil && x.EntityMatchThreshold != nil {
		return *x.EntityMatchThreshold
	}
	return 0
}

func (x *RederiveOverrides) GetExtractModel() *ModelBinding {
	if x != nil {
		return x.ExtractModel
	}
	return nil
}

func (x *RederiveOverrides) GetTimeParseModel() *ModelBinding {
	if x != nil {
		return x.TimeParseModel
	}
	return nil
}

// RefreshOverrides is what a mental-model refresh may override, per call or stored on the model: the reflect
// group, the delta ratio, and the REFLECT, MENTAL_MODEL and STRUCTURED_OUTPUT models.
type RefreshOverrides struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Reflect settings for the refresh: mission, disposition, and the loop with its tool caps
	// (recall_max_tokens, search_observations_max_tokens, search_observations_include_entities).
	Reflect *ReflectConfig `protobuf:"bytes,1,opt,name=reflect,proto3" json:"reflect,omitempty"`
	// Largest share of the page a delta refresh may change before it falls back to a full refresh.
	MaxDeltaRatio *float64 `protobuf:"fixed64,2,opt,name=max_delta_ratio,json=maxDeltaRatio,proto3,oneof" json:"max_delta_ratio,omitempty"`
	// MODEL_SLOT_REFLECT (agentic refresh).
	ReflectModel *ModelBinding `protobuf:"bytes,3,opt,name=reflect_model,json=reflectModel,proto3" json:"reflect_model,omitempty"`
	// MODEL_SLOT_MENTAL_MODEL (single-shot refresh).
	MentalModelModel *ModelBinding `protobuf:"bytes,4,opt,name=mental_model_model,json=mentalModelModel,proto3" json:"mental_model_model,omitempty"`
	// MODEL_SLOT_STRUCTURED_OUTPUT.
	StructuredOutputModel *ModelBinding `protobuf:"bytes,5,opt,name=structured_output_model,json=structuredOutputModel,proto3" json:"structured_output_model,omitempty"`
	unknownFields         protoimpl.UnknownFields
	sizeCache             protoimpl.SizeCache
}

func (x *RefreshOverrides) Reset() {
	*x = RefreshOverrides{}
	mi := &file_memory_v1_memory_proto_msgTypes[169]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RefreshOverrides) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RefreshOverrides) ProtoMessage() {}

func (x *RefreshOverrides) ProtoReflect() protoreflect.Message {
	mi := &file_memory_v1_memory_proto_msgTypes[169]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RefreshOverrides.ProtoReflect.Descriptor instead.
func (*RefreshOverrides) Descriptor() ([]byte, []int) {
	return file_memory_v1_memory_proto_rawDescGZIP(), []int{169}
}

func (x *RefreshOverrides) GetReflect() *ReflectConfig {
	if x != nil {
		return x.Reflect
	}
	return nil
}

func (x *RefreshOverrides) GetMaxDeltaRatio() float64 {
	if x != nil && x.MaxDeltaRatio != nil {
		return *x.MaxDeltaRatio
	}
	return 0
}

func (x *RefreshOverrides) GetReflectModel() *ModelBinding {
	if x != nil {
		return x.ReflectModel
	}
	return nil
}

func (x *RefreshOverrides) GetMentalModelModel() *ModelBinding {
	if x != nil {
		return x.MentalModelModel
	}
	return nil
}

func (x *RefreshOverrides) GetStructuredOutputModel() *ModelBinding {
	if x != nil {
		return x.StructuredOutputModel
	}
	return nil
}

var File_memory_v1_memory_proto protoreflect.FileDescriptor

const file_memory_v1_memory_proto_rawDesc = "" +
	"\n" +
	"\x16memory/v1/memory.proto\x12\tmemory.v1\x1a google/protobuf/field_mask.proto\x1a\x1cgoogle/protobuf/struct.proto\x1a\x1fgoogle/protobuf/timestamp.proto\"\x80\x02\n" +
	"\rRetainRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12+\n" +
	"\x05items\x18\x02 \x03(\v2\x15.memory.v1.RetainItemR\x05items\x12#\n" +
	"\rdocument_tags\x18\x03 \x03(\tR\fdocumentTags\x128\n" +
	"\toverrides\x18\x04 \x01(\v2\x1a.memory.v1.RetainOverridesR\toverrides\x12-\n" +
	"\x12skip_consolidation\x18\x05 \x01(\bR\x11skipConsolidation\x12\x1b\n" +
	"\trun_async\x18\x06 \x01(\bR\brunAsync\"\xb6\x01\n" +
	"\x0eRetainResponse\x129\n" +
	"\tdocuments\x18\x01 \x03(\v2\x1b.memory.v1.RetainedDocumentR\tdocuments\x12=\n" +
	"\x0fbatch_operation\x18\x02 \x01(\v2\x14.memory.v1.OperationR\x0ebatchOperation\x12*\n" +
	"\x05usage\x18\x03 \x03(\v2\x14.memory.v1.SlotUsageR\x05usage\"\xfe\x04\n" +
	"\n" +
	"RetainItem\x12\x18\n" +
	"\acontent\x18\x01 \x01(\tR\acontent\x12\x18\n" +
	"\acontext\x18\x02 \x01(\tR\acontext\x128\n" +
	"\ttimestamp\x18\x03 \x01(\v2\x1a.google.protobuf.TimestampR\ttimestamp\x12\x1a\n" +
	"\btimeless\x18\x04 \x01(\bR\btimeless\x12\x1f\n" +
	"\vdocument_id\x18\x05 \x01(\tR\n" +
	"documentId\x12\x12\n" +
	"\x04tags\x18\x06 \x03(\tR\x04tags\x12X\n" +
	"\x17observation_scope_modes\x18\a \x01(\v2 .memory.v1.ObservationScopeModesR\x15observationScopeModes\x12?\n" +
	"\bmetadata\x18\b \x03(\v2#.memory.v1.RetainItem.MetadataEntryR\bmetadata\x121\n" +
	"\bentities\x18\t \x03(\v2\x15.memory.v1.EntityHintR\bentities\x12.\n" +
	"\x10resolve_entities\x18\n" +
	" \x01(\bH\x00R\x0fresolveEntities\x88\x01\x01\x128\n" +
	"\toverrides\x18\v \x01(\v2\x1a.memory.v1.RetainOverridesR\toverrides\x12'\n" +
	"\x0fforce_reextract\x18\f \x01(\bR\x0eforceReextract\x1a;\n" +
	"\rMetadataEntry\x12\x10\n" +
	"\x03key\x18\x01 \x01(\tR\x03key\x12\x14\n" +
	"\x05value\x18\x02 \x01(\tR\x05value:\x028\x01B\x13\n" +
	"\x11_resolve_entities\"4\n" +
	"\n" +
	"EntityHint\x12\x12\n" +
	"\x04text\x18\x01 \x01(\tR\x04text\x12\x12\n" +
	"\x04type\x18\x02 \x01(\tR\x04type\"\xb0\x01\n" +
	"\x10RetainedDocument\x12\x1f\n" +
	"\vdocument_id\x18\x01 \x01(\tR\n" +
	"documentId\x12)\n" +
	"\x10memories_created\x18\x02 \x01(\x05R\x0fmemoriesCreated\x12\x1c\n" +
	"\tunchanged\x18\x03 \x01(\bR\tunchanged\x122\n" +
	"\toperation\x18\x04 \x01(\v2\x14.memory.v1.OperationR\toperation\"\xab\x05\n" +
	"\rRecallRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12\x14\n" +
	"\x05query\x18\x02 \x01(\tR\x05query\x122\n" +
	"\n" +
	"fact_types\x18\x03 \x03(\x0e2\x13.memory.v1.FactTypeR\tfactTypes\x12(\n" +
	"\x04tags\x18\x04 \x01(\v2\x14.memory.v1.TagFilterR\x04tags\x12)\n" +
	"\x06budget\x18\x05 \x01(\x0e2\x11.memory.v1.BudgetR\x06budget\x12\x1d\n" +
	"\n" +
	"max_tokens\x18\x06 \x01(\x05R\tmaxTokens\x129\n" +
	"\n" +
	"query_time\x18\a \x01(\v2\x1a.google.protobuf.TimestampR\tqueryTime\x12F\n" +
	"\x11time_window_start\x18\b \x01(\v2\x1a.google.protobuf.TimestampR\x0ftimeWindowStart\x12B\n" +
	"\x0ftime_window_end\x18\t \x01(\v2\x1a.google.protobuf.TimestampR\rtimeWindowEnd\x12/\n" +
	"\x13prefer_observations\x18\n" +
	" \x01(\bR\x12preferObservations\x123\n" +
	"\n" +
	"min_scores\x18\v \x01(\v2\x14.memory.v1.MinScoresR\tminScores\x122\n" +
	"\ainclude\x18\f \x01(\v2\x18.memory.v1.RecallIncludeR\ainclude\x12\x14\n" +
	"\x05trace\x18\r \x01(\bR\x05trace\x128\n" +
	"\toverrides\x18\x0e \x01(\v2\x1a.memory.v1.RecallOverridesR\toverrides\x12\x12\n" +
	"\x04arms\x18\x0f \x03(\tR\x04arms\"\xe9\x04\n" +
	"\x0eRecallResponse\x12+\n" +
	"\aresults\x18\x01 \x03(\v2\x11.memory.v1.MemoryR\aresults\x12M\n" +
	"\fsource_facts\x18\x02 \x03(\v2*.memory.v1.RecallResponse.SourceFactsEntryR\vsourceFacts\x124\n" +
	"\x16source_facts_truncated\x18\x03 \x01(\bR\x14sourceFactsTruncated\x12=\n" +
	"\x06chunks\x18\x04 \x03(\v2%.memory.v1.RecallResponse.ChunksEntryR\x06chunks\x12C\n" +
	"\bentities\x18\x05 \x03(\v2'.memory.v1.RecallResponse.EntitiesEntryR\bentities\x12,\n" +
	"\x05trace\x18\x06 \x01(\v2\x16.memory.v1.RecallTraceR\x05trace\x1aQ\n" +
	"\x10SourceFactsEntry\x12\x10\n" +
	"\x03key\x18\x01 \x01(\tR\x03key\x12'\n" +
	"\x05value\x18\x02 \x01(\v2\x11.memory.v1.MemoryR\x05value:\x028\x01\x1aK\n" +
	"\vChunksEntry\x12\x10\n" +
	"\x03key\x18\x01 \x01(\tR\x03key\x12&\n" +
	"\x05value\x18\x02 \x01(\v2\x10.memory.v1.ChunkR\x05value:\x028\x01\x1aS\n" +
	"\rEntitiesEntry\x12\x10\n" +
	"\x03key\x18\x01 \x01(\tR\x03key\x12,\n" +
	"\x05value\x18\x02 \x01(\v2\x16.memory.v1.EntityStateR\x05value:\x028\x01\"\xbd\x02\n" +
	"\rRecallInclude\x12\x1b\n" +
	"\x06chunks\x18\x01 \x01(\bH\x00R\x06chunks\x88\x01\x01\x12/\n" +
	"\x11chunks_max_tokens\x18\x02 \x01(\x05H\x01R\x0fchunksMaxTokens\x88\x01\x01\x12&\n" +
	"\fsource_facts\x18\x03 \x01(\bH\x02R\vsourceFacts\x88\x01\x01\x12:\n" +
	"\x17source_facts_max_tokens\x18\x04 \x01(\x05H\x03R\x14sourceFactsMaxTokens\x88\x01\x01\x12\x1f\n" +
	"\bentities\x18\x05 \x01(\bH\x04R\bentities\x88\x01\x01B\t\n" +
	"\a_chunksB\x14\n" +
	"\x12_chunks_max_tokensB\x0f\n" +
	"\r_source_factsB\x1a\n" +
	"\x18_source_facts_max_tokensB\v\n" +
	"\t_entities\"z\n" +
	"\vEntityState\x12\x1b\n" +
	"\tentity_id\x18\x01 \x01(\tR\bentityId\x12%\n" +
	"\x0ecanonical_name\x18\x02 \x01(\tR\rcanonicalName\x12'\n" +
	"\x0fobservation_ids\x18\x03 \x03(\tR\x0eobservationIds\"\xab\x04\n" +
	"\x0eReflectRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12\x14\n" +
	"\x05query\x18\x02 \x01(\tR\x05query\x12(\n" +
	"\x04tags\x18\x03 \x01(\v2\x14.memory.v1.TagFilterR\x04tags\x12)\n" +
	"\x06budget\x18\x04 \x01(\x0e2\x11.memory.v1.BudgetR\x06budget\x12\"\n" +
	"\n" +
	"max_tokens\x18\x05 \x01(\x05H\x00R\tmaxTokens\x88\x01\x01\x120\n" +
	"\x14response_schema_json\x18\x06 \x01(\tR\x12responseSchemaJson\x120\n" +
	"\x14apply_all_directives\x18\a \x01(\bR\x12applyAllDirectives\x12?\n" +
	"\x10extra_directives\x18\b \x03(\v2\x14.memory.v1.DirectiveR\x0fextraDirectives\x123\n" +
	"\ainclude\x18\t \x01(\v2\x19.memory.v1.ReflectIncludeR\ainclude\x129\n" +
	"\n" +
	"query_time\x18\n" +
	" \x01(\v2\x1a.google.protobuf.TimestampR\tqueryTime\x129\n" +
	"\toverrides\x18\v \x01(\v2\x1b.memory.v1.ReflectOverridesR\toverrides\x12\x12\n" +
	"\x04arms\x18\f \x03(\tR\x04armsB\r\n" +
	"\v_max_tokens\"\x9d\x02\n" +
	"\x0fReflectResponse\x12\x12\n" +
	"\x04text\x18\x01 \x01(\tR\x04text\x124\n" +
	"\x16structured_output_json\x18\x02 \x01(\tR\x14structuredOutputJson\x126\n" +
	"\x17structured_output_error\x18\x03 \x01(\tR\x15structuredOutputError\x12-\n" +
	"\bbased_on\x18\x04 \x01(\v2\x12.memory.v1.BasedOnR\abasedOn\x12-\n" +
	"\x05trace\x18\x05 \x01(\v2\x17.memory.v1.ReflectTraceR\x05trace\x12*\n" +
	"\x05usage\x18\x06 \x03(\v2\x14.memory.v1.SlotUsageR\x05usage\"\x8c\x01\n" +
	"\x0eReflectInclude\x12\x1e\n" +
	"\bbased_on\x18\x01 \x01(\bH\x00R\abasedOn\x88\x01\x01\x12\x14\n" +
	"\x05trace\x18\x02 \x01(\bR\x05trace\x12&\n" +
	"\ftool_outputs\x18\x03 \x01(\bH\x01R\vtoolOutputs\x88\x01\x01B\v\n" +
	"\t_based_onB\x0f\n" +
	"\r_tool_outputs\"\x87\x01\n" +
	"\aBasedOn\x12-\n" +
	"\bmemories\x18\x01 \x03(\v2\x11.memory.v1.MemoryR\bmemories\x12(\n" +
	"\x10mental_model_ids\x18\x02 \x03(\tR\x0ementalModelIds\x12#\n" +
	"\rdirective_ids\x18\x03 \x03(\tR\fdirectiveIds\"\x17\n" +
	"\x15ListRecallArmsRequest\"B\n" +
	"\x16ListRecallArmsResponse\x12(\n" +
	"\x04arms\x18\x01 \x03(\v2\x14.memory.v1.RecallArmR\x04arms\"A\n" +
	"\tRecallArm\x12\x12\n" +
	"\x04name\x18\x01 \x01(\tR\x04name\x12 \n" +
	"\vdescription\x18\x02 \x01(\tR\vdescription\"\xbb\x05\n" +
	"\x13ListMemoriesRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x122\n" +
	"\n" +
	"fact_types\x18\x02 \x03(\x0e2\x13.memory.v1.FactTypeR\tfactTypes\x12#\n" +
	"\rtext_contains\x18\x03 \x01(\tR\ftextContains\x12\x1f\n" +
	"\vdocument_id\x18\x04 \x01(\tR\n" +
	"documentId\x12\x1b\n" +
	"\tentity_id\x18\x05 \x01(\tR\bentityId\x12,\n" +
	"\x05state\x18\x06 \x01(\x0e2\x16.memory.v1.MemoryStateR\x05state\x120\n" +
	"\x05start\x18\a \x01(\v2\x1a.google.protobuf.TimestampR\x05start\x12,\n" +
	"\x03end\x18\b \x01(\v2\x1a.google.protobuf.TimestampR\x03end\x12G\n" +
	"\n" +
	"time_field\x18\t \x01(\x0e2(.memory.v1.ListMemoriesRequest.TimeFieldR\ttimeField\x12(\n" +
	"\x04tags\x18\n" +
	" \x01(\v2\x14.memory.v1.TagFilterR\x04tags\x12\x1b\n" +
	"\tpage_size\x18\v \x01(\x05R\bpageSize\x12\x1d\n" +
	"\n" +
	"page_token\x18\f \x01(\tR\tpageToken\"\xb6\x01\n" +
	"\tTimeField\x12\x1a\n" +
	"\x16TIME_FIELD_UNSPECIFIED\x10\x00\x12\x19\n" +
	"\x15TIME_FIELD_CREATED_AT\x10\x01\x12\x19\n" +
	"\x15TIME_FIELD_UPDATED_AT\x10\x02\x12\x1b\n" +
	"\x17TIME_FIELD_MENTIONED_AT\x10\x03\x12\x1d\n" +
	"\x19TIME_FIELD_OCCURRED_START\x10\x04\x12\x1b\n" +
	"\x17TIME_FIELD_OCCURRED_END\x10\x05\"m\n" +
	"\x14ListMemoriesResponse\x12-\n" +
	"\bmemories\x18\x01 \x03(\v2\x11.memory.v1.MemoryR\bmemories\x12&\n" +
	"\x0fnext_page_token\x18\x02 \x01(\tR\rnextPageToken\"H\n" +
	"\x10GetMemoryRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12\x1b\n" +
	"\tmemory_id\x18\x02 \x01(\tR\bmemoryId\">\n" +
	"\x11GetMemoryResponse\x12)\n" +
	"\x06memory\x18\x01 \x01(\v2\x11.memory.v1.MemoryR\x06memory\"\xd0\x02\n" +
	"\x13UpdateMemoryRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12\x1b\n" +
	"\tmemory_id\x18\x02 \x01(\tR\bmemoryId\x12-\n" +
	"\x06memory\x18\x03 \x01(\v2\x15.memory.v1.MemoryEditR\x06memory\x12;\n" +
	"\vupdate_mask\x18\x04 \x01(\v2\x1a.google.protobuf.FieldMaskR\n" +
	"updateMask\x12.\n" +
	"\x10resolve_entities\x18\x05 \x01(\bH\x00R\x0fresolveEntities\x88\x01\x01\x12\x16\n" +
	"\x06reason\x18\x06 \x01(\tR\x06reason\x12:\n" +
	"\toverrides\x18\a \x01(\v2\x1c.memory.v1.RederiveOverridesR\toverridesB\x13\n" +
	"\x11_resolve_entities\"u\n" +
	"\x14UpdateMemoryResponse\x12)\n" +
	"\x06memory\x18\x01 \x01(\v2\x11.memory.v1.MemoryR\x06memory\x122\n" +
	"\toperation\x18\x02 \x01(\v2\x14.memory.v1.OperationR\toperation\"\x8a\x02\n" +
	"\n" +
	"MemoryEdit\x12\x12\n" +
	"\x04text\x18\x01 \x01(\tR\x04text\x12\x18\n" +
	"\acontext\x18\x02 \x01(\tR\acontext\x12A\n" +
	"\x0eoccurred_start\x18\x03 \x01(\v2\x1a.google.protobuf.TimestampR\roccurredStart\x12=\n" +
	"\foccurred_end\x18\x04 \x01(\v2\x1a.google.protobuf.TimestampR\voccurredEnd\x120\n" +
	"\tfact_type\x18\x05 \x01(\x0e2\x13.memory.v1.FactTypeR\bfactType\x12\x1a\n" +
	"\bentities\x18\x06 \x03(\tR\bentities\"g\n" +
	"\x17InvalidateMemoryRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12\x1b\n" +
	"\tmemory_id\x18\x02 \x01(\tR\bmemoryId\x12\x16\n" +
	"\x06reason\x18\x03 \x01(\tR\x06reason\"E\n" +
	"\x18InvalidateMemoryResponse\x12)\n" +
	"\x06memory\x18\x01 \x01(\v2\x11.memory.v1.MemoryR\x06memory\"\xe5\x01\n" +
	"\x14RestoreMemoryRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12\x1b\n" +
	"\tmemory_id\x18\x02 \x01(\tR\bmemoryId\x12\x16\n" +
	"\x06reason\x18\x03 \x01(\tR\x06reason\x12.\n" +
	"\x10resolve_entities\x18\x04 \x01(\bH\x00R\x0fresolveEntities\x88\x01\x01\x12:\n" +
	"\toverrides\x18\x05 \x01(\v2\x1c.memory.v1.RederiveOverridesR\toverridesB\x13\n" +
	"\x11_resolve_entities\"v\n" +
	"\x15RestoreMemoryResponse\x12)\n" +
	"\x06memory\x18\x01 \x01(\v2\x11.memory.v1.MemoryR\x06memory\x122\n" +
	"\toperation\x18\x02 \x01(\v2\x14.memory.v1.OperationR\toperation\"\x90\x06\n" +
	"\x06Memory\x12\x0e\n" +
	"\x02id\x18\x01 \x01(\tR\x02id\x120\n" +
	"\tfact_type\x18\x02 \x01(\x0e2\x13.memory.v1.FactTypeR\bfactType\x12\x12\n" +
	"\x04text\x18\x03 \x01(\tR\x04text\x12\x18\n" +
	"\acontext\x18\x04 \x01(\tR\acontext\x12;\n" +
	"\bmetadata\x18\x05 \x03(\v2\x1f.memory.v1.Memory.MetadataEntryR\bmetadata\x12\x12\n" +
	"\x04tags\x18\x06 \x03(\tR\x04tags\x12\x1a\n" +
	"\bentities\x18\a \x03(\tR\bentities\x12A\n" +
	"\x0eoccurred_start\x18\b \x01(\v2\x1a.google.protobuf.TimestampR\roccurredStart\x12=\n" +
	"\foccurred_end\x18\t \x01(\v2\x1a.google.protobuf.TimestampR\voccurredEnd\x12=\n" +
	"\fmentioned_at\x18\n" +
	" \x01(\v2\x1a.google.protobuf.TimestampR\vmentionedAt\x12\x1f\n" +
	"\vdocument_id\x18\v \x01(\tR\n" +
	"documentId\x12\x19\n" +
	"\bchunk_id\x18\f \x01(\tR\achunkId\x12\x1f\n" +
	"\vproof_count\x18\r \x01(\x05R\n" +
	"proofCount\x12&\n" +
	"\x0fsource_fact_ids\x18\x0e \x03(\tR\rsourceFactIds\x12)\n" +
	"\x06scores\x18\x0f \x01(\v2\x11.memory.v1.ScoresR\x06scores\x12,\n" +
	"\x05state\x18\x10 \x01(\x0e2\x16.memory.v1.MemoryStateR\x05state\x127\n" +
	"\tedited_at\x18\x11 \x01(\v2\x1a.google.protobuf.TimestampR\beditedAt\x12\x14\n" +
	"\x05stale\x18\x12 \x01(\bR\x05stale\x1a;\n" +
	"\rMetadataEntry\x12\x10\n" +
	"\x03key\x18\x01 \x01(\tR\x03key\x12\x14\n" +
	"\x05value\x18\x02 \x01(\tR\x05value:\x028\x01\"\xd0\x03\n" +
	"\x14ListDocumentsRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x120\n" +
	"\x14document_id_contains\x18\x02 \x01(\tR\x12documentIdContains\x12(\n" +
	"\x04tags\x18\x03 \x01(\v2\x14.memory.v1.TagFilterR\x04tags\x120\n" +
	"\x05start\x18\x04 \x01(\v2\x1a.google.protobuf.TimestampR\x05start\x12,\n" +
	"\x03end\x18\x05 \x01(\v2\x1a.google.protobuf.TimestampR\x03end\x12H\n" +
	"\n" +
	"time_field\x18\x06 \x01(\x0e2).memory.v1.ListDocumentsRequest.TimeFieldR\ttimeField\x12\x1b\n" +
	"\tpage_size\x18\a \x01(\x05R\bpageSize\x12\x1d\n" +
	"\n" +
	"page_token\x18\b \x01(\tR\tpageToken\"]\n" +
	"\tTimeField\x12\x1a\n" +
	"\x16TIME_FIELD_UNSPECIFIED\x10\x00\x12\x19\n" +
	"\x15TIME_FIELD_CREATED_AT\x10\x01\x12\x19\n" +
	"\x15TIME_FIELD_UPDATED_AT\x10\x02\"r\n" +
	"\x15ListDocumentsResponse\x121\n" +
	"\tdocuments\x18\x01 \x03(\v2\x13.memory.v1.DocumentR\tdocuments\x12&\n" +
	"\x0fnext_page_token\x18\x02 \x01(\tR\rnextPageToken\"q\n" +
	"\x12GetDocumentRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12\x1f\n" +
	"\vdocument_id\x18\x02 \x01(\tR\n" +
	"documentId\x12!\n" +
	"\finclude_text\x18\x03 \x01(\bR\vincludeText\"F\n" +
	"\x13GetDocumentResponse\x12/\n" +
	"\bdocument\x18\x01 \x01(\v2\x13.memory.v1.DocumentR\bdocument\"w\n" +
	"\x14RetagDocumentRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12\x1f\n" +
	"\vdocument_id\x18\x02 \x01(\tR\n" +
	"documentId\x12%\n" +
	"\x04tags\x18\x03 \x01(\v2\x11.memory.v1.TagSetR\x04tags\"K\n" +
	"\x15RetagDocumentResponse\x122\n" +
	"\toperation\x18\x01 \x01(\v2\x14.memory.v1.OperationR\toperation\"\x8e\x01\n" +
	"\x18ReprocessDocumentRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12\x1f\n" +
	"\vdocument_id\x18\x02 \x01(\tR\n" +
	"documentId\x128\n" +
	"\toverrides\x18\x03 \x01(\v2\x1a.memory.v1.RetainOverridesR\toverrides\"O\n" +
	"\x19ReprocessDocumentResponse\x122\n" +
	"\toperation\x18\x01 \x01(\v2\x14.memory.v1.OperationR\toperation\"\xbc\x04\n" +
	"\bDocument\x12\x0e\n" +
	"\x02id\x18\x01 \x01(\tR\x02id\x12!\n" +
	"\fcontent_hash\x18\x02 \x01(\tR\vcontentHash\x12\x12\n" +
	"\x04tags\x18\x03 \x03(\tR\x04tags\x12!\n" +
	"\fmemory_count\x18\x04 \x01(\x03R\vmemoryCount\x12^\n" +
	"\x15memories_by_fact_type\x18\x05 \x03(\v2+.memory.v1.Document.MemoriesByFactTypeEntryR\x12memoriesByFactType\x12E\n" +
	"\x10retain_overrides\x18\x06 \x01(\v2\x1a.memory.v1.RetainOverridesR\x0fretainOverrides\x129\n" +
	"\n" +
	"created_at\x18\a \x01(\v2\x1a.google.protobuf.TimestampR\tcreatedAt\x129\n" +
	"\n" +
	"updated_at\x18\b \x01(\v2\x1a.google.protobuf.TimestampR\tupdatedAt\x12#\n" +
	"\roriginal_text\x18\t \x01(\tR\foriginalText\x12=\n" +
	"\fextracted_at\x18\n" +
	" \x01(\v2\x1a.google.protobuf.TimestampR\vextractedAt\x1aE\n" +
	"\x17MemoriesByFactTypeEntry\x12\x10\n" +
	"\x03key\x18\x01 \x01(\tR\x03key\x12\x14\n" +
	"\x05value\x18\x02 \x01(\x03R\x05value:\x028\x01\"\x97\x01\n" +
	"\x12ConsolidateRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12)\n" +
	"\x06scopes\x18\x02 \x03(\v2\x11.memory.v1.TagSetR\x06scopes\x12=\n" +
	"\toverrides\x18\x03 \x01(\v2\x1f.memory.v1.ConsolidateOverridesR\toverrides\"I\n" +
	"\x13ConsolidateResponse\x122\n" +
	"\toperation\x18\x01 \x01(\v2\x14.memory.v1.OperationR\toperation\"s\n" +
	"\x1cListObservationScopesRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12\x1b\n" +
	"\tpage_size\x18\x02 \x01(\x05R\bpageSize\x12\x1d\n" +
	"\n" +
	"page_token\x18\x03 \x01(\tR\tpageToken\"|\n" +
	"\x1dListObservationScopesResponse\x123\n" +
	"\x06scopes\x18\x01 \x03(\v2\x1b.memory.v1.ObservationScopeR\x06scopes\x12&\n" +
	"\x0fnext_page_token\x18\x02 \x01(\tR\rnextPageToken\"\x98\x01\n" +
	"\x10ObservationScope\x12'\n" +
	"\x05scope\x18\x01 \x01(\v2\x11.memory.v1.TagSetR\x05scope\x12\"\n" +
	"\fobservations\x18\x02 \x01(\x03R\fobservations\x127\n" +
	"\x17unconsolidated_memories\x18\x03 \x01(\x03R\x16unconsolidatedMemories\"\x9b\x01\n" +
	"\x1dListObservationHistoryRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12%\n" +
	"\x0eobservation_id\x18\x02 \x01(\tR\robservationId\x12\x1b\n" +
	"\tpage_size\x18\x03 \x01(\x05R\bpageSize\x12\x1d\n" +
	"\n" +
	"page_token\x18\x04 \x01(\tR\tpageToken\"\x83\x01\n" +
	"\x1eListObservationHistoryResponse\x129\n" +
	"\bversions\x18\x01 \x03(\v2\x1d.memory.v1.ObservationVersionR\bversions\x12&\n" +
	"\x0fnext_page_token\x18\x02 \x01(\tR\rnextPageToken\"\xa5\x01\n" +
	"\x12ObservationVersion\x12\x18\n" +
	"\aversion\x18\x01 \x01(\x03R\aversion\x12\x12\n" +
	"\x04text\x18\x02 \x01(\tR\x04text\x12&\n" +
	"\x0fsource_fact_ids\x18\x03 \x03(\tR\rsourceFactIds\x129\n" +
	"\n" +
	"created_at\x18\x04 \x01(\v2\x1a.google.protobuf.TimestampR\tcreatedAt\"\xc7\x01\n" +
	"\x18ClearObservationsRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12/\n" +
	"\x06scopes\x18\x02 \x01(\v2\x15.memory.v1.TagSetListH\x00R\x06scopes\x12\x1d\n" +
	"\tmemory_id\x18\x03 \x01(\tH\x00R\bmemoryId\x12\x12\n" +
	"\x03all\x18\x04 \x01(\bH\x00R\x03all\x12$\n" +
	"\rreconsolidate\x18\x05 \x01(\bR\rreconsolidateB\b\n" +
	"\x06target\":\n" +
	"\n" +
	"TagSetList\x12,\n" +
	"\btag_sets\x18\x01 \x03(\v2\x11.memory.v1.TagSetR\atagSets\"i\n" +
	"\x19ClearObservationsResponse\x12\x18\n" +
	"\adeleted\x18\x01 \x01(\x03R\adeleted\x122\n" +
	"\toperation\x18\x02 \x01(\v2\x14.memory.v1.OperationR\toperation\"\x82\x01\n" +
	"%PreviewConsolidationStrategiesRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12@\n" +
	"\n" +
	"strategies\x18\x02 \x03(\v2 .memory.v1.ConsolidationStrategyR\n" +
	"strategies\"x\n" +
	"&PreviewConsolidationStrategiesResponse\x122\n" +
	"\amatches\x18\x01 \x03(\v2\x18.memory.v1.StrategyMatchR\amatches\x12\x1a\n" +
	"\bcomplete\x18\x02 \x01(\bR\bcomplete\"{\n" +
	"\rStrategyMatch\x12\x1a\n" +
	"\bstrategy\x18\x01 \x01(\tR\bstrategy\x12%\n" +
	"\x0ematched_scopes\x18\x02 \x03(\tR\rmatchedScopes\x12'\n" +
	"\x0fshadowed_scopes\x18\x03 \x03(\tR\x0eshadowedScopes\"c\n" +
	"\x18CreateMentalModelRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12.\n" +
	"\x04spec\x18\x02 \x01(\v2\x1a.memory.v1.MentalModelSpecR\x04spec\"\x8a\x01\n" +
	"\x19CreateMentalModelResponse\x129\n" +
	"\fmental_model\x18\x01 \x01(\v2\x16.memory.v1.MentalModelR\vmentalModel\x122\n" +
	"\toperation\x18\x02 \x01(\v2\x14.memory.v1.OperationR\toperation\"\x88\x01\n" +
	"\x15GetMentalModelRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12&\n" +
	"\x0fmental_model_id\x18\x02 \x01(\tR\rmentalModelId\x12.\n" +
	"\x04view\x18\x03 \x01(\x0e2\x1a.memory.v1.MentalModelViewR\x04view\"S\n" +
	"\x16GetMentalModelResponse\x129\n" +
	"\fmental_model\x18\x01 \x01(\v2\x16.memory.v1.MentalModelR\vmentalModel\"\xed\x01\n" +
	"\x17ListMentalModelsRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12#\n" +
	"\rfolder_prefix\x18\x02 \x01(\tR\ffolderPrefix\x12.\n" +
	"\x04view\x18\x03 \x01(\x0e2\x1a.memory.v1.MentalModelViewR\x04view\x12(\n" +
	"\x04tags\x18\x04 \x01(\v2\x14.memory.v1.TagFilterR\x04tags\x12\x1b\n" +
	"\tpage_size\x18\x05 \x01(\x05R\bpageSize\x12\x1d\n" +
	"\n" +
	"page_token\x18\x06 \x01(\tR\tpageToken\"\x7f\n" +
	"\x18ListMentalModelsResponse\x12;\n" +
	"\rmental_models\x18\x01 \x03(\v2\x16.memory.v1.MentalModelR\fmentalModels\x12&\n" +
	"\x0fnext_page_token\x18\x02 \x01(\tR\rnextPageToken\"\xc8\x01\n" +
	"\x18UpdateMentalModelRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12&\n" +
	"\x0fmental_model_id\x18\x02 \x01(\tR\rmentalModelId\x12.\n" +
	"\x04spec\x18\x03 \x01(\v2\x1a.memory.v1.MentalModelSpecR\x04spec\x12;\n" +
	"\vupdate_mask\x18\x04 \x01(\v2\x1a.google.protobuf.FieldMaskR\n" +
	"updateMask\"V\n" +
	"\x19UpdateMentalModelResponse\x129\n" +
	"\fmental_model\x18\x01 \x01(\v2\x16.memory.v1.MentalModelR\vmentalModel\"\xee\x01\n" +
	"\x19RefreshMentalModelRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12&\n" +
	"\x0fmental_model_id\x18\x02 \x01(\tR\rmentalModelId\x12\x1d\n" +
	"\n" +
	"force_full\x18\x03 \x01(\bR\tforceFull\x129\n" +
	"\toverrides\x18\x04 \x01(\v2\x1b.memory.v1.RefreshOverridesR\toverrides\x126\n" +
	"\bstrategy\x18\x05 \x01(\x0e2\x1a.memory.v1.RefreshStrategyR\bstrategy\"u\n" +
	"\x1aRefreshMentalModelResponse\x122\n" +
	"\toperation\x18\x01 \x01(\v2\x14.memory.v1.OperationR\toperation\x12#\n" +
	"\rjoined_queued\x18\x02 \x01(\bR\fjoinedQueued\"\x9b\x01\n" +
	" PreviewMentalModelRefreshRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12&\n" +
	"\x0fmental_model_id\x18\x02 \x01(\tR\rmentalModelId\x126\n" +
	"\bstrategy\x18\x03 \x01(\x0e2\x1a.memory.v1.RefreshStrategyR\bstrategy\"\xa9\x03\n" +
	"!PreviewMentalModelRefreshResponse\x12=\n" +
	"\x0erequested_mode\x18\x01 \x01(\x0e2\x16.memory.v1.RefreshModeR\rrequestedMode\x12-\n" +
	"\x05trace\x18\x02 \x01(\v2\x17.memory.v1.RefreshTraceR\x05trace\x12?\n" +
	"\x0eresolved_scope\x18\x03 \x01(\v2\x18.memory.v1.ResolvedScopeR\rresolvedScope\x12(\n" +
	"\x10window_after_seq\x18\x04 \x01(\x03R\x0ewindowAfterSeq\x12*\n" +
	"\x11window_before_seq\x18\x05 \x01(\x03R\x0fwindowBeforeSeq\x12'\n" +
	"\x0ffacts_retrieved\x18\x06 \x01(\x05R\x0efactsRetrieved\x12\x1d\n" +
	"\n" +
	"facts_used\x18\a \x01(\x05R\tfactsUsed\x12\x12\n" +
	"\x04diff\x18\b \x01(\tR\x04diff\x12#\n" +
	"\rwould_persist\x18\t \x01(\bR\fwouldPersist\"m\n" +
	"\rResolvedScope\x12(\n" +
	"\x04tags\x18\x01 \x01(\v2\x14.memory.v1.TagFilterR\x04tags\x122\n" +
	"\n" +
	"fact_types\x18\x02 \x03(\x0e2\x13.memory.v1.FactTypeR\tfactTypes\"\xd9\x01\n" +
	"\x1dListMentalModelHistoryRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12&\n" +
	"\x0fmental_model_id\x18\x02 \x01(\tR\rmentalModelId\x12;\n" +
	"\x04kind\x18\x03 \x01(\x0e2'.memory.v1.MentalModelHistoryEntry.KindR\x04kind\x12\x1b\n" +
	"\tpage_size\x18\x04 \x01(\x05R\bpageSize\x12\x1d\n" +
	"\n" +
	"page_token\x18\x05 \x01(\tR\tpageToken\"\x86\x01\n" +
	"\x1eListMentalModelHistoryResponse\x12<\n" +
	"\aentries\x18\x01 \x03(\v2\".memory.v1.MentalModelHistoryEntryR\aentries\x12&\n" +
	"\x0fnext_page_token\x18\x02 \x01(\tR\rnextPageToken\"\x86\x03\n" +
	"\x17MentalModelHistoryEntry\x12;\n" +
	"\x04kind\x18\x01 \x01(\x0e2'.memory.v1.MentalModelHistoryEntry.KindR\x04kind\x12)\n" +
	"\x10previous_content\x18\x02 \x01(\tR\x0fpreviousContent\x129\n" +
	"\n" +
	"created_at\x18\x03 \x01(\v2\x1a.google.protobuf.TimestampR\tcreatedAt\x123\n" +
	"\aoutcome\x18\x04 \x01(\x0e2\x19.memory.v1.RefreshOutcomeR\aoutcome\x12%\n" +
	"\x0efailure_reason\x18\x05 \x01(\tR\rfailureReason\x12#\n" +
	"\rerror_message\x18\x06 \x01(\tR\ferrorMessage\"G\n" +
	"\x04Kind\x12\x14\n" +
	"\x10KIND_UNSPECIFIED\x10\x00\x12\x10\n" +
	"\fKIND_VERSION\x10\x01\x12\x17\n" +
	"\x13KIND_REFRESH_FAILED\x10\x02\"[\n" +
	"\x18DeleteMentalModelRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12&\n" +
	"\x0fmental_model_id\x18\x02 \x01(\tR\rmentalModelId\"\x1b\n" +
	"\x19DeleteMentalModelResponse\"Z\n" +
	"\x1eDeleteMentalModelFolderRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12\x1f\n" +
	"\vfolder_path\x18\x02 \x01(\tR\n" +
	"folderPath\";\n" +
	"\x1fDeleteMentalModelFolderResponse\x12\x18\n" +
	"\adeleted\x18\x01 \x01(\x05R\adeleted\"\x90\x01\n" +
	"\x19SearchMentalModelsRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12\x14\n" +
	"\x05query\x18\x02 \x01(\tR\x05query\x12#\n" +
	"\rfolder_prefix\x18\x03 \x01(\tR\ffolderPrefix\x12\x1f\n" +
	"\vmax_results\x18\x04 \x01(\x05R\n" +
	"maxResults\"K\n" +
	"\x1aSearchMentalModelsResponse\x12-\n" +
	"\x04hits\x18\x01 \x03(\v2\x19.memory.v1.MentalModelHitR\x04hits\"\x9d\x01\n" +
	"\x0eMentalModelHit\x12&\n" +
	"\x0fmental_model_id\x18\x01 \x01(\tR\rmentalModelId\x12\x1f\n" +
	"\vfolder_path\x18\x02 \x01(\tR\n" +
	"folderPath\x12\x12\n" +
	"\x04name\x18\x03 \x01(\tR\x04name\x12\x18\n" +
	"\asnippet\x18\x04 \x01(\tR\asnippet\x12\x14\n" +
	"\x05score\x18\x05 \x01(\x01R\x05score\"\xce\x04\n" +
	"\vMentalModel\x12.\n" +
	"\x04spec\x18\x01 \x01(\v2\x1a.memory.v1.MentalModelSpecR\x04spec\x12\x1d\n" +
	"\n" +
	"content_md\x18\x02 \x01(\tR\tcontentMd\x124\n" +
	"\x16structured_output_json\x18\x03 \x01(\tR\x14structuredOutputJson\x12\x18\n" +
	"\aversion\x18\x04 \x01(\x03R\aversion\x12\x14\n" +
	"\x05stale\x18\x05 \x01(\bR\x05stale\x12F\n" +
	"\x11last_refreshed_at\x18\x06 \x01(\v2\x1a.google.protobuf.TimestampR\x0flastRefreshedAt\x12\"\n" +
	"\rlast_seq_seen\x18\a \x01(\x03R\vlastSeqSeen\x12O\n" +
	"\x16last_refresh_failed_at\x18\b \x01(\v2\x1a.google.protobuf.TimestampR\x13lastRefreshFailedAt\x12.\n" +
	"\x13auto_refresh_paused\x18\t \x01(\bR\x11autoRefreshPaused\x12*\n" +
	"\x11source_memory_ids\x18\n" +
	" \x03(\tR\x0fsourceMemoryIds\x126\n" +
	"\n" +
	"last_trace\x18\v \x01(\v2\x17.memory.v1.RefreshTraceR\tlastTrace\x129\n" +
	"\n" +
	"created_at\x18\f \x01(\v2\x1a.google.protobuf.TimestampR\tcreatedAt\"\xcd\x06\n" +
	"\x0fMentalModelSpec\x12\x0e\n" +
	"\x02id\x18\x01 \x01(\tR\x02id\x12\x12\n" +
	"\x04name\x18\x02 \x01(\tR\x04name\x12!\n" +
	"\fsource_query\x18\x03 \x01(\tR\vsourceQuery\x12\x1f\n" +
	"\vfolder_path\x18\x04 \x01(\tR\n" +
	"folderPath\x12\x12\n" +
	"\x04tags\x18\x05 \x03(\tR\x04tags\x126\n" +
	"\bschedule\x18\x06 \x01(\v2\x1a.memory.v1.RefreshScheduleR\bschedule\x121\n" +
	"\x05scope\x18\a \x01(\v2\x1b.memory.v1.MentalModelScopeR\x05scope\x12*\n" +
	"\x04mode\x18\b \x01(\x0e2\x16.memory.v1.RefreshModeR\x04mode\x126\n" +
	"\bstrategy\x18\t \x01(\x0e2\x1a.memory.v1.RefreshStrategyR\bstrategy\x12)\n" +
	"\x06budget\x18\n" +
	" \x01(\x0e2\x11.memory.v1.BudgetR\x06budget\x12\"\n" +
	"\n" +
	"max_tokens\x18\v \x01(\x05H\x00R\tmaxTokens\x88\x01\x01\x12*\n" +
	"\x0einclude_chunks\x18\f \x01(\bH\x01R\rincludeChunks\x88\x01\x01\x12<\n" +
	"\x18recall_chunks_max_tokens\x18\r \x01(\x05H\x02R\x15recallChunksMaxTokens\x88\x01\x01\x122\n" +
	"\x15exclude_mental_models\x18\x0e \x01(\bR\x13excludeMentalModels\x127\n" +
	"\x18exclude_mental_model_ids\x18\x0f \x03(\tR\x15excludeMentalModelIds\x120\n" +
	"\x14response_schema_json\x18\x10 \x01(\tR\x12responseSchemaJson\x12\x1d\n" +
	"\n" +
	"keep_trace\x18\x11 \x01(\bR\tkeepTrace\x129\n" +
	"\toverrides\x18\x12 \x01(\v2\x1b.memory.v1.RefreshOverridesR\toverridesB\r\n" +
	"\v_max_tokensB\x11\n" +
	"\x0f_include_chunksB\x1b\n" +
	"\x19_recall_chunks_max_tokens\"\xb2\x01\n" +
	"\x0fRefreshSchedule\x121\n" +
	"\x13after_consolidation\x18\x01 \x01(\bH\x00R\x12afterConsolidation\x12\x14\n" +
	"\x04cron\x18\x02 \x01(\tH\x00R\x04cron\x125\n" +
	"\x14min_interval_seconds\x18\x03 \x01(\x05H\x01R\x12minIntervalSeconds\x88\x01\x01B\x06\n" +
	"\x04whenB\x17\n" +
	"\x15_min_interval_seconds\"p\n" +
	"\x10MentalModelScope\x12(\n" +
	"\x04tags\x18\x01 \x01(\v2\x14.memory.v1.TagFilterR\x04tags\x122\n" +
	"\n" +
	"fact_types\x18\x02 \x03(\x0e2\x13.memory.v1.FactTypeR\tfactTypes\"\x9d\x04\n" +
	"\fRefreshTrace\x12=\n" +
	"\x0eeffective_mode\x18\x01 \x01(\x0e2\x16.memory.v1.RefreshModeR\reffectiveMode\x120\n" +
	"\x14mode_fallback_reason\x18\x02 \x01(\tR\x12modeFallbackReason\x123\n" +
	"\aoutcome\x18\x03 \x01(\x0e2\x19.memory.v1.RefreshOutcomeR\aoutcome\x127\n" +
	"\n" +
	"tool_calls\x18\x04 \x03(\v2\x18.memory.v1.ToolCallTraceR\ttoolCalls\x12=\n" +
	"\x10delta_operations\x18\x05 \x03(\v2\x12.memory.v1.DeltaOpR\x0fdeltaOperations\x12*\n" +
	"\x05usage\x18\x06 \x03(\v2\x14.memory.v1.SlotUsageR\x05usage\x12;\n" +
	"\vrecorded_at\x18\a \x01(\v2\x1a.google.protobuf.TimestampR\n" +
	"recordedAt\x12\x1f\n" +
	"\vduration_ms\x18\b \x01(\x03R\n" +
	"durationMs\x12\x1a\n" +
	"\bwarnings\x18\t \x03(\tR\bwarnings\x12I\n" +
	"\x12effective_strategy\x18\n" +
	" \x01(\x0e2\x1a.memory.v1.RefreshStrategyR\x11effectiveStrategy\"\xed\x03\n" +
	"\aDeltaOp\x12+\n" +
	"\x04kind\x18\x01 \x01(\x0e2\x17.memory.v1.DeltaOp.KindR\x04kind\x12\x1d\n" +
	"\n" +
	"section_id\x18\x02 \x01(\tR\tsectionId\x12\x19\n" +
	"\bblock_id\x18\x03 \x01(\tR\ablockId\x12$\n" +
	"\x0eafter_block_id\x18\x04 \x01(\tR\fafterBlockId\x12\x18\n" +
	"\aheading\x18\x05 \x01(\tR\aheading\x12\x1b\n" +
	"\tblocks_md\x18\x06 \x03(\tR\bblocksMd\x12\x18\n" +
	"\aapplied\x18\a \x01(\bR\aapplied\x12\x1f\n" +
	"\vskip_reason\x18\b \x01(\tR\n" +
	"skipReason\"\xe2\x01\n" +
	"\x04Kind\x12\x14\n" +
	"\x10KIND_UNSPECIFIED\x10\x00\x12\x14\n" +
	"\x10KIND_ADD_SECTION\x10\x01\x12\x17\n" +
	"\x13KIND_REMOVE_SECTION\x10\x02\x12\x17\n" +
	"\x13KIND_RENAME_SECTION\x10\x03\x12\x1f\n" +
	"\x1bKIND_REPLACE_SECTION_BLOCKS\x10\x04\x12\x15\n" +
	"\x11KIND_APPEND_BLOCK\x10\x05\x12\x15\n" +
	"\x11KIND_INSERT_BLOCK\x10\x06\x12\x16\n" +
	"\x12KIND_REPLACE_BLOCK\x10\a\x12\x15\n" +
	"\x11KIND_REMOVE_BLOCK\x10\b\"\xb3\x01\n" +
	"\x11CreateBankRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12\x12\n" +
	"\x04name\x18\x02 \x01(\tR\x04name\x12/\n" +
	"\x06config\x18\x03 \x01(\v2\x15.memory.v1.BankConfigH\x00R\x06config\x125\n" +
	"\btemplate\x18\x04 \x01(\v2\x17.memory.v1.BankTemplateH\x00R\btemplateB\t\n" +
	"\ainitial\"\x87\x01\n" +
	"\x12CreateBankResponse\x12#\n" +
	"\x04bank\x18\x01 \x01(\v2\x0f.memory.v1.BankR\x04bank\x12L\n" +
	"\x17mental_model_operations\x18\x02 \x03(\v2\x14.memory.v1.OperationR\x15mentalModelOperations\"\xb4\x01\n" +
	"\fBankTemplate\x12-\n" +
	"\x06config\x18\x01 \x01(\v2\x15.memory.v1.BankConfigR\x06config\x124\n" +
	"\n" +
	"directives\x18\x02 \x03(\v2\x14.memory.v1.DirectiveR\n" +
	"directives\x12?\n" +
	"\rmental_models\x18\x03 \x03(\v2\x1a.memory.v1.MentalModelSpecR\fmentalModels\")\n" +
	"\x0eGetBankRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\"6\n" +
	"\x0fGetBankResponse\x12#\n" +
	"\x04bank\x18\x01 \x01(\v2\x0f.memory.v1.BankR\x04bank\"k\n" +
	"\x10ListBanksRequest\x12\x1b\n" +
	"\tpage_size\x18\x01 \x01(\x05R\bpageSize\x12\x1d\n" +
	"\n" +
	"page_token\x18\x02 \x01(\tR\tpageToken\x12\x1b\n" +
	"\tid_prefix\x18\x03 \x01(\tR\bidPrefix\"b\n" +
	"\x11ListBanksResponse\x12%\n" +
	"\x05banks\x18\x01 \x03(\v2\x0f.memory.v1.BankR\x05banks\x12&\n" +
	"\x0fnext_page_token\x18\x02 \x01(\tR\rnextPageToken\",\n" +
	"\x11DeleteBankRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\"H\n" +
	"\x12DeleteBankResponse\x122\n" +
	"\toperation\x18\x01 \x01(\v2\x14.memory.v1.OperationR\toperation\".\n" +
	"\x13GetBankStatsRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\"B\n" +
	"\x14GetBankStatsResponse\x12*\n" +
	"\x05stats\x18\x01 \x01(\v2\x14.memory.v1.BankStatsR\x05stats\"\xa6\x05\n" +
	"\tBankStats\x12_\n" +
	"\x15memories_by_fact_type\x18\x01 \x03(\v2,.memory.v1.BankStats.MemoriesByFactTypeEntryR\x12memoriesByFactType\x121\n" +
	"\x14invalidated_memories\x18\x02 \x01(\x03R\x13invalidatedMemories\x12\x1c\n" +
	"\tdocuments\x18\x03 \x01(\x03R\tdocuments\x12\x1a\n" +
	"\bentities\x18\x04 \x01(\x03R\bentities\x12#\n" +
	"\rmental_models\x18\x05 \x01(\x03R\fmentalModels\x12n\n" +
	"\x1apending_operations_by_kind\x18\x06 \x03(\v21.memory.v1.BankStats.PendingOperationsByKindEntryR\x17pendingOperationsByKind\x127\n" +
	"\x17unconsolidated_memories\x18\a \x01(\x03R\x16unconsolidatedMemories\x12K\n" +
	"\x14last_memory_write_at\x18\b \x01(\v2\x1a.google.protobuf.TimestampR\x11lastMemoryWriteAt\x12\x1d\n" +
	"\n" +
	"change_seq\x18\t \x01(\x03R\tchangeSeq\x1aE\n" +
	"\x17MemoriesByFactTypeEntry\x12\x10\n" +
	"\x03key\x18\x01 \x01(\tR\x03key\x12\x14\n" +
	"\x05value\x18\x02 \x01(\x03R\x05value:\x028\x01\x1aJ\n" +
	"\x1cPendingOperationsByKindEntry\x12\x10\n" +
	"\x03key\x18\x01 \x01(\tR\x03key\x12\x14\n" +
	"\x05value\x18\x02 \x01(\x03R\x05value:\x028\x01\"\xb6\x02\n" +
	"\x0fListTagsRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12<\n" +
	"\x06source\x18\x02 \x01(\x0e2$.memory.v1.ListTagsRequest.TagSourceR\x06source\x12\x16\n" +
	"\x06prefix\x18\x03 \x01(\tR\x06prefix\x12\x1b\n" +
	"\tpage_size\x18\x04 \x01(\x05R\bpageSize\x12\x1d\n" +
	"\n" +
	"page_token\x18\x05 \x01(\tR\tpageToken\"x\n" +
	"\tTagSource\x12\x1a\n" +
	"\x16TAG_SOURCE_UNSPECIFIED\x10\x00\x12\x17\n" +
	"\x13TAG_SOURCE_MEMORIES\x10\x01\x12\x1c\n" +
	"\x18TAG_SOURCE_MENTAL_MODELS\x10\x02\x12\x18\n" +
	"\x14TAG_SOURCE_DOCUMENTS\x10\x03\"c\n" +
	"\x10ListTagsResponse\x12'\n" +
	"\x04tags\x18\x01 \x03(\v2\x13.memory.v1.TagCountR\x04tags\x12&\n" +
	"\x0fnext_page_token\x18\x02 \x01(\tR\rnextPageToken\"2\n" +
	"\bTagCount\x12\x10\n" +
	"\x03tag\x18\x01 \x01(\tR\x03tag\x12\x14\n" +
	"\x05count\x18\x02 \x01(\x03R\x05count\"4\n" +
	"\x19ExportBankTemplateRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\"Q\n" +
	"\x1aExportBankTemplateResponse\x123\n" +
	"\btemplate\x18\x01 \x01(\v2\x17.memory.v1.BankTemplateR\btemplate\"e\n" +
	"\x04Bank\x12\x0e\n" +
	"\x02id\x18\x01 \x01(\tR\x02id\x12\x12\n" +
	"\x04name\x18\x02 \x01(\tR\x04name\x129\n" +
	"\n" +
	"created_at\x18\x03 \x01(\v2\x1a.google.protobuf.TimestampR\tcreatedAt\"\x18\n" +
	"\x16GetTenantConfigRequest\"H\n" +
	"\x17GetTenantConfigResponse\x12-\n" +
	"\x06config\x18\x01 \x01(\v2\x15.memory.v1.ConfigViewR\x06config\"\x87\x01\n" +
	"\x19UpdateTenantConfigRequest\x12-\n" +
	"\x06config\x18\x01 \x01(\v2\x15.memory.v1.BankConfigR\x06config\x12;\n" +
	"\vupdate_mask\x18\x02 \x01(\v2\x1a.google.protobuf.FieldMaskR\n" +
	"updateMask\"K\n" +
	"\x1aUpdateTenantConfigResponse\x12-\n" +
	"\x06config\x18\x01 \x01(\v2\x15.memory.v1.ConfigViewR\x06config\"U\n" +
	"\x18ResetTenantConfigRequest\x129\n" +
	"\n" +
	"reset_mask\x18\x01 \x01(\v2\x1a.google.protobuf.FieldMaskR\tresetMask\"J\n" +
	"\x19ResetTenantConfigResponse\x12-\n" +
	"\x06config\x18\x01 \x01(\v2\x15.memory.v1.ConfigViewR\x06config\"/\n" +
	"\x14GetBankConfigRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\"F\n" +
	"\x15GetBankConfigResponse\x12-\n" +
	"\x06config\x18\x01 \x01(\v2\x15.memory.v1.ConfigViewR\x06config\"\x9e\x01\n" +
	"\x17UpdateBankConfigRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12-\n" +
	"\x06config\x18\x02 \x01(\v2\x15.memory.v1.BankConfigR\x06config\x12;\n" +
	"\vupdate_mask\x18\x03 \x01(\v2\x1a.google.protobuf.FieldMaskR\n" +
	"updateMask\"I\n" +
	"\x18UpdateBankConfigResponse\x12-\n" +
	"\x06config\x18\x01 \x01(\v2\x15.memory.v1.ConfigViewR\x06config\"l\n" +
	"\x16ResetBankConfigRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x129\n" +
	"\n" +
	"reset_mask\x18\x02 \x01(\v2\x1a.google.protobuf.FieldMaskR\tresetMask\"H\n" +
	"\x17ResetBankConfigResponse\x12-\n" +
	"\x06config\x18\x01 \x01(\v2\x15.memory.v1.ConfigViewR\x06config\"\xe4\x04\n" +
	"\x14PreviewPromptRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12M\n" +
	"\toperation\x18\x02 \x01(\x0e2/.memory.v1.PreviewPromptRequest.PromptOperationR\toperation\x124\n" +
	"\x06retain\x18\x03 \x01(\v2\x1a.memory.v1.RetainOverridesH\x00R\x06retain\x12G\n" +
	"\rconsolidation\x18\x04 \x01(\v2\x1f.memory.v1.ConsolidateOverridesH\x00R\rconsolidation\x127\n" +
	"\areflect\x18\x05 \x01(\v2\x1b.memory.v1.ReflectOverridesH\x00R\areflect\x12@\n" +
	"\fmental_model\x18\x06 \x01(\v2\x1b.memory.v1.RefreshOverridesH\x00R\vmentalModel\"\xdc\x01\n" +
	"\x0fPromptOperation\x12 \n" +
	"\x1cPROMPT_OPERATION_UNSPECIFIED\x10\x00\x12\x1b\n" +
	"\x17PROMPT_OPERATION_RETAIN\x10\x01\x12\"\n" +
	"\x1ePROMPT_OPERATION_CONSOLIDATION\x10\x02\x12\x1c\n" +
	"\x18PROMPT_OPERATION_REFLECT\x10\x03\x12'\n" +
	"#PROMPT_OPERATION_MENTAL_MODEL_DELTA\x10\x04\x12\x1f\n" +
	"\x1bPROMPT_OPERATION_SYNTHESIZE\x10\x05B\v\n" +
	"\toverrides\"K\n" +
	"\x15PreviewPromptResponse\x122\n" +
	"\apreview\x18\x01 \x01(\v2\x18.memory.v1.PromptPreviewR\apreview\"\x9e\x01\n" +
	"\rPromptPreview\x124\n" +
	"\bmessages\x18\x01 \x03(\v2\x18.memory.v1.PromptMessageR\bmessages\x120\n" +
	"\x14response_schema_json\x18\x02 \x01(\tR\x12responseSchemaJson\x12%\n" +
	"\x0eskipped_reason\x18\x03 \x01(\tR\rskippedReason\"\xd1\x01\n" +
	"\rPromptMessage\x127\n" +
	"\x04role\x18\x01 \x01(\x0e2#.memory.v1.PromptMessage.PromptRoleR\x04role\x12.\n" +
	"\x06blocks\x18\x02 \x03(\v2\x16.memory.v1.PromptBlockR\x06blocks\"W\n" +
	"\n" +
	"PromptRole\x12\x1b\n" +
	"\x17PROMPT_ROLE_UNSPECIFIED\x10\x00\x12\x16\n" +
	"\x12PROMPT_ROLE_SYSTEM\x10\x01\x12\x14\n" +
	"\x10PROMPT_ROLE_USER\x10\x02\"\x8a\x02\n" +
	"\vPromptBlock\x12\x12\n" +
	"\x04text\x18\x01 \x01(\tR\x04text\x12;\n" +
	"\x06source\x18\x02 \x01(\x0e2#.memory.v1.PromptBlock.PromptSourceR\x06source\x12\x14\n" +
	"\x05field\x18\x03 \x01(\tR\x05field\x12\x18\n" +
	"\asection\x18\x04 \x01(\tR\asection\x12\x16\n" +
	"\x06active\x18\x05 \x01(\bR\x06active\"b\n" +
	"\fPromptSource\x12\x1d\n" +
	"\x19PROMPT_SOURCE_UNSPECIFIED\x10\x00\x12\x19\n" +
	"\x15PROMPT_SOURCE_BUILTIN\x10\x01\x12\x18\n" +
	"\x14PROMPT_SOURCE_CONFIG\x10\x02\"e\n" +
	"\x16CreateDirectiveRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x122\n" +
	"\tdirective\x18\x02 \x01(\v2\x14.memory.v1.DirectiveR\tdirective\"M\n" +
	"\x17CreateDirectiveResponse\x122\n" +
	"\tdirective\x18\x01 \x01(\v2\x14.memory.v1.DirectiveR\tdirective\"\xc5\x01\n" +
	"\x16UpdateDirectiveRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12!\n" +
	"\fdirective_id\x18\x02 \x01(\tR\vdirectiveId\x122\n" +
	"\tdirective\x18\x03 \x01(\v2\x14.memory.v1.DirectiveR\tdirective\x12;\n" +
	"\vupdate_mask\x18\x04 \x01(\v2\x1a.google.protobuf.FieldMaskR\n" +
	"updateMask\"M\n" +
	"\x17UpdateDirectiveResponse\x122\n" +
	"\tdirective\x18\x01 \x01(\v2\x14.memory.v1.DirectiveR\tdirective\"\xb7\x01\n" +
	"\x15ListDirectivesRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12(\n" +
	"\x04tags\x18\x02 \x01(\v2\x14.memory.v1.TagFilterR\x04tags\x12\x1f\n" +
	"\vactive_only\x18\x03 \x01(\bR\n" +
	"activeOnly\x12\x1b\n" +
	"\tpage_size\x18\x04 \x01(\x05R\bpageSize\x12\x1d\n" +
	"\n" +
	"page_token\x18\x05 \x01(\tR\tpageToken\"v\n" +
	"\x16ListDirectivesResponse\x124\n" +
	"\n" +
	"directives\x18\x01 \x03(\v2\x14.memory.v1.DirectiveR\n" +
	"directives\x12&\n" +
	"\x0fnext_page_token\x18\x02 \x01(\tR\rnextPageToken\"T\n" +
	"\x16DeleteDirectiveRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12!\n" +
	"\fdirective_id\x18\x02 \x01(\tR\vdirectiveId\"\x19\n" +
	"\x17DeleteDirectiveResponse\"j\n" +
	"\n" +
	"ConfigView\x12'\n" +
	"\x03own\x18\x01 \x01(\v2\x15.memory.v1.BankConfigR\x03own\x123\n" +
	"\teffective\x18\x02 \x01(\v2\x15.memory.v1.BankConfigR\teffective\"q\n" +
	"\tDirective\x12\x0e\n" +
	"\x02id\x18\x01 \x01(\tR\x02id\x12\x12\n" +
	"\x04name\x18\x02 \x01(\tR\x04name\x12\x14\n" +
	"\x05rules\x18\x03 \x03(\tR\x05rules\x12\x12\n" +
	"\x04tags\x18\x04 \x03(\tR\x04tags\x12\x16\n" +
	"\x06active\x18\x05 \x01(\bR\x06active\"Q\n" +
	"\x13GetOperationRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12!\n" +
	"\foperation_id\x18\x02 \x01(\tR\voperationId\"J\n" +
	"\x14GetOperationResponse\x122\n" +
	"\toperation\x18\x01 \x01(\v2\x14.memory.v1.OperationR\toperation\"\xf7\x01\n" +
	"\x15ListOperationsRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x122\n" +
	"\x06status\x18\x02 \x01(\x0e2\x1a.memory.v1.OperationStatusR\x06status\x12,\n" +
	"\x04kind\x18\x03 \x01(\x0e2\x18.memory.v1.OperationKindR\x04kind\x12'\n" +
	"\x0fexclude_parents\x18\x04 \x01(\bR\x0eexcludeParents\x12\x1b\n" +
	"\tpage_size\x18\x05 \x01(\x05R\bpageSize\x12\x1d\n" +
	"\n" +
	"page_token\x18\x06 \x01(\tR\tpageToken\"v\n" +
	"\x16ListOperationsResponse\x124\n" +
	"\n" +
	"operations\x18\x01 \x03(\v2\x14.memory.v1.OperationR\n" +
	"operations\x12&\n" +
	"\x0fnext_page_token\x18\x02 \x01(\tR\rnextPageToken\"T\n" +
	"\x16CancelOperationRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12!\n" +
	"\foperation_id\x18\x02 \x01(\tR\voperationId\"M\n" +
	"\x17CancelOperationResponse\x122\n" +
	"\toperation\x18\x01 \x01(\v2\x14.memory.v1.OperationR\toperation\"S\n" +
	"\x15RetryOperationRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12!\n" +
	"\foperation_id\x18\x02 \x01(\tR\voperationId\"L\n" +
	"\x16RetryOperationResponse\x122\n" +
	"\toperation\x18\x01 \x01(\v2\x14.memory.v1.OperationR\toperation\"\x92\x01\n" +
	"\x11ListEventsRequest\x12\x17\n" +
	"\abank_id\x18\x01 \x01(\tR\x06bankId\x12\x1b\n" +
	"\tafter_seq\x18\x02 \x01(\x03R\bafterSeq\x12\x1b\n" +
	"\tpage_size\x18\x03 \x01(\x05R\bpageSize\x12*\n" +
	"\x05types\x18\x04 \x03(\x0e2\x14.memory.v1.EventTypeR\x05types\"m\n" +
	"\x12ListEventsResponse\x121\n" +
	"\x06events\x18\x01 \x03(\v2\x19.memory.v1.LifecycleEventR\x06events\x12$\n" +
	"\x0enext_after_seq\x18\x02 \x01(\x03R\fnextAfterSeq\"\xe2\x01\n" +
	"\x0eLifecycleEvent\x12\x10\n" +
	"\x03seq\x18\x01 \x01(\x03R\x03seq\x12(\n" +
	"\x04type\x18\x02 \x01(\x0e2\x14.memory.v1.EventTypeR\x04type\x12\x17\n" +
	"\abank_id\x18\x03 \x01(\tR\x06bankId\x129\n" +
	"\n" +
	"created_at\x18\x04 \x01(\v2\x1a.google.protobuf.TimestampR\tcreatedAt\x12!\n" +
	"\foperation_id\x18\x05 \x01(\tR\voperationId\x12\x1d\n" +
	"\n" +
	"subject_id\x18\x06 \x01(\tR\tsubjectId\"\xa0\x05\n" +
	"\tOperation\x12\x0e\n" +
	"\x02id\x18\x01 \x01(\tR\x02id\x12\x17\n" +
	"\abank_id\x18\x02 \x01(\tR\x06bankId\x12,\n" +
	"\x04kind\x18\x03 \x01(\x0e2\x18.memory.v1.OperationKindR\x04kind\x122\n" +
	"\x06status\x18\x04 \x01(\x0e2\x1a.memory.v1.OperationStatusR\x06status\x12\x14\n" +
	"\x05error\x18\x05 \x01(\tR\x05error\x12#\n" +
	"\rdebug_summary\x18\x06 \x01(\tR\fdebugSummary\x12*\n" +
	"\x05usage\x18\a \x03(\v2\x14.memory.v1.SlotUsageR\x05usage\x12F\n" +
	"\x13effective_overrides\x18\b \x01(\v2\x15.memory.v1.BankConfigR\x12effectiveOverrides\x12\x1b\n" +
	"\tparent_id\x18\t \x01(\tR\bparentId\x12/\n" +
	"\bprogress\x18\n" +
	" \x01(\v2\x13.memory.v1.ProgressR\bprogress\x129\n" +
	"\n" +
	"created_at\x18\v \x01(\v2\x1a.google.protobuf.TimestampR\tcreatedAt\x129\n" +
	"\n" +
	"updated_at\x18\f \x01(\v2\x1a.google.protobuf.TimestampR\tupdatedAt\x12:\n" +
	"\vnext_run_at\x18\r \x01(\v2\x1a.google.protobuf.TimestampR\tnextRunAt\x12\x1a\n" +
	"\battempts\x18\x0e \x01(\x05R\battempts\x12=\n" +
	"\fheartbeat_at\x18\x0f \x01(\v2\x1a.google.protobuf.TimestampR\vheartbeatAt\"\x91\x01\n" +
	"\bProgress\x12\x14\n" +
	"\x05stage\x18\x01 \x01(\tR\x05stage\x12;\n" +
	"\vrecorded_at\x18\x02 \x01(\v2\x1a.google.protobuf.TimestampR\n" +
	"recordedAt\x12\x1c\n" +
	"\tprocessed\x18\x03 \x01(\x03R\tprocessed\x12\x14\n" +
	"\x05total\x18\x04 \x01(\x03R\x05total\"\x1c\n" +
	"\x06TagSet\x12\x12\n" +
	"\x04tags\x18\x01 \x03(\tR\x04tags\"\xc2\x02\n" +
	"\x15ObservationScopeModes\x129\n" +
	"\x04mode\x18\x01 \x01(\x0e2%.memory.v1.ObservationScopeModes.ModeR\x04mode\x12)\n" +
	"\x06custom\x18\x02 \x03(\v2\x11.memory.v1.TagSetR\x06custom\x12.\n" +
	"\x10max_combinations\x18\x03 \x01(\x05H\x00R\x0fmaxCombinations\x88\x01\x01\"~\n" +
	"\x04Mode\x12\x14\n" +
	"\x10MODE_UNSPECIFIED\x10\x00\x12\x11\n" +
	"\rMODE_COMBINED\x10\x01\x12\x0f\n" +
	"\vMODE_SHARED\x10\x02\x12\x10\n" +
	"\fMODE_PER_TAG\x10\x03\x12\x19\n" +
	"\x15MODE_ALL_COMBINATIONS\x10\x04\x12\x0f\n" +
	"\vMODE_CUSTOM\x10\x05B\x13\n" +
	"\x11_max_combinations\"x\n" +
	"\tTagFilter\x12\x12\n" +
	"\x04tags\x18\x01 \x03(\tR\x04tags\x12*\n" +
	"\x05match\x18\x02 \x01(\x0e2\x14.memory.v1.TagsMatchR\x05match\x12+\n" +
	"\x06groups\x18\x03 \x03(\v2\x13.memory.v1.TagGroupR\x06groups\"\xd0\x01\n" +
	"\bTagGroup\x12(\n" +
	"\x04leaf\x18\x01 \x01(\v2\x12.memory.v1.TagLeafH\x00R\x04leaf\x120\n" +
	"\x06all_of\x18\x02 \x01(\v2\x17.memory.v1.TagGroupListH\x00R\x05allOf\x120\n" +
	"\x06any_of\x18\x03 \x01(\v2\x17.memory.v1.TagGroupListH\x00R\x05anyOf\x12.\n" +
	"\anone_of\x18\x04 \x01(\v2\x13.memory.v1.TagGroupH\x00R\x06noneOfB\x06\n" +
	"\x04node\";\n" +
	"\fTagGroupList\x12+\n" +
	"\x06groups\x18\x01 \x03(\v2\x13.memory.v1.TagGroupR\x06groups\"\xcf\x01\n" +
	"\aTagLeaf\x12\x12\n" +
	"\x04tags\x18\x01 \x03(\tR\x04tags\x12*\n" +
	"\x05match\x18\x02 \x01(\x0e2\x14.memory.v1.TagsMatchR\x05match\x124\n" +
	"\aresolve\x18\x03 \x01(\x0e2\x1a.memory.v1.TagLeaf.ResolveR\aresolve\"N\n" +
	"\aResolve\x12\x17\n" +
	"\x13RESOLVE_UNSPECIFIED\x10\x00\x12\x17\n" +
	"\x13RESOLVE_EXACT_NAMES\x10\x01\x12\x11\n" +
	"\rRESOLVE_FUZZY\x10\x02\"\xb6\x01\n" +
	"\x06Scores\x12\x14\n" +
	"\x05final\x18\x01 \x01(\x01R\x05final\x12\x1f\n" +
	"\breranker\x18\x02 \x01(\x01H\x00R\breranker\x88\x01\x01\x12/\n" +
	"\x04arms\x18\x03 \x03(\v2\x1b.memory.v1.Scores.ArmsEntryR\x04arms\x1a7\n" +
	"\tArmsEntry\x12\x10\n" +
	"\x03key\x18\x01 \x01(\tR\x03key\x12\x14\n" +
	"\x05value\x18\x02 \x01(\x01R\x05value:\x028\x01B\v\n" +
	"\t_reranker\"\xcb\x01\n" +
	"\tMinScores\x122\n" +
	"\x04arms\x18\x01 \x03(\v2\x1e.memory.v1.MinScores.ArmsEntryR\x04arms\x12\x1f\n" +
	"\breranker\x18\x02 \x01(\x01H\x00R\breranker\x88\x01\x01\x12\x19\n" +
	"\x05final\x18\x03 \x01(\x01H\x01R\x05final\x88\x01\x01\x1a7\n" +
	"\tArmsEntry\x12\x10\n" +
	"\x03key\x18\x01 \x01(\tR\x03key\x12\x14\n" +
	"\x05value\x18\x02 \x01(\x01R\x05value:\x028\x01B\v\n" +
	"\t_rerankerB\b\n" +
	"\x06_final\"j\n" +
	"\x05Chunk\x12\x0e\n" +
	"\x02id\x18\x01 \x01(\tR\x02id\x12\x12\n" +
	"\x04text\x18\x02 \x01(\tR\x04text\x12\x1f\n" +
	"\vchunk_index\x18\x03 \x01(\x05R\n" +
	"chunkIndex\x12\x1c\n" +
	"\ttruncated\x18\x04 \x01(\bR\ttruncated\"\xb6\x03\n" +
	"\tSlotUsage\x122\n" +
	"\x04slot\x18\x01 \x01(\x0e2\x1e.memory.v1.SlotUsage.ModelSlotR\x04slot\x12\x14\n" +
	"\x05model\x18\x02 \x01(\tR\x05model\x12!\n" +
	"\finput_tokens\x18\x03 \x01(\x03R\vinputTokens\x12#\n" +
	"\routput_tokens\x18\x04 \x01(\x03R\foutputTokens\"\x96\x02\n" +
	"\tModelSlot\x12\x1a\n" +
	"\x16MODEL_SLOT_UNSPECIFIED\x10\x00\x12\x16\n" +
	"\x12MODEL_SLOT_EXTRACT\x10\x01\x12\x19\n" +
	"\x15MODEL_SLOT_TIME_PARSE\x10\x02\x12\x1a\n" +
	"\x16MODEL_SLOT_CONSOLIDATE\x10\x03\x12\x1a\n" +
	"\x16MODEL_SLOT_DEDUP_CHECK\x10\x04\x12\x16\n" +
	"\x12MODEL_SLOT_REFLECT\x10\x05\x12 \n" +
	"\x1cMODEL_SLOT_STRUCTURED_OUTPUT\x10\x06\x12\x1b\n" +
	"\x17MODEL_SLOT_MENTAL_MODEL\x10\a\x12\x15\n" +
	"\x11MODEL_SLOT_RERANK\x10\b\x12\x14\n" +
	"\x10MODEL_SLOT_EMBED\x10\t\"\xf1\x02\n" +
	"\vRecallTrace\x12'\n" +
	"\x04arms\x18\x01 \x03(\v2\x13.memory.v1.ArmTraceR\x04arms\x12\x14\n" +
	"\x05fused\x18\x02 \x01(\x05R\x05fused\x12\x1a\n" +
	"\breranked\x18\x03 \x01(\x05R\breranked\x12\x16\n" +
	"\x06packed\x18\x04 \x01(\x05R\x06packed\x12!\n" +
	"\ftoken_budget\x18\x05 \x01(\x05R\vtokenBudget\x12\x1f\n" +
	"\vtokens_used\x18\x06 \x01(\x05R\n" +
	"tokensUsed\x12F\n" +
	"\x11time_window_start\x18\a \x01(\v2\x1a.google.protobuf.TimestampR\x0ftimeWindowStart\x12B\n" +
	"\x0ftime_window_end\x18\b \x01(\v2\x1a.google.protobuf.TimestampR\rtimeWindowEnd\x12\x1f\n" +
	"\vduration_ms\x18\t \x01(\x03R\n" +
	"durationMs\"v\n" +
	"\bArmTrace\x12\x10\n" +
	"\x03arm\x18\x01 \x01(\tR\x03arm\x12\x1e\n" +
	"\n" +
	"candidates\x18\x02 \x01(\x05R\n" +
	"candidates\x12\x1f\n" +
	"\vduration_ms\x18\x03 \x01(\x03R\n" +
	"durationMs\x12\x17\n" +
	"\atop_ids\x18\x04 \x03(\tR\x06topIds\"\xc0\x01\n" +
	"\rToolCallTrace\x12\x1c\n" +
	"\titeration\x18\x01 \x01(\x05R\titeration\x12\x12\n" +
	"\x04tool\x18\x02 \x01(\tR\x04tool\x12%\n" +
	"\x0earguments_json\x18\x03 \x01(\tR\rargumentsJson\x12\x1f\n" +
	"\voutput_json\x18\x04 \x01(\tR\n" +
	"outputJson\x12\x14\n" +
	"\x05error\x18\x05 \x01(\tR\x05error\x12\x1f\n" +
	"\vduration_ms\x18\x06 \x01(\x03R\n" +
	"durationMs\"\xce\x01\n" +
	"\fReflectTrace\x127\n" +
	"\n" +
	"tool_calls\x18\x01 \x03(\v2\x18.memory.v1.ToolCallTraceR\ttoolCalls\x12\x1e\n" +
	"\n" +
	"iterations\x18\x02 \x01(\x05R\n" +
	"iterations\x12\x1f\n" +
	"\vstop_reason\x18\x03 \x01(\tR\n" +
	"stopReason\x12#\n" +
	"\rdirective_ids\x18\x04 \x03(\tR\fdirectiveIds\x12\x1f\n" +
	"\vduration_ms\x18\x05 \x01(\x03R\n" +
	"durationMs\"\xd9\x02\n" +
	"\n" +
	"BankConfig\x12/\n" +
	"\x06retain\x18\x01 \x01(\v2\x17.memory.v1.RetainConfigR\x06retain\x12@\n" +
	"\fobservations\x18\x02 \x01(\v2\x1c.memory.v1.ObservationConfigR\fobservations\x12/\n" +
	"\x06recall\x18\x03 \x01(\v2\x17.memory.v1.RecallConfigR\x06recall\x122\n" +
	"\areflect\x18\x04 \x01(\v2\x18.memory.v1.ReflectConfigR\areflect\x12A\n" +
	"\rmental_models\x18\x05 \x01(\v2\x1c.memory.v1.MentalModelConfigR\fmentalModels\x120\n" +
	"\x06models\x18\x06 \x01(\v2\x18.memory.v1.ModelBindingsR\x06models\"\xe6\n" +
	"\n" +
	"\fRetainConfig\x12-\n" +
	"\x10chunk_max_tokens\x18\x01 \x01(\x05H\x00R\x0echunkMaxTokens\x88\x01\x01\x125\n" +
	"\x14chunk_overlap_tokens\x18\x02 \x01(\x05H\x01R\x12chunkOverlapTokens\x88\x01\x01\x129\n" +
	"\x16entity_match_threshold\x18\x03 \x01(\x01H\x02R\x14entityMatchThreshold\x88\x01\x01\x12@\n" +
	"\x1atemporal_link_window_hours\x18\x04 \x01(\x05H\x03R\x17temporalLinkWindowHours\x88\x01\x01\x12+\n" +
	"\x0fsemantic_link_k\x18\x05 \x01(\x05H\x04R\rsemanticLinkK\x88\x01\x01\x12D\n" +
	"\x1csemantic_link_min_similarity\x18\x06 \x01(\x01H\x05R\x19semanticLinkMinSimilarity\x88\x01\x01\x125\n" +
	"\x14extract_causal_links\x18\a \x01(\bH\x06R\x12extractCausalLinks\x88\x01\x01\x12B\n" +
	"\x1bstructured_chunk_max_tokens\x18\b \x01(\x05H\aR\x18structuredChunkMaxTokens\x88\x01\x01\x12/\n" +
	"\x11temporal_link_cap\x18\t \x01(\x05H\bR\x0ftemporalLinkCap\x88\x01\x01\x12\x18\n" +
	"\amission\x18\n" +
	" \x01(\tR\amission\x12O\n" +
	"\x0fextraction_mode\x18\v \x01(\x0e2&.memory.v1.RetainConfig.ExtractionModeR\x0eextractionMode\x128\n" +
	"\x18custom_extraction_prompt\x18\f \x01(\tR\x16customExtractionPrompt\x12;\n" +
	"\rentity_labels\x18\r \x03(\v2\x16.memory.v1.EntityLabelR\fentityLabels\x12<\n" +
	"\x18entities_allow_free_form\x18\x0e \x01(\bH\tR\x15entitiesAllowFreeForm\x88\x01\x01\x12g\n" +
	"\x1fdefault_observation_scope_modes\x18\x0f \x01(\v2 .memory.v1.ObservationScopeModesR\x1cdefaultObservationScopeModes\"\xc1\x01\n" +
	"\x0eExtractionMode\x12\x1f\n" +
	"\x1bEXTRACTION_MODE_UNSPECIFIED\x10\x00\x12\x1b\n" +
	"\x17EXTRACTION_MODE_CONCISE\x10\x01\x12\x1b\n" +
	"\x17EXTRACTION_MODE_VERBOSE\x10\x02\x12\x1c\n" +
	"\x18EXTRACTION_MODE_VERBATIM\x10\x03\x12\x1a\n" +
	"\x16EXTRACTION_MODE_CHUNKS\x10\x04\x12\x1a\n" +
	"\x16EXTRACTION_MODE_CUSTOM\x10\x05B\x13\n" +
	"\x11_chunk_max_tokensB\x17\n" +
	"\x15_chunk_overlap_tokensB\x19\n" +
	"\x17_entity_match_thresholdB\x1d\n" +
	"\x1b_temporal_link_window_hoursB\x12\n" +
	"\x10_semantic_link_kB\x1f\n" +
	"\x1d_semantic_link_min_similarityB\x17\n" +
	"\x15_extract_causal_linksB\x1e\n" +
	"\x1c_structured_chunk_max_tokensB\x14\n" +
	"\x12_temporal_link_capB\x1b\n" +
	"\x19_entities_allow_free_form\"\xc3\x06\n" +
	"\x11ObservationConfig\x12\x1d\n" +
	"\aenabled\x18\x01 \x01(\bH\x00R\aenabled\x88\x01\x01\x122\n" +
	"\x12auto_consolidation\x18\x02 \x01(\bH\x01R\x11autoConsolidation\x88\x01\x01\x12\x18\n" +
	"\amission\x18\x03 \x01(\tR\amission\x12,\n" +
	"\x0fdedup_threshold\x18\x04 \x01(\x01H\x02R\x0ededupThreshold\x88\x01\x01\x12@\n" +
	"\x1amax_observations_per_scope\x18\x05 \x01(\x05H\x03R\x17maxObservationsPerScope\x88\x01\x01\x12:\n" +
	"\x17source_facts_max_tokens\x18\x06 \x01(\x05H\x04R\x14sourceFactsMaxTokens\x88\x01\x01\x12X\n" +
	"'source_facts_max_tokens_per_observation\x18\a \x01(\x05H\x05R\"sourceFactsMaxTokensPerObservation\x88\x01\x01\x12)\n" +
	"\x0ellm_batch_size\x18\b \x01(\x05H\x06R\fllmBatchSize\x88\x01\x01\x12.\n" +
	"\x11max_units_per_run\x18\t \x01(\x05H\aR\x0emaxUnitsPerRun\x88\x01\x01\x12@\n" +
	"\n" +
	"strategies\x18\n" +
	" \x03(\v2 .memory.v1.ConsolidationStrategyR\n" +
	"strategies\x12;\n" +
	"\x17reconcile_grace_seconds\x18\v \x01(\x05H\bR\x15reconcileGraceSeconds\x88\x01\x01B\n" +
	"\n" +
	"\b_enabledB\x15\n" +
	"\x13_auto_consolidationB\x12\n" +
	"\x10_dedup_thresholdB\x1d\n" +
	"\x1b_max_observations_per_scopeB\x1a\n" +
	"\x18_source_facts_max_tokensB*\n" +
	"(_source_facts_max_tokens_per_observationB\x11\n" +
	"\x0f_llm_batch_sizeB\x14\n" +
	"\x12_max_units_per_runB\x1a\n" +
	"\x18_reconcile_grace_seconds\"g\n" +
	"\vDisposition\x12\x1e\n" +
	"\n" +
	"skepticism\x18\x01 \x01(\x05R\n" +
	"skepticism\x12\x1e\n" +
	"\n" +
	"literalism\x18\x02 \x01(\x05R\n" +
	"literalism\x12\x18\n" +
	"\aempathy\x18\x03 \x01(\x05R\aempathy\"\xf9\x03\n" +
	"\vEntityLabel\x12\x10\n" +
	"\x03key\x18\x01 \x01(\tR\x03key\x12 \n" +
	"\vdescription\x18\x02 \x01(\tR\vdescription\x12/\n" +
	"\x04type\x18\x03 \x01(\x0e2\x1b.memory.v1.EntityLabel.TypeR\x04type\x12-\n" +
	"\x06values\x18\x04 \x03(\v2\x15.memory.v1.LabelValueR\x06values\x12:\n" +
	"\x06fields\x18\x05 \x03(\v2\".memory.v1.EntityLabel.FieldsEntryR\x06fields\x12\x1f\n" +
	"\boptional\x18\x06 \x01(\bH\x00R\boptional\x88\x01\x01\x12\"\n" +
	"\rwrite_to_tags\x18\a \x01(\bR\vwriteToTags\x1aQ\n" +
	"\vFieldsEntry\x12\x10\n" +
	"\x03key\x18\x01 \x01(\tR\x03key\x12,\n" +
	"\x05value\x18\x02 \x01(\v2\x16.memory.v1.EntityLabelR\x05value:\x028\x01\"u\n" +
	"\x04Type\x12\x14\n" +
	"\x10TYPE_UNSPECIFIED\x10\x00\x12\x0e\n" +
	"\n" +
	"TYPE_VALUE\x10\x01\x12\x15\n" +
	"\x11TYPE_MULTI_VALUES\x10\x02\x12\r\n" +
	"\tTYPE_TEXT\x10\x03\x12\x13\n" +
	"\x0fTYPE_MULTI_TEXT\x10\x04\x12\f\n" +
	"\bTYPE_MAP\x10\x05B\v\n" +
	"\t_optional\"D\n" +
	"\n" +
	"LabelValue\x12\x14\n" +
	"\x05value\x18\x01 \x01(\tR\x05value\x12 \n" +
	"\vdescription\x18\x02 \x01(\tR\vdescription\"\xbd\x04\n" +
	"\x15ConsolidationStrategy\x12\x12\n" +
	"\x04name\x18\x01 \x01(\tR\x04name\x12,\n" +
	"\x06scopes\x18\x02 \x03(\v2\x14.memory.v1.TagFilterR\x06scopes\x12\x18\n" +
	"\amission\x18\x03 \x01(\tR\amission\x12@\n" +
	"\x1amax_observations_per_scope\x18\x04 \x01(\x05H\x00R\x17maxObservationsPerScope\x88\x01\x01\x12:\n" +
	"\x17source_facts_max_tokens\x18\x05 \x01(\x05H\x01R\x14sourceFactsMaxTokens\x88\x01\x01\x12X\n" +
	"'source_facts_max_tokens_per_observation\x18\x06 \x01(\x05H\x02R\"sourceFactsMaxTokensPerObservation\x88\x01\x01\x12D\n" +
	"\x11consolidate_model\x18\a \x01(\v2\x17.memory.v1.ModelBindingR\x10consolidateModel\x12C\n" +
	"\x11dedup_check_model\x18\b \x01(\v2\x17.memory.v1.ModelBindingR\x0fdedupCheckModelB\x1d\n" +
	"\x1b_max_observations_per_scopeB\x1a\n" +
	"\x18_source_facts_max_tokensB*\n" +
	"(_source_facts_max_tokens_per_observation\"\xea\t\n" +
	"\fRecallConfig\x12O\n" +
	"\x0fbudget_function\x18\x01 \x01(\x0e2&.memory.v1.RecallConfig.BudgetFunctionR\x0ebudgetFunction\x12\"\n" +
	"\n" +
	"budget_low\x18\x02 \x01(\x05H\x00R\tbudgetLow\x88\x01\x01\x12\"\n" +
	"\n" +
	"budget_mid\x18\x03 \x01(\x05H\x01R\tbudgetMid\x88\x01\x01\x12$\n" +
	"\vbudget_high\x18\x04 \x01(\x05H\x02R\n" +
	"budgetHigh\x88\x01\x01\x12E\n" +
	"\n" +
	"arm_boosts\x18\x05 \x03(\v2&.memory.v1.RecallConfig.ArmBoostsEntryR\tarmBoosts\x12\x12\n" +
	"\x04arms\x18\x06 \x03(\tR\x04arms\x12;\n" +
	"\x17reranker_max_candidates\x18\a \x01(\x05H\x03R\x15rerankerMaxCandidates\x88\x01\x01\x12\x1b\n" +
	"\x06rerank\x18\b \x01(\bH\x04R\x06rerank\x88\x01\x01\x12\x18\n" +
	"\x05rrf_k\x18\t \x01(\x05H\x05R\x04rrfK\x88\x01\x01\x12(\n" +
	"\rrecency_alpha\x18\n" +
	" \x01(\x01H\x06R\frecencyAlpha\x88\x01\x01\x12*\n" +
	"\x0etemporal_alpha\x18\v \x01(\x01H\aR\rtemporalAlpha\x88\x01\x01\x12$\n" +
	"\vproof_alpha\x18\f \x01(\x01H\bR\n" +
	"proofAlpha\x88\x01\x01\x125\n" +
	"\x14recency_horizon_days\x18\r \x01(\x05H\tR\x12recencyHorizonDays\x88\x01\x01\x12)\n" +
	"\x0egraph_max_hops\x18\x0e \x01(\x05H\n" +
	"R\fgraphMaxHops\x88\x01\x01\x12.\n" +
	"\x10temporal_buckets\x18\x0f \x01(\x05H\vR\x0ftemporalBuckets\x88\x01\x01\x12<\n" +
	"\x18fuzzy_tag_min_similarity\x18\x10 \x01(\x01H\fR\x15fuzzyTagMinSimilarity\x88\x01\x01\x12\"\n" +
	"\n" +
	"budget_min\x18\x11 \x01(\x05H\rR\tbudgetMin\x88\x01\x01\x12\"\n" +
	"\n" +
	"budget_max\x18\x12 \x01(\x05H\x0eR\tbudgetMax\x88\x01\x01\x1a<\n" +
	"\x0eArmBoostsEntry\x12\x10\n" +
	"\x03key\x18\x01 \x01(\tR\x03key\x12\x14\n" +
	"\x05value\x18\x02 \x01(\x01R\x05value:\x028\x01\"j\n" +
	"\x0eBudgetFunction\x12\x1f\n" +
	"\x1bBUDGET_FUNCTION_UNSPECIFIED\x10\x00\x12\x19\n" +
	"\x15BUDGET_FUNCTION_FIXED\x10\x01\x12\x1c\n" +
	"\x18BUDGET_FUNCTION_ADAPTIVE\x10\x02B\r\n" +
	"\v_budget_lowB\r\n" +
	"\v_budget_midB\x0e\n" +
	"\f_budget_highB\x1a\n" +
	"\x18_reranker_max_candidatesB\t\n" +
	"\a_rerankB\b\n" +
	"\x06_rrf_kB\x10\n" +
	"\x0e_recency_alphaB\x11\n" +
	"\x0f_temporal_alphaB\x0e\n" +
	"\f_proof_alphaB\x17\n" +
	"\x15_recency_horizon_daysB\x11\n" +
	"\x0f_graph_max_hopsB\x13\n" +
	"\x11_temporal_bucketsB\x1b\n" +
	"\x19_fuzzy_tag_min_similarityB\r\n" +
	"\v_budget_minB\r\n" +
	"\v_budget_max\"\x82\x06\n" +
	"\rReflectConfig\x12*\n" +
	"\x0emax_iterations\x18\x01 \x01(\x05H\x00R\rmaxIterations\x88\x01\x01\x12#\n" +
	"\renabled_tools\x18\x02 \x03(\tR\fenabledTools\x12.\n" +
	"\x10require_evidence\x18\x03 \x01(\bH\x01R\x0frequireEvidence\x88\x01\x01\x126\n" +
	"\rrecall_budget\x18\x04 \x01(\x0e2\x11.memory.v1.BudgetR\frecallBudget\x12/\n" +
	"\x11recall_max_tokens\x18\x05 \x01(\x05H\x02R\x0frecallMaxTokens\x88\x01\x01\x12H\n" +
	"\x1esearch_observations_max_tokens\x18\x06 \x01(\x05H\x03R\x1bsearchObservationsMaxTokens\x88\x01\x01\x12T\n" +
	"$search_observations_include_entities\x18\a \x01(\bH\x04R!searchObservationsIncludeEntities\x88\x01\x01\x12/\n" +
	"\x11max_answer_tokens\x18\b \x01(\x05H\x05R\x0fmaxAnswerTokens\x88\x01\x01\x12-\n" +
	"\x10max_total_tokens\x18\t \x01(\x03H\x06R\x0emaxTotalTokens\x88\x01\x01\x12\x18\n" +
	"\amission\x18\n" +
	" \x01(\tR\amission\x128\n" +
	"\vdisposition\x18\v \x01(\v2\x16.memory.v1.DispositionR\vdispositionB\x11\n" +
	"\x0f_max_iterationsB\x13\n" +
	"\x11_require_evidenceB\x14\n" +
	"\x12_recall_max_tokensB!\n" +
	"\x1f_search_observations_max_tokensB'\n" +
	"%_search_observations_include_entitiesB\x14\n" +
	"\x12_max_answer_tokensB\x13\n" +
	"\x11_max_total_tokens\"\xb0\x04\n" +
	"\x11MentalModelConfig\x129\n" +
	"\fdefault_mode\x18\x01 \x01(\x0e2\x16.memory.v1.RefreshModeR\vdefaultMode\x12+\n" +
	"\x0fmax_delta_ratio\x18\x02 \x01(\x01H\x00R\rmaxDeltaRatio\x88\x01\x01\x12=\n" +
	"\x18refresh_debounce_seconds\x18\x03 \x01(\x05H\x01R\x16refreshDebounceSeconds\x88\x01\x01\x121\n" +
	"\x12max_content_tokens\x18\x04 \x01(\x05H\x02R\x10maxContentTokens\x88\x01\x01\x12D\n" +
	"\x1cmin_refresh_interval_seconds\x18\x05 \x01(\x05H\x03R\x19minRefreshIntervalSeconds\x88\x01\x01\x123\n" +
	"\x13history_max_entries\x18\x06 \x01(\x05H\x04R\x11historyMaxEntries\x88\x01\x01\x12E\n" +
	"\x10default_strategy\x18\a \x01(\x0e2\x1a.memory.v1.RefreshStrategyR\x0fdefaultStrategyB\x12\n" +
	"\x10_max_delta_ratioB\x1b\n" +
	"\x19_refresh_debounce_secondsB\x15\n" +
	"\x13_max_content_tokensB\x1f\n" +
	"\x1d_min_refresh_interval_secondsB\x16\n" +
	"\x14_history_max_entries\"\xb7\x04\n" +
	"\rModelBindings\x121\n" +
	"\adefault\x18\x01 \x01(\v2\x17.memory.v1.ModelBindingR\adefault\x121\n" +
	"\aextract\x18\x02 \x01(\v2\x17.memory.v1.ModelBindingR\aextract\x126\n" +
	"\n" +
	"time_parse\x18\x03 \x01(\v2\x17.memory.v1.ModelBindingR\ttimeParse\x129\n" +
	"\vconsolidate\x18\x04 \x01(\v2\x17.memory.v1.ModelBindingR\vconsolidate\x128\n" +
	"\vdedup_check\x18\x05 \x01(\v2\x17.memory.v1.ModelBindingR\n" +
	"dedupCheck\x121\n" +
	"\areflect\x18\x06 \x01(\v2\x17.memory.v1.ModelBindingR\areflect\x12D\n" +
	"\x11structured_output\x18\a \x01(\v2\x17.memory.v1.ModelBindingR\x10structuredOutput\x12:\n" +
	"\fmental_model\x18\b \x01(\v2\x17.memory.v1.ModelBindingR\vmentalModel\x12/\n" +
	"\x06rerank\x18\t \x01(\v2\x17.memory.v1.ModelBindingR\x06rerank\x12-\n" +
	"\x05embed\x18\n" +
	" \x01(\v2\x17.memory.v1.ModelBindingR\x05embed\"\x89\x03\n" +
	"\fModelBinding\x12\x14\n" +
	"\x05model\x18\x01 \x01(\tR\x05model\x12%\n" +
	"\vtemperature\x18\x02 \x01(\x01H\x00R\vtemperature\x88\x01\x01\x12\x18\n" +
	"\x05top_p\x18\x03 \x01(\x01H\x01R\x04topP\x88\x01\x01\x12/\n" +
	"\x11max_output_tokens\x18\x04 \x01(\x05H\x02R\x0fmaxOutputTokens\x88\x01\x01\x12)\n" +
	"\x10reasoning_effort\x18\x05 \x01(\tR\x0freasoningEffort\x12\"\n" +
	"\n" +
	"timeout_ms\x18\x06 \x01(\x05H\x03R\ttimeoutMs\x88\x01\x01\x12$\n" +
	"\vmax_retries\x18\a \x01(\x05H\x04R\n" +
	"maxRetries\x88\x01\x01\x12-\n" +
	"\x05extra\x18\b \x01(\v2\x17.google.protobuf.StructR\x05extraB\x0e\n" +
	"\f_temperatureB\b\n" +
	"\x06_top_pB\x14\n" +
	"\x12_max_output_tokensB\r\n" +
	"\v_timeout_msB\x0e\n" +
	"\f_max_retries\"\xc3\x01\n" +
	"\x0fRetainOverrides\x12/\n" +
	"\x06retain\x18\x01 \x01(\v2\x17.memory.v1.RetainConfigR\x06retain\x12<\n" +
	"\rextract_model\x18\x02 \x01(\v2\x17.memory.v1.ModelBindingR\fextractModel\x12A\n" +
	"\x10time_parse_model\x18\x03 \x01(\v2\x17.memory.v1.ModelBindingR\x0etimeParseModel\"\xc1\x01\n" +
	"\x0fRecallOverrides\x12/\n" +
	"\x06recall\x18\x01 \x01(\v2\x17.memory.v1.RecallConfigR\x06recall\x12A\n" +
	"\x10time_parse_model\x18\x02 \x01(\v2\x17.memory.v1.ModelBindingR\x0etimeParseModel\x12:\n" +
	"\frerank_model\x18\x03 \x01(\v2\x17.memory.v1.ModelBindingR\vrerankModel\"\xc2\x02\n" +
	"\x10ReflectOverrides\x122\n" +
	"\areflect\x18\x01 \x01(\v2\x18.memory.v1.ReflectConfigR\areflect\x12/\n" +
	"\x06recall\x18\x02 \x01(\v2\x17.memory.v1.RecallConfigR\x06recall\x12<\n" +
	"\rreflect_model\x18\x03 \x01(\v2\x17.memory.v1.ModelBindingR\freflectModel\x12O\n" +
	"\x17structured_output_model\x18\x04 \x01(\v2\x17.memory.v1.ModelBindingR\x15structuredOutputModel\x12:\n" +
	"\frerank_model\x18\x05 \x01(\v2\x17.memory.v1.ModelBindingR\vrerankModel\"\x82\x06\n" +
	"\x14ConsolidateOverrides\x12\x18\n" +
	"\amission\x18\x01 \x01(\tR\amission\x12@\n" +
	"\x1amax_observations_per_scope\x18\x02 \x01(\x05H\x00R\x17maxObservationsPerScope\x88\x01\x01\x12:\n" +
	"\x17source_facts_max_tokens\x18\x03 \x01(\x05H\x01R\x14sourceFactsMaxTokens\x88\x01\x01\x12X\n" +
	"'source_facts_max_tokens_per_observation\x18\x04 \x01(\x05H\x02R\"sourceFactsMaxTokensPerObservation\x88\x01\x01\x12)\n" +
	"\x0ellm_batch_size\x18\x05 \x01(\x05H\x03R\fllmBatchSize\x88\x01\x01\x12.\n" +
	"\x11max_units_per_run\x18\x06 \x01(\x05H\x04R\x0emaxUnitsPerRun\x88\x01\x01\x12,\n" +
	"\x0fdedup_threshold\x18\a \x01(\x01H\x05R\x0ededupThreshold\x88\x01\x01\x12@\n" +
	"\n" +
	"strategies\x18\b \x03(\v2 .memory.v1.ConsolidationStrategyR\n" +
	"strategies\x12D\n" +
	"\x11consolidate_model\x18\t \x01(\v2\x17.memory.v1.ModelBindingR\x10consolidateModel\x12C\n" +
	"\x11dedup_check_model\x18\n" +
	" \x01(\v2\x17.memory.v1.ModelBindingR\x0fdedupCheckModelB\x1d\n" +
	"\x1b_max_observations_per_scopeB\x1a\n" +
	"\x18_source_facts_max_tokensB*\n" +
	"(_source_facts_max_tokens_per_observationB\x11\n" +
	"\x0f_llm_batch_sizeB\x14\n" +
	"\x12_max_units_per_runB\x12\n" +
	"\x10_dedup_threshold\"\xea\x01\n" +
	"\x11RederiveOverrides\x129\n" +
	"\x16entity_match_threshold\x18\x01 \x01(\x01H\x00R\x14entityMatchThreshold\x88\x01\x01\x12<\n" +
	"\rextract_model\x18\x02 \x01(\v2\x17.memory.v1.ModelBindingR\fextractModel\x12A\n" +
	"\x10time_parse_model\x18\x03 \x01(\v2\x17.memory.v1.ModelBindingR\x0etimeParseModelB\x19\n" +
	"\x17_entity_match_threshold\"\xdd\x02\n" +
	"\x10RefreshOverrides\x122\n" +
	"\areflect\x18\x01 \x01(\v2\x18.memory.v1.ReflectConfigR\areflect\x12+\n" +
	"\x0fmax_delta_ratio\x18\x02 \x01(\x01H\x00R\rmaxDeltaRatio\x88\x01\x01\x12<\n" +
	"\rreflect_model\x18\x03 \x01(\v2\x17.memory.v1.ModelBindingR\freflectModel\x12E\n" +
	"\x12mental_model_model\x18\x04 \x01(\v2\x17.memory.v1.ModelBindingR\x10mentalModelModel\x12O\n" +
	"\x17structured_output_model\x18\x05 \x01(\v2\x17.memory.v1.ModelBindingR\x15structuredOutputModelB\x12\n" +
	"\x10_max_delta_ratio*a\n" +
	"\vMemoryState\x12\x1c\n" +
	"\x18MEMORY_STATE_UNSPECIFIED\x10\x00\x12\x16\n" +
	"\x12MEMORY_STATE_VALID\x10\x01\x12\x1c\n" +
	"\x18MEMORY_STATE_INVALIDATED\x10\x02*\x8c\x01\n" +
	"\x0fMentalModelView\x12!\n" +
	"\x1dMENTAL_MODEL_VIEW_UNSPECIFIED\x10\x00\x12\x1b\n" +
	"\x17MENTAL_MODEL_VIEW_BASIC\x10\x01\x12\x1d\n" +
	"\x19MENTAL_MODEL_VIEW_CONTENT\x10\x02\x12\x1a\n" +
	"\x16MENTAL_MODEL_VIEW_FULL\x10\x03*\x94\x01\n" +
	"\x0eRefreshOutcome\x12\x1f\n" +
	"\x1bREFRESH_OUTCOME_UNSPECIFIED\x10\x00\x12#\n" +
	"\x1fREFRESH_OUTCOME_CONTENT_WRITTEN\x10\x01\x12 \n" +
	"\x1cREFRESH_OUTCOME_NO_NEW_FACTS\x10\x02\x12\x1a\n" +
	"\x16REFRESH_OUTCOME_FAILED\x10\x03*o\n" +
	"\bFactType\x12\x19\n" +
	"\x15FACT_TYPE_UNSPECIFIED\x10\x00\x12\x13\n" +
	"\x0fFACT_TYPE_WORLD\x10\x01\x12\x18\n" +
	"\x14FACT_TYPE_EXPERIENCE\x10\x02\x12\x19\n" +
	"\x15FACT_TYPE_OBSERVATION\x10\x03*Q\n" +
	"\x06Budget\x12\x16\n" +
	"\x12BUDGET_UNSPECIFIED\x10\x00\x12\x0e\n" +
	"\n" +
	"BUDGET_LOW\x10\x01\x12\x0e\n" +
	"\n" +
	"BUDGET_MID\x10\x02\x12\x0f\n" +
	"\vBUDGET_HIGH\x10\x03*\x9b\x01\n" +
	"\tTagsMatch\x12\x1a\n" +
	"\x16TAGS_MATCH_UNSPECIFIED\x10\x00\x12\x12\n" +
	"\x0eTAGS_MATCH_ANY\x10\x01\x12\x12\n" +
	"\x0eTAGS_MATCH_ALL\x10\x02\x12\x14\n" +
	"\x10TAGS_MATCH_EXACT\x10\x03\x12\x19\n" +
	"\x15TAGS_MATCH_ANY_STRICT\x10\x04\x12\x19\n" +
	"\x15TAGS_MATCH_ALL_STRICT\x10\x05*Z\n" +
	"\vRefreshMode\x12\x1c\n" +
	"\x18REFRESH_MODE_UNSPECIFIED\x10\x00\x12\x15\n" +
	"\x11REFRESH_MODE_FULL\x10\x01\x12\x16\n" +
	"\x12REFRESH_MODE_DELTA\x10\x02*s\n" +
	"\x0fRefreshStrategy\x12 \n" +
	"\x1cREFRESH_STRATEGY_UNSPECIFIED\x10\x00\x12\x1c\n" +
	"\x18REFRESH_STRATEGY_AGENTIC\x10\x01\x12 \n" +
	"\x1cREFRESH_STRATEGY_SINGLE_SHOT\x10\x02*\xca\x02\n" +
	"\rOperationKind\x12\x1e\n" +
	"\x1aOPERATION_KIND_UNSPECIFIED\x10\x00\x12\x1f\n" +
	"\x1bOPERATION_KIND_RETAIN_BATCH\x10\x01\x12\"\n" +
	"\x1eOPERATION_KIND_RETAIN_DOCUMENT\x10\x02\x12\x1e\n" +
	"\x1aOPERATION_KIND_CONSOLIDATE\x10\x03\x12\"\n" +
	"\x1eOPERATION_KIND_REDERIVE_MEMORY\x10\x04\x12!\n" +
	"\x1dOPERATION_KIND_RETAG_DOCUMENT\x10\x05\x12'\n" +
	"#OPERATION_KIND_REFRESH_MENTAL_MODEL\x10\x06\x12$\n" +
	" OPERATION_KIND_GRAPH_MAINTENANCE\x10\a\x12\x1e\n" +
	"\x1aOPERATION_KIND_DELETE_BANK\x10\b*\xcf\x01\n" +
	"\x0fOperationStatus\x12 \n" +
	"\x1cOPERATION_STATUS_UNSPECIFIED\x10\x00\x12\x1c\n" +
	"\x18OPERATION_STATUS_PENDING\x10\x01\x12\x1f\n" +
	"\x1bOPERATION_STATUS_PROCESSING\x10\x02\x12\x1e\n" +
	"\x1aOPERATION_STATUS_COMPLETED\x10\x03\x12\x1b\n" +
	"\x17OPERATION_STATUS_FAILED\x10\x04\x12\x1e\n" +
	"\x1aOPERATION_STATUS_CANCELLED\x10\x05*\x83\x02\n" +
	"\tEventType\x12\x1a\n" +
	"\x16EVENT_TYPE_UNSPECIFIED\x10\x00\x12\x1f\n" +
	"\x1bEVENT_TYPE_RETAIN_COMPLETED\x10\x01\x12&\n" +
	"\"EVENT_TYPE_CONSOLIDATION_COMPLETED\x10\x02\x12%\n" +
	"!EVENT_TYPE_MENTAL_MODEL_REFRESHED\x10\x03\x12*\n" +
	"&EVENT_TYPE_MENTAL_MODEL_REFRESH_FAILED\x10\x04\x12\x1d\n" +
	"\x19EVENT_TYPE_MEMORY_CURATED\x10\x05\x12\x1f\n" +
	"\x1bEVENT_TYPE_OPERATION_FAILED\x10\x062\xc1\x05\n" +
	"\rMemoryService\x12=\n" +
	"\x06Retain\x12\x18.memory.v1.RetainRequest\x1a\x19.memory.v1.RetainResponse\x12=\n" +
	"\x06Recall\x12\x18.memory.v1.RecallRequest\x1a\x19.memory.v1.RecallResponse\x12@\n" +
	"\aReflect\x12\x19.memory.v1.ReflectRequest\x1a\x1a.memory.v1.ReflectResponse\x12U\n" +
	"\x0eListRecallArms\x12 .memory.v1.ListRecallArmsRequest\x1a!.memory.v1.ListRecallArmsResponse\x12O\n" +
	"\fListMemories\x12\x1e.memory.v1.ListMemoriesRequest\x1a\x1f.memory.v1.ListMemoriesResponse\x12F\n" +
	"\tGetMemory\x12\x1b.memory.v1.GetMemoryRequest\x1a\x1c.memory.v1.GetMemoryResponse\x12O\n" +
	"\fUpdateMemory\x12\x1e.memory.v1.UpdateMemoryRequest\x1a\x1f.memory.v1.UpdateMemoryResponse\x12[\n" +
	"\x10InvalidateMemory\x12\".memory.v1.InvalidateMemoryRequest\x1a#.memory.v1.InvalidateMemoryResponse\x12R\n" +
	"\rRestoreMemory\x12\x1f.memory.v1.RestoreMemoryRequest\x1a .memory.v1.RestoreMemoryResponse2\xe7\x02\n" +
	"\x0fDocumentService\x12R\n" +
	"\rListDocuments\x12\x1f.memory.v1.ListDocumentsRequest\x1a .memory.v1.ListDocumentsResponse\x12L\n" +
	"\vGetDocument\x12\x1d.memory.v1.GetDocumentRequest\x1a\x1e.memory.v1.GetDocumentResponse\x12R\n" +
	"\rRetagDocument\x12\x1f.memory.v1.RetagDocumentRequest\x1a .memory.v1.RetagDocumentResponse\x12^\n" +
	"\x11ReprocessDocument\x12#.memory.v1.ReprocessDocumentRequest\x1a$.memory.v1.ReprocessDocumentResponse2\xa5\x04\n" +
	"\x12ObservationService\x12L\n" +
	"\vConsolidate\x12\x1d.memory.v1.ConsolidateRequest\x1a\x1e.memory.v1.ConsolidateResponse\x12j\n" +
	"\x15ListObservationScopes\x12'.memory.v1.ListObservationScopesRequest\x1a(.memory.v1.ListObservationScopesResponse\x12m\n" +
	"\x16ListObservationHistory\x12(.memory.v1.ListObservationHistoryRequest\x1a).memory.v1.ListObservationHistoryResponse\x12^\n" +
	"\x11ClearObservations\x12#.memory.v1.ClearObservationsRequest\x1a$.memory.v1.ClearObservationsResponse\x12\x85\x01\n" +
	"\x1ePreviewConsolidationStrategies\x120.memory.v1.PreviewConsolidationStrategiesRequest\x1a1.memory.v1.PreviewConsolidationStrategiesResponse2\x87\b\n" +
	"\x12MentalModelService\x12^\n" +
	"\x11CreateMentalModel\x12#.memory.v1.CreateMentalModelRequest\x1a$.memory.v1.CreateMentalModelResponse\x12U\n" +
	"\x0eGetMentalModel\x12 .memory.v1.GetMentalModelRequest\x1a!.memory.v1.GetMentalModelResponse\x12[\n" +
	"\x10ListMentalModels\x12\".memory.v1.ListMentalModelsRequest\x1a#.memory.v1.ListMentalModelsResponse\x12^\n" +
	"\x11UpdateMentalModel\x12#.memory.v1.UpdateMentalModelRequest\x1a$.memory.v1.UpdateMentalModelResponse\x12a\n" +
	"\x12RefreshMentalModel\x12$.memory.v1.RefreshMentalModelRequest\x1a%.memory.v1.RefreshMentalModelResponse\x12v\n" +
	"\x19PreviewMentalModelRefresh\x12+.memory.v1.PreviewMentalModelRefreshRequest\x1a,.memory.v1.PreviewMentalModelRefreshResponse\x12m\n" +
	"\x16ListMentalModelHistory\x12(.memory.v1.ListMentalModelHistoryRequest\x1a).memory.v1.ListMentalModelHistoryResponse\x12^\n" +
	"\x11DeleteMentalModel\x12#.memory.v1.DeleteMentalModelRequest\x1a$.memory.v1.DeleteMentalModelResponse\x12p\n" +
	"\x17DeleteMentalModelFolder\x12).memory.v1.DeleteMentalModelFolderRequest\x1a*.memory.v1.DeleteMentalModelFolderResponse\x12a\n" +
	"\x12SearchMentalModels\x12$.memory.v1.SearchMentalModelsRequest\x1a%.memory.v1.SearchMentalModelsResponse2\xa6\x04\n" +
	"\vBankService\x12I\n" +
	"\n" +
	"CreateBank\x12\x1c.memory.v1.CreateBankRequest\x1a\x1d.memory.v1.CreateBankResponse\x12@\n" +
	"\aGetBank\x12\x19.memory.v1.GetBankRequest\x1a\x1a.memory.v1.GetBankResponse\x12F\n" +
	"\tListBanks\x12\x1b.memory.v1.ListBanksRequest\x1a\x1c.memory.v1.ListBanksResponse\x12I\n" +
	"\n" +
	"DeleteBank\x12\x1c.memory.v1.DeleteBankRequest\x1a\x1d.memory.v1.DeleteBankResponse\x12O\n" +
	"\fGetBankStats\x12\x1e.memory.v1.GetBankStatsRequest\x1a\x1f.memory.v1.GetBankStatsResponse\x12C\n" +
	"\bListTags\x12\x1a.memory.v1.ListTagsRequest\x1a\x1b.memory.v1.ListTagsResponse\x12a\n" +
	"\x12ExportBankTemplate\x12$.memory.v1.ExportBankTemplateRequest\x1a%.memory.v1.ExportBankTemplateResponse2\xf0\a\n" +
	"\rConfigService\x12X\n" +
	"\x0fGetTenantConfig\x12!.memory.v1.GetTenantConfigRequest\x1a\".memory.v1.GetTenantConfigResponse\x12a\n" +
	"\x12UpdateTenantConfig\x12$.memory.v1.UpdateTenantConfigRequest\x1a%.memory.v1.UpdateTenantConfigResponse\x12^\n" +
	"\x11ResetTenantConfig\x12#.memory.v1.ResetTenantConfigRequest\x1a$.memory.v1.ResetTenantConfigResponse\x12R\n" +
	"\rGetBankConfig\x12\x1f.memory.v1.GetBankConfigRequest\x1a .memory.v1.GetBankConfigResponse\x12[\n" +
	"\x10UpdateBankConfig\x12\".memory.v1.UpdateBankConfigRequest\x1a#.memory.v1.UpdateBankConfigResponse\x12X\n" +
	"\x0fResetBankConfig\x12!.memory.v1.ResetBankConfigRequest\x1a\".memory.v1.ResetBankConfigResponse\x12R\n" +
	"\rPreviewPrompt\x12\x1f.memory.v1.PreviewPromptRequest\x1a .memory.v1.PreviewPromptResponse\x12X\n" +
	"\x0fCreateDirective\x12!.memory.v1.CreateDirectiveRequest\x1a\".memory.v1.CreateDirectiveResponse\x12X\n" +
	"\x0fUpdateDirective\x12!.memory.v1.UpdateDirectiveRequest\x1a\".memory.v1.UpdateDirectiveResponse\x12U\n" +
	"\x0eListDirectives\x12 .memory.v1.ListDirectivesRequest\x1a!.memory.v1.ListDirectivesResponse\x12X\n" +
	"\x0fDeleteDirective\x12!.memory.v1.DeleteDirectiveRequest\x1a\".memory.v1.DeleteDirectiveResponse2\xb6\x03\n" +
	"\x10OperationService\x12O\n" +
	"\fGetOperation\x12\x1e.memory.v1.GetOperationRequest\x1a\x1f.memory.v1.GetOperationResponse\x12U\n" +
	"\x0eListOperations\x12 .memory.v1.ListOperationsRequest\x1a!.memory.v1.ListOperationsResponse\x12X\n" +
	"\x0fCancelOperation\x12!.memory.v1.CancelOperationRequest\x1a\".memory.v1.CancelOperationResponse\x12U\n" +
	"\x0eRetryOperation\x12 .memory.v1.RetryOperationRequest\x1a!.memory.v1.RetryOperationResponse\x12I\n" +
	"\n" +
	"ListEvents\x12\x1c.memory.v1.ListEventsRequest\x1a\x1d.memory.v1.ListEventsResponseB\x1dZ\x1bmemd/gen/memory/v1;memoryv1b\x06proto3"

var (
	file_memory_v1_memory_proto_rawDescOnce sync.Once
	file_memory_v1_memory_proto_rawDescData []byte
)

func file_memory_v1_memory_proto_rawDescGZIP() []byte {
	file_memory_v1_memory_proto_rawDescOnce.Do(func() {
		file_memory_v1_memory_proto_rawDescData = protoimpl.X.CompressGZIP(unsafe.Slice(unsafe.StringData(file_memory_v1_memory_proto_rawDesc), len(file_memory_v1_memory_proto_rawDesc)))
	})
	return file_memory_v1_memory_proto_rawDescData
}

var file_memory_v1_memory_proto_enumTypes = make([]protoimpl.EnumInfo, 25)
var file_memory_v1_memory_proto_msgTypes = make([]protoimpl.MessageInfo, 182)
var file_memory_v1_memory_proto_goTypes = []any{
	(MemoryState)(0),                               // 0: memory.v1.MemoryState
	(MentalModelView)(0),                           // 1: memory.v1.MentalModelView
	(RefreshOutcome)(0),                            // 2: memory.v1.RefreshOutcome
	(FactType)(0),                                  // 3: memory.v1.FactType
	(Budget)(0),                                    // 4: memory.v1.Budget
	(TagsMatch)(0),                                 // 5: memory.v1.TagsMatch
	(RefreshMode)(0),                               // 6: memory.v1.RefreshMode
	(RefreshStrategy)(0),                           // 7: memory.v1.RefreshStrategy
	(OperationKind)(0),                             // 8: memory.v1.OperationKind
	(OperationStatus)(0),                           // 9: memory.v1.OperationStatus
	(EventType)(0),                                 // 10: memory.v1.EventType
	(ListMemoriesRequest_TimeField)(0),             // 11: memory.v1.ListMemoriesRequest.TimeField
	(ListDocumentsRequest_TimeField)(0),            // 12: memory.v1.ListDocumentsRequest.TimeField
	(MentalModelHistoryEntry_Kind)(0),              // 13: memory.v1.MentalModelHistoryEntry.Kind
	(DeltaOp_Kind)(0),                              // 14: memory.v1.DeltaOp.Kind
	(ListTagsRequest_TagSource)(0),                 // 15: memory.v1.ListTagsRequest.TagSource
	(PreviewPromptRequest_PromptOperation)(0),      // 16: memory.v1.PreviewPromptRequest.PromptOperation
	(PromptMessage_PromptRole)(0),                  // 17: memory.v1.PromptMessage.PromptRole
	(PromptBlock_PromptSource)(0),                  // 18: memory.v1.PromptBlock.PromptSource
	(ObservationScopeModes_Mode)(0),                // 19: memory.v1.ObservationScopeModes.Mode
	(TagLeaf_Resolve)(0),                           // 20: memory.v1.TagLeaf.Resolve
	(SlotUsage_ModelSlot)(0),                       // 21: memory.v1.SlotUsage.ModelSlot
	(RetainConfig_ExtractionMode)(0),               // 22: memory.v1.RetainConfig.ExtractionMode
	(EntityLabel_Type)(0),                          // 23: memory.v1.EntityLabel.Type
	(RecallConfig_BudgetFunction)(0),               // 24: memory.v1.RecallConfig.BudgetFunction
	(*RetainRequest)(nil),                          // 25: memory.v1.RetainRequest
	(*RetainResponse)(nil),                         // 26: memory.v1.RetainResponse
	(*RetainItem)(nil),                             // 27: memory.v1.RetainItem
	(*EntityHint)(nil),                             // 28: memory.v1.EntityHint
	(*RetainedDocument)(nil),                       // 29: memory.v1.RetainedDocument
	(*RecallRequest)(nil),                          // 30: memory.v1.RecallRequest
	(*RecallResponse)(nil),                         // 31: memory.v1.RecallResponse
	(*RecallInclude)(nil),                          // 32: memory.v1.RecallInclude
	(*EntityState)(nil),                            // 33: memory.v1.EntityState
	(*ReflectRequest)(nil),                         // 34: memory.v1.ReflectRequest
	(*ReflectResponse)(nil),                        // 35: memory.v1.ReflectResponse
	(*ReflectInclude)(nil),                         // 36: memory.v1.ReflectInclude
	(*BasedOn)(nil),                                // 37: memory.v1.BasedOn
	(*ListRecallArmsRequest)(nil),                  // 38: memory.v1.ListRecallArmsRequest
	(*ListRecallArmsResponse)(nil),                 // 39: memory.v1.ListRecallArmsResponse
	(*RecallArm)(nil),                              // 40: memory.v1.RecallArm
	(*ListMemoriesRequest)(nil),                    // 41: memory.v1.ListMemoriesRequest
	(*ListMemoriesResponse)(nil),                   // 42: memory.v1.ListMemoriesResponse
	(*GetMemoryRequest)(nil),                       // 43: memory.v1.GetMemoryRequest
	(*GetMemoryResponse)(nil),                      // 44: memory.v1.GetMemoryResponse
	(*UpdateMemoryRequest)(nil),                    // 45: memory.v1.UpdateMemoryRequest
	(*UpdateMemoryResponse)(nil),                   // 46: memory.v1.UpdateMemoryResponse
	(*MemoryEdit)(nil),                             // 47: memory.v1.MemoryEdit
	(*InvalidateMemoryRequest)(nil),                // 48: memory.v1.InvalidateMemoryRequest
	(*InvalidateMemoryResponse)(nil),               // 49: memory.v1.InvalidateMemoryResponse
	(*RestoreMemoryRequest)(nil),                   // 50: memory.v1.RestoreMemoryRequest
	(*RestoreMemoryResponse)(nil),                  // 51: memory.v1.RestoreMemoryResponse
	(*Memory)(nil),                                 // 52: memory.v1.Memory
	(*ListDocumentsRequest)(nil),                   // 53: memory.v1.ListDocumentsRequest
	(*ListDocumentsResponse)(nil),                  // 54: memory.v1.ListDocumentsResponse
	(*GetDocumentRequest)(nil),                     // 55: memory.v1.GetDocumentRequest
	(*GetDocumentResponse)(nil),                    // 56: memory.v1.GetDocumentResponse
	(*RetagDocumentRequest)(nil),                   // 57: memory.v1.RetagDocumentRequest
	(*RetagDocumentResponse)(nil),                  // 58: memory.v1.RetagDocumentResponse
	(*ReprocessDocumentRequest)(nil),               // 59: memory.v1.ReprocessDocumentRequest
	(*ReprocessDocumentResponse)(nil),              // 60: memory.v1.ReprocessDocumentResponse
	(*Document)(nil),                               // 61: memory.v1.Document
	(*ConsolidateRequest)(nil),                     // 62: memory.v1.ConsolidateRequest
	(*ConsolidateResponse)(nil),                    // 63: memory.v1.ConsolidateResponse
	(*ListObservationScopesRequest)(nil),           // 64: memory.v1.ListObservationScopesRequest
	(*ListObservationScopesResponse)(nil),          // 65: memory.v1.ListObservationScopesResponse
	(*ObservationScope)(nil),                       // 66: memory.v1.ObservationScope
	(*ListObservationHistoryRequest)(nil),          // 67: memory.v1.ListObservationHistoryRequest
	(*ListObservationHistoryResponse)(nil),         // 68: memory.v1.ListObservationHistoryResponse
	(*ObservationVersion)(nil),                     // 69: memory.v1.ObservationVersion
	(*ClearObservationsRequest)(nil),               // 70: memory.v1.ClearObservationsRequest
	(*TagSetList)(nil),                             // 71: memory.v1.TagSetList
	(*ClearObservationsResponse)(nil),              // 72: memory.v1.ClearObservationsResponse
	(*PreviewConsolidationStrategiesRequest)(nil),  // 73: memory.v1.PreviewConsolidationStrategiesRequest
	(*PreviewConsolidationStrategiesResponse)(nil), // 74: memory.v1.PreviewConsolidationStrategiesResponse
	(*StrategyMatch)(nil),                          // 75: memory.v1.StrategyMatch
	(*CreateMentalModelRequest)(nil),               // 76: memory.v1.CreateMentalModelRequest
	(*CreateMentalModelResponse)(nil),              // 77: memory.v1.CreateMentalModelResponse
	(*GetMentalModelRequest)(nil),                  // 78: memory.v1.GetMentalModelRequest
	(*GetMentalModelResponse)(nil),                 // 79: memory.v1.GetMentalModelResponse
	(*ListMentalModelsRequest)(nil),                // 80: memory.v1.ListMentalModelsRequest
	(*ListMentalModelsResponse)(nil),               // 81: memory.v1.ListMentalModelsResponse
	(*UpdateMentalModelRequest)(nil),               // 82: memory.v1.UpdateMentalModelRequest
	(*UpdateMentalModelResponse)(nil),              // 83: memory.v1.UpdateMentalModelResponse
	(*RefreshMentalModelRequest)(nil),              // 84: memory.v1.RefreshMentalModelRequest
	(*RefreshMentalModelResponse)(nil),             // 85: memory.v1.RefreshMentalModelResponse
	(*PreviewMentalModelRefreshRequest)(nil),       // 86: memory.v1.PreviewMentalModelRefreshRequest
	(*PreviewMentalModelRefreshResponse)(nil),      // 87: memory.v1.PreviewMentalModelRefreshResponse
	(*ResolvedScope)(nil),                          // 88: memory.v1.ResolvedScope
	(*ListMentalModelHistoryRequest)(nil),          // 89: memory.v1.ListMentalModelHistoryRequest
	(*ListMentalModelHistoryResponse)(nil),         // 90: memory.v1.ListMentalModelHistoryResponse
	(*MentalModelHistoryEntry)(nil),                // 91: memory.v1.MentalModelHistoryEntry
	(*DeleteMentalModelRequest)(nil),               // 92: memory.v1.DeleteMentalModelRequest
	(*DeleteMentalModelResponse)(nil),              // 93: memory.v1.DeleteMentalModelResponse
	(*DeleteMentalModelFolderRequest)(nil),         // 94: memory.v1.DeleteMentalModelFolderRequest
	(*DeleteMentalModelFolderResponse)(nil),        // 95: memory.v1.DeleteMentalModelFolderResponse
	(*SearchMentalModelsRequest)(nil),              // 96: memory.v1.SearchMentalModelsRequest
	(*SearchMentalModelsResponse)(nil),             // 97: memory.v1.SearchMentalModelsResponse
	(*MentalModelHit)(nil),                         // 98: memory.v1.MentalModelHit
	(*MentalModel)(nil),                            // 99: memory.v1.MentalModel
	(*MentalModelSpec)(nil),                        // 100: memory.v1.MentalModelSpec
	(*RefreshSchedule)(nil),                        // 101: memory.v1.RefreshSchedule
	(*MentalModelScope)(nil),                       // 102: memory.v1.MentalModelScope
	(*RefreshTrace)(nil),                           // 103: memory.v1.RefreshTrace
	(*DeltaOp)(nil),                                // 104: memory.v1.DeltaOp
	(*CreateBankRequest)(nil),                      // 105: memory.v1.CreateBankRequest
	(*CreateBankResponse)(nil),                     // 106: memory.v1.CreateBankResponse
	(*BankTemplate)(nil),                           // 107: memory.v1.BankTemplate
	(*GetBankRequest)(nil),                         // 108: memory.v1.GetBankRequest
	(*GetBankResponse)(nil),                        // 109: memory.v1.GetBankResponse
	(*ListBanksRequest)(nil),                       // 110: memory.v1.ListBanksRequest
	(*ListBanksResponse)(nil),                      // 111: memory.v1.ListBanksResponse
	(*DeleteBankRequest)(nil),                      // 112: memory.v1.DeleteBankRequest
	(*DeleteBankResponse)(nil),                     // 113: memory.v1.DeleteBankResponse
	(*GetBankStatsRequest)(nil),                    // 114: memory.v1.GetBankStatsRequest
	(*GetBankStatsResponse)(nil),                   // 115: memory.v1.GetBankStatsResponse
	(*BankStats)(nil),                              // 116: memory.v1.BankStats
	(*ListTagsRequest)(nil),                        // 117: memory.v1.ListTagsRequest
	(*ListTagsResponse)(nil),                       // 118: memory.v1.ListTagsResponse
	(*TagCount)(nil),                               // 119: memory.v1.TagCount
	(*ExportBankTemplateRequest)(nil),              // 120: memory.v1.ExportBankTemplateRequest
	(*ExportBankTemplateResponse)(nil),             // 121: memory.v1.ExportBankTemplateResponse
	(*Bank)(nil),                                   // 122: memory.v1.Bank
	(*GetTenantConfigRequest)(nil),                 // 123: memory.v1.GetTenantConfigRequest
	(*GetTenantConfigResponse)(nil),                // 124: memory.v1.GetTenantConfigResponse
	(*UpdateTenantConfigRequest)(nil),              // 125: memory.v1.UpdateTenantConfigRequest
	(*UpdateTenantConfigResponse)(nil),             // 126: memory.v1.UpdateTenantConfigResponse
	(*ResetTenantConfigRequest)(nil),               // 127: memory.v1.ResetTenantConfigRequest
	(*ResetTenantConfigResponse)(nil),              // 128: memory.v1.ResetTenantConfigResponse
	(*GetBankConfigRequest)(nil),                   // 129: memory.v1.GetBankConfigRequest
	(*GetBankConfigResponse)(nil),                  // 130: memory.v1.GetBankConfigResponse
	(*UpdateBankConfigRequest)(nil),                // 131: memory.v1.UpdateBankConfigRequest
	(*UpdateBankConfigResponse)(nil),               // 132: memory.v1.UpdateBankConfigResponse
	(*ResetBankConfigRequest)(nil),                 // 133: memory.v1.ResetBankConfigRequest
	(*ResetBankConfigResponse)(nil),                // 134: memory.v1.ResetBankConfigResponse
	(*PreviewPromptRequest)(nil),                   // 135: memory.v1.PreviewPromptRequest
	(*PreviewPromptResponse)(nil),                  // 136: memory.v1.PreviewPromptResponse
	(*PromptPreview)(nil),                          // 137: memory.v1.PromptPreview
	(*PromptMessage)(nil),                          // 138: memory.v1.PromptMessage
	(*PromptBlock)(nil),                            // 139: memory.v1.PromptBlock
	(*CreateDirectiveRequest)(nil),                 // 140: memory.v1.CreateDirectiveRequest
	(*CreateDirectiveResponse)(nil),                // 141: memory.v1.CreateDirectiveResponse
	(*UpdateDirectiveRequest)(nil),                 // 142: memory.v1.UpdateDirectiveRequest
	(*UpdateDirectiveResponse)(nil),                // 143: memory.v1.UpdateDirectiveResponse
	(*ListDirectivesRequest)(nil),                  // 144: memory.v1.ListDirectivesRequest
	(*ListDirectivesResponse)(nil),                 // 145: memory.v1.ListDirectivesResponse
	(*DeleteDirectiveRequest)(nil),                 // 146: memory.v1.DeleteDirectiveRequest
	(*DeleteDirectiveResponse)(nil),                // 147: memory.v1.DeleteDirectiveResponse
	(*ConfigView)(nil),                             // 148: memory.v1.ConfigView
	(*Directive)(nil),                              // 149: memory.v1.Directive
	(*GetOperationRequest)(nil),                    // 150: memory.v1.GetOperationRequest
	(*GetOperationResponse)(nil),                   // 151: memory.v1.GetOperationResponse
	(*ListOperationsRequest)(nil),                  // 152: memory.v1.ListOperationsRequest
	(*ListOperationsResponse)(nil),                 // 153: memory.v1.ListOperationsResponse
	(*CancelOperationRequest)(nil),                 // 154: memory.v1.CancelOperationRequest
	(*CancelOperationResponse)(nil),                // 155: memory.v1.CancelOperationResponse
	(*RetryOperationRequest)(nil),                  // 156: memory.v1.RetryOperationRequest
	(*RetryOperationResponse)(nil),                 // 157: memory.v1.RetryOperationResponse
	(*ListEventsRequest)(nil),                      // 158: memory.v1.ListEventsRequest
	(*ListEventsResponse)(nil),                     // 159: memory.v1.ListEventsResponse
	(*LifecycleEvent)(nil),                         // 160: memory.v1.LifecycleEvent
	(*Operation)(nil),                              // 161: memory.v1.Operation
	(*Progress)(nil),                               // 162: memory.v1.Progress
	(*TagSet)(nil),                                 // 163: memory.v1.TagSet
	(*ObservationScopeModes)(nil),                  // 164: memory.v1.ObservationScopeModes
	(*TagFilter)(nil),                              // 165: memory.v1.TagFilter
	(*TagGroup)(nil),                               // 166: memory.v1.TagGroup
	(*TagGroupList)(nil),                           // 167: memory.v1.TagGroupList
	(*TagLeaf)(nil),                                // 168: memory.v1.TagLeaf
	(*Scores)(nil),                                 // 169: memory.v1.Scores
	(*MinScores)(nil),                              // 170: memory.v1.MinScores
	(*Chunk)(nil),                                  // 171: memory.v1.Chunk
	(*SlotUsage)(nil),                              // 172: memory.v1.SlotUsage
	(*RecallTrace)(nil),                            // 173: memory.v1.RecallTrace
	(*ArmTrace)(nil),                               // 174: memory.v1.ArmTrace
	(*ToolCallTrace)(nil),                          // 175: memory.v1.ToolCallTrace
	(*ReflectTrace)(nil),                           // 176: memory.v1.ReflectTrace
	(*BankConfig)(nil),                             // 177: memory.v1.BankConfig
	(*RetainConfig)(nil),                           // 178: memory.v1.RetainConfig
	(*ObservationConfig)(nil),                      // 179: memory.v1.ObservationConfig
	(*Disposition)(nil),                            // 180: memory.v1.Disposition
	(*EntityLabel)(nil),                            // 181: memory.v1.EntityLabel
	(*LabelValue)(nil),                             // 182: memory.v1.LabelValue
	(*ConsolidationStrategy)(nil),                  // 183: memory.v1.ConsolidationStrategy
	(*RecallConfig)(nil),                           // 184: memory.v1.RecallConfig
	(*ReflectConfig)(nil),                          // 185: memory.v1.ReflectConfig
	(*MentalModelConfig)(nil),                      // 186: memory.v1.MentalModelConfig
	(*ModelBindings)(nil),                          // 187: memory.v1.ModelBindings
	(*ModelBinding)(nil),                           // 188: memory.v1.ModelBinding
	(*RetainOverrides)(nil),                        // 189: memory.v1.RetainOverrides
	(*RecallOverrides)(nil),                        // 190: memory.v1.RecallOverrides
	(*ReflectOverrides)(nil),                       // 191: memory.v1.ReflectOverrides
	(*ConsolidateOverrides)(nil),                   // 192: memory.v1.ConsolidateOverrides
	(*RederiveOverrides)(nil),                      // 193: memory.v1.RederiveOverrides
	(*RefreshOverrides)(nil),                       // 194: memory.v1.RefreshOverrides
	nil,                                            // 195: memory.v1.RetainItem.MetadataEntry
	nil,                                            // 196: memory.v1.RecallResponse.SourceFactsEntry
	nil,                                            // 197: memory.v1.RecallResponse.ChunksEntry
	nil,                                            // 198: memory.v1.RecallResponse.EntitiesEntry
	nil,                                            // 199: memory.v1.Memory.MetadataEntry
	nil,                                            // 200: memory.v1.Document.MemoriesByFactTypeEntry
	nil,                                            // 201: memory.v1.BankStats.MemoriesByFactTypeEntry
	nil,                                            // 202: memory.v1.BankStats.PendingOperationsByKindEntry
	nil,                                            // 203: memory.v1.Scores.ArmsEntry
	nil,                                            // 204: memory.v1.MinScores.ArmsEntry
	nil,                                            // 205: memory.v1.EntityLabel.FieldsEntry
	nil,                                            // 206: memory.v1.RecallConfig.ArmBoostsEntry
	(*timestamppb.Timestamp)(nil),                  // 207: google.protobuf.Timestamp
	(*fieldmaskpb.FieldMask)(nil),                  // 208: google.protobuf.FieldMask
	(*structpb.Struct)(nil),                        // 209: google.protobuf.Struct
}
var file_memory_v1_memory_proto_depIdxs = []int32{
	27,  // 0: memory.v1.RetainRequest.items:type_name -> memory.v1.RetainItem
	189, // 1: memory.v1.RetainRequest.overrides:type_name -> memory.v1.RetainOverrides
	29,  // 2: memory.v1.RetainResponse.documents:type_name -> memory.v1.RetainedDocument
	161, // 3: memory.v1.RetainResponse.batch_operation:type_name -> memory.v1.Operation
	172, // 4: memory.v1.RetainResponse.usage:type_name -> memory.v1.SlotUsage
	207, // 5: memory.v1.RetainItem.timestamp:type_name -> google.protobuf.Timestamp
	164, // 6: memory.v1.RetainItem.observation_scope_modes:type_name -> memory.v1.ObservationScopeModes
	195, // 7: memory.v1.RetainItem.metadata:type_name -> memory.v1.RetainItem.MetadataEntry
	28,  // 8: memory.v1.RetainItem.entities:type_name -> memory.v1.EntityHint
	189, // 9: memory.v1.RetainItem.overrides:type_name -> memory.v1.RetainOverrides
	161, // 10: memory.v1.RetainedDocument.operation:type_name -> memory.v1.Operation
	3,   // 11: memory.v1.RecallRequest.fact_types:type_name -> memory.v1.FactType
	165, // 12: memory.v1.RecallRequest.tags:type_name -> memory.v1.TagFilter
	4,   // 13: memory.v1.RecallRequest.budget:type_name -> memory.v1.Budget
	207, // 14: memory.v1.RecallRequest.query_time:type_name -> google.protobuf.Timestamp
	207, // 15: memory.v1.RecallRequest.time_window_start:type_name -> google.protobuf.Timestamp
	207, // 16: memory.v1.RecallRequest.time_window_end:type_name -> google.protobuf.Timestamp
	170, // 17: memory.v1.RecallRequest.min_scores:type_name -> memory.v1.MinScores
	32,  // 18: memory.v1.RecallRequest.include:type_name -> memory.v1.RecallInclude
	190, // 19: memory.v1.RecallRequest.overrides:type_name -> memory.v1.RecallOverrides
	52,  // 20: memory.v1.RecallResponse.results:type_name -> memory.v1.Memory
	196, // 21: memory.v1.RecallResponse.source_facts:type_name -> memory.v1.RecallResponse.SourceFactsEntry
	197, // 22: memory.v1.RecallResponse.chunks:type_name -> memory.v1.RecallResponse.ChunksEntry
	198, // 23: memory.v1.RecallResponse.entities:type_name -> memory.v1.RecallResponse.EntitiesEntry
	173, // 24: memory.v1.RecallResponse.trace:type_name -> memory.v1.RecallTrace
	165, // 25: memory.v1.ReflectRequest.tags:type_name -> memory.v1.TagFilter
	4,   // 26: memory.v1.ReflectRequest.budget:type_name -> memory.v1.Budget
	149, // 27: memory.v1.ReflectRequest.extra_directives:type_name -> memory.v1.Directive
	36,  // 28: memory.v1.ReflectRequest.include:type_name -> memory.v1.ReflectInclude
	207, // 29: memory.v1.ReflectRequest.query_time:type_name -> google.protobuf.Timestamp
	191, // 30: memory.v1.ReflectRequest.overrides:type_name -> memory.v1.ReflectOverrides
	37,  // 31: memory.v1.ReflectResponse.based_on:type_name -> memory.v1.BasedOn
	176, // 32: memory.v1.ReflectResponse.trace:type_name -> memory.v1.ReflectTrace
	172, // 33: memory.v1.ReflectResponse.usage:type_name -> memory.v1.SlotUsage
	52,  // 34: memory.v1.BasedOn.memories:type_name -> memory.v1.Memory
	40,  // 35: memory.v1.ListRecallArmsResponse.arms:type_name -> memory.v1.RecallArm
	3,   // 36: memory.v1.ListMemoriesRequest.fact_types:type_name -> memory.v1.FactType
	0,   // 37: memory.v1.ListMemoriesRequest.state:type_name -> memory.v1.MemoryState
	207, // 38: memory.v1.ListMemoriesRequest.start:type_name -> google.protobuf.Timestamp
	207, // 39: memory.v1.ListMemoriesRequest.end:type_name -> google.protobuf.Timestamp
	11,  // 40: memory.v1.ListMemoriesRequest.time_field:type_name -> memory.v1.ListMemoriesRequest.TimeField
	165, // 41: memory.v1.ListMemoriesRequest.tags:type_name -> memory.v1.TagFilter
	52,  // 42: memory.v1.ListMemoriesResponse.memories:type_name -> memory.v1.Memory
	52,  // 43: memory.v1.GetMemoryResponse.memory:type_name -> memory.v1.Memory
	47,  // 44: memory.v1.UpdateMemoryRequest.memory:type_name -> memory.v1.MemoryEdit
	208, // 45: memory.v1.UpdateMemoryRequest.update_mask:type_name -> google.protobuf.FieldMask
	193, // 46: memory.v1.UpdateMemoryRequest.overrides:type_name -> memory.v1.RederiveOverrides
	52,  // 47: memory.v1.UpdateMemoryResponse.memory:type_name -> memory.v1.Memory
	161, // 48: memory.v1.UpdateMemoryResponse.operation:type_name -> memory.v1.Operation
	207, // 49: memory.v1.MemoryEdit.occurred_start:type_name -> google.protobuf.Timestamp
	207, // 50: memory.v1.MemoryEdit.occurred_end:type_name -> google.protobuf.Timestamp
	3,   // 51: memory.v1.MemoryEdit.fact_type:type_name -> memory.v1.FactType
	52,  // 52: memory.v1.InvalidateMemoryResponse.memory:type_name -> memory.v1.Memory
	193, // 53: memory.v1.RestoreMemoryRequest.overrides:type_name -> memory.v1.RederiveOverrides
	52,  // 54: memory.v1.RestoreMemoryResponse.memory:type_name -> memory.v1.Memory
	161, // 55: memory.v1.RestoreMemoryResponse.operation:type_name -> memory.v1.Operation
	3,   // 56: memory.v1.Memory.fact_type:type_name -> memory.v1.FactType
	199, // 57: memory.v1.Memory.metadata:type_name -> memory.v1.Memory.MetadataEntry
	207, // 58: memory.v1.Memory.occurred_start:type_name -> google.protobuf.Timestamp
	207, // 59: memory.v1.Memory.occurred_end:type_name -> google.protobuf.Timestamp
	207, // 60: memory.v1.Memory.mentioned_at:type_name -> google.protobuf.Timestamp
	169, // 61: memory.v1.Memory.scores:type_name -> memory.v1.Scores
	0,   // 62: memory.v1.Memory.state:type_name -> memory.v1.MemoryState
	207, // 63: memory.v1.Memory.edited_at:type_name -> google.protobuf.Timestamp
	165, // 64: memory.v1.ListDocumentsRequest.tags:type_name -> memory.v1.TagFilter
	207, // 65: memory.v1.ListDocumentsRequest.start:type_name -> google.protobuf.Timestamp
	207, // 66: memory.v1.ListDocumentsRequest.end:type_name -> google.protobuf.Timestamp
	12,  // 67: memory.v1.ListDocumentsRequest.time_field:type_name -> memory.v1.ListDocumentsRequest.TimeField
	61,  // 68: memory.v1.ListDocumentsResponse.documents:type_name -> memory.v1.Document
	61,  // 69: memory.v1.GetDocumentResponse.document:type_name -> memory.v1.Document
	163, // 70: memory.v1.RetagDocumentRequest.tags:type_name -> memory.v1.TagSet
	161, // 71: memory.v1.RetagDocumentResponse.operation:type_name -> memory.v1.Operation
	189, // 72: memory.v1.ReprocessDocumentRequest.overrides:type_name -> memory.v1.RetainOverrides
	161, // 73: memory.v1.ReprocessDocumentResponse.operation:type_name -> memory.v1.Operation
	200, // 74: memory.v1.Document.memories_by_fact_type:type_name -> memory.v1.Document.MemoriesByFactTypeEntry
	189, // 75: memory.v1.Document.retain_overrides:type_name -> memory.v1.RetainOverrides
	207, // 76: memory.v1.Document.created_at:type_name -> google.protobuf.Timestamp
	207, // 77: memory.v1.Document.updated_at:type_name -> google.protobuf.Timestamp
	207, // 78: memory.v1.Document.extracted_at:type_name -> google.protobuf.Timestamp
	163, // 79: memory.v1.ConsolidateRequest.scopes:type_name -> memory.v1.TagSet
	192, // 80: memory.v1.ConsolidateRequest.overrides:type_name -> memory.v1.ConsolidateOverrides
	161, // 81: memory.v1.ConsolidateResponse.operation:type_name -> memory.v1.Operation
	66,  // 82: memory.v1.ListObservationScopesResponse.scopes:type_name -> memory.v1.ObservationScope
	163, // 83: memory.v1.ObservationScope.scope:type_name -> memory.v1.TagSet
	69,  // 84: memory.v1.ListObservationHistoryResponse.versions:type_name -> memory.v1.ObservationVersion
	207, // 85: memory.v1.ObservationVersion.created_at:type_name -> google.protobuf.Timestamp
	71,  // 86: memory.v1.ClearObservationsRequest.scopes:type_name -> memory.v1.TagSetList
	163, // 87: memory.v1.TagSetList.tag_sets:type_name -> memory.v1.TagSet
	161, // 88: memory.v1.ClearObservationsResponse.operation:type_name -> memory.v1.Operation
	183, // 89: memory.v1.PreviewConsolidationStrategiesRequest.strategies:type_name -> memory.v1.ConsolidationStrategy
	75,  // 90: memory.v1.PreviewConsolidationStrategiesResponse.matches:type_name -> memory.v1.StrategyMatch
	100, // 91: memory.v1.CreateMentalModelRequest.spec:type_name -> memory.v1.MentalModelSpec
	99,  // 92: memory.v1.CreateMentalModelResponse.mental_model:type_name -> memory.v1.MentalModel
	161, // 93: memory.v1.CreateMentalModelResponse.operation:type_name -> memory.v1.Operation
	1,   // 94: memory.v1.GetMentalModelRequest.view:type_name -> memory.v1.MentalModelView
	99,  // 95: memory.v1.GetMentalModelResponse.mental_model:type_name -> memory.v1.MentalModel
	1,   // 96: memory.v1.ListMentalModelsRequest.view:type_name -> memory.v1.MentalModelView
	165, // 97: memory.v1.ListMentalModelsRequest.tags:type_name -> memory.v1.TagFilter
	99,  // 98: memory.v1.ListMentalModelsResponse.mental_models:type_name -> memory.v1.MentalModel
	100, // 99: memory.v1.UpdateMentalModelRequest.spec:type_name -> memory.v1.MentalModelSpec
	208, // 100: memory.v1.UpdateMentalModelRequest.update_mask:type_name -> google.protobuf.FieldMask
	99,  // 101: memory.v1.UpdateMentalModelResponse.mental_model:type_name -> memory.v1.MentalModel
	194, // 102: memory.v1.RefreshMentalModelRequest.overrides:type_name -> memory.v1.RefreshOverrides
	7,   // 103: memory.v1.RefreshMentalModelRequest.strategy:type_name -> memory.v1.RefreshStrategy
	161, // 104: memory.v1.RefreshMentalModelResponse.operation:type_name -> memory.v1.Operation
	7,   // 105: memory.v1.PreviewMentalModelRefreshRequest.strategy:type_name -> memory.v1.RefreshStrategy
	6,   // 106: memory.v1.PreviewMentalModelRefreshResponse.requested_mode:type_name -> memory.v1.RefreshMode
	103, // 107: memory.v1.PreviewMentalModelRefreshResponse.trace:type_name -> memory.v1.RefreshTrace
	88,  // 108: memory.v1.PreviewMentalModelRefreshResponse.resolved_scope:type_name -> memory.v1.ResolvedScope
	165, // 109: memory.v1.ResolvedScope.tags:type_name -> memory.v1.TagFilter
	3,   // 110: memory.v1.ResolvedScope.fact_types:type_name -> memory.v1.FactType
	13,  // 111: memory.v1.ListMentalModelHistoryRequest.kind:type_name -> memory.v1.MentalModelHistoryEntry.Kind
	91,  // 112: memory.v1.ListMentalModelHistoryResponse.entries:type_name -> memory.v1.MentalModelHistoryEntry
	13,  // 113: memory.v1.MentalModelHistoryEntry.kind:type_name -> memory.v1.MentalModelHistoryEntry.Kind
	207, // 114: memory.v1.MentalModelHistoryEntry.created_at:type_name -> google.protobuf.Timestamp
	2,   // 115: memory.v1.MentalModelHistoryEntry.outcome:type_name -> memory.v1.RefreshOutcome
	98,  // 116: memory.v1.SearchMentalModelsResponse.hits:type_name -> memory.v1.MentalModelHit
	100, // 117: memory.v1.MentalModel.spec:type_name -> memory.v1.MentalModelSpec
	207, // 118: memory.v1.MentalModel.last_refreshed_at:type_name -> google.protobuf.Timestamp
	207, // 119: memory.v1.MentalModel.last_refresh_failed_at:type_name -> google.protobuf.Timestamp
	103, // 120: memory.v1.MentalModel.last_trace:type_name -> memory.v1.RefreshTrace
	207, // 121: memory.v1.MentalModel.created_at:type_name -> google.protobuf.Timestamp
	101, // 122: memory.v1.MentalModelSpec.schedule:type_name -> memory.v1.RefreshSchedule
	102, // 123: memory.v1.MentalModelSpec.scope:type_name -> memory.v1.MentalModelScope
	6,   // 124: memory.v1.MentalModelSpec.mode:type_name -> memory.v1.RefreshMode
	7,   // 125: memory.v1.MentalModelSpec.strategy:type_name -> memory.v1.RefreshStrategy
	4,   // 126: memory.v1.MentalModelSpec.budget:type_name -> memory.v1.Budget
	194, // 127: memory.v1.MentalModelSpec.overrides:type_name -> memory.v1.RefreshOverrides
	165, // 128: memory.v1.MentalModelScope.tags:type_name -> memory.v1.TagFilter
	3,   // 129: memory.v1.MentalModelScope.fact_types:type_name -> memory.v1.FactType
	6,   // 130: memory.v1.RefreshTrace.effective_mode:type_name -> memory.v1.RefreshMode
	2,   // 131: memory.v1.RefreshTrace.outcome:type_name -> memory.v1.RefreshOutcome
	175, // 132: memory.v1.RefreshTrace.tool_calls:type_name -> memory.v1.ToolCallTrace
	104, // 133: memory.v1.RefreshTrace.delta_operations:type_name -> memory.v1.DeltaOp
	172, // 134: memory.v1.RefreshTrace.usage:type_name -> memory.v1.SlotUsage
	207, // 135: memory.v1.RefreshTrace.recorded_at:type_name -> google.protobuf.Timestamp
	7,   // 136: memory.v1.RefreshTrace.effective_strategy:type_name -> memory.v1.RefreshStrategy
	14,  // 137: memory.v1.DeltaOp.kind:type_name -> memory.v1.DeltaOp.Kind
	177, // 138: memory.v1.CreateBankRequest.config:type_name -> memory.v1.BankConfig
	107, // 139: memory.v1.CreateBankRequest.template:type_name -> memory.v1.BankTemplate
	122, // 140: memory.v1.CreateBankResponse.bank:type_name -> memory.v1.Bank
	161, // 141: memory.v1.CreateBankResponse.mental_model_operations:type_name -> memory.v1.Operation
	177, // 142: memory.v1.BankTemplate.config:type_name -> memory.v1.BankConfig
	149, // 143: memory.v1.BankTemplate.directives:type_name -> memory.v1.Directive
	100, // 144: memory.v1.BankTemplate.mental_models:type_name -> memory.v1.MentalModelSpec
	122, // 145: memory.v1.GetBankResponse.bank:type_name -> memory.v1.Bank
	122, // 146: memory.v1.ListBanksResponse.banks:type_name -> memory.v1.Bank
	161, // 147: memory.v1.DeleteBankResponse.operation:type_name -> memory.v1.Operation
	116, // 148: memory.v1.GetBankStatsResponse.stats:type_name -> memory.v1.BankStats
	201, // 149: memory.v1.BankStats.memories_by_fact_type:type_name -> memory.v1.BankStats.MemoriesByFactTypeEntry
	202, // 150: memory.v1.BankStats.pending_operations_by_kind:type_name -> memory.v1.BankStats.PendingOperationsByKindEntry
	207, // 151: memory.v1.BankStats.last_memory_write_at:type_name -> google.protobuf.Timestamp
	15,  // 152: memory.v1.ListTagsRequest.source:type_name -> memory.v1.ListTagsRequest.TagSource
	119, // 153: memory.v1.ListTagsResponse.tags:type_name -> memory.v1.TagCount
	107, // 154: memory.v1.ExportBankTemplateResponse.template:type_name -> memory.v1.BankTemplate
	207, // 155: memory.v1.Bank.created_at:type_name -> google.protobuf.Timestamp
	148, // 156: memory.v1.GetTenantConfigResponse.config:type_name -> memory.v1.ConfigView
	177, // 157: memory.v1.UpdateTenantConfigRequest.config:type_name -> memory.v1.BankConfig
	208, // 158: memory.v1.UpdateTenantConfigRequest.update_mask:type_name -> google.protobuf.FieldMask
	148, // 159: memory.v1.UpdateTenantConfigResponse.config:type_name -> memory.v1.ConfigView
	208, // 160: memory.v1.ResetTenantConfigRequest.reset_mask:type_name -> google.protobuf.FieldMask
	148, // 161: memory.v1.ResetTenantConfigResponse.config:type_name -> memory.v1.ConfigView
	148, // 162: memory.v1.GetBankConfigResponse.config:type_name -> memory.v1.ConfigView
	177, // 163: memory.v1.UpdateBankConfigRequest.config:type_name -> memory.v1.BankConfig
	208, // 164: memory.v1.UpdateBankConfigRequest.update_mask:type_name -> google.protobuf.FieldMask
	148, // 165: memory.v1.UpdateBankConfigResponse.config:type_name -> memory.v1.ConfigView
	208, // 166: memory.v1.ResetBankConfigRequest.reset_mask:type_name -> google.protobuf.FieldMask
	148, // 167: memory.v1.ResetBankConfigResponse.config:type_name -> memory.v1.ConfigView
	16,  // 168: memory.v1.PreviewPromptRequest.operation:type_name -> memory.v1.PreviewPromptRequest.PromptOperation
	189, // 169: memory.v1.PreviewPromptRequest.retain:type_name -> memory.v1.RetainOverrides
	192, // 170: memory.v1.PreviewPromptRequest.consolidation:type_name -> memory.v1.ConsolidateOverrides
	191, // 171: memory.v1.PreviewPromptRequest.reflect:type_name -> memory.v1.ReflectOverrides
	194, // 172: memory.v1.PreviewPromptRequest.mental_model:type_name -> memory.v1.RefreshOverrides
	137, // 173: memory.v1.PreviewPromptResponse.preview:type_name -> memory.v1.PromptPreview
	138, // 174: memory.v1.PromptPreview.messages:type_name -> memory.v1.PromptMessage
	17,  // 175: memory.v1.PromptMessage.role:type_name -> memory.v1.PromptMessage.PromptRole
	139, // 176: memory.v1.PromptMessage.blocks:type_name -> memory.v1.PromptBlock
	18,  // 177: memory.v1.PromptBlock.source:type_name -> memory.v1.PromptBlock.PromptSource
	149, // 178: memory.v1.CreateDirectiveRequest.directive:type_name -> memory.v1.Directive
	149, // 179: memory.v1.CreateDirectiveResponse.directive:type_name -> memory.v1.Directive
	149, // 180: memory.v1.UpdateDirectiveRequest.directive:type_name -> memory.v1.Directive
	208, // 181: memory.v1.UpdateDirectiveRequest.update_mask:type_name -> google.protobuf.FieldMask
	149, // 182: memory.v1.UpdateDirectiveResponse.directive:type_name -> memory.v1.Directive
	165, // 183: memory.v1.ListDirectivesRequest.tags:type_name -> memory.v1.TagFilter
	149, // 184: memory.v1.ListDirectivesResponse.directives:type_name -> memory.v1.Directive
	177, // 185: memory.v1.ConfigView.own:type_name -> memory.v1.BankConfig
	177, // 186: memory.v1.ConfigView.effective:type_name -> memory.v1.BankConfig
	161, // 187: memory.v1.GetOperationResponse.operation:type_name -> memory.v1.Operation
	9,   // 188: memory.v1.ListOperationsRequest.status:type_name -> memory.v1.OperationStatus
	8,   // 189: memory.v1.ListOperationsRequest.kind:type_name -> memory.v1.OperationKind
	161, // 190: memory.v1.ListOperationsResponse.operations:type_name -> memory.v1.Operation
	161, // 191: memory.v1.CancelOperationResponse.operation:type_name -> memory.v1.Operation
	161, // 192: memory.v1.RetryOperationResponse.operation:type_name -> memory.v1.Operation
	10,  // 193: memory.v1.ListEventsRequest.types:type_name -> memory.v1.EventType
	160, // 194: memory.v1.ListEventsResponse.events:type_name -> memory.v1.LifecycleEvent
	10,  // 195: memory.v1.LifecycleEvent.type:type_name -> memory.v1.EventType
	207, // 196: memory.v1.LifecycleEvent.created_at:type_name -> google.protobuf.Timestamp
	8,   // 197: memory.v1.Operation.kind:type_name -> memory.v1.OperationKind
	9,   // 198: memory.v1.Operation.status:type_name -> memory.v1.OperationStatus
	172, // 199: memory.v1.Operation.usage:type_name -> memory.v1.SlotUsage
	177, // 200: memory.v1.Operation.effective_overrides:type_name -> memory.v1.BankConfig
	162, // 201: memory.v1.Operation.progress:type_name -> memory.v1.Progress
	207, // 202: memory.v1.Operation.created_at:type_name -> google.protobuf.Timestamp
	207, // 203: memory.v1.Operation.updated_at:type_name -> google.protobuf.Timestamp
	207, // 204: memory.v1.Operation.next_run_at:type_name -> google.protobuf.Timestamp
	207, // 205: memory.v1.Operation.heartbeat_at:type_name -> google.protobuf.Timestamp
	207, // 206: memory.v1.Progress.recorded_at:type_name -> google.protobuf.Timestamp
	19,  // 207: memory.v1.ObservationScopeModes.mode:type_name -> memory.v1.ObservationScopeModes.Mode
	163, // 208: memory.v1.ObservationScopeModes.custom:type_name -> memory.v1.TagSet
	5,   // 209: memory.v1.TagFilter.match:type_name -> memory.v1.TagsMatch
	166, // 210: memory.v1.TagFilter.groups:type_name -> memory.v1.TagGroup
	168, // 211: memory.v1.TagGroup.leaf:type_name -> memory.v1.TagLeaf
	167, // 212: memory.v1.TagGroup.all_of:type_name -> memory.v1.TagGroupList
	167, // 213: memory.v1.TagGroup.any_of:type_name -> memory.v1.TagGroupList
	166, // 214: memory.v1.TagGroup.none_of:type_name -> memory.v1.TagGroup
	166, // 215: memory.v1.TagGroupList.groups:type_name -> memory.v1.TagGroup
	5,   // 216: memory.v1.TagLeaf.match:type_name -> memory.v1.TagsMatch
	20,  // 217: memory.v1.TagLeaf.resolve:type_name -> memory.v1.TagLeaf.Resolve
	203, // 218: memory.v1.Scores.arms:type_name -> memory.v1.Scores.ArmsEntry
	204, // 219: memory.v1.MinScores.arms:type_name -> memory.v1.MinScores.ArmsEntry
	21,  // 220: memory.v1.SlotUsage.slot:type_name -> memory.v1.SlotUsage.ModelSlot
	174, // 221: memory.v1.RecallTrace.arms:type_name -> memory.v1.ArmTrace
	207, // 222: memory.v1.RecallTrace.time_window_start:type_name -> google.protobuf.Timestamp
	207, // 223: memory.v1.RecallTrace.time_window_end:type_name -> google.protobuf.Timestamp
	175, // 224: memory.v1.ReflectTrace.tool_calls:type_name -> memory.v1.ToolCallTrace
	178, // 225: memory.v1.BankConfig.retain:type_name -> memory.v1.RetainConfig
	179, // 226: memory.v1.BankConfig.observations:type_name -> memory.v1.ObservationConfig
	184, // 227: memory.v1.BankConfig.recall:type_name -> memory.v1.RecallConfig
	185, // 228: memory.v1.BankConfig.reflect:type_name -> memory.v1.ReflectConfig
	186, // 229: memory.v1.BankConfig.mental_models:type_name -> memory.v1.MentalModelConfig
	187, // 230: memory.v1.BankConfig.models:type_name -> memory.v1.ModelBindings
	22,  // 231: memory.v1.RetainConfig.extraction_mode:type_name -> memory.v1.RetainConfig.ExtractionMode
	181, // 232: memory.v1.RetainConfig.entity_labels:type_name -> memory.v1.EntityLabel
	164, // 233: memory.v1.RetainConfig.default_observation_scope_modes:type_name -> memory.v1.ObservationScopeModes
	183, // 234: memory.v1.ObservationConfig.strategies:type_name -> memory.v1.ConsolidationStrategy
	23,  // 235: memory.v1.EntityLabel.type:type_name -> memory.v1.EntityLabel.Type
	182, // 236: memory.v1.EntityLabel.values:type_name -> memory.v1.LabelValue
	205, // 237: memory.v1.EntityLabel.fields:type_name -> memory.v1.EntityLabel.FieldsEntry
	165, // 238: memory.v1.ConsolidationStrategy.scopes:type_name -> memory.v1.TagFilter
	188, // 239: memory.v1.ConsolidationStrategy.consolidate_model:type_name -> memory.v1.ModelBinding
	188, // 240: memory.v1.ConsolidationStrategy.dedup_check_model:type_name -> memory.v1.ModelBinding
	24,  // 241: memory.v1.RecallConfig.budget_function:type_name -> memory.v1.RecallConfig.BudgetFunction
	206, // 242: memory.v1.RecallConfig.arm_boosts:type_name -> memory.v1.RecallConfig.ArmBoostsEntry
	4,   // 243: memory.v1.ReflectConfig.recall_budget:type_name -> memory.v1.Budget
	180, // 244: memory.v1.ReflectConfig.disposition:type_name -> memory.v1.Disposition
	6,   // 245: memory.v1.MentalModelConfig.default_mode:type_name -> memory.v1.RefreshMode
	7,   // 246: memory.v1.MentalModelConfig.default_strategy:type_name -> memory.v1.RefreshStrategy
	188, // 247: memory.v1.ModelBindings.default:type_name -> memory.v1.ModelBinding
	188, // 248: memory.v1.ModelBindings.extract:type_name -> memory.v1.ModelBinding
	188, // 249: memory.v1.ModelBindings.time_parse:type_name -> memory.v1.ModelBinding
	188, // 250: memory.v1.ModelBindings.consolidate:type_name -> memory.v1.ModelBinding
	188, // 251: memory.v1.ModelBindings.dedup_check:type_name -> memory.v1.ModelBinding
	188, // 252: memory.v1.ModelBindings.reflect:type_name -> memory.v1.ModelBinding
	188, // 253: memory.v1.ModelBindings.structured_output:type_name -> memory.v1.ModelBinding
	188, // 254: memory.v1.ModelBindings.mental_model:type_name -> memory.v1.ModelBinding
	188, // 255: memory.v1.ModelBindings.rerank:type_name -> memory.v1.ModelBinding
	188, // 256: memory.v1.ModelBindings.embed:type_name -> memory.v1.ModelBinding
	209, // 257: memory.v1.ModelBinding.extra:type_name -> google.protobuf.Struct
	178, // 258: memory.v1.RetainOverrides.retain:type_name -> memory.v1.RetainConfig
	188, // 259: memory.v1.RetainOverrides.extract_model:type_name -> memory.v1.ModelBinding
	188, // 260: memory.v1.RetainOverrides.time_parse_model:type_name -> memory.v1.ModelBinding
	184, // 261: memory.v1.RecallOverrides.recall:type_name -> memory.v1.RecallConfig
	188, // 262: memory.v1.RecallOverrides.time_parse_model:type_name -> memory.v1.ModelBinding
	188, // 263: memory.v1.RecallOverrides.rerank_model:type_name -> memory.v1.ModelBinding
	185, // 264: memory.v1.ReflectOverrides.reflect:type_name -> memory.v1.ReflectConfig
	184, // 265: memory.v1.ReflectOverrides.recall:type_name -> memory.v1.RecallConfig
	188, // 266: memory.v1.ReflectOverrides.reflect_model:type_name -> memory.v1.ModelBinding
	188, // 267: memory.v1.ReflectOverrides.structured_output_model:type_name -> memory.v1.ModelBinding
	188, // 268: memory.v1.ReflectOverrides.rerank_model:type_name -> memory.v1.ModelBinding
	183, // 269: memory.v1.ConsolidateOverrides.strategies:type_name -> memory.v1.ConsolidationStrategy
	188, // 270: memory.v1.ConsolidateOverrides.consolidate_model:type_name -> memory.v1.ModelBinding
	188, // 271: memory.v1.ConsolidateOverrides.dedup_check_model:type_name -> memory.v1.ModelBinding
	188, // 272: memory.v1.RederiveOverrides.extract_model:type_name -> memory.v1.ModelBinding
	188, // 273: memory.v1.RederiveOverrides.time_parse_model:type_name -> memory.v1.ModelBinding
	185, // 274: memory.v1.RefreshOverrides.reflect:type_name -> memory.v1.ReflectConfig
	188, // 275: memory.v1.RefreshOverrides.reflect_model:type_name -> memory.v1.ModelBinding
	188, // 276: memory.v1.RefreshOverrides.mental_model_model:type_name -> memory.v1.ModelBinding
	188, // 277: memory.v1.RefreshOverrides.structured_output_model:type_name -> memory.v1.ModelBinding
	52,  // 278: memory.v1.RecallResponse.SourceFactsEntry.value:type_name -> memory.v1.Memory
	171, // 279: memory.v1.RecallResponse.ChunksEntry.value:type_name -> memory.v1.Chunk
	33,  // 280: memory.v1.RecallResponse.EntitiesEntry.value:type_name -> memory.v1.EntityState
	181, // 281: memory.v1.EntityLabel.FieldsEntry.value:type_name -> memory.v1.EntityLabel
	25,  // 282: memory.v1.MemoryService.Retain:input_type -> memory.v1.RetainRequest
	30,  // 283: memory.v1.MemoryService.Recall:input_type -> memory.v1.RecallRequest
	34,  // 284: memory.v1.MemoryService.Reflect:input_type -> memory.v1.ReflectRequest
	38,  // 285: memory.v1.MemoryService.ListRecallArms:input_type -> memory.v1.ListRecallArmsRequest
	41,  // 286: memory.v1.MemoryService.ListMemories:input_type -> memory.v1.ListMemoriesRequest
	43,  // 287: memory.v1.MemoryService.GetMemory:input_type -> memory.v1.GetMemoryRequest
	45,  // 288: memory.v1.MemoryService.UpdateMemory:input_type -> memory.v1.UpdateMemoryRequest
	48,  // 289: memory.v1.MemoryService.InvalidateMemory:input_type -> memory.v1.InvalidateMemoryRequest
	50,  // 290: memory.v1.MemoryService.RestoreMemory:input_type -> memory.v1.RestoreMemoryRequest
	53,  // 291: memory.v1.DocumentService.ListDocuments:input_type -> memory.v1.ListDocumentsRequest
	55,  // 292: memory.v1.DocumentService.GetDocument:input_type -> memory.v1.GetDocumentRequest
	57,  // 293: memory.v1.DocumentService.RetagDocument:input_type -> memory.v1.RetagDocumentRequest
	59,  // 294: memory.v1.DocumentService.ReprocessDocument:input_type -> memory.v1.ReprocessDocumentRequest
	62,  // 295: memory.v1.ObservationService.Consolidate:input_type -> memory.v1.ConsolidateRequest
	64,  // 296: memory.v1.ObservationService.ListObservationScopes:input_type -> memory.v1.ListObservationScopesRequest
	67,  // 297: memory.v1.ObservationService.ListObservationHistory:input_type -> memory.v1.ListObservationHistoryRequest
	70,  // 298: memory.v1.ObservationService.ClearObservations:input_type -> memory.v1.ClearObservationsRequest
	73,  // 299: memory.v1.ObservationService.PreviewConsolidationStrategies:input_type -> memory.v1.PreviewConsolidationStrategiesRequest
	76,  // 300: memory.v1.MentalModelService.CreateMentalModel:input_type -> memory.v1.CreateMentalModelRequest
	78,  // 301: memory.v1.MentalModelService.GetMentalModel:input_type -> memory.v1.GetMentalModelRequest
	80,  // 302: memory.v1.MentalModelService.ListMentalModels:input_type -> memory.v1.ListMentalModelsRequest
	82,  // 303: memory.v1.MentalModelService.UpdateMentalModel:input_type -> memory.v1.UpdateMentalModelRequest
	84,  // 304: memory.v1.MentalModelService.RefreshMentalModel:input_type -> memory.v1.RefreshMentalModelRequest
	86,  // 305: memory.v1.MentalModelService.PreviewMentalModelRefresh:input_type -> memory.v1.PreviewMentalModelRefreshRequest
	89,  // 306: memory.v1.MentalModelService.ListMentalModelHistory:input_type -> memory.v1.ListMentalModelHistoryRequest
	92,  // 307: memory.v1.MentalModelService.DeleteMentalModel:input_type -> memory.v1.DeleteMentalModelRequest
	94,  // 308: memory.v1.MentalModelService.DeleteMentalModelFolder:input_type -> memory.v1.DeleteMentalModelFolderRequest
	96,  // 309: memory.v1.MentalModelService.SearchMentalModels:input_type -> memory.v1.SearchMentalModelsRequest
	105, // 310: memory.v1.BankService.CreateBank:input_type -> memory.v1.CreateBankRequest
	108, // 311: memory.v1.BankService.GetBank:input_type -> memory.v1.GetBankRequest
	110, // 312: memory.v1.BankService.ListBanks:input_type -> memory.v1.ListBanksRequest
	112, // 313: memory.v1.BankService.DeleteBank:input_type -> memory.v1.DeleteBankRequest
	114, // 314: memory.v1.BankService.GetBankStats:input_type -> memory.v1.GetBankStatsRequest
	117, // 315: memory.v1.BankService.ListTags:input_type -> memory.v1.ListTagsRequest
	120, // 316: memory.v1.BankService.ExportBankTemplate:input_type -> memory.v1.ExportBankTemplateRequest
	123, // 317: memory.v1.ConfigService.GetTenantConfig:input_type -> memory.v1.GetTenantConfigRequest
	125, // 318: memory.v1.ConfigService.UpdateTenantConfig:input_type -> memory.v1.UpdateTenantConfigRequest
	127, // 319: memory.v1.ConfigService.ResetTenantConfig:input_type -> memory.v1.ResetTenantConfigRequest
	129, // 320: memory.v1.ConfigService.GetBankConfig:input_type -> memory.v1.GetBankConfigRequest
	131, // 321: memory.v1.ConfigService.UpdateBankConfig:input_type -> memory.v1.UpdateBankConfigRequest
	133, // 322: memory.v1.ConfigService.ResetBankConfig:input_type -> memory.v1.ResetBankConfigRequest
	135, // 323: memory.v1.ConfigService.PreviewPrompt:input_type -> memory.v1.PreviewPromptRequest
	140, // 324: memory.v1.ConfigService.CreateDirective:input_type -> memory.v1.CreateDirectiveRequest
	142, // 325: memory.v1.ConfigService.UpdateDirective:input_type -> memory.v1.UpdateDirectiveRequest
	144, // 326: memory.v1.ConfigService.ListDirectives:input_type -> memory.v1.ListDirectivesRequest
	146, // 327: memory.v1.ConfigService.DeleteDirective:input_type -> memory.v1.DeleteDirectiveRequest
	150, // 328: memory.v1.OperationService.GetOperation:input_type -> memory.v1.GetOperationRequest
	152, // 329: memory.v1.OperationService.ListOperations:input_type -> memory.v1.ListOperationsRequest
	154, // 330: memory.v1.OperationService.CancelOperation:input_type -> memory.v1.CancelOperationRequest
	156, // 331: memory.v1.OperationService.RetryOperation:input_type -> memory.v1.RetryOperationRequest
	158, // 332: memory.v1.OperationService.ListEvents:input_type -> memory.v1.ListEventsRequest
	26,  // 333: memory.v1.MemoryService.Retain:output_type -> memory.v1.RetainResponse
	31,  // 334: memory.v1.MemoryService.Recall:output_type -> memory.v1.RecallResponse
	35,  // 335: memory.v1.MemoryService.Reflect:output_type -> memory.v1.ReflectResponse
	39,  // 336: memory.v1.MemoryService.ListRecallArms:output_type -> memory.v1.ListRecallArmsResponse
	42,  // 337: memory.v1.MemoryService.ListMemories:output_type -> memory.v1.ListMemoriesResponse
	44,  // 338: memory.v1.MemoryService.GetMemory:output_type -> memory.v1.GetMemoryResponse
	46,  // 339: memory.v1.MemoryService.UpdateMemory:output_type -> memory.v1.UpdateMemoryResponse
	49,  // 340: memory.v1.MemoryService.InvalidateMemory:output_type -> memory.v1.InvalidateMemoryResponse
	51,  // 341: memory.v1.MemoryService.RestoreMemory:output_type -> memory.v1.RestoreMemoryResponse
	54,  // 342: memory.v1.DocumentService.ListDocuments:output_type -> memory.v1.ListDocumentsResponse
	56,  // 343: memory.v1.DocumentService.GetDocument:output_type -> memory.v1.GetDocumentResponse
	58,  // 344: memory.v1.DocumentService.RetagDocument:output_type -> memory.v1.RetagDocumentResponse
	60,  // 345: memory.v1.DocumentService.ReprocessDocument:output_type -> memory.v1.ReprocessDocumentResponse
	63,  // 346: memory.v1.ObservationService.Consolidate:output_type -> memory.v1.ConsolidateResponse
	65,  // 347: memory.v1.ObservationService.ListObservationScopes:output_type -> memory.v1.ListObservationScopesResponse
	68,  // 348: memory.v1.ObservationService.ListObservationHistory:output_type -> memory.v1.ListObservationHistoryResponse
	72,  // 349: memory.v1.ObservationService.ClearObservations:output_type -> memory.v1.ClearObservationsResponse
	74,  // 350: memory.v1.ObservationService.PreviewConsolidationStrategies:output_type -> memory.v1.PreviewConsolidationStrategiesResponse
	77,  // 351: memory.v1.MentalModelService.CreateMentalModel:output_type -> memory.v1.CreateMentalModelResponse
	79,  // 352: memory.v1.MentalModelService.GetMentalModel:output_type -> memory.v1.GetMentalModelResponse
	81,  // 353: memory.v1.MentalModelService.ListMentalModels:output_type -> memory.v1.ListMentalModelsResponse
	83,  // 354: memory.v1.MentalModelService.UpdateMentalModel:output_type -> memory.v1.UpdateMentalModelResponse
	85,  // 355: memory.v1.MentalModelService.RefreshMentalModel:output_type -> memory.v1.RefreshMentalModelResponse
	87,  // 356: memory.v1.MentalModelService.PreviewMentalModelRefresh:output_type -> memory.v1.PreviewMentalModelRefreshResponse
	90,  // 357: memory.v1.MentalModelService.ListMentalModelHistory:output_type -> memory.v1.ListMentalModelHistoryResponse
	93,  // 358: memory.v1.MentalModelService.DeleteMentalModel:output_type -> memory.v1.DeleteMentalModelResponse
	95,  // 359: memory.v1.MentalModelService.DeleteMentalModelFolder:output_type -> memory.v1.DeleteMentalModelFolderResponse
	97,  // 360: memory.v1.MentalModelService.SearchMentalModels:output_type -> memory.v1.SearchMentalModelsResponse
	106, // 361: memory.v1.BankService.CreateBank:output_type -> memory.v1.CreateBankResponse
	109, // 362: memory.v1.BankService.GetBank:output_type -> memory.v1.GetBankResponse
	111, // 363: memory.v1.BankService.ListBanks:output_type -> memory.v1.ListBanksResponse
	113, // 364: memory.v1.BankService.DeleteBank:output_type -> memory.v1.DeleteBankResponse
	115, // 365: memory.v1.BankService.GetBankStats:output_type -> memory.v1.GetBankStatsResponse
	118, // 366: memory.v1.BankService.ListTags:output_type -> memory.v1.ListTagsResponse
	121, // 367: memory.v1.BankService.ExportBankTemplate:output_type -> memory.v1.ExportBankTemplateResponse
	124, // 368: memory.v1.ConfigService.GetTenantConfig:output_type -> memory.v1.GetTenantConfigResponse
	126, // 369: memory.v1.ConfigService.UpdateTenantConfig:output_type -> memory.v1.UpdateTenantConfigResponse
	128, // 370: memory.v1.ConfigService.ResetTenantConfig:output_type -> memory.v1.ResetTenantConfigResponse
	130, // 371: memory.v1.ConfigService.GetBankConfig:output_type -> memory.v1.GetBankConfigResponse
	132, // 372: memory.v1.ConfigService.UpdateBankConfig:output_type -> memory.v1.UpdateBankConfigResponse
	134, // 373: memory.v1.ConfigService.ResetBankConfig:output_type -> memory.v1.ResetBankConfigResponse
	136, // 374: memory.v1.ConfigService.PreviewPrompt:output_type -> memory.v1.PreviewPromptResponse
	141, // 375: memory.v1.ConfigService.CreateDirective:output_type -> memory.v1.CreateDirectiveResponse
	143, // 376: memory.v1.ConfigService.UpdateDirective:output_type -> memory.v1.UpdateDirectiveResponse
	145, // 377: memory.v1.ConfigService.ListDirectives:output_type -> memory.v1.ListDirectivesResponse
	147, // 378: memory.v1.ConfigService.DeleteDirective:output_type -> memory.v1.DeleteDirectiveResponse
	151, // 379: memory.v1.OperationService.GetOperation:output_type -> memory.v1.GetOperationResponse
	153, // 380: memory.v1.OperationService.ListOperations:output_type -> memory.v1.ListOperationsResponse
	155, // 381: memory.v1.OperationService.CancelOperation:output_type -> memory.v1.CancelOperationResponse
	157, // 382: memory.v1.OperationService.RetryOperation:output_type -> memory.v1.RetryOperationResponse
	159, // 383: memory.v1.OperationService.ListEvents:output_type -> memory.v1.ListEventsResponse
	333, // [333:384] is the sub-list for method output_type
	282, // [282:333] is the sub-list for method input_type
	282, // [282:282] is the sub-list for extension type_name
	282, // [282:282] is the sub-list for extension extendee
	0,   // [0:282] is the sub-list for field type_name
}

func init() { file_memory_v1_memory_proto_init() }
func file_memory_v1_memory_proto_init() {
	if File_memory_v1_memory_proto != nil {
		return
	}
	file_memory_v1_memory_proto_msgTypes[2].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[7].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[9].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[11].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[20].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[25].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[45].OneofWrappers = []any{
		(*ClearObservationsRequest_Scopes)(nil),
		(*ClearObservationsRequest_MemoryId)(nil),
		(*ClearObservationsRequest_All)(nil),
	}
	file_memory_v1_memory_proto_msgTypes[75].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[76].OneofWrappers = []any{
		(*RefreshSchedule_AfterConsolidation)(nil),
		(*RefreshSchedule_Cron)(nil),
	}
	file_memory_v1_memory_proto_msgTypes[80].OneofWrappers = []any{
		(*CreateBankRequest_Config)(nil),
		(*CreateBankRequest_Template)(nil),
	}
	file_memory_v1_memory_proto_msgTypes[110].OneofWrappers = []any{
		(*PreviewPromptRequest_Retain)(nil),
		(*PreviewPromptRequest_Consolidation)(nil),
		(*PreviewPromptRequest_Reflect)(nil),
		(*PreviewPromptRequest_MentalModel)(nil),
	}
	file_memory_v1_memory_proto_msgTypes[139].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[141].OneofWrappers = []any{
		(*TagGroup_Leaf)(nil),
		(*TagGroup_AllOf)(nil),
		(*TagGroup_AnyOf)(nil),
		(*TagGroup_NoneOf)(nil),
	}
	file_memory_v1_memory_proto_msgTypes[144].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[145].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[153].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[154].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[156].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[158].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[159].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[160].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[161].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[163].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[167].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[168].OneofWrappers = []any{}
	file_memory_v1_memory_proto_msgTypes[169].OneofWrappers = []any{}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: unsafe.Slice(unsafe.StringData(file_memory_v1_memory_proto_rawDesc), len(file_memory_v1_memory_proto_rawDesc)),
			NumEnums:      25,
			NumMessages:   182,
			NumExtensions: 0,
			NumServices:   7,
		},
		GoTypes:           file_memory_v1_memory_proto_goTypes,
		DependencyIndexes: file_memory_v1_memory_proto_depIdxs,
		EnumInfos:         file_memory_v1_memory_proto_enumTypes,
		MessageInfos:      file_memory_v1_memory_proto_msgTypes,
	}.Build()
	File_memory_v1_memory_proto = out.File
	file_memory_v1_memory_proto_goTypes = nil
	file_memory_v1_memory_proto_depIdxs = nil
}

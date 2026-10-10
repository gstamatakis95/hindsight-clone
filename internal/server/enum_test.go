package server

import (
	"errors"
	"fmt"
	"memd/internal/domain"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	pb "memd/gen/memory/v1"
)

// enumCase is one proto enum value and the domain constant it must map to.
type enumCase struct {
	// value names the proto value, for messages.
	value string
	// enum is the enum's full name.
	enum protoreflect.FullName
	// number is the proto value number.
	number protoreflect.EnumNumber
	// check converts both ways and reports a mismatch.
	check func() error
}

// pair builds the case for proto value e and domain constant want.
func pair[E protoEnum, T ~string](e E, want T) enumCase {
	ed := e.Descriptor()
	return enumCase{
		value: string(ed.Values().ByNumber(protoreflect.EnumNumber(e)).Name()), enum: ed.FullName(),
		number: protoreflect.EnumNumber(e),
		check: func() error {
			var d decoder
			if got := fromEnum[T](&d, e); got != want || d.err != nil {
				return fmt.Errorf("fromEnum = %q (%w), want %q", got, d.err, want)
			}
			if got := toEnum[E](want); got != e {
				return fmt.Errorf("toEnum(%q) = %v, want %v", want, got, e)
			}
			return nil
		},
	}
}

// enumCases lists every value of every memory.v1 enum with its domain constant.
func enumCases() []enumCase {
	return []enumCase{
		pair(pb.FactType_FACT_TYPE_UNSPECIFIED, domain.FactTypeUnspecified),
		pair(pb.FactType_FACT_TYPE_WORLD, domain.FactTypeWorld),
		pair(pb.FactType_FACT_TYPE_EXPERIENCE, domain.FactTypeExperience),
		pair(pb.FactType_FACT_TYPE_OBSERVATION, domain.FactTypeObservation),
		pair(pb.Budget_BUDGET_UNSPECIFIED, domain.BudgetUnspecified),
		pair(pb.Budget_BUDGET_LOW, domain.BudgetLow),
		pair(pb.Budget_BUDGET_MID, domain.BudgetMid),
		pair(pb.Budget_BUDGET_HIGH, domain.BudgetHigh),
		pair(pb.TagsMatch_TAGS_MATCH_UNSPECIFIED, domain.TagsMatchUnspecified),
		pair(pb.TagsMatch_TAGS_MATCH_ANY, domain.TagsMatchAny),
		pair(pb.TagsMatch_TAGS_MATCH_ALL, domain.TagsMatchAll),
		pair(pb.TagsMatch_TAGS_MATCH_EXACT, domain.TagsMatchExact),
		pair(pb.TagsMatch_TAGS_MATCH_ANY_STRICT, domain.TagsMatchAnyStrict),
		pair(pb.TagsMatch_TAGS_MATCH_ALL_STRICT, domain.TagsMatchAllStrict),
		pair(pb.RefreshMode_REFRESH_MODE_UNSPECIFIED, domain.RefreshModeUnspecified),
		pair(pb.RefreshMode_REFRESH_MODE_FULL, domain.RefreshModeFull),
		pair(pb.RefreshMode_REFRESH_MODE_DELTA, domain.RefreshModeDelta),
		pair(pb.RefreshStrategy_REFRESH_STRATEGY_UNSPECIFIED, domain.RefreshStrategyUnspecified),
		pair(pb.RefreshStrategy_REFRESH_STRATEGY_AGENTIC, domain.RefreshStrategyAgentic),
		pair(pb.RefreshStrategy_REFRESH_STRATEGY_SINGLE_SHOT, domain.RefreshStrategySingleShot),
		pair(pb.MemoryState_MEMORY_STATE_UNSPECIFIED, domain.MemoryStateUnspecified),
		pair(pb.MemoryState_MEMORY_STATE_VALID, domain.MemoryStateValid),
		pair(pb.MemoryState_MEMORY_STATE_INVALIDATED, domain.MemoryStateInvalidated),
		pair(pb.ObservationScopes_MODE_UNSPECIFIED, domain.ScopeModeUnspecified),
		pair(pb.ObservationScopes_MODE_COMBINED, domain.ScopeModeCombined),
		pair(pb.ObservationScopes_MODE_SHARED, domain.ScopeModeShared),
		pair(pb.ObservationScopes_MODE_PER_TAG, domain.ScopeModePerTag),
		pair(pb.ObservationScopes_MODE_ALL_COMBINATIONS, domain.ScopeModeAllCombinations),
		pair(pb.ObservationScopes_MODE_CUSTOM, domain.ScopeModeCustom),
		pair(pb.TagLeaf_RESOLVE_UNSPECIFIED, domain.TagResolveUnspecified),
		pair(pb.TagLeaf_RESOLVE_EXACT_NAMES, domain.TagResolveExactNames),
		pair(pb.TagLeaf_RESOLVE_FUZZY, domain.TagResolveFuzzy),
		pair(pb.ModelSlot_MODEL_SLOT_UNSPECIFIED, domain.ModelSlotUnspecified),
		pair(pb.ModelSlot_MODEL_SLOT_EXTRACT, domain.ModelSlotExtract),
		pair(pb.ModelSlot_MODEL_SLOT_TIME_PARSE, domain.ModelSlotTimeParse),
		pair(pb.ModelSlot_MODEL_SLOT_CONSOLIDATE, domain.ModelSlotConsolidate),
		pair(pb.ModelSlot_MODEL_SLOT_DEDUP_CHECK, domain.ModelSlotDedupCheck),
		pair(pb.ModelSlot_MODEL_SLOT_REFLECT, domain.ModelSlotReflect),
		pair(pb.ModelSlot_MODEL_SLOT_STRUCTURED_OUTPUT, domain.ModelSlotStructuredOutput),
		pair(pb.ModelSlot_MODEL_SLOT_MENTAL_MODEL_DELTA, domain.ModelSlotMentalModelDelta),
		pair(pb.ModelSlot_MODEL_SLOT_RERANK, domain.ModelSlotRerank),
		pair(pb.ModelSlot_MODEL_SLOT_EMBED, domain.ModelSlotEmbed),
		pair(pb.ModelSlot_MODEL_SLOT_SYNTHESIZE, domain.ModelSlotSynthesize),
		pair(pb.EventType_EVENT_TYPE_UNSPECIFIED, domain.EventTypeUnspecified),
		pair(pb.EventType_EVENT_TYPE_RETAIN_COMPLETED, domain.EventTypeRetainCompleted),
		pair(pb.EventType_EVENT_TYPE_CONSOLIDATION_COMPLETED, domain.EventTypeConsolidationCompleted),
		pair(pb.EventType_EVENT_TYPE_MENTAL_MODEL_REFRESHED, domain.EventTypeMentalModelRefreshed),
		pair(pb.EventType_EVENT_TYPE_MENTAL_MODEL_REFRESH_FAILED, domain.EventTypeMentalModelRefreshFailed),
		pair(pb.EventType_EVENT_TYPE_MEMORY_CURATED, domain.EventTypeMemoryCurated),
		pair(pb.EventType_EVENT_TYPE_OPERATION_FAILED, domain.EventTypeOperationFailed),
		pair(pb.ExtractionMode_EXTRACTION_MODE_UNSPECIFIED, domain.ExtractionModeUnspecified),
		pair(pb.ExtractionMode_EXTRACTION_MODE_CONCISE, domain.ExtractionModeConcise),
		pair(pb.ExtractionMode_EXTRACTION_MODE_VERBOSE, domain.ExtractionModeVerbose),
		pair(pb.ExtractionMode_EXTRACTION_MODE_VERBATIM, domain.ExtractionModeVerbatim),
		pair(pb.ExtractionMode_EXTRACTION_MODE_CHUNKS, domain.ExtractionModeChunks),
		pair(pb.ExtractionMode_EXTRACTION_MODE_CUSTOM, domain.ExtractionModeCustom),
		pair(pb.BudgetFunction_BUDGET_FUNCTION_UNSPECIFIED, domain.BudgetFunctionUnspecified),
		pair(pb.BudgetFunction_BUDGET_FUNCTION_FIXED, domain.BudgetFunctionFixed),
		pair(pb.BudgetFunction_BUDGET_FUNCTION_ADAPTIVE, domain.BudgetFunctionAdaptive),
		pair(pb.EntityLabel_TYPE_UNSPECIFIED, domain.LabelTypeUnspecified),
		pair(pb.EntityLabel_TYPE_VALUE, domain.LabelTypeValue),
		pair(pb.EntityLabel_TYPE_MULTI_VALUES, domain.LabelTypeMultiValues),
		pair(pb.EntityLabel_TYPE_TEXT, domain.LabelTypeText),
		pair(pb.EntityLabel_TYPE_MULTI_TEXT, domain.LabelTypeMultiText),
		pair(pb.EntityLabel_TYPE_MAP, domain.LabelTypeMap),
		pair(pb.TimeField_TIME_FIELD_UNSPECIFIED, domain.TimeFieldUnspecified),
		pair(pb.TimeField_TIME_FIELD_CREATED_AT, domain.TimeFieldCreatedAt),
		pair(pb.TimeField_TIME_FIELD_UPDATED_AT, domain.TimeFieldUpdatedAt),
		pair(pb.TimeField_TIME_FIELD_MENTIONED_AT, domain.TimeFieldMentionedAt),
		pair(pb.TimeField_TIME_FIELD_OCCURRED_START, domain.TimeFieldOccurredStart),
		pair(pb.TimeField_TIME_FIELD_OCCURRED_END, domain.TimeFieldOccurredEnd),
		pair(pb.OperationStatus_OPERATION_STATUS_UNSPECIFIED, domain.OperationStatusUnspecified),
		pair(pb.OperationStatus_OPERATION_STATUS_PENDING, domain.OperationStatusPending),
		pair(pb.OperationStatus_OPERATION_STATUS_PROCESSING, domain.OperationStatusProcessing),
		pair(pb.OperationStatus_OPERATION_STATUS_COMPLETED, domain.OperationStatusCompleted),
		pair(pb.OperationStatus_OPERATION_STATUS_FAILED, domain.OperationStatusFailed),
		pair(pb.OperationStatus_OPERATION_STATUS_CANCELLED, domain.OperationStatusCancelled),
		pair(pb.OperationKind_OPERATION_KIND_UNSPECIFIED, domain.OperationKindUnspecified),
		pair(pb.OperationKind_OPERATION_KIND_RETAIN_BATCH, domain.OperationKindRetainBatch),
		pair(pb.OperationKind_OPERATION_KIND_RETAIN_DOCUMENT, domain.OperationKindRetainDocument),
		pair(pb.OperationKind_OPERATION_KIND_CONSOLIDATE, domain.OperationKindConsolidate),
		pair(pb.OperationKind_OPERATION_KIND_REDERIVE_MEMORY, domain.OperationKindRederiveMemory),
		pair(pb.OperationKind_OPERATION_KIND_RETAG_DOCUMENT, domain.OperationKindRetagDocument),
		pair(pb.OperationKind_OPERATION_KIND_REFRESH_MENTAL_MODEL, domain.OperationKindRefreshMentalModel),
		pair(pb.OperationKind_OPERATION_KIND_GRAPH_MAINTENANCE, domain.OperationKindGraphMaintenance),
		pair(pb.OperationKind_OPERATION_KIND_DELETE_BANK, domain.OperationKindDeleteBank),
		pair(pb.DocTimeField_DOC_TIME_FIELD_UNSPECIFIED, domain.DocTimeFieldUnspecified),
		pair(pb.DocTimeField_DOC_TIME_FIELD_CREATED_AT, domain.DocTimeFieldCreatedAt),
		pair(pb.DocTimeField_DOC_TIME_FIELD_UPDATED_AT, domain.DocTimeFieldUpdatedAt),
		pair(pb.Detail_DETAIL_UNSPECIFIED, domain.DetailUnspecified),
		pair(pb.Detail_DETAIL_METADATA, domain.DetailMetadata),
		pair(pb.Detail_DETAIL_CONTENT, domain.DetailContent),
		pair(pb.Detail_DETAIL_FULL, domain.DetailFull),
		pair(pb.DeltaOp_KIND_UNSPECIFIED, domain.DeltaOpKind("")),
		pair(pb.DeltaOp_KIND_ADD_SECTION, domain.DeltaOpAddSection),
		pair(pb.DeltaOp_KIND_REMOVE_SECTION, domain.DeltaOpRemoveSection),
		pair(pb.DeltaOp_KIND_RENAME_SECTION, domain.DeltaOpRenameSection),
		pair(pb.DeltaOp_KIND_REPLACE_SECTION_BLOCKS, domain.DeltaOpReplaceSectionBlocks),
		pair(pb.DeltaOp_KIND_APPEND_BLOCK, domain.DeltaOpAppendBlock),
		pair(pb.DeltaOp_KIND_INSERT_BLOCK, domain.DeltaOpInsertBlock),
		pair(pb.DeltaOp_KIND_REPLACE_BLOCK, domain.DeltaOpReplaceBlock),
		pair(pb.DeltaOp_KIND_REMOVE_BLOCK, domain.DeltaOpRemoveBlock),
		pair(pb.RefreshOutcome_REFRESH_OUTCOME_UNSPECIFIED, domain.RefreshOutcomeUnspecified),
		pair(pb.RefreshOutcome_REFRESH_OUTCOME_CONTENT_WRITTEN, domain.RefreshOutcomeContentWritten),
		pair(pb.RefreshOutcome_REFRESH_OUTCOME_NO_NEW_FACTS, domain.RefreshOutcomeNoNewFacts),
		pair(pb.RefreshOutcome_REFRESH_OUTCOME_FAILED, domain.RefreshOutcomeFailed),
		pair(pb.HistoryKind_HISTORY_KIND_UNSPECIFIED, domain.HistoryKindUnspecified),
		pair(pb.HistoryKind_HISTORY_KIND_VERSION, domain.HistoryKindVersion),
		pair(pb.HistoryKind_HISTORY_KIND_REFRESH_FAILED, domain.HistoryKindRefreshFailed),
		pair(pb.TagSource_TAG_SOURCE_UNSPECIFIED, domain.TagSourceUnspecified),
		pair(pb.TagSource_TAG_SOURCE_MEMORIES, domain.TagSourceMemories),
		pair(pb.TagSource_TAG_SOURCE_MENTAL_MODELS, domain.TagSourceMentalModels),
		pair(pb.TagSource_TAG_SOURCE_DOCUMENTS, domain.TagSourceDocuments),
		pair(pb.PromptOperation_PROMPT_OPERATION_UNSPECIFIED, domain.PromptOperationUnspecified),
		pair(pb.PromptOperation_PROMPT_OPERATION_RETAIN, domain.PromptOperationRetain),
		pair(pb.PromptOperation_PROMPT_OPERATION_CONSOLIDATION, domain.PromptOperationConsolidation),
		pair(pb.PromptOperation_PROMPT_OPERATION_REFLECT, domain.PromptOperationReflect),
		pair(pb.PromptOperation_PROMPT_OPERATION_MENTAL_MODEL_DELTA, domain.PromptOperationMentalModelDelta),
		pair(pb.PromptOperation_PROMPT_OPERATION_SYNTHESIZE, domain.PromptOperationSynthesize),
		pair(pb.PromptRole_PROMPT_ROLE_UNSPECIFIED, domain.PromptRoleUnspecified),
		pair(pb.PromptRole_PROMPT_ROLE_SYSTEM, domain.PromptRoleSystem),
		pair(pb.PromptRole_PROMPT_ROLE_USER, domain.PromptRoleUser),
		pair(pb.PromptSource_PROMPT_SOURCE_UNSPECIFIED, domain.PromptSourceUnspecified),
		pair(pb.PromptSource_PROMPT_SOURCE_BUILTIN, domain.PromptSourceBuiltin),
		pair(pb.PromptSource_PROMPT_SOURCE_CONFIG, domain.PromptSourceConfig),
	}
}

// TestEnumMapping checks every proto enum value maps to exactly its domain constant, both ways, and that
// the table covers every value of every memory.v1 enum.
func TestEnumMapping(t *testing.T) {
	covered := map[protoreflect.FullName]map[protoreflect.EnumNumber]bool{}
	for _, c := range enumCases() {
		if err := c.check(); err != nil {
			t.Errorf("%s: %v", c.value, err)
		}
		if covered[c.enum] == nil {
			covered[c.enum] = map[protoreflect.EnumNumber]bool{}
		}
		covered[c.enum][c.number] = true
	}
	var walk func(protoreflect.EnumDescriptors, protoreflect.MessageDescriptors)
	walk = func(es protoreflect.EnumDescriptors, ms protoreflect.MessageDescriptors) {
		for i := range es.Len() {
			ed := es.Get(i)
			for j := range ed.Values().Len() {
				if v := ed.Values().Get(j); !covered[ed.FullName()][v.Number()] {
					t.Errorf("enum value %s is not in the mapping table", v.FullName())
				}
			}
		}
		for i := range ms.Len() {
			walk(ms.Get(i).Enums(), ms.Get(i).Messages())
		}
	}
	protoregistry.GlobalFiles.RangeFilesByPackage("memory.v1", func(fd protoreflect.FileDescriptor) bool {
		walk(fd.Enums(), fd.Messages())
		return true
	})
}

// TestUnknownEnumRejected checks an undefined enum number is INVALID_ARGUMENT.
func TestUnknownEnumRejected(t *testing.T) {
	_, err := configFromProto(&pb.BankConfig{ExtractionMode: pb.ExtractionMode(99)})
	if err == nil || !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
	if got := toEnum[pb.FactType](domain.FactType("bogus")); got != pb.FactType_FACT_TYPE_UNSPECIFIED {
		t.Fatalf("unknown domain value = %v, want UNSPECIFIED", got)
	}
}

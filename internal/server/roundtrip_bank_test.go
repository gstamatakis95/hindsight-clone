package server

import (
	"errors"
	"memd/internal/domain"
	"testing"

	pb "memd/gen/memory/v1"
)

// Round trips for bank.proto, tenant.proto, document.proto, observation.proto, operation.proto and
// events.proto.
func init() {
	bankRoundTrips()
	documentRoundTrips()
	observationRoundTrips()
	operationRoundTrips()
	bankExemptions()
	otherExemptions()
}

// emptyConfig sets a nil value-config field to the empty message the converters emit.
func emptyConfig(c **pb.BankConfig) {
	if *c == nil {
		*c = &pb.BankConfig{}
	}
}

// bankRoundTrips registers the bank.proto round trips.
func bankRoundTrips() {
	// Bank.config is a value in domain.Bank, so nil comes back empty.
	addRT(func(m *pb.Bank) (*pb.Bank, error) {
		var d decoder
		return bankToProto(d.bank(m)), d.err
	}, func(m *pb.Bank) { emptyConfig(&m.Config) })
	addRT(func(m *pb.CreateBankRequest) (*pb.CreateBankRequest, error) {
		b, err := createBankFromProto(m)
		return createBankRequestToProto(b), err
	}, func(m *pb.CreateBankRequest) { emptyConfig(&m.Config) })
	addRT(func(m *pb.BankStats) (*pb.BankStats, error) {
		var d decoder
		return bankStatsToProto(d.bankStats(m)), d.err
	}, nil)
	addRT(func(m *pb.ListTagsRequest) (*pb.ListTagsRequest, error) {
		r, err := listTagsFromProto(m)
		return listTagsRequestToProto(r), err
	}, nil)
	addRT(func(m *pb.TagCount) (*pb.TagCount, error) { return tagCountToProto(tagCountFromProto(m)), nil }, nil)
	addRT(func(m *pb.ListTagsResponse) (*pb.ListTagsResponse, error) {
		return tagPageToProto(tagPageFromProto(m)), nil
	}, nil)
	addRT(func(m *pb.PromptBlock) (*pb.PromptBlock, error) {
		var d decoder
		return promptBlockToProto(d.promptBlock(m)), d.err
	}, nil)
	addRT(func(m *pb.PromptMessage) (*pb.PromptMessage, error) {
		var d decoder
		return promptMessageToProto(d.promptMessage(m)), d.err
	}, nil)
	addRT(func(m *pb.PromptPreview) (*pb.PromptPreview, error) {
		var d decoder
		return promptPreviewToProto(d.promptPreview(m)), d.err
	}, nil)
	// The handler passes bank, operation and overrides as separate arguments.
	// The operation is aligned with the overrides kind first (a mismatch is rejected).
	addRT(func(m *pb.PreviewPromptRequest) (*pb.PreviewPromptRequest, error) {
		matchPreviewOperation(m)
		op, o, err := previewPromptFromProto(m)
		out := &pb.PreviewPromptRequest{BankId: m.GetBankId(), Operation: toEnum[pb.PromptOperation](op)}
		previewPromptToProto(out, op, o)
		return out, err
	}, matchPreviewOperation)
	// BankTemplate.config is a value in domain.BankTemplate.
	addRT(func(m *pb.BankTemplate) (*pb.BankTemplate, error) {
		t, err := bankTemplateFromProto(m)
		return bankTemplateToProto(t), err
	}, func(m *pb.BankTemplate) { emptyConfig(&m.Config) })
	addRT(func(m *pb.CreateBankFromTemplateRequest) (*pb.CreateBankFromTemplateRequest, error) {
		t, err := bankTemplateFromProto(m.GetTemplate())
		return &pb.CreateBankFromTemplateRequest{BankId: m.GetBankId(), Template: bankTemplateToProto(t)}, err
	}, func(m *pb.CreateBankFromTemplateRequest) {
		// The converter covers the template only; the handler passes name to CreateNamed itself.
		m.Name = ""
		if m.Template == nil {
			m.Template = &pb.BankTemplate{}
		}
		emptyConfig(&m.Template.Config)
	})
	// A nil directive decodes to the zero directive.
	addRT(func(m *pb.UpsertDirectiveRequest) (*pb.UpsertDirectiveRequest, error) {
		return upsertDirectiveRequestToProto(upsertDirectiveFromProto(m)), nil
	}, func(m *pb.UpsertDirectiveRequest) {
		if m.Directive == nil {
			m.Directive = &pb.Directive{}
		}
	})
}

// documentRoundTrips registers the document.proto round trips.
func documentRoundTrips() {
	addRT(func(m *pb.Document) (*pb.Document, error) {
		var d decoder
		return documentViewToProto(d.documentView(m)), d.err
	}, nil)
	addRT(func(m *pb.ListDocumentsRequest) (*pb.ListDocumentsRequest, error) {
		r, err := listDocumentsFromProto(m)
		return listDocumentsRequestToProto(r), err
	}, nil)
	// DocumentPage holds stored documents, which carry no original text.
	addRT(func(m *pb.ListDocumentsResponse) (*pb.ListDocumentsResponse, error) {
		var d decoder
		return documentPageToProto(d.documentPage(m)), d.err
	}, func(m *pb.ListDocumentsResponse) {
		for _, x := range m.GetDocuments() {
			x.OriginalText = ""
		}
	})
	// The handler passes bank, document id and overrides as separate arguments.
	addRT(func(m *pb.ReprocessDocumentRequest) (*pb.ReprocessDocumentRequest, error) {
		var d decoder
		o := d.retainOverrides(m.GetOverrides())
		return &pb.ReprocessDocumentRequest{
			BankId: m.GetBankId(), DocumentId: m.GetDocumentId(), Overrides: retainOverridesToProto(o),
		}, d.err
	}, nil)
}

// observationRoundTrips registers the observation.proto round trips.
func observationRoundTrips() {
	// ScopeInfo.scope is a value in domain.ScopeInfo.
	addRT(func(m *pb.ScopeInfo) (*pb.ScopeInfo, error) {
		return scopeInfoToProto(scopeInfoFromProto(m)), nil
	}, normScopeInfo)
	addRT(func(m *pb.ListScopesResponse) (*pb.ListScopesResponse, error) {
		return scopePageToProto(scopePageFromProto(m)), nil
	}, func(m *pb.ListScopesResponse) {
		for _, s := range m.GetScopes() {
			normScopeInfo(s)
		}
	})
	addRT(func(m *pb.ClearObservationsResponse) (*pb.ClearObservationsResponse, error) {
		var d decoder
		return clearResultToObservationsProto(d.clearResult(m.GetDeleted(), m.GetOperation())), d.err
	}, nil)
	addRT(func(m *pb.ClearMemoryObservationsResponse) (*pb.ClearMemoryObservationsResponse, error) {
		var d decoder
		return clearResultToMemoryProto(d.clearResult(m.GetDeleted(), m.GetOperation())), d.err
	}, nil)
	addRT(func(m *pb.StrategyMatch) (*pb.StrategyMatch, error) {
		var d decoder
		return strategyMatchToProto(d.strategyMatch(m)), d.err
	}, nil)
	addRT(func(m *pb.PreviewConsolidationStrategiesResponse) (*pb.PreviewConsolidationStrategiesResponse, error) {
		var d decoder
		return strategyPreviewToProto(d.strategyPreview(m)), d.err
	}, nil)
	// The handlers pass bank, scopes, overrides and flags as separate arguments.
	addRT(func(m *pb.ConsolidateRequest) (*pb.ConsolidateRequest, error) {
		var d decoder
		o := d.consolidateOverrides(m.GetOverrides())
		return &pb.ConsolidateRequest{
			BankId: m.GetBankId(), Scopes: tagSetsToProto(tagSetsFromProto(m.GetScopes())),
			Overrides: consolidateOverridesToProto(o),
		}, d.err
	}, nil)
	addRT(func(m *pb.ClearObservationsRequest) (*pb.ClearObservationsRequest, error) {
		return &pb.ClearObservationsRequest{
			BankId: m.GetBankId(), Scopes: tagSetsToProto(tagSetsFromProto(m.GetScopes())),
			Reconsolidate: m.GetReconsolidate(),
		}, nil
	}, nil)
	addRT(func(m *pb.PreviewConsolidationStrategiesRequest) (*pb.PreviewConsolidationStrategiesRequest, error) {
		s, err := strategiesFromProto(m.GetStrategies())
		return &pb.PreviewConsolidationStrategiesRequest{BankId: m.GetBankId(), Strategies: strategiesToProto(s)}, err
	}, nil)
}

// normScopeInfo sets a nil scope to the empty set the converter emits.
func normScopeInfo(m *pb.ScopeInfo) {
	if m.Scope == nil {
		m.Scope = &pb.TagSet{}
	}
}

// operationRoundTrips registers the operation.proto and events.proto round trips.
func operationRoundTrips() {
	addRT(func(m *pb.Operation) (*pb.Operation, error) {
		var d decoder
		return operationToProto(d.operation(m)), d.err
	}, nil)
	addRT(func(m *pb.Progress) (*pb.Progress, error) {
		var d decoder
		return progressToProto(d.progress(m)), d.err
	}, nil)
	// OperationFilter has no bank; the handler passes it separately.
	addRT(func(m *pb.ListOperationsRequest) (*pb.ListOperationsRequest, error) {
		f, err := listOperationsFromProto(m)
		return listOperationsRequestToProto(f), err
	}, func(m *pb.ListOperationsRequest) { m.BankId = "" })
	addRT(func(m *pb.ListOperationsResponse) (*pb.ListOperationsResponse, error) {
		var d decoder
		return &pb.ListOperationsResponse{
			Operations: operationsToProto(d.operations(m.GetOperations())), NextPageToken: m.GetNextPageToken(),
		}, d.err
	}, nil)
	addRT(func(m *pb.LifecycleEvent) (*pb.LifecycleEvent, error) {
		var d decoder
		return eventToProto(d.event(m)), d.err
	}, nil)
	addRT(func(m *pb.ListEventsRequest) (*pb.ListEventsRequest, error) {
		types, err := eventTypesFromProto(m.GetTypes())
		return &pb.ListEventsRequest{
			BankId: m.GetBankId(), AfterSeq: m.GetAfterSeq(), Limit: m.GetLimit(),
			Types: toEnums[pb.EventType](types),
		}, err
	}, nil)
	addRT(func(m *pb.ListEventsResponse) (*pb.ListEventsResponse, error) {
		var d decoder
		var events []domain.LifecycleEvent
		if m.GetEvents() != nil {
			events = make([]domain.LifecycleEvent, len(m.GetEvents()))
			for i, e := range m.GetEvents() {
				events[i] = d.event(e)
			}
		}
		return &pb.ListEventsResponse{Events: eventsToProto(events), LastSeq: m.GetLastSeq()}, d.err
	}, nil)
}

// envelope and inline are the exemption reasons shared by most of this file's requests and responses.
const (
	envelope = "envelope around converted messages; covered by their round trips"
	inline   = "ids and scalars only; read inline by the handler, covered by TestEveryRPCReachable"
	patch    = "decoded into a ConfigPatch (convert_fieldmask.go), which has its own tests"
)

// bankExemptions registers the bank.proto and tenant.proto messages with no counterpart.
func bankExemptions() {
	addExempt[*pb.CreateBankResponse](envelope)
	addExempt[*pb.GetBankRequest](inline)
	addExempt[*pb.GetBankResponse](envelope)
	addExempt[*pb.GetBankConfigRequest](inline)
	addExempt[*pb.GetBankConfigResponse](envelope)
	addExempt[*pb.UpdateBankConfigRequest](patch)
	addExempt[*pb.UpdateBankConfigResponse](envelope)
	addExempt[*pb.ResetBankConfigRequest](patch)
	addExempt[*pb.ResetBankConfigResponse](envelope)
	addExempt[*pb.DeleteBankRequest](inline)
	addExempt[*pb.DeleteBankResponse](envelope)
	addExempt[*pb.ExportBankTemplateRequest](inline)
	addExempt[*pb.ExportBankTemplateResponse](envelope)
	addExempt[*pb.CreateBankFromTemplateResponse](envelope)
	addExempt[*pb.GetBankStatsRequest](inline)
	addExempt[*pb.GetBankStatsResponse](envelope)
	addExempt[*pb.PreviewPromptResponse](envelope)
	addExempt[*pb.UpsertDirectiveResponse](envelope)
	addExempt[*pb.ListDirectivesRequest](inline)
	addExempt[*pb.ListDirectivesResponse](envelope)
	addExempt[*pb.DeleteDirectiveRequest](inline)
	addExempt[*pb.DeleteDirectiveResponse]("empty")
	addExempt[*pb.GetTenantConfigRequest]("empty; the tenant comes from metadata")
	addExempt[*pb.GetTenantConfigResponse](envelope)
	addExempt[*pb.UpdateTenantConfigRequest](patch)
	addExempt[*pb.UpdateTenantConfigResponse](envelope)
}

// otherExemptions registers the document, observation and operation messages with no counterpart.
func otherExemptions() {
	addExempt[*pb.GetDocumentRequest](inline)
	addExempt[*pb.GetDocumentResponse](envelope)
	addExempt[*pb.UpdateDocumentRequest](inline)
	addExempt[*pb.UpdateDocumentResponse](envelope)
	addExempt[*pb.ReprocessDocumentResponse](envelope)
	addExempt[*pb.ConsolidateResponse](envelope)
	addExempt[*pb.ListScopesRequest](inline)
	addExempt[*pb.ClearMemoryObservationsRequest](inline)
	addExempt[*pb.GetOperationRequest](inline)
	addExempt[*pb.GetOperationResponse](envelope)
	addExempt[*pb.CancelOperationRequest](inline)
	addExempt[*pb.CancelOperationResponse](envelope)
	addExempt[*pb.RetryOperationRequest](inline)
	addExempt[*pb.RetryOperationResponse](envelope)
}

// TestStrategyMatchBadScope checks that a malformed scope string fails decoding with INVALID_ARGUMENT.
func TestStrategyMatchBadScope(t *testing.T) {
	for _, s := range []string{"x", "3:fo", "3:foo;1:a"} {
		var d decoder
		d.strategyMatch(&pb.StrategyMatch{Strategy: "s", ShadowedScopes: []string{"3:foo", s}})
		if !errors.Is(d.err, domain.ErrInvalidArgument) {
			t.Fatalf("scope %q: err = %v, want ErrInvalidArgument", s, d.err)
		}
	}
}

// matchPreviewOperation sets m's operation to the one its overrides kind previews (keeping a delta refresh).
func matchPreviewOperation(m *pb.PreviewPromptRequest) {
	switch m.GetOverrides().(type) {
	case *pb.PreviewPromptRequest_Retain:
		m.Operation = pb.PromptOperation_PROMPT_OPERATION_RETAIN
	case *pb.PreviewPromptRequest_Consolidation:
		m.Operation = pb.PromptOperation_PROMPT_OPERATION_CONSOLIDATION
	case *pb.PreviewPromptRequest_Reflect:
		m.Operation = pb.PromptOperation_PROMPT_OPERATION_REFLECT
	case *pb.PreviewPromptRequest_MentalModel:
		if m.Operation != pb.PromptOperation_PROMPT_OPERATION_MENTAL_MODEL_DELTA {
			m.Operation = pb.PromptOperation_PROMPT_OPERATION_SYNTHESIZE
		}
	}
}

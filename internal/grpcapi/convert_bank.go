package grpcapi

import (
	"memd/internal/bank"
	"memd/internal/configsvc"
	"memd/internal/domain"

	pb "memd/gen/memory/v1"
)

// ---------- banks ----------

// bankToProto converts a Bank; its config is always set (the bank's own overrides, possibly empty).
func bankToProto(b domain.Bank) *pb.Bank {
	return &pb.Bank{
		Id:        b.Ref.Bank,
		Name:      b.Name,
		Config:    configValueToProto(b.Config),
		CreatedAt: tsToProto(b.CreatedAt),
		Deleting:  b.Deleting(),
	}
}

// createBankFromProto converts a CreateBankRequest to the bank to create; the tenant is set by the caller.
func createBankFromProto(r *pb.CreateBankRequest) (domain.Bank, error) {
	var d decoder
	out := domain.Bank{
		Ref:    domain.BankRef{Bank: r.GetBankId()},
		Name:   r.GetName(),
		Config: d.config(r.GetConfig()),
	}
	return out, d.err
}

// ---------- stats and tags ----------

// bankStatsToProto converts BankStats; map keys are the domain enum strings as-is.
func bankStatsToProto(s domain.BankStats) *pb.BankStats {
	return &pb.BankStats{
		UnitsByFactType:         mapToProto(s.UnitsByFactType),
		InvalidatedUnits:        s.InvalidatedUnits,
		Documents:               s.Documents,
		Entities:                s.Entities,
		MentalModels:            s.MentalModels,
		PendingOperationsByKind: mapToProto(s.PendingOperationsByKind),
		UnconsolidatedUnits:     s.UnconsolidatedUnits,
		LastMemoryWriteAt:       tsPtrToProto(s.LastMemoryWriteAt),
		ChangeSeq:               s.ChangeSeq,
	}
}

// listTagsFromProto converts a ListTagsRequest; the tenant is set by the caller.
func listTagsFromProto(r *pb.ListTagsRequest) (bank.ListTagsRequest, error) {
	var d decoder
	out := bank.ListTagsRequest{
		Bank:      domain.BankRef{Bank: r.GetBankId()},
		Source:    fromEnum[domain.TagSource](&d, r.GetSource()),
		Prefix:    r.GetPrefix(),
		PageSize:  int(r.GetPageSize()),
		PageToken: r.GetPageToken(),
	}
	return out, d.err
}

// tagCountToProto converts a TagCount.
func tagCountToProto(t domain.TagCount) *pb.TagCount {
	return &pb.TagCount{Tag: t.Tag, Count: t.Count}
}

// tagPageToProto converts a TagPage to a ListTagsResponse.
func tagPageToProto(p bank.TagPage) *pb.ListTagsResponse {
	var tags []*pb.TagCount
	if p.Tags != nil {
		tags = make([]*pb.TagCount, len(p.Tags))
		for i := range p.Tags {
			tags[i] = tagCountToProto(p.Tags[i])
		}
	}
	return &pb.ListTagsResponse{Tags: tags, NextPageToken: p.NextPageToken}
}

// ---------- prompt preview ----------

// promptBlockToProto converts a PromptBlock.
func promptBlockToProto(b domain.PromptBlock) *pb.PromptBlock {
	return &pb.PromptBlock{
		Text: b.Text, Source: toEnum[pb.PromptSource](b.Source), Field: b.Field, Section: b.Section, Active: b.Active,
	}
}

// promptMessageToProto converts a PromptMessage.
func promptMessageToProto(m domain.PromptMessage) *pb.PromptMessage {
	var blocks []*pb.PromptBlock
	if m.Blocks != nil {
		blocks = make([]*pb.PromptBlock, len(m.Blocks))
		for i := range m.Blocks {
			blocks[i] = promptBlockToProto(m.Blocks[i])
		}
	}
	return &pb.PromptMessage{Role: toEnum[pb.PromptRole](m.Role), Blocks: blocks}
}

// promptPreviewToProto converts a PromptPreview.
func promptPreviewToProto(p domain.PromptPreview) *pb.PromptPreview {
	var msgs []*pb.PromptMessage
	if p.Messages != nil {
		msgs = make([]*pb.PromptMessage, len(p.Messages))
		for i := range p.Messages {
			msgs[i] = promptMessageToProto(p.Messages[i])
		}
	}
	return &pb.PromptPreview{Messages: msgs, ResponseSchemaJson: p.ResponseSchemaJSON, SkippedReason: p.SkippedReason}
}

// previewPromptFromProto converts a PreviewPromptRequest's operation and overrides (nil = none). Overrides of
// a kind that does not match the operation are domain.ErrInvalidArgument.
func previewPromptFromProto(r *pb.PreviewPromptRequest) (domain.PromptOperation, *domain.BankConfig, error) {
	var d decoder
	op := fromEnum[domain.PromptOperation](&d, r.GetOperation())
	var o *domain.BankConfig
	var want domain.PromptOperation
	switch x := r.GetOverrides().(type) {
	case *pb.PreviewPromptRequest_Retain:
		o, want = d.retainOverrides(x.Retain), domain.PromptOperationRetain
	case *pb.PreviewPromptRequest_Consolidation:
		o, want = d.consolidateOverrides(x.Consolidation), domain.PromptOperationConsolidation
	case *pb.PreviewPromptRequest_Reflect:
		o, want = d.reflectOverrides(x.Reflect), domain.PromptOperationReflect
	case *pb.PreviewPromptRequest_MentalModel:
		o, want = d.refreshOverrides(x.MentalModel), domain.PromptOperationSynthesize
		if op == domain.PromptOperationMentalModelDelta {
			want = op
		}
	default:
		return op, nil, d.err
	}
	if d.err == nil && op != want {
		return op, nil, domain.InvalidField("overrides", "overrides do not match operation %s", op)
	}
	return op, o, d.err
}

// ---------- templates ----------

// bankTemplateFromProto converts a request's BankTemplate; nil = the zero template.
func bankTemplateFromProto(t *pb.BankTemplate) (domain.BankTemplate, error) {
	var d decoder
	out := d.bankTemplate(t)
	return out, d.err
}

// bankTemplateToProto converts a BankTemplate; its config is always set.
func bankTemplateToProto(t domain.BankTemplate) *pb.BankTemplate {
	return &pb.BankTemplate{
		Config:       configValueToProto(t.Config),
		Directives:   directivesToProto(t.Directives),
		MentalModels: specsToProto(t.MentalModels),
	}
}

// bankTemplate converts a BankTemplate; nil = the zero template. Directive banks are not on the wire.
func (d *decoder) bankTemplate(m *pb.BankTemplate) domain.BankTemplate {
	return domain.BankTemplate{
		Config:       d.config(m.GetConfig()),
		Directives:   directivesFromProto(m.GetDirectives()),
		MentalModels: d.specs(m.GetMentalModels()),
	}
}

// ---------- directives ----------

// upsertDirectiveFromProto converts an UpsertDirectiveRequest to the directive to store; the tenant is set
// by the caller.
func upsertDirectiveFromProto(r *pb.UpsertDirectiveRequest) domain.Directive {
	out := directiveFromProto(r.GetDirective())
	out.Bank = domain.BankRef{Bank: r.GetBankId()}
	return out
}

// listDirectivesFromProto converts a ListDirectivesRequest; the bank is set by the caller.
func listDirectivesFromProto(r *pb.ListDirectivesRequest) (configsvc.ListDirectivesRequest, error) {
	var d decoder
	out := configsvc.ListDirectivesRequest{
		Tags: d.tagFilter(r.GetTags()), ActiveOnly: r.GetActiveOnly(),
		PageSize: int(r.GetPageSize()), PageToken: r.GetPageToken(),
	}
	return out, d.err
}

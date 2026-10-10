package server

import (
	"memd/internal/domain"
	"memd/internal/engine/bank"
	"memd/internal/port"
	"time"

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

// bank converts a Bank; deleting becomes a non-nil DeletingAt holding the zero time (the wire has no time).
func (d *decoder) bank(m *pb.Bank) domain.Bank {
	var deleting *time.Time
	if m.GetDeleting() {
		deleting = &time.Time{}
	}
	return domain.Bank{
		Ref:        port.BankRef{Bank: m.GetId()},
		Name:       m.GetName(),
		Config:     d.config(m.GetConfig()),
		CreatedAt:  d.ts(m.GetCreatedAt()),
		DeletingAt: deleting,
	}
}

// createBankFromProto converts a CreateBankRequest to the bank to create; the tenant is set by the caller.
func createBankFromProto(r *pb.CreateBankRequest) (domain.Bank, error) {
	var d decoder
	out := domain.Bank{
		Ref:    port.BankRef{Bank: r.GetBankId()},
		Name:   r.GetName(),
		Config: d.config(r.GetConfig()),
	}
	return out, d.err
}

// createBankRequestToProto converts a bank to create back to a CreateBankRequest.
func createBankRequestToProto(b domain.Bank) *pb.CreateBankRequest {
	return &pb.CreateBankRequest{BankId: b.Ref.Bank, Name: b.Name, Config: configValueToProto(b.Config)}
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

// bankStats converts BankStats; map keys are taken as-is (no parsing).
func (d *decoder) bankStats(m *pb.BankStats) domain.BankStats {
	return domain.BankStats{
		UnitsByFactType:         mapFromProto[domain.FactType](m.GetUnitsByFactType()),
		InvalidatedUnits:        m.GetInvalidatedUnits(),
		Documents:               m.GetDocuments(),
		Entities:                m.GetEntities(),
		MentalModels:            m.GetMentalModels(),
		PendingOperationsByKind: mapFromProto[domain.OperationKind](m.GetPendingOperationsByKind()),
		UnconsolidatedUnits:     m.GetUnconsolidatedUnits(),
		LastMemoryWriteAt:       d.tsPtr(m.GetLastMemoryWriteAt()),
		ChangeSeq:               m.GetChangeSeq(),
	}
}

// listTagsFromProto converts a ListTagsRequest; the tenant is set by the caller.
func listTagsFromProto(r *pb.ListTagsRequest) (bank.ListTagsRequest, error) {
	var d decoder
	out := bank.ListTagsRequest{
		Bank:      port.BankRef{Bank: r.GetBankId()},
		Source:    fromEnum[domain.TagSource](&d, r.GetSource()),
		Prefix:    r.GetPrefix(),
		PageSize:  int(r.GetPageSize()),
		PageToken: r.GetPageToken(),
	}
	return out, d.err
}

// listTagsRequestToProto converts a ListTagsRequest back to proto.
func listTagsRequestToProto(r bank.ListTagsRequest) *pb.ListTagsRequest {
	return &pb.ListTagsRequest{
		BankId:    r.Bank.Bank,
		Source:    toEnum[pb.TagSource](r.Source),
		Prefix:    r.Prefix,
		PageSize:  i32(r.PageSize),
		PageToken: r.PageToken,
	}
}

// tagCountToProto converts a TagCount.
func tagCountToProto(t domain.TagCount) *pb.TagCount {
	return &pb.TagCount{Tag: t.Tag, Count: t.Count}
}

// tagCountFromProto converts a TagCount.
func tagCountFromProto(m *pb.TagCount) domain.TagCount {
	return domain.TagCount{Tag: m.GetTag(), Count: m.GetCount()}
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

// tagPageFromProto converts a ListTagsResponse to a TagPage.
func tagPageFromProto(m *pb.ListTagsResponse) bank.TagPage {
	var tags []domain.TagCount
	if m.GetTags() != nil {
		tags = make([]domain.TagCount, len(m.GetTags()))
		for i, t := range m.GetTags() {
			tags[i] = tagCountFromProto(t)
		}
	}
	return bank.TagPage{Tags: tags, NextPageToken: m.GetNextPageToken()}
}

// ---------- prompt preview ----------

// promptBlockToProto converts a PromptBlock.
func promptBlockToProto(b domain.PromptBlock) *pb.PromptBlock {
	return &pb.PromptBlock{
		Text: b.Text, Source: toEnum[pb.PromptSource](b.Source), Field: b.Field, Section: b.Section, Active: b.Active,
	}
}

// promptBlock converts a PromptBlock.
func (d *decoder) promptBlock(m *pb.PromptBlock) domain.PromptBlock {
	return domain.PromptBlock{
		Text: m.GetText(), Source: fromEnum[domain.PromptSource](d, m.GetSource()), Field: m.GetField(),
		Section: m.GetSection(), Active: m.GetActive(),
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

// promptMessage converts a PromptMessage.
func (d *decoder) promptMessage(m *pb.PromptMessage) domain.PromptMessage {
	var blocks []domain.PromptBlock
	if m.GetBlocks() != nil {
		blocks = make([]domain.PromptBlock, len(m.GetBlocks()))
		for i, b := range m.GetBlocks() {
			blocks[i] = d.promptBlock(b)
		}
	}
	return domain.PromptMessage{Role: fromEnum[domain.PromptRole](d, m.GetRole()), Blocks: blocks}
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

// promptPreview converts a PromptPreview.
func (d *decoder) promptPreview(m *pb.PromptPreview) domain.PromptPreview {
	var msgs []domain.PromptMessage
	if m.GetMessages() != nil {
		msgs = make([]domain.PromptMessage, len(m.GetMessages()))
		for i, x := range m.GetMessages() {
			msgs[i] = d.promptMessage(x)
		}
	}
	return domain.PromptPreview{
		Messages: msgs, ResponseSchemaJSON: m.GetResponseSchemaJson(), SkippedReason: m.GetSkippedReason(),
	}
}

// previewPromptFromProto converts a PreviewPromptRequest's operation and overrides (nil = none).
func previewPromptFromProto(r *pb.PreviewPromptRequest) (domain.PromptOperation, *domain.BankConfig, error) {
	var d decoder
	op := fromEnum[domain.PromptOperation](&d, r.GetOperation())
	o := d.configPtr(r.GetOverrides())
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
	out.Bank = port.BankRef{Bank: r.GetBankId()}
	return out
}

// upsertDirectiveRequestToProto converts a directive to store back to an UpsertDirectiveRequest.
func upsertDirectiveRequestToProto(x domain.Directive) *pb.UpsertDirectiveRequest {
	return &pb.UpsertDirectiveRequest{BankId: x.Bank.Bank, Directive: directiveToProto(x)}
}

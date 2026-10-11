package server

import (
	"memd/internal/domain"
	"memd/internal/engine/docs"

	pb "memd/gen/memory/v1"
)

// documentToProto converts a stored document without its original text.
func documentToProto(x domain.Document) *pb.Document {
	return &pb.Document{
		Id:              string(x.ID),
		ContentHash:     x.ContentHash,
		Tags:            x.Tags,
		MemoryUnitCount: x.UnitCount,
		UnitsByFactType: mapToProto(x.UnitsByFactType),
		StoredOverrides: configToProto(x.Overrides),
		CreatedAt:       tsToProto(x.CreatedAt),
		UpdatedAt:       tsToProto(x.UpdatedAt),
		ExtractedAt:     tsPtrToProto(x.ExtractedAt),
	}
}

// documentViewToProto converts a DocumentView: the document plus its original text.
func documentViewToProto(v docs.DocumentView) *pb.Document {
	out := documentToProto(v.Document)
	out.OriginalText = v.OriginalText
	return out
}

// document converts a Document to a stored document, dropping original_text (see documentView).
func (d *decoder) document(m *pb.Document) domain.Document {
	return domain.Document{
		ID:              domain.DocumentID(m.GetId()),
		ContentHash:     m.GetContentHash(),
		Tags:            m.GetTags(),
		UnitCount:       m.GetMemoryUnitCount(),
		UnitsByFactType: mapFromProto[domain.FactType](m.GetUnitsByFactType()),
		Overrides:       d.configPtr(m.GetStoredOverrides()),
		CreatedAt:       d.ts(m.GetCreatedAt()),
		UpdatedAt:       d.ts(m.GetUpdatedAt()),
		ExtractedAt:     d.tsPtr(m.GetExtractedAt()),
	}
}

// documentView converts a Document to a DocumentView.
func (d *decoder) documentView(m *pb.Document) docs.DocumentView {
	return docs.DocumentView{Document: d.document(m), OriginalText: m.GetOriginalText()}
}

// listDocumentsFromProto converts a ListDocumentsRequest; the tenant is set by the caller.
func listDocumentsFromProto(r *pb.ListDocumentsRequest) (docs.ListDocumentsRequest, error) {
	var d decoder
	out := docs.ListDocumentsRequest{
		Bank:      domain.BankRef{Bank: r.GetBankId()},
		Q:         r.GetQ(),
		Tags:      r.GetTags(),
		TagsMatch: fromEnum[domain.TagsMatch](&d, r.GetTagsMatch()),
		Start:     d.tsPtr(r.GetStart()),
		End:       d.tsPtr(r.GetEnd()),
		TimeField: fromEnum[domain.DocTimeField](&d, r.GetTimeField()),
		PageSize:  int(r.GetPageSize()),
		PageToken: r.GetPageToken(),
	}
	return out, d.err
}

// listDocumentsRequestToProto converts a ListDocumentsRequest back to proto.
func listDocumentsRequestToProto(r docs.ListDocumentsRequest) *pb.ListDocumentsRequest {
	return &pb.ListDocumentsRequest{
		BankId:    r.Bank.Bank,
		Q:         r.Q,
		Tags:      r.Tags,
		TagsMatch: toEnum[pb.TagsMatch](r.TagsMatch),
		Start:     tsPtrToProto(r.Start),
		End:       tsPtrToProto(r.End),
		TimeField: toEnum[pb.DocTimeField](r.TimeField),
		PageSize:  i32(r.PageSize),
		PageToken: r.PageToken,
	}
}

// documentPageToProto converts a DocumentPage to a ListDocumentsResponse (listed documents carry no text).
func documentPageToProto(p docs.DocumentPage) *pb.ListDocumentsResponse {
	var items []*pb.Document
	if p.Items != nil {
		items = make([]*pb.Document, len(p.Items))
		for i := range p.Items {
			items[i] = documentToProto(p.Items[i])
		}
	}
	return &pb.ListDocumentsResponse{Documents: items, NextPageToken: p.NextPageToken}
}

// documentPage converts a ListDocumentsResponse to a DocumentPage.
func (d *decoder) documentPage(m *pb.ListDocumentsResponse) docs.DocumentPage {
	var items []domain.Document
	if m.GetDocuments() != nil {
		items = make([]domain.Document, len(m.GetDocuments()))
		for i, x := range m.GetDocuments() {
			items[i] = d.document(x)
		}
	}
	return docs.DocumentPage{Items: items, NextPageToken: m.GetNextPageToken()}
}

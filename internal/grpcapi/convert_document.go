package grpcapi

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
		RetainOverrides: retainOverridesToProto(x.Overrides),
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

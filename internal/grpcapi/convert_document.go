package grpcapi

import (
	"memd/internal/document"
	"memd/internal/domain"

	pb "memd/gen/memory/v1"
)

// documentToProto converts a stored document without its original text.
func documentToProto(x domain.Document) *pb.Document {
	return &pb.Document{
		Id:                 string(x.ID),
		ContentHash:        x.ContentHash,
		Tags:               x.Tags,
		MemoryCount:        x.MemoryCount,
		MemoriesByFactType: mapToProto(x.MemoriesByFactType),
		RetainOverrides:    retainOverridesToProto(x.Overrides),
		CreatedAt:          tsToProto(x.CreatedAt),
		UpdatedAt:          tsToProto(x.UpdatedAt),
		ExtractedAt:        tsPtrToProto(x.ExtractedAt),
	}
}

// documentViewToProto converts a document.View: the document plus its original text.
func documentViewToProto(v document.View) *pb.Document {
	out := documentToProto(v.Document)
	out.OriginalText = v.OriginalText
	return out
}

// listDocumentsFromProto converts a ListDocumentsRequest; the tenant is set by the caller.
func listDocumentsFromProto(r *pb.ListDocumentsRequest) (document.ListRequest, error) {
	var d decoder
	out := document.ListRequest{
		DocumentIDContains: r.GetDocumentIdContains(),
		Tags:               d.tagFilter(r.GetTags()),
		Start:              d.tsPtr(r.GetStart()),
		End:                d.tsPtr(r.GetEnd()),
		TimeField:          fromEnum[domain.DocumentTimeField](&d, r.GetTimeField()),
		PageSize:           int(r.GetPageSize()),
		PageToken:          r.GetPageToken(),
	}
	return out, d.err
}

// documentPageToProto converts a document.Page to a ListDocumentsResponse (listed documents carry no text).
func documentPageToProto(p document.Page) *pb.ListDocumentsResponse {
	var items []*pb.Document
	if p.Items != nil {
		items = make([]*pb.Document, len(p.Items))
		for i := range p.Items {
			items[i] = documentToProto(p.Items[i])
		}
	}
	return &pb.ListDocumentsResponse{Documents: items, NextPageToken: p.NextPageToken}
}

package grpcapi

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/docs"

	pb "memd/gen/memory/v1"
)

// DocumentHandler serves memory.v1.DocumentService.
type DocumentHandler struct {
	pb.UnimplementedDocumentServiceServer
	// Documents serves ListDocuments, GetDocument and ReprocessDocument.
	Documents docs.DocumentAdmin
	// Retagger serves UpdateDocument.
	Retagger docs.Retagger
	// Operations loads the operation UpdateDocument started.
	Operations OperationReader
}

var _ pb.DocumentServiceServer = (*DocumentHandler)(nil)

// ListDocuments returns one page of the bank's documents.
func (h *DocumentHandler) ListDocuments(
	ctx context.Context, req *pb.ListDocumentsRequest,
) (*pb.ListDocumentsResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := listDocumentsFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	out, err := h.Documents.ListDocuments(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return documentPageToProto(out), nil
}

// GetDocument returns one document, with its original text when asked for.
func (h *DocumentHandler) GetDocument(
	ctx context.Context, req *pb.GetDocumentRequest,
) (*pb.GetDocumentResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := requireID("document_id", req.GetDocumentId()); err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Documents.GetDocument(ctx, b, domain.DocumentID(req.GetDocumentId()), req.GetIncludeText())
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetDocumentResponse{Document: documentViewToProto(out)}, nil
}

// UpdateDocument replaces the document's tag set; a set-equal update returns no operation.
func (h *DocumentHandler) UpdateDocument(
	ctx context.Context, req *pb.UpdateDocumentRequest,
) (*pb.UpdateDocumentResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := requireID("document_id", req.GetDocumentId()); err != nil {
		return nil, toStatus(err)
	}
	if req.GetTags() == nil {
		// The new set replaces the old one entirely, so it must be given; an empty set clears the tags.
		return nil, toStatus(fmt.Errorf("%w: tags is required", domain.ErrInvalidArgument))
	}
	tags := req.GetTags().GetTags()
	if tags == nil {
		tags = []string{} // a set but empty TagSet clears the document's tags
	}
	id, err := h.Retagger.Retag(ctx, b, domain.DocumentID(req.GetDocumentId()), tags)
	if err != nil {
		return nil, toStatus(err)
	}
	if id == "" {
		return &pb.UpdateDocumentResponse{}, nil
	}
	op, err := h.Operations.GetOperation(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.UpdateDocumentResponse{Operation: operationToProto(op)}, nil
}

// ReprocessDocument re-extracts the document from its stored text and returns the retain operation.
func (h *DocumentHandler) ReprocessDocument(
	ctx context.Context, req *pb.ReprocessDocumentRequest,
) (*pb.ReprocessDocumentResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := requireID("document_id", req.GetDocumentId()); err != nil {
		return nil, toStatus(err)
	}
	var d decoder
	overrides := d.retainOverrides(req.GetOverrides())
	err = d.err
	if err != nil {
		return nil, toStatus(err)
	}
	if err := validate(overrides); err != nil {
		return nil, toStatus(err)
	}
	op, err := h.Documents.ReprocessDocument(ctx, b, domain.DocumentID(req.GetDocumentId()), overrides)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ReprocessDocumentResponse{Operation: operationToProto(op)}, nil
}

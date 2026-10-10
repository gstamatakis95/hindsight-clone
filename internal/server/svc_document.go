package server

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"

	pb "memd/gen/memory/v1"
)

// documentServer implements memoryv1.DocumentServiceServer over its engines.
type documentServer struct {
	pb.UnimplementedDocumentServiceServer
	// d holds the engines and consumer interfaces.
	d Deps
}

var _ pb.DocumentServiceServer = (*documentServer)(nil)

// newDocumentServer returns the DocumentService server over d.
func newDocumentServer(d Deps) *documentServer { return &documentServer{d: d} }

// ListDocuments returns one page of the bank's documents.
func (s *documentServer) ListDocuments(
	ctx context.Context, req *pb.ListDocumentsRequest,
) (*pb.ListDocumentsResponse, error) {
	if s.d.Documents == nil {
		return nil, notWired("Deps.Documents")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := listDocumentsFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	out, err := s.d.Documents.ListDocuments(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return documentPageToProto(out), nil
}

// GetDocument returns one document, with its original text when asked for.
func (s *documentServer) GetDocument(ctx context.Context, req *pb.GetDocumentRequest) (*pb.GetDocumentResponse, error) {
	if s.d.Documents == nil {
		return nil, notWired("Deps.Documents")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := requireID("document_id", req.GetDocumentId()); err != nil {
		return nil, toStatus(err)
	}
	out, err := s.d.Documents.GetDocument(ctx, b, port.DocumentID(req.GetDocumentId()), req.GetIncludeText())
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetDocumentResponse{Document: documentViewToProto(out)}, nil
}

// UpdateDocument replaces the document's tag set; a set-equal update returns no operation.
func (s *documentServer) UpdateDocument(
	ctx context.Context, req *pb.UpdateDocumentRequest,
) (*pb.UpdateDocumentResponse, error) {
	if s.d.Retagger == nil {
		return nil, notWired("Deps.Retagger")
	}
	if s.d.Operations == nil {
		return nil, notWired("Deps.Operations")
	}
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
	id, err := s.d.Retagger.Retag(ctx, b, port.DocumentID(req.GetDocumentId()), req.GetTags().GetTags())
	if err != nil {
		return nil, toStatus(err)
	}
	if id == "" {
		return &pb.UpdateDocumentResponse{}, nil
	}
	op, err := s.d.Operations.GetOperation(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.UpdateDocumentResponse{Operation: operationToProto(op)}, nil
}

// ReprocessDocument re-extracts the document from its stored text and returns the retain operation.
func (s *documentServer) ReprocessDocument(
	ctx context.Context, req *pb.ReprocessDocumentRequest,
) (*pb.ReprocessDocumentResponse, error) {
	if s.d.Documents == nil {
		return nil, notWired("Deps.Documents")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := requireID("document_id", req.GetDocumentId()); err != nil {
		return nil, toStatus(err)
	}
	overrides, err := configFromProto(req.GetOverrides())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := s.d.validate("ReprocessDocument", overrides); err != nil {
		return nil, toStatus(err)
	}
	op, err := s.d.Documents.ReprocessDocument(ctx, b, port.DocumentID(req.GetDocumentId()), overrides)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ReprocessDocumentResponse{Operation: operationToProto(op)}, nil
}

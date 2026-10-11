package grpcapi

import (
	"context"
	"memd/internal/domain"
	"memd/internal/mentalmodel"

	pb "memd/gen/memory/v1"
)

// MentalModelHandler serves memory.v1.MentalModelService.
type MentalModelHandler struct {
	pb.UnimplementedMentalModelServiceServer
	// MentalModels serves every RPC.
	MentalModels *mentalmodel.Service
}

var _ pb.MentalModelServiceServer = (*MentalModelHandler)(nil)

// modelRef resolves the bank and the required mental_model_id of a request.
func modelRef(ctx context.Context, bankID, id string) (domain.BankRef, domain.MentalModelID, error) {
	b, err := bankRef(ctx, bankID)
	if err != nil {
		return domain.BankRef{}, "", err
	}
	if err := requireID("mental_model_id", id); err != nil {
		return domain.BankRef{}, "", err
	}
	return b, domain.MentalModelID(id), nil
}

// CreateMentalModel stores a model and enqueues its first build.
func (h *MentalModelHandler) CreateMentalModel(
	ctx context.Context, req *pb.CreateMentalModelRequest,
) (*pb.CreateMentalModelResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	if req.GetSpec() == nil {
		return nil, toStatus(domain.InvalidField("spec", "spec is required"))
	}
	var d decoder
	spec := d.spec(req.GetSpec())
	if d.err != nil {
		return nil, toStatus(d.err)
	}
	if err := validate(spec.Overrides); err != nil {
		return nil, toStatus(err)
	}
	m, op, err := h.MentalModels.CreateMentalModel(ctx, b, spec)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.CreateMentalModelResponse{
		MentalModel: mentalModelToProto(m, domain.MentalModelViewFull),
		Operation:   operationToProto(op),
	}, nil
}

// GetMentalModel returns one model in the requested view, default FULL.
func (h *MentalModelHandler) GetMentalModel(
	ctx context.Context, req *pb.GetMentalModelRequest,
) (*pb.GetMentalModelResponse, error) {
	b, id, err := modelRef(ctx, req.GetBankId(), req.GetMentalModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	var d decoder
	view := viewOr(fromEnum[domain.MentalModelView](&d, req.GetView()), getViewDefault)
	if d.err != nil {
		return nil, toStatus(d.err)
	}
	m, err := h.MentalModels.GetMentalModel(ctx, b, id, view)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetMentalModelResponse{MentalModel: mentalModelToProto(m, view)}, nil
}

// ListMentalModels pages through models, default BASIC.
func (h *MentalModelHandler) ListMentalModels(
	ctx context.Context, req *pb.ListMentalModelsRequest,
) (*pb.ListMentalModelsResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := listMentalModelsFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	in.View = viewOr(in.View, listViewDefault)
	page, err := h.MentalModels.ListMentalModels(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return mentalModelPageToProto(page, in.View), nil
}

// UpdateMentalModel patches a model's spec with a FieldMask.
func (h *MentalModelHandler) UpdateMentalModel(
	ctx context.Context, req *pb.UpdateMentalModelRequest,
) (*pb.UpdateMentalModelResponse, error) {
	b, id, err := modelRef(ctx, req.GetBankId(), req.GetMentalModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	patch, err := specPatch(req.GetSpec(), req.GetUpdateMask())
	if err != nil {
		return nil, toStatus(err)
	}
	m, err := h.MentalModels.UpdateMentalModel(ctx, b, id, validatedSpec(patch))
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.UpdateMentalModelResponse{MentalModel: mentalModelToProto(m, domain.MentalModelViewFull)}, nil
}

// RefreshMentalModel enqueues a manual refresh, or joins the queued one.
func (h *MentalModelHandler) RefreshMentalModel(
	ctx context.Context, req *pb.RefreshMentalModelRequest,
) (*pb.RefreshMentalModelResponse, error) {
	b, _, err := modelRef(ctx, req.GetBankId(), req.GetMentalModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := refreshRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	if err := validate(in.Overrides); err != nil {
		return nil, toStatus(err)
	}
	op, joined, err := h.MentalModels.RefreshMentalModel(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.RefreshMentalModelResponse{Operation: operationToProto(op), JoinedQueued: joined}, nil
}

// PreviewMentalModelRefresh runs the refresh pipeline synchronously and writes nothing.
func (h *MentalModelHandler) PreviewMentalModelRefresh(
	ctx context.Context, req *pb.PreviewMentalModelRefreshRequest,
) (*pb.PreviewMentalModelRefreshResponse, error) {
	b, id, err := modelRef(ctx, req.GetBankId(), req.GetMentalModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	var d decoder
	strategy := fromEnum[domain.RefreshStrategy](&d, req.GetStrategy())
	if d.err != nil {
		return nil, toStatus(d.err)
	}
	out, err := h.MentalModels.PreviewMentalModelRefresh(ctx, b, id, strategy)
	if err != nil {
		return nil, toStatus(err)
	}
	return previewToProto(out), nil
}

// ListMentalModelHistory pages through a model's versions and refresh failures, newest first.
func (h *MentalModelHandler) ListMentalModelHistory(
	ctx context.Context, req *pb.ListMentalModelHistoryRequest,
) (*pb.ListMentalModelHistoryResponse, error) {
	b, _, err := modelRef(ctx, req.GetBankId(), req.GetMentalModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := historyRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	page, err := h.MentalModels.ListMentalModelHistory(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return historyPageToProto(page), nil
}

// DeleteMentalModel deletes one model and its history.
func (h *MentalModelHandler) DeleteMentalModel(
	ctx context.Context, req *pb.DeleteMentalModelRequest,
) (*pb.DeleteMentalModelResponse, error) {
	b, id, err := modelRef(ctx, req.GetBankId(), req.GetMentalModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := h.MentalModels.DeleteMentalModel(ctx, b, id); err != nil {
		return nil, toStatus(err)
	}
	return &pb.DeleteMentalModelResponse{}, nil
}

// DeleteMentalModelFolder deletes every model under a folder path.
func (h *MentalModelHandler) DeleteMentalModelFolder(
	ctx context.Context, req *pb.DeleteMentalModelFolderRequest,
) (*pb.DeleteMentalModelFolderResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := requireID("folder_path", req.GetFolderPath()); err != nil {
		return nil, toStatus(err)
	}
	n, err := h.MentalModels.DeleteMentalModelFolder(ctx, b, req.GetFolderPath())
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.DeleteMentalModelFolderResponse{Deleted: i32(n)}, nil
}

// SearchMentalModels ranks whole models by fused full-text and semantic scores.
func (h *MentalModelHandler) SearchMentalModels(
	ctx context.Context, req *pb.SearchMentalModelsRequest,
) (*pb.SearchMentalModelsResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in := searchFromProto(req)
	in.Bank = b
	hits, err := h.MentalModels.SearchMentalModels(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.SearchMentalModelsResponse{Hits: hitsToProto(hits)}, nil
}

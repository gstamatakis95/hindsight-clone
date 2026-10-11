package grpcapi

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/mentalmodel"
	"memd/internal/recall"

	pb "memd/gen/memory/v1"
)

// MentalModelHandler serves memory.v1.MentalModelService.
type MentalModelHandler struct {
	pb.UnimplementedMentalModelServiceServer
	// MentalModels serves every RPC but DryRunRefresh and SearchPages.
	MentalModels mentalmodel.Admin
	// Refresher serves DryRunRefresh.
	Refresher mentalmodel.RefreshRunner
	// Pages serves SearchPages.
	Pages *recall.Engine
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
		return nil, toStatus(fmt.Errorf("%w: spec is required", domain.ErrInvalidArgument))
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
		MentalModel: mentalModelToProto(m, domain.DetailFull),
		Build:       operationToProto(op),
	}, nil
}

// GetMentalModel returns one model at the requested detail, default FULL.
func (h *MentalModelHandler) GetMentalModel(
	ctx context.Context, req *pb.GetMentalModelRequest,
) (*pb.GetMentalModelResponse, error) {
	b, id, err := modelRef(ctx, req.GetBankId(), req.GetMentalModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	var d decoder
	detail := detailOr(fromEnum[domain.Detail](&d, req.GetDetail()), getDetailDefault)
	if d.err != nil {
		return nil, toStatus(d.err)
	}
	m, err := h.MentalModels.GetMentalModel(ctx, b, id, detail)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetMentalModelResponse{MentalModel: mentalModelToProto(m, detail)}, nil
}

// ListMentalModels pages through models, default METADATA.
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
	in.Detail = detailOr(in.Detail, listDetailDefault)
	page, err := h.MentalModels.ListMentalModels(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return mentalModelPageToProto(page, in.Detail), nil
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
	return &pb.UpdateMentalModelResponse{MentalModel: mentalModelToProto(m, domain.DetailFull)}, nil
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

// DryRunRefresh runs the refresh pipeline synchronously and writes nothing.
func (h *MentalModelHandler) DryRunRefresh(
	ctx context.Context, req *pb.DryRunRefreshRequest,
) (*pb.DryRunRefreshResponse, error) {
	b, id, err := modelRef(ctx, req.GetBankId(), req.GetMentalModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	var d decoder
	strategy := fromEnum[domain.RefreshStrategy](&d, req.GetStrategy())
	if d.err != nil {
		return nil, toStatus(d.err)
	}
	out, err := h.Refresher.Run(ctx, b, id, mentalmodel.RefreshOptions{Manual: true, DryRun: true, Strategy: strategy})
	if err != nil {
		return nil, toStatus(err)
	}
	return dryRunToProto(out), nil
}

// ClearMentalModel empties a model's content so its next refresh is full.
func (h *MentalModelHandler) ClearMentalModel(
	ctx context.Context, req *pb.ClearMentalModelRequest,
) (*pb.ClearMentalModelResponse, error) {
	b, id, err := modelRef(ctx, req.GetBankId(), req.GetMentalModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	m, err := h.MentalModels.ClearMentalModel(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ClearMentalModelResponse{MentalModel: mentalModelToProto(m, domain.DetailFull)}, nil
}

// GetMentalModelHistory pages through a model's versions and refresh failures.
func (h *MentalModelHandler) GetMentalModelHistory(
	ctx context.Context, req *pb.GetMentalModelHistoryRequest,
) (*pb.GetMentalModelHistoryResponse, error) {
	b, _, err := modelRef(ctx, req.GetBankId(), req.GetMentalModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := historyRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	page, err := h.MentalModels.MentalModelHistory(ctx, in)
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

// DeleteFolder deletes every model under a folder path.
func (h *MentalModelHandler) DeleteFolder(
	ctx context.Context, req *pb.DeleteFolderRequest,
) (*pb.DeleteFolderResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := requireID("folder_path", req.GetFolderPath()); err != nil {
		return nil, toStatus(err)
	}
	n, err := h.MentalModels.DeleteFolder(ctx, b, req.GetFolderPath())
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.DeleteFolderResponse{Deleted: i32(n)}, nil
}

// SearchPages ranks whole pages by fused full-text and semantic scores.
func (h *MentalModelHandler) SearchPages(
	ctx context.Context, req *pb.SearchPagesRequest,
) (*pb.SearchPagesResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in := searchPagesFromProto(req)
	in.Bank = b
	hits, err := h.Pages.SearchPages(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.SearchPagesResponse{Hits: pageHitsToProto(hits)}, nil
}

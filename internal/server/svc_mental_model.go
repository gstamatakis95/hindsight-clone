package server

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/mentalmodel"

	pb "memd/gen/memory/v1"
)

// mentalModelServer implements memoryv1.MentalModelServiceServer over its engines.
type mentalModelServer struct {
	pb.UnimplementedMentalModelServiceServer
	// d holds the dependencies.
	d Deps
}

var _ pb.MentalModelServiceServer = (*mentalModelServer)(nil)

// newMentalModelServer returns the MentalModelService server over d.
func newMentalModelServer(d Deps) *mentalModelServer { return &mentalModelServer{d: d} }

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
func (s *mentalModelServer) CreateMentalModel(
	ctx context.Context, req *pb.CreateMentalModelRequest,
) (*pb.CreateMentalModelResponse, error) {
	if s.d.MentalModels == nil {
		return nil, notWired("Deps.MentalModels")
	}
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
	m, op, err := s.d.MentalModels.CreateMentalModel(ctx, b, spec)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.CreateMentalModelResponse{
		MentalModel: mentalModelToProto(m, domain.DetailFull),
		Build:       operationToProto(op),
	}, nil
}

// GetMentalModel returns one model at the requested detail, default FULL.
func (s *mentalModelServer) GetMentalModel(
	ctx context.Context, req *pb.GetMentalModelRequest,
) (*pb.GetMentalModelResponse, error) {
	if s.d.MentalModels == nil {
		return nil, notWired("Deps.MentalModels")
	}
	b, id, err := modelRef(ctx, req.GetBankId(), req.GetMentalModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	var d decoder
	detail := detailOr(fromEnum[domain.Detail](&d, req.GetDetail()), getDetailDefault)
	if d.err != nil {
		return nil, toStatus(d.err)
	}
	m, err := s.d.MentalModels.GetMentalModel(ctx, b, id, detail)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetMentalModelResponse{MentalModel: mentalModelToProto(m, detail)}, nil
}

// ListMentalModels pages through models, default METADATA.
func (s *mentalModelServer) ListMentalModels(
	ctx context.Context, req *pb.ListMentalModelsRequest,
) (*pb.ListMentalModelsResponse, error) {
	if s.d.MentalModels == nil {
		return nil, notWired("Deps.MentalModels")
	}
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
	page, err := s.d.MentalModels.ListMentalModels(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return mentalModelPageToProto(page, in.Detail), nil
}

// UpdateMentalModel patches a model's spec with a FieldMask.
func (s *mentalModelServer) UpdateMentalModel(
	ctx context.Context, req *pb.UpdateMentalModelRequest,
) (*pb.UpdateMentalModelResponse, error) {
	if s.d.MentalModels == nil {
		return nil, notWired("Deps.MentalModels")
	}
	b, id, err := modelRef(ctx, req.GetBankId(), req.GetMentalModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	patch, err := specPatch(req.GetSpec(), req.GetUpdateMask())
	if err != nil {
		return nil, toStatus(err)
	}
	m, err := s.d.MentalModels.UpdateMentalModel(ctx, b, id, s.d.validatedSpec(patch))
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.UpdateMentalModelResponse{MentalModel: mentalModelToProto(m, domain.DetailFull)}, nil
}

// RefreshMentalModel enqueues a manual refresh, or joins the queued one.
func (s *mentalModelServer) RefreshMentalModel(
	ctx context.Context, req *pb.RefreshMentalModelRequest,
) (*pb.RefreshMentalModelResponse, error) {
	if s.d.MentalModels == nil {
		return nil, notWired("Deps.MentalModels")
	}
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
	op, joined, err := s.d.MentalModels.RefreshMentalModel(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.RefreshMentalModelResponse{Operation: operationToProto(op), JoinedQueued: joined}, nil
}

// DryRunRefresh runs the refresh pipeline synchronously and writes nothing.
func (s *mentalModelServer) DryRunRefresh(
	ctx context.Context, req *pb.DryRunRefreshRequest,
) (*pb.DryRunRefreshResponse, error) {
	if s.d.Refresher == nil {
		return nil, notWired("Deps.Refresher")
	}
	b, id, err := modelRef(ctx, req.GetBankId(), req.GetMentalModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	var d decoder
	strategy := fromEnum[domain.RefreshStrategy](&d, req.GetStrategy())
	if d.err != nil {
		return nil, toStatus(d.err)
	}
	out, err := s.d.Refresher.Run(ctx, b, id, mentalmodel.RefreshOptions{Manual: true, DryRun: true, Strategy: strategy})
	if err != nil {
		return nil, toStatus(err)
	}
	return dryRunToProto(out), nil
}

// ClearMentalModel empties a model's content so its next refresh is full.
func (s *mentalModelServer) ClearMentalModel(
	ctx context.Context, req *pb.ClearMentalModelRequest,
) (*pb.ClearMentalModelResponse, error) {
	if s.d.MentalModels == nil {
		return nil, notWired("Deps.MentalModels")
	}
	b, id, err := modelRef(ctx, req.GetBankId(), req.GetMentalModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	m, err := s.d.MentalModels.ClearMentalModel(ctx, b, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ClearMentalModelResponse{MentalModel: mentalModelToProto(m, domain.DetailFull)}, nil
}

// GetMentalModelHistory pages through a model's versions and refresh failures.
func (s *mentalModelServer) GetMentalModelHistory(
	ctx context.Context, req *pb.GetMentalModelHistoryRequest,
) (*pb.GetMentalModelHistoryResponse, error) {
	if s.d.MentalModels == nil {
		return nil, notWired("Deps.MentalModels")
	}
	b, _, err := modelRef(ctx, req.GetBankId(), req.GetMentalModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := historyRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	page, err := s.d.MentalModels.MentalModelHistory(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return historyPageToProto(page), nil
}

// DeleteMentalModel deletes one model and its history.
func (s *mentalModelServer) DeleteMentalModel(
	ctx context.Context, req *pb.DeleteMentalModelRequest,
) (*pb.DeleteMentalModelResponse, error) {
	if s.d.MentalModels == nil {
		return nil, notWired("Deps.MentalModels")
	}
	b, id, err := modelRef(ctx, req.GetBankId(), req.GetMentalModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := s.d.MentalModels.DeleteMentalModel(ctx, b, id); err != nil {
		return nil, toStatus(err)
	}
	return &pb.DeleteMentalModelResponse{}, nil
}

// DeleteFolder deletes every model under a folder path.
func (s *mentalModelServer) DeleteFolder(
	ctx context.Context, req *pb.DeleteFolderRequest,
) (*pb.DeleteFolderResponse, error) {
	if s.d.MentalModels == nil {
		return nil, notWired("Deps.MentalModels")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := requireID("folder_path", req.GetFolderPath()); err != nil {
		return nil, toStatus(err)
	}
	n, err := s.d.MentalModels.DeleteFolder(ctx, b, req.GetFolderPath())
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.DeleteFolderResponse{Deleted: i32(n)}, nil
}

// SearchPages ranks whole pages by fused full-text and semantic scores.
func (s *mentalModelServer) SearchPages(
	ctx context.Context, req *pb.SearchPagesRequest,
) (*pb.SearchPagesResponse, error) {
	if s.d.Pages == nil {
		return nil, notWired("Deps.Pages")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in := searchPagesFromProto(req)
	in.Bank = b
	hits, err := s.d.Pages.Search(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.SearchPagesResponse{Hits: pageHitsToProto(hits)}, nil
}

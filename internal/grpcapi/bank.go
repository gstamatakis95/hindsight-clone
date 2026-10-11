package grpcapi

import (
	"context"
	"fmt"
	"memd/internal/bank"
	"memd/internal/configsvc"
	"memd/internal/domain"

	pb "memd/gen/memory/v1"
)

// BankHandler serves memory.v1.BankService.
type BankHandler struct {
	pb.UnimplementedBankServiceServer
	// Banks serves CreateBank, CreateBankFromTemplate, GetBank, DeleteBank, GetBankStats, ListTags and
	// ExportBankTemplate.
	Banks *bank.Service
	// Config serves GetBankConfig, UpdateBankConfig, ResetBankConfig, PreviewPrompt and the directive RPCs.
	Config *configsvc.Service
}

var _ pb.BankServiceServer = (*BankHandler)(nil)

// CreateBank creates a bank with its own config overrides; they must pass config.Validate.
func (h *BankHandler) CreateBank(ctx context.Context, req *pb.CreateBankRequest) (*pb.CreateBankResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := createBankFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	if err := validate(&in.Config); err != nil {
		return nil, toStatus(err)
	}
	in.Ref = b
	out, _, err := h.Banks.CreateBank(ctx, bank.CreateRequest{Bank: in})
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.CreateBankResponse{Bank: bankToProto(out)}, nil
}

// GetBank returns a bank and its own config overrides.
func (h *BankHandler) GetBank(ctx context.Context, req *pb.GetBankRequest) (*pb.GetBankResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Banks.GetBank(ctx, b)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetBankResponse{Bank: bankToProto(out)}, nil
}

// GetBankConfig returns the resolved config and the bank's own overrides.
func (h *BankHandler) GetBankConfig(
	ctx context.Context, req *pb.GetBankConfigRequest,
) (*pb.GetBankConfigResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	view, err := h.Config.GetBankConfig(ctx, b)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetBankConfigResponse{
		Resolved: configValueToProto(view.Effective), Overrides: configValueToProto(view.Own),
	}, nil
}

// UpdateBankConfig patches the bank's own overrides with the masked fields of the request config; the
// patched overrides must pass config.Validate (checked inside the patch).
func (h *BankHandler) UpdateBankConfig(
	ctx context.Context, req *pb.UpdateBankConfigRequest,
) (*pb.UpdateBankConfigResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	patch, err := configPatch(req.GetConfig(), req.GetUpdateMask())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Config.UpdateBankConfig(ctx, b, patch)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.UpdateBankConfigResponse{Bank: bankToProto(out)}, nil
}

// ResetBankConfig resets the given override paths (all when empty) so they inherit again.
func (h *BankHandler) ResetBankConfig(
	ctx context.Context, req *pb.ResetBankConfigRequest,
) (*pb.ResetBankConfigResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Config.ResetBankConfig(ctx, b, req.GetPaths())
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ResetBankConfigResponse{Bank: bankToProto(out)}, nil
}

// GetBankStats returns the bank's counts.
func (h *BankHandler) GetBankStats(ctx context.Context, req *pb.GetBankStatsRequest) (*pb.GetBankStatsResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Banks.GetBankStats(ctx, b)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetBankStatsResponse{Stats: bankStatsToProto(out)}, nil
}

// ListTags pages through the bank's tags with counts.
func (h *BankHandler) ListTags(ctx context.Context, req *pb.ListTagsRequest) (*pb.ListTagsResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := listTagsFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	out, err := h.Banks.ListTags(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return tagPageToProto(out), nil
}

// PreviewPrompt renders the messages an operation would send, with request overrides on top.
func (h *BankHandler) PreviewPrompt(
	ctx context.Context, req *pb.PreviewPromptRequest,
) (*pb.PreviewPromptResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	op, overrides, err := previewPromptFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	if err := validate(overrides); err != nil {
		return nil, toStatus(err)
	}
	var o domain.BankConfig
	if overrides != nil {
		o = *overrides
	}
	out, err := h.Config.PreviewPrompt(ctx, b, op, o)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.PreviewPromptResponse{Preview: promptPreviewToProto(out)}, nil
}

// DeleteBank tombstones the bank and returns the delete_bank operation.
func (h *BankHandler) DeleteBank(ctx context.Context, req *pb.DeleteBankRequest) (*pb.DeleteBankResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	op, err := h.Banks.DeleteBank(ctx, b)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.DeleteBankResponse{Operation: operationToProto(op)}, nil
}

// ExportBankTemplate exports the bank's config, directives and mental-model specs.
func (h *BankHandler) ExportBankTemplate(
	ctx context.Context, req *pb.ExportBankTemplateRequest,
) (*pb.ExportBankTemplateResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Banks.ExportBankTemplate(ctx, b)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ExportBankTemplateResponse{Template: bankTemplateToProto(out)}, nil
}

// CreateBankFromTemplate creates a bank from a template.
func (h *BankHandler) CreateBankFromTemplate(
	ctx context.Context, req *pb.CreateBankFromTemplateRequest,
) (*pb.CreateBankFromTemplateResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	t, err := bankTemplateFromProto(req.GetTemplate())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := validate(&t.Config); err != nil {
		return nil, toStatus(err)
	}
	for i := range t.MentalModels {
		if err := validate(t.MentalModels[i].Overrides); err != nil {
			return nil, toStatus(err)
		}
	}
	out, builds, err := h.Banks.CreateBank(ctx, bank.CreateRequest{
		Bank: domain.Bank{Ref: b, Name: req.GetName()}, Template: &t,
	})
	if err != nil {
		return nil, toStatus(err)
	}
	resp := &pb.CreateBankFromTemplateResponse{Bank: bankToProto(out)}
	for _, op := range builds {
		resp.MentalModelBuilds = append(resp.MentalModelBuilds, operationToProto(op))
	}
	return resp, nil
}

// UpsertDirective creates or replaces a directive.
func (h *BankHandler) UpsertDirective(
	ctx context.Context, req *pb.UpsertDirectiveRequest,
) (*pb.UpsertDirectiveResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	if req.GetDirective() == nil {
		return nil, toStatus(fmt.Errorf("%w: directive is required", domain.ErrInvalidArgument))
	}
	in := upsertDirectiveFromProto(req)
	in.Bank = b
	out, err := h.Config.UpsertDirective(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.UpsertDirectiveResponse{Directive: directiveToProto(out)}, nil
}

// ListDirectives lists the bank's directives, filtered by tags and activity.
func (h *BankHandler) ListDirectives(
	ctx context.Context, req *pb.ListDirectivesRequest,
) (*pb.ListDirectivesResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Config.ListDirectives(ctx, b, req.GetTags(), req.GetActiveOnly())
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ListDirectivesResponse{Directives: directivesToProto(out)}, nil
}

// DeleteDirective removes one directive.
func (h *BankHandler) DeleteDirective(
	ctx context.Context, req *pb.DeleteDirectiveRequest,
) (*pb.DeleteDirectiveResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := requireID("directive_id", req.GetDirectiveId()); err != nil {
		return nil, toStatus(err)
	}
	if err := h.Config.DeleteDirective(ctx, b, domain.DirectiveID(req.GetDirectiveId())); err != nil {
		return nil, toStatus(err)
	}
	return &pb.DeleteDirectiveResponse{}, nil
}

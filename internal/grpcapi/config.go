package grpcapi

import (
	"context"
	"memd/internal/configsvc"
	"memd/internal/domain"

	pb "memd/gen/memory/v1"
)

// ConfigHandler serves memory.v1.ConfigService.
type ConfigHandler struct {
	pb.UnimplementedConfigServiceServer
	// Config serves every ConfigService RPC.
	Config *configsvc.Service
}

var _ pb.ConfigServiceServer = (*ConfigHandler)(nil)

// tenant returns the context's tenant, validated.
func tenant(ctx context.Context) (string, error) {
	t := TenantFrom(ctx)
	if err := checkName("tenant", t); err != nil {
		return "", err
	}
	return t, nil
}

// GetTenantConfig returns the tenant's config view (an empty own config when none is set).
func (h *ConfigHandler) GetTenantConfig(
	ctx context.Context, _ *pb.GetTenantConfigRequest,
) (*pb.GetTenantConfigResponse, error) {
	t, err := tenant(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Config.GetTenantConfig(ctx, t)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetTenantConfigResponse{Config: configViewToProto(out)}, nil
}

// UpdateTenantConfig patches the tenant config with the masked fields of the request config; the patched config
// must pass config.Validate (checked inside the patch).
func (h *ConfigHandler) UpdateTenantConfig(
	ctx context.Context, req *pb.UpdateTenantConfigRequest,
) (*pb.UpdateTenantConfigResponse, error) {
	t, err := tenant(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	patch, err := configPatch(req.GetConfig(), req.GetUpdateMask())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Config.UpdateTenantConfig(ctx, t, patch)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.UpdateTenantConfigResponse{Config: configViewToProto(out)}, nil
}

// ResetTenantConfig unsets the masked paths of the tenant config ("*" = all), so they inherit again.
func (h *ConfigHandler) ResetTenantConfig(
	ctx context.Context, req *pb.ResetTenantConfigRequest,
) (*pb.ResetTenantConfigResponse, error) {
	t, err := tenant(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	paths, err := resetPaths(req.GetResetMask())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Config.ResetTenantConfig(ctx, t, paths)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ResetTenantConfigResponse{Config: configViewToProto(out)}, nil
}

// GetOrgConfig returns the org's config view.
func (h *ConfigHandler) GetOrgConfig(
	ctx context.Context, req *pb.GetOrgConfigRequest,
) (*pb.GetOrgConfigResponse, error) {
	o, err := orgRef(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Config.GetOrgConfig(ctx, o)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetOrgConfigResponse{Config: configViewToProto(out)}, nil
}

// UpdateOrgConfig patches the org's config with the masked fields of the request config; the patched config must
// pass config.Validate (checked inside the patch).
func (h *ConfigHandler) UpdateOrgConfig(
	ctx context.Context, req *pb.UpdateOrgConfigRequest,
) (*pb.UpdateOrgConfigResponse, error) {
	o, err := orgRef(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	patch, err := configPatch(req.GetConfig(), req.GetUpdateMask())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Config.UpdateOrgConfig(ctx, o, patch)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.UpdateOrgConfigResponse{Config: configViewToProto(out)}, nil
}

// ResetOrgConfig unsets the masked paths of the org's config ("*" = all), so they inherit again.
func (h *ConfigHandler) ResetOrgConfig(
	ctx context.Context, req *pb.ResetOrgConfigRequest,
) (*pb.ResetOrgConfigResponse, error) {
	o, err := orgRef(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	paths, err := resetPaths(req.GetResetMask())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Config.ResetOrgConfig(ctx, o, paths)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ResetOrgConfigResponse{Config: configViewToProto(out)}, nil
}

// GetBankConfig returns the bank's config view.
func (h *ConfigHandler) GetBankConfig(
	ctx context.Context, req *pb.GetBankConfigRequest,
) (*pb.GetBankConfigResponse, error) {
	b, err := bankRef(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Config.GetBankConfig(ctx, b)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetBankConfigResponse{Config: configViewToProto(out)}, nil
}

// UpdateBankConfig patches the bank's own overrides with the masked fields of the request config; the patched
// overrides must pass config.Validate (checked inside the patch).
func (h *ConfigHandler) UpdateBankConfig(
	ctx context.Context, req *pb.UpdateBankConfigRequest,
) (*pb.UpdateBankConfigResponse, error) {
	b, err := bankRef(ctx, req)
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
	return &pb.UpdateBankConfigResponse{Config: configViewToProto(out)}, nil
}

// ResetBankConfig unsets the masked paths of the bank's own overrides ("*" = all), so they inherit again.
func (h *ConfigHandler) ResetBankConfig(
	ctx context.Context, req *pb.ResetBankConfigRequest,
) (*pb.ResetBankConfigResponse, error) {
	b, err := bankRef(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	paths, err := resetPaths(req.GetResetMask())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Config.ResetBankConfig(ctx, b, paths)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ResetBankConfigResponse{Config: configViewToProto(out)}, nil
}

// PreviewPrompt renders the messages an operation would send, with request overrides on top.
func (h *ConfigHandler) PreviewPrompt(
	ctx context.Context, req *pb.PreviewPromptRequest,
) (*pb.PreviewPromptResponse, error) {
	b, err := bankRef(ctx, req)
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

// CreateDirective adds a directive to the bank with a server-assigned id.
func (h *ConfigHandler) CreateDirective(
	ctx context.Context, req *pb.CreateDirectiveRequest,
) (*pb.CreateDirectiveResponse, error) {
	b, err := bankRef(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	if req.GetDirective() == nil {
		return nil, toStatus(domain.InvalidField("directive", "directive is required"))
	}
	in := directiveFromProto(req.GetDirective())
	in.Bank = b
	out, err := h.Config.CreateDirective(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.CreateDirectiveResponse{Directive: directiveToProto(out)}, nil
}

// UpdateDirective patches one directive with the masked fields of the request directive.
func (h *ConfigHandler) UpdateDirective(
	ctx context.Context, req *pb.UpdateDirectiveRequest,
) (*pb.UpdateDirectiveResponse, error) {
	b, err := bankRef(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	if err := requireID("directive_id", req.GetDirectiveId()); err != nil {
		return nil, toStatus(err)
	}
	patch, err := directivePatch(req.GetDirective(), req.GetUpdateMask())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Config.UpdateDirective(ctx, b, domain.DirectiveID(req.GetDirectiveId()), patch)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.UpdateDirectiveResponse{Directive: directiveToProto(out)}, nil
}

// ListDirectives pages the bank's directives, filtered by tags and activity.
func (h *ConfigHandler) ListDirectives(
	ctx context.Context, req *pb.ListDirectivesRequest,
) (*pb.ListDirectivesResponse, error) {
	b, err := bankRef(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := listDirectivesFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	out, err := h.Config.ListDirectives(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ListDirectivesResponse{
		Directives: directivesToProto(out.Items), NextPageToken: out.NextPageToken,
	}, nil
}

// DeleteDirective removes one directive.
func (h *ConfigHandler) DeleteDirective(
	ctx context.Context, req *pb.DeleteDirectiveRequest,
) (*pb.DeleteDirectiveResponse, error) {
	b, err := bankRef(ctx, req)
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

package grpcapi

import (
	"context"
	"memd/internal/engine/bank"

	pb "memd/gen/memory/v1"
)

// TenantHandler serves memory.v1.TenantService.
type TenantHandler struct {
	pb.UnimplementedTenantServiceServer
	// Tenants reads and patches the tenant-level config.
	Tenants bank.TenantAdmin
}

var _ pb.TenantServiceServer = (*TenantHandler)(nil)

// tenant returns the context's tenant, validated.
func tenant(ctx context.Context) (string, error) {
	t := TenantFrom(ctx)
	if err := checkName("tenant", t); err != nil {
		return "", err
	}
	return t, nil
}

// GetTenantConfig returns the tenant-level config (the zero config when none is set).
func (h *TenantHandler) GetTenantConfig(
	ctx context.Context, _ *pb.GetTenantConfigRequest,
) (*pb.GetTenantConfigResponse, error) {
	t, err := tenant(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Tenants.TenantConfig(ctx, t)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetTenantConfigResponse{Config: configValueToProto(out)}, nil
}

// UpdateTenantConfig patches the tenant-level config with the masked fields of the request config; the
// patched config must pass config.Validate (checked inside the patch).
func (h *TenantHandler) UpdateTenantConfig(
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
	out, err := h.Tenants.UpdateTenantConfig(ctx, t, patch)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.UpdateTenantConfigResponse{Config: configValueToProto(out)}, nil
}

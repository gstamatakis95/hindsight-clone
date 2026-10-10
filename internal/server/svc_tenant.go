package server

import (
	"context"

	pb "memd/gen/memory/v1"
)

// tenantServer implements memoryv1.TenantServiceServer over its engines.
type tenantServer struct {
	pb.UnimplementedTenantServiceServer
	// d holds the engines and consumer interfaces.
	d Deps
}

var _ pb.TenantServiceServer = (*tenantServer)(nil)

// newTenantServer returns the TenantService server over d.
func newTenantServer(d Deps) *tenantServer { return &tenantServer{d: d} }

// tenant returns the context's tenant, validated.
func tenant(ctx context.Context) (string, error) {
	t := TenantFrom(ctx)
	if err := checkName("tenant", t); err != nil {
		return "", err
	}
	return t, nil
}

// GetTenantConfig returns the tenant-level config (the zero config when none is set).
func (s *tenantServer) GetTenantConfig(
	ctx context.Context, _ *pb.GetTenantConfigRequest,
) (*pb.GetTenantConfigResponse, error) {
	if s.d.Tenants == nil {
		return nil, notWired("Deps.Tenants")
	}
	t, err := tenant(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := s.d.Tenants.TenantConfig(ctx, t)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetTenantConfigResponse{Config: configValueToProto(out)}, nil
}

// UpdateTenantConfig patches the tenant-level config with the masked fields of the request config.
func (s *tenantServer) UpdateTenantConfig(
	ctx context.Context, req *pb.UpdateTenantConfigRequest,
) (*pb.UpdateTenantConfigResponse, error) {
	if s.d.Tenants == nil {
		return nil, notWired("Deps.Tenants")
	}
	t, err := tenant(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	patch, err := configPatch(req.GetConfig(), req.GetUpdateMask())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := s.d.Tenants.UpdateTenantConfig(ctx, t, patch)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.UpdateTenantConfigResponse{Config: configValueToProto(out)}, nil
}

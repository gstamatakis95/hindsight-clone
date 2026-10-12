package grpcapi

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/org"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "memd/gen/memory/v1"
)

// OrgHandler serves memory.v1.OrgService.
type OrgHandler struct {
	pb.UnimplementedOrgServiceServer
	// Orgs serves CreateOrg, GetOrg and ListOrgs.
	Orgs *org.Service
}

var _ pb.OrgServiceServer = (*OrgHandler)(nil)

// CreateOrg creates an org with its initial config, which must pass config.Validate as a whole level.
func (h *OrgHandler) CreateOrg(ctx context.Context, req *pb.CreateOrgRequest) (*pb.CreateOrgResponse, error) {
	o, err := orgRef(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	var d decoder
	cfg := d.config(req.GetConfig())
	if d.err != nil {
		return nil, toStatus(d.err)
	}
	if err := validate(&cfg); err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Orgs.CreateOrg(ctx, domain.Org{Ref: o, Name: req.GetName(), Config: cfg})
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.CreateOrgResponse{Org: orgToProto(out)}, nil
}

// GetOrg returns a live org.
func (h *OrgHandler) GetOrg(ctx context.Context, req *pb.GetOrgRequest) (*pb.GetOrgResponse, error) {
	o, err := orgRef(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Orgs.GetOrg(ctx, o)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetOrgResponse{Org: orgToProto(out)}, nil
}

// ListOrgs pages the calling tenant's live orgs in id order.
func (h *OrgHandler) ListOrgs(ctx context.Context, req *pb.ListOrgsRequest) (*pb.ListOrgsResponse, error) {
	t, err := tenant(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Orgs.ListOrgs(ctx, org.ListRequest{
		Tenant: t, IDPrefix: req.GetIdPrefix(), PageSize: int(req.GetPageSize()), PageToken: req.GetPageToken(),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	orgs := make([]*pb.Org, len(out.Orgs))
	for i := range out.Orgs {
		orgs[i] = orgToProto(out.Orgs[i])
	}
	return &pb.ListOrgsResponse{Orgs: orgs, NextPageToken: out.NextPageToken}, nil
}

// errDeleteOrgUnavailable is DeleteOrg's answer in this version.
var errDeleteOrgUnavailable = errors.New("DeleteOrg is not available in this server version")

// DeleteOrg is not available in this version: it answers UNIMPLEMENTED (reason UNIMPLEMENTED) after checking the
// request, so a malformed one is still INVALID_ARGUMENT.
func (h *OrgHandler) DeleteOrg(ctx context.Context, req *pb.DeleteOrgRequest) (*pb.DeleteOrgResponse, error) {
	if _, err := orgRef(ctx, req); err != nil {
		return nil, toStatus(err)
	}
	return nil, withDetails(status.New(codes.Unimplemented, errDeleteOrgUnavailable.Error()), errDeleteOrgUnavailable).Err()
}

// orgToProto converts an org.
func orgToProto(o domain.Org) *pb.Org {
	return &pb.Org{Id: o.Ref.Org, Name: o.Name, CreatedAt: tsToProto(o.CreatedAt)}
}

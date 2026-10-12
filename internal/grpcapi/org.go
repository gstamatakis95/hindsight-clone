package grpcapi

import (
	"context"
	"memd/internal/domain"
	"memd/internal/org"

	pb "memd/gen/memory/v1"
)

// OrgHandler serves memory.v1.OrgService.
type OrgHandler struct {
	pb.UnimplementedOrgServiceServer
	// Orgs serves every OrgService RPC.
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

// DeleteOrg tombstones the org and its banks and enqueues delete_org, returning the operation and the receipt.
func (h *OrgHandler) DeleteOrg(ctx context.Context, req *pb.DeleteOrgRequest) (*pb.DeleteOrgResponse, error) {
	o, err := orgRef(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	op, rc, err := h.Orgs.DeleteOrg(ctx, o)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.DeleteOrgResponse{Operation: operationToProto(op), Receipt: receiptToProto(rc)}, nil
}

// GetDeletionReceipt returns the receipt of the org's most recent delete.
func (h *OrgHandler) GetDeletionReceipt(
	ctx context.Context, req *pb.GetDeletionReceiptRequest,
) (*pb.GetDeletionReceiptResponse, error) {
	o, err := orgRef(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	rc, err := h.Orgs.GetDeletionReceipt(ctx, o)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetDeletionReceiptResponse{Receipt: receiptToProto(rc)}, nil
}

// receiptToProto converts a deletion receipt.
func receiptToProto(rc domain.DeletionReceipt) *pb.DeletionReceipt {
	out := &pb.DeletionReceipt{
		Id: rc.ID, State: toEnum[pb.DeletionReceipt_State](rc.Status), RequestedAt: tsToProto(rc.RequestedAt),
		CompletedAt: tsPtrToProto(rc.CompletedAt), Banks: i32(rc.Banks),
	}
	if v := rc.Verification; v != nil {
		out.TablesChecked, out.RowsRemaining, out.BlobsRemaining = i32(v.TablesChecked), v.RowsRemaining, v.BlobsRemaining
		out.VerifiedAt = tsToProto(v.VerifiedAt)
	}
	return out
}

// orgToProto converts an org.
func orgToProto(o domain.Org) *pb.Org {
	return &pb.Org{Id: o.Ref.Org, Name: o.Name, CreatedAt: tsToProto(o.CreatedAt)}
}

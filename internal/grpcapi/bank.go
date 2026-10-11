package grpcapi

import (
	"context"
	"memd/internal/bank"

	pb "memd/gen/memory/v1"
)

// BankHandler serves memory.v1.BankService.
type BankHandler struct {
	pb.UnimplementedBankServiceServer
	// Banks serves every BankService RPC.
	Banks *bank.Service
}

var _ pb.BankServiceServer = (*BankHandler)(nil)

// CreateBank creates a bank with its own config overrides, or from a template; the config and the template's
// mental-model overrides must pass config.Validate.
func (h *BankHandler) CreateBank(ctx context.Context, req *pb.CreateBankRequest) (*pb.CreateBankResponse, error) {
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := createBankFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank.Ref = b
	if err := validate(&in.Bank.Config); err != nil {
		return nil, toStatus(err)
	}
	if t := in.Template; t != nil {
		if err := validate(&t.Config); err != nil {
			return nil, toStatus(err)
		}
		for i := range t.MentalModels {
			if err := validate(t.MentalModels[i].Overrides); err != nil {
				return nil, toStatus(err)
			}
		}
	}
	out, builds, err := h.Banks.CreateBank(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.CreateBankResponse{Bank: bankToProto(out), MentalModelOperations: operationsToProto(builds)}, nil
}

// GetBank returns a live bank.
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

// ListBanks pages the calling tenant's live banks in id order.
func (h *BankHandler) ListBanks(ctx context.Context, req *pb.ListBanksRequest) (*pb.ListBanksResponse, error) {
	t, err := tenant(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := h.Banks.ListBanks(ctx, bank.ListBanksRequest{
		Tenant: t, IDPrefix: req.GetIdPrefix(), PageSize: int(req.GetPageSize()), PageToken: req.GetPageToken(),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	banks := make([]*pb.Bank, len(out.Banks))
	for i := range out.Banks {
		banks[i] = bankToProto(out.Banks[i])
	}
	return &pb.ListBanksResponse{Banks: banks, NextPageToken: out.NextPageToken}, nil
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

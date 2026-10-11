package server

import (
	"context"
	"fmt"
	"memd/internal/config"
	"memd/internal/domain"

	pb "memd/gen/memory/v1"
)

// bankServer implements memoryv1.BankServiceServer over its engines.
type bankServer struct {
	pb.UnimplementedBankServiceServer
	// d holds the engines and consumer interfaces.
	d Deps
}

var _ pb.BankServiceServer = (*bankServer)(nil)

// newBankServer returns the BankService server over d.
func newBankServer(d Deps) *bankServer { return &bankServer{d: d} }

// checker returns a check of one config level against rpc's override rules, for configPatch and
// specPatch; it passes when no validator is wired.
func (d Deps) checker(rpc string) func(domain.BankConfig) error {
	return func(c domain.BankConfig) error { return d.validate(rpc, &c) }
}

// CreateBank creates a bank with its own config overrides. Every field is allowed (they are the bank's
// config); the slot and disposition rules of config.RPCBankConfig apply.
func (s *bankServer) CreateBank(ctx context.Context, req *pb.CreateBankRequest) (*pb.CreateBankResponse, error) {
	if s.d.Banks == nil {
		return nil, notWired("Deps.Banks")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := createBankFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	if err := s.d.validate(config.RPCBankConfig, &in.Config); err != nil {
		return nil, toStatus(err)
	}
	in.Ref = b
	out, err := s.d.Banks.CreateBank(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.CreateBankResponse{Bank: bankToProto(out)}, nil
}

// GetBank returns a bank and its own config overrides.
func (s *bankServer) GetBank(ctx context.Context, req *pb.GetBankRequest) (*pb.GetBankResponse, error) {
	if s.d.Banks == nil {
		return nil, notWired("Deps.Banks")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := s.d.Banks.GetBank(ctx, b)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetBankResponse{Bank: bankToProto(out)}, nil
}

// GetBankConfig returns the resolved config and the bank's own overrides.
func (s *bankServer) GetBankConfig(
	ctx context.Context, req *pb.GetBankConfigRequest,
) (*pb.GetBankConfigResponse, error) {
	if s.d.Banks == nil {
		return nil, notWired("Deps.Banks")
	}
	if s.d.Config == nil {
		return nil, notWired("Deps.Config")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	bk, err := s.d.Banks.GetBank(ctx, b)
	if err != nil {
		return nil, toStatus(err)
	}
	resolved, err := s.d.Config.Effective(ctx, b)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetBankConfigResponse{
		Resolved: configValueToProto(resolved), Overrides: configValueToProto(bk.Config),
	}, nil
}

// UpdateBankConfig patches the bank's own overrides with the masked fields of the request config; the
// patched overrides must pass config.RPCBankConfig (checked inside the patch).
func (s *bankServer) UpdateBankConfig(
	ctx context.Context, req *pb.UpdateBankConfigRequest,
) (*pb.UpdateBankConfigResponse, error) {
	if s.d.Banks == nil {
		return nil, notWired("Deps.Banks")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	patch, err := configPatch(req.GetConfig(), req.GetUpdateMask(), s.d.checker(config.RPCBankConfig))
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := s.d.Banks.UpdateBankConfig(ctx, b, patch)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.UpdateBankConfigResponse{Bank: bankToProto(out)}, nil
}

// ResetBankConfig resets the given override paths (all when empty) so they inherit again.
func (s *bankServer) ResetBankConfig(
	ctx context.Context, req *pb.ResetBankConfigRequest,
) (*pb.ResetBankConfigResponse, error) {
	if s.d.Banks == nil {
		return nil, notWired("Deps.Banks")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	patch, err := resetPatch(req.GetPaths())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := s.d.Banks.UpdateBankConfig(ctx, b, patch)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ResetBankConfigResponse{Bank: bankToProto(out)}, nil
}

// GetBankStats returns the bank's counts.
func (s *bankServer) GetBankStats(ctx context.Context, req *pb.GetBankStatsRequest) (*pb.GetBankStatsResponse, error) {
	if s.d.Stats == nil {
		return nil, notWired("Deps.Stats")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := s.d.Stats.Stats(ctx, b)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.GetBankStatsResponse{Stats: bankStatsToProto(out)}, nil
}

// ListTags pages through the bank's tags with counts.
func (s *bankServer) ListTags(ctx context.Context, req *pb.ListTagsRequest) (*pb.ListTagsResponse, error) {
	if s.d.Stats == nil {
		return nil, notWired("Deps.Stats")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	in, err := listTagsFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	in.Bank = b
	out, err := s.d.Stats.Tags(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return tagPageToProto(out), nil
}

// PreviewPrompt renders the messages an operation would send, with request overrides on top.
func (s *bankServer) PreviewPrompt(
	ctx context.Context, req *pb.PreviewPromptRequest,
) (*pb.PreviewPromptResponse, error) {
	if s.d.Previewer == nil {
		return nil, notWired("Deps.Previewer")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	op, overrides, err := previewPromptFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}
	if err := s.d.validate(config.PreviewRPC(op), overrides); err != nil {
		return nil, toStatus(err)
	}
	var o domain.BankConfig
	if overrides != nil {
		o = *overrides
	}
	out, err := s.d.Previewer.Preview(ctx, b, op, o)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.PreviewPromptResponse{Preview: promptPreviewToProto(out)}, nil
}

// DeleteBank tombstones the bank and returns the delete_bank operation.
func (s *bankServer) DeleteBank(ctx context.Context, req *pb.DeleteBankRequest) (*pb.DeleteBankResponse, error) {
	if s.d.Deleter == nil {
		return nil, notWired("Deps.Deleter")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	op, err := s.d.Deleter.Tombstone(ctx, b)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.DeleteBankResponse{Operation: operationToProto(op)}, nil
}

// ExportBankTemplate exports the bank's config, directives and mental-model specs.
func (s *bankServer) ExportBankTemplate(
	ctx context.Context, req *pb.ExportBankTemplateRequest,
) (*pb.ExportBankTemplateResponse, error) {
	if s.d.Templates == nil {
		return nil, notWired("Deps.Templates")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := s.d.Templates.Export(ctx, b)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ExportBankTemplateResponse{Template: bankTemplateToProto(out)}, nil
}

// CreateBankFromTemplate creates a bank from a template.
func (s *bankServer) CreateBankFromTemplate(
	ctx context.Context, req *pb.CreateBankFromTemplateRequest,
) (*pb.CreateBankFromTemplateResponse, error) {
	if s.d.Templates == nil {
		return nil, notWired("Deps.Templates")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	t, err := bankTemplateFromProto(req.GetTemplate())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := s.d.validate(config.RPCBankConfig, &t.Config); err != nil {
		return nil, toStatus(err)
	}
	for i := range t.MentalModels {
		if err := s.d.validate(config.RPCMentalModelSpec, t.MentalModels[i].Overrides); err != nil {
			return nil, toStatus(err)
		}
	}
	out, builds, err := s.d.Templates.CreateNamed(ctx, b, req.GetName(), t)
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
func (s *bankServer) UpsertDirective(
	ctx context.Context, req *pb.UpsertDirectiveRequest,
) (*pb.UpsertDirectiveResponse, error) {
	if s.d.Directives == nil {
		return nil, notWired("Deps.Directives")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	if req.GetDirective() == nil {
		return nil, toStatus(fmt.Errorf("%w: directive is required", domain.ErrInvalidArgument))
	}
	in := upsertDirectiveFromProto(req)
	in.Bank = b
	out, err := s.d.Directives.UpsertDirective(ctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.UpsertDirectiveResponse{Directive: directiveToProto(out)}, nil
}

// ListDirectives lists the bank's directives, filtered by tags and activity.
func (s *bankServer) ListDirectives(
	ctx context.Context, req *pb.ListDirectivesRequest,
) (*pb.ListDirectivesResponse, error) {
	if s.d.Directives == nil {
		return nil, notWired("Deps.Directives")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	out, err := s.d.Directives.ListDirectives(ctx, b, req.GetTags(), req.GetActiveOnly())
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ListDirectivesResponse{Directives: directivesToProto(out)}, nil
}

// DeleteDirective removes one directive.
func (s *bankServer) DeleteDirective(
	ctx context.Context, req *pb.DeleteDirectiveRequest,
) (*pb.DeleteDirectiveResponse, error) {
	if s.d.Directives == nil {
		return nil, notWired("Deps.Directives")
	}
	b, err := bankRef(ctx, req.GetBankId())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := requireID("directive_id", req.GetDirectiveId()); err != nil {
		return nil, toStatus(err)
	}
	if err := s.d.Directives.DeleteDirective(ctx, b, domain.DirectiveID(req.GetDirectiveId())); err != nil {
		return nil, toStatus(err)
	}
	return &pb.DeleteDirectiveResponse{}, nil
}

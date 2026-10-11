package server_test

import (
	"errors"
	"fmt"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/server"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "memd/gen/memory/v1"
)

// TestUpdateDocumentTags checks a missing tag set is rejected and an empty one clears the tags.
func TestUpdateDocumentTags(t *testing.T) {
	h := newHarness(t)
	client := pb.NewDocumentServiceClient(h.conn)
	_, err := client.UpdateDocument(tenantCtx(t), &pb.UpdateDocumentRequest{BankId: "x", DocumentId: "d"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("nil tags: err = %v, want INVALID_ARGUMENT", err)
	}
	h.calls.reset()
	_, err = client.UpdateDocument(tenantCtx(t), &pb.UpdateDocumentRequest{
		BankId: "x", DocumentId: "d", Tags: &pb.TagSet{},
	})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := h.calls.args("Retagger.Retag")
	if tags, ok := args[2].([]string); !ok || tags == nil || len(tags) != 0 {
		t.Fatalf("empty tag set forwarded as %#v, want a non-nil empty slice", args[2])
	}
}

// TestSpecOverridesValidated checks UpdateMentalModel (inside the patch) and CreateBankFromTemplate (per
// template spec) validate spec overrides the way CreateMentalModel does.
func TestSpecOverridesValidated(t *testing.T) {
	var rpcs []string
	h := newHarness(t, func(d *server.Deps) {
		d.ValidateOverrides = func(rpc string, o domain.BankConfig) error {
			rpcs = append(rpcs, rpc)
			if o.RetainMission != "" {
				return fmt.Errorf("%w: retain_mission on %s", domain.ErrInvalidArgument, rpc)
			}
			return nil
		}
	})
	ctx := tenantCtx(t)
	bad := &pb.BankConfig{RetainMission: "no"}
	_, err := pb.NewBankServiceClient(h.conn).CreateBankFromTemplate(ctx, &pb.CreateBankFromTemplateRequest{
		BankId: "x", Template: &pb.BankTemplate{MentalModels: []*pb.MentalModelSpec{{Id: "m", Overrides: bad}}},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("template spec: err = %v, want INVALID_ARGUMENT", err)
	}
	if _, called := h.calls.args("TemplateCodec.CreateNamed"); called {
		t.Error("template with invalid spec overrides reached TemplateCodec.CreateNamed")
	}
	_, err = pb.NewMentalModelServiceClient(h.conn).UpdateMentalModel(ctx, &pb.UpdateMentalModelRequest{
		BankId: "x", MentalModelId: "m", Spec: &pb.MentalModelSpec{Overrides: bad},
	})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := h.calls.args("MentalModelAdmin.UpdateMentalModel")
	_, err = args[2].(server.SpecPatch)(domain.MentalModelSpec{ID: "m"})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("patched spec with invalid overrides: err = %v, want ErrInvalidArgument", err)
	}
	if !slices.Contains(rpcs, config.RPCMentalModelSpec) {
		t.Errorf("validator saw %v, want %s", rpcs, config.RPCMentalModelSpec)
	}
}

// TestRequestEdgeCases checks the cheap request guards: a nil directive, negative paging, and based_on
// present when facts were asked for.
func TestRequestEdgeCases(t *testing.T) {
	h := newHarness(t)
	ctx := tenantCtx(t)
	_, err := pb.NewBankServiceClient(h.conn).UpsertDirective(ctx, &pb.UpsertDirectiveRequest{BankId: "x"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("nil directive: err = %v, want INVALID_ARGUMENT", err)
	}
	_, err = pb.NewObservationServiceClient(h.conn).ListScopes(ctx, &pb.ListScopesRequest{BankId: "x", PageSize: -1})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("negative page_size: err = %v, want INVALID_ARGUMENT", err)
	}
	_, err = pb.NewOperationServiceClient(h.conn).ListEvents(ctx, &pb.ListEventsRequest{BankId: "x", Limit: -1})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("negative limit: err = %v, want INVALID_ARGUMENT", err)
	}
	resp, err := pb.NewMemoryServiceClient(h.conn).Reflect(ctx, &pb.ReflectRequest{BankId: "x", Query: "q"})
	if err != nil || resp.GetBasedOn() == nil {
		t.Errorf("facts included by default: based_on = %v, err = %v; want non-nil", resp.GetBasedOn(), err)
	}
	resp, err = pb.NewMemoryServiceClient(h.conn).Reflect(ctx, &pb.ReflectRequest{
		BankId: "x", Query: "q", Include: &pb.ReflectInclude{Facts: proto.Bool(false)},
	})
	if err != nil || resp.GetBasedOn() != nil {
		t.Errorf("facts excluded: based_on = %v, err = %v; want nil", resp.GetBasedOn(), err)
	}
}

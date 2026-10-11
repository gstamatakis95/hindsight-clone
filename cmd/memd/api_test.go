package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	pb "memd/gen/memory/v1"
)

// API behaviour through the real engines, on the shared test stack (stack_test.go).

// fillRequest sets every id-like string field to "x" and every singular message field to an empty message, so
// required-field checks pass with otherwise zero requests.
func fillRequest(m protoreflect.Message) {
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		switch {
		case fd.IsList() && fd.Name() == "paths": // a reset needs a mask
			m.Mutable(fd).List().Append(protoreflect.ValueOfString("*"))
		case fd.IsList() || fd.IsMap():
		case fd.Kind() == protoreflect.StringKind && (strings.HasSuffix(string(fd.Name()), "id") ||
			strings.HasSuffix(string(fd.Name()), "_path")):
			m.Set(fd, protoreflect.ValueOfString("x"))
		case fd.Message() != nil && fd.ContainingOneof() == nil:
			m.Set(fd, protoreflect.ValueOfMessage(m.NewField(fd).Message()))
		}
	}
}

// TestEveryRPCWired calls every memory.v1 RPC (enumerated from the service descriptors) with a minimal request and
// checks each reaches a wired handler: no UNIMPLEMENTED, and no INTERNAL or UNKNOWN from the engines.
func TestEveryRPCWired(t *testing.T) {
	w := startWired(t)
	n := 0
	protoregistry.GlobalFiles.RangeFilesByPackage("memory.v1", func(fd protoreflect.FileDescriptor) bool {
		for i := range fd.Services().Len() {
			sd := fd.Services().Get(i)
			for j := range sd.Methods().Len() {
				md := sd.Methods().Get(j)
				n++
				t.Run(string(md.FullName()), func(t *testing.T) { w.callRPC(t, sd, md) })
			}
		}
		return true
	})
	if n != 48 {
		t.Fatalf("found %d RPCs, want 48", n)
	}
}

// callRPC invokes one method with a filled request and checks the outcome.
func (w *wiredStack) callRPC(t *testing.T, sd protoreflect.ServiceDescriptor, md protoreflect.MethodDescriptor) {
	t.Helper()
	in, err := protoregistry.GlobalTypes.FindMessageByName(md.Input().FullName())
	if err != nil {
		t.Fatal(err)
	}
	out, err := protoregistry.GlobalTypes.FindMessageByName(md.Output().FullName())
	if err != nil {
		t.Fatal(err)
	}
	req := in.New()
	fillRequest(req)
	method := "/" + string(sd.FullName()) + "/" + string(md.Name())
	err = w.conn.Invoke(w.ctx(t), method, req.Interface(), out.New().Interface())
	switch status.Code(err) {
	case codes.Unimplemented, codes.Internal, codes.Unknown:
		t.Fatalf("%s: %v", method, err)
	}
}

// invalidConfigs are configs every config write must reject with INVALID_ARGUMENT.
func invalidConfigs() map[string]*pb.BankConfig {
	return map[string]*pb.BankConfig{
		"embed binding": {Models: &pb.ModelBindings{Embed: &pb.ModelBinding{Model: "e"}}},
		"no model":      {Models: &pb.ModelBindings{Extract: &pb.ModelBinding{}}},
		"disposition 9": {Reflect: &pb.ReflectConfig{Disposition: &pb.Disposition{Skepticism: 9}}},
		"duplicate strategy names": {Observations: &pb.ObservationConfig{Strategies: []*pb.ConsolidationStrategy{
			{Name: "s"}, {Name: "s"},
		}}},
	}
}

// configWrite is one RPC that writes a whole config.
type configWrite struct {
	// creates is true when the RPC creates the bank, so a rejected call must leave no bank behind.
	creates bool
	// send runs the RPC.
	send func(ctx context.Context, w *wiredStack, id string, c *pb.BankConfig) *status.Status
}

// configWrites are the RPCs that take a whole config: CreateBank, UpdateBankConfig (no mask and a mask),
// UpdateTenantConfig and CreateBankFromTemplate.
func configWrites() map[string]configWrite {
	masked := &fieldmaskpb.FieldMask{Paths: []string{"models", "reflect.disposition", "observations.strategies"}}
	banks := func(w *wiredStack) pb.BankServiceClient { return pb.NewBankServiceClient(w.conn) }
	return map[string]configWrite{
		"CreateBank": {creates: true, send: func(
			ctx context.Context, w *wiredStack, id string, c *pb.BankConfig,
		) *status.Status {
			_, err := banks(w).CreateBank(ctx, &pb.CreateBankRequest{BankId: id, Config: c})
			return status.Convert(err)
		}},
		"CreateBankFromTemplate": {creates: true, send: func(
			ctx context.Context, w *wiredStack, id string, c *pb.BankConfig,
		) *status.Status {
			_, err := banks(w).CreateBankFromTemplate(ctx, &pb.CreateBankFromTemplateRequest{
				BankId: id, Template: &pb.BankTemplate{Config: c},
			})
			return status.Convert(err)
		}},
		"UpdateBankConfig": {send: func(ctx context.Context, w *wiredStack, id string, c *pb.BankConfig) *status.Status {
			_, err := banks(w).UpdateBankConfig(ctx, &pb.UpdateBankConfigRequest{BankId: id, Config: c})
			return status.Convert(err)
		}},
		"UpdateBankConfig/masked": {send: func(
			ctx context.Context, w *wiredStack, id string, c *pb.BankConfig,
		) *status.Status {
			_, err := banks(w).UpdateBankConfig(ctx, &pb.UpdateBankConfigRequest{
				BankId: id, Config: c, UpdateMask: masked,
			})
			return status.Convert(err)
		}},
		"UpdateTenantConfig": {send: func(ctx context.Context, w *wiredStack, _ string, c *pb.BankConfig) *status.Status {
			_, err := pb.NewTenantServiceClient(w.conn).UpdateTenantConfig(ctx,
				&pb.UpdateTenantConfigRequest{Config: c})
			return status.Convert(err)
		}},
	}
}

// storedConfigs reads the tenant's config and the bank's own overrides (nil when the bank does not exist).
func (w *wiredStack) storedConfigs(t *testing.T, ctx context.Context, bank string) []proto.Message {
	t.Helper()
	tc, err := pb.NewTenantServiceClient(w.conn).GetTenantConfig(ctx, &pb.GetTenantConfigRequest{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := pb.NewBankServiceClient(w.conn).GetBank(ctx, &pb.GetBankRequest{BankId: bank})
	if status.Code(err) != codes.NotFound && err != nil {
		t.Fatal(err)
	}
	return []proto.Message{tc.GetConfig(), b.GetBank().GetConfig()}
}

// TestConfigWritesValidated checks every config write rejects each invalid config with INVALID_ARGUMENT and
// stores nothing (a create leaves no bank, an update leaves the stored config as it was), and accepts a valid one.
func TestConfigWritesValidated(t *testing.T) {
	w := startWired(t)
	seed := &pb.BankConfig{Retain: &pb.RetainConfig{Mission: "seed"}}
	n := 0
	for rpc, cw := range configWrites() {
		for name, c := range invalidConfigs() {
			n++
			tenant, bank := fmt.Sprintf("t%d", n), fmt.Sprintf("b%d", n)
			t.Run(rpc+"/"+name, func(t *testing.T) {
				ctx := w.ctxAs(t, tenant)
				if !cw.creates {
					if _, err := pb.NewBankServiceClient(w.conn).CreateBank(ctx, &pb.CreateBankRequest{
						BankId: bank, Config: seed,
					}); err != nil {
						t.Fatal(err)
					}
				}
				before := w.storedConfigs(t, ctx, bank)
				if st := cw.send(ctx, w, bank, c); st.Code() != codes.InvalidArgument {
					t.Fatalf("status = %v, want INVALID_ARGUMENT", st)
				}
				for i, after := range w.storedConfigs(t, ctx, bank) {
					if !proto.Equal(before[i], after) {
						t.Fatalf("rejected write changed the stored config: %v -> %v", before[i], after)
					}
				}
			})
		}
		n++
		tenant, bank := fmt.Sprintf("t%d", n), fmt.Sprintf("b%d", n)
		t.Run(rpc+"/valid", func(t *testing.T) {
			valid := &pb.BankConfig{
				Models:  &pb.ModelBindings{Default: &pb.ModelBinding{Model: "m"}},
				Reflect: &pb.ReflectConfig{Disposition: &pb.Disposition{Skepticism: 5, Literalism: 1, Empathy: 3}},
				Observations: &pb.ObservationConfig{Strategies: []*pb.ConsolidationStrategy{{
					Name: "s", ConsolidateModel: &pb.ModelBinding{Model: "m"},
				}}},
			}
			if st := cw.send(w.ctxAs(t, tenant), w, bank, valid); st.Code() != codes.OK {
				t.Fatalf("valid config: %v", st)
			}
		})
	}
}

// TestSpecOverridesValidated checks UpdateMentalModel and CreateBankFromTemplate validate mental-model spec
// overrides the way CreateMentalModel does.
func TestSpecOverridesValidated(t *testing.T) {
	w := startWired(t)
	ctx := w.ctx(t)
	bad := &pb.RefreshOverrides{Reflect: &pb.ReflectConfig{Disposition: &pb.Disposition{Empathy: 9}}}
	banks := pb.NewBankServiceClient(w.conn)
	_, err := banks.CreateBankFromTemplate(ctx, &pb.CreateBankFromTemplateRequest{
		BankId: "tpl", Template: &pb.BankTemplate{MentalModels: []*pb.MentalModelSpec{{Id: "m", Overrides: bad}}},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("template spec: err = %v, want INVALID_ARGUMENT", err)
	}
	if _, err := banks.GetBank(ctx, &pb.GetBankRequest{BankId: "tpl"}); status.Code(err) != codes.NotFound {
		t.Errorf("rejected template left a bank: err = %v, want NOT_FOUND", err)
	}
	models := pb.NewMentalModelServiceClient(w.conn)
	if _, err := models.CreateMentalModel(ctx, &pb.CreateMentalModelRequest{BankId: "b", Spec: &pb.MentalModelSpec{
		Id: "m", Name: "M", SourceQuery: "q",
	}}); err != nil {
		t.Fatal(err)
	}
	_, err = models.UpdateMentalModel(ctx, &pb.UpdateMentalModelRequest{
		BankId: "b", MentalModelId: "m", Spec: &pb.MentalModelSpec{Overrides: bad},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("update with invalid overrides: err = %v, want INVALID_ARGUMENT", err)
	}
}

// TestUpdateDocumentTags checks a missing tag set is rejected and an empty one clears the document's tags.
func TestUpdateDocumentTags(t *testing.T) {
	w := startWired(t)
	ctx := w.ctx(t)
	if _, err := pb.NewMemoryServiceClient(w.conn).Retain(ctx, &pb.RetainRequest{
		BankId: "b", DocumentTags: []string{"x"},
		Items: []*pb.RetainItem{{Content: "Alice works as a nurse.", DocumentId: "d"}},
	}); err != nil {
		t.Fatal(err)
	}
	w.idle(t, "b")
	documents := pb.NewDocumentServiceClient(w.conn)
	_, err := documents.UpdateDocument(ctx, &pb.UpdateDocumentRequest{BankId: "b", DocumentId: "d"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("nil tags: err = %v, want INVALID_ARGUMENT", err)
	}
	resp, err := documents.UpdateDocument(ctx, &pb.UpdateDocumentRequest{
		BankId: "b", DocumentId: "d", Tags: &pb.TagSet{},
	})
	if err != nil || resp.GetOperation() == nil {
		t.Fatalf("empty tag set: %v, %v; want a retag operation", resp, err)
	}
	w.idle(t, "b")
	doc, err := documents.GetDocument(ctx, &pb.GetDocumentRequest{BankId: "b", DocumentId: "d"})
	if err != nil || len(doc.GetDocument().GetTags()) != 0 {
		t.Fatalf("after clearing: %v, %v; want no tags", doc, err)
	}
}

// TestReflectBasedOn checks based_on is present when facts are included (the default), even when the answer cites
// nothing, and absent when they are excluded.
func TestReflectBasedOn(t *testing.T) {
	w := startWired(t)
	ctx := w.ctx(t)
	mem := pb.NewMemoryServiceClient(w.conn)
	if _, err := mem.Retain(ctx, &pb.RetainRequest{
		BankId: "b", Items: []*pb.RetainItem{{Content: "Alice works as a nurse."}},
	}); err != nil {
		t.Fatal(err)
	}
	resp, err := mem.Reflect(ctx, &pb.ReflectRequest{BankId: "b", Query: "q"})
	if err != nil || resp.GetBasedOn() == nil {
		t.Errorf("facts included by default: based_on = %v, err = %v; want non-nil", resp.GetBasedOn(), err)
	}
	resp, err = mem.Reflect(ctx, &pb.ReflectRequest{
		BankId: "b", Query: "q", Include: &pb.ReflectInclude{Facts: proto.Bool(false)},
	})
	if err != nil || resp.GetBasedOn() != nil {
		t.Errorf("facts excluded: based_on = %v, err = %v; want nil", resp.GetBasedOn(), err)
	}
}

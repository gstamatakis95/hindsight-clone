package server_test

import (
	"errors"
	"fmt"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/engine/bank"
	"memd/internal/engine/curate"
	"memd/internal/engine/mentalmodel"
	"memd/internal/engine/recall"
	"memd/internal/engine/retain"
	"memd/internal/port"
	"memd/internal/server"
	"reflect"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	reflecteng "memd/internal/engine/reflect"

	pb "memd/gen/memory/v1"
)

// fwdCase says what one RPC must forward: the mock method it calls and one representative argument.
type fwdCase struct {
	// req sets the RPC's representative fields; ids are filled with "x" by fillRequest.
	req proto.Message
	// mock is the "Mock.Method" the RPC must call.
	mock string
	// tenantOnly marks tenant-scoped RPCs: the first argument is the tenant, there is no bank.
	tenantOnly bool
	// check inspects the call's arguments (after ctx); nil = bank/tenant only.
	check func(args []any) error
}

// want returns an error unless got equals want.
func want(what string, got, want any) error {
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("%s = %#v, want %#v", what, got, want)
	}
	return nil
}

// refOf finds the BankRef among args: a BankRef argument, or a Bank / Ref field of a struct argument.
func refOf(args []any) (port.BankRef, bool) {
	for _, a := range args {
		if r, ok := a.(port.BankRef); ok {
			return r, true
		}
		v := reflect.ValueOf(a)
		if v.Kind() != reflect.Struct {
			continue
		}
		for _, name := range []string{"Bank", "Ref"} {
			if f := v.FieldByName(name); f.IsValid() {
				if r, ok := f.Interface().(port.BankRef); ok {
					return r, true
				}
			}
		}
	}
	return port.BankRef{}, false
}

// patched runs a ConfigPatch argument on cur.
func patched(a any, cur domain.BankConfig) domain.BankConfig {
	out, err := a.(server.ConfigPatch)(cur)
	if err != nil {
		panic(err)
	}
	return out
}

// fwdCases maps every RPC (method full name) to what it must forward.
func fwdCases() map[protoreflect.FullName]fwdCase {
	return map[protoreflect.FullName]fwdCase{
		"memory.v1.MemoryService.Retain": {
			req:  &pb.RetainRequest{Items: []*pb.RetainItem{{Content: "hello"}}, RunAsync: true},
			mock: "Ingestor.Ingest",
			check: func(a []any) error {
				r := a[0].(retain.IngestRequest)
				return want("items/async", []any{r.Items[0].Content, r.RunAsync}, []any{"hello", true})
			},
		},
		"memory.v1.MemoryService.Recall": {
			req: &pb.RecallRequest{Query: "q", MaxTokens: 7}, mock: "Recaller.Recall",
			check: func(a []any) error {
				r := a[0].(recall.RecallRequest)
				return want("query/max_tokens", []any{r.Query, r.MaxTokens}, []any{"q", 7})
			},
		},
		"memory.v1.MemoryService.Reflect": {
			req: &pb.ReflectRequest{Query: "why"}, mock: "Reflector.Reflect",
			check: func(a []any) error { return want("query", a[0].(reflecteng.ReflectRequest).Query, "why") },
		},
		"memory.v1.MemoryUnitService.ListMemories": {
			req: &pb.ListMemoriesRequest{Q: "needle", PageSize: 5}, mock: "MemoryLister.List",
			check: func(a []any) error {
				r := a[0].(curate.ListMemoriesRequest)
				return want("q/page_size", []any{r.Q, r.PageSize}, []any{"needle", 5})
			},
		},
		"memory.v1.MemoryUnitService.GetMemory": {
			mock: "MemoryLister.Get", check: func(a []any) error { return want("id", a[1], port.UnitID("x")) },
		},
		"memory.v1.MemoryUnitService.GetObservationHistory": {
			mock:  "MemoryLister.ObservationHistory",
			check: func(a []any) error { return want("id", a[1], port.UnitID("x")) },
		},
		"memory.v1.MemoryUnitService.UpdateMemory": {
			req: &pb.UpdateMemoryRequest{Text: proto.String("t"), Reason: "why"}, mock: "Curator.Update",
			check: func(a []any) error {
				r := a[0].(curate.UpdateMemoryRequest)
				return want("text/reason/id", []any{*r.Text, r.Reason, r.ID}, []any{"t", "why", port.UnitID("x")})
			},
		},
		"memory.v1.TenantService.GetTenantConfig": {mock: "TenantAdmin.TenantConfig", tenantOnly: true},
		"memory.v1.TenantService.UpdateTenantConfig": {
			req:  &pb.UpdateTenantConfigRequest{Config: &pb.BankConfig{RetainMission: "m"}},
			mock: "TenantAdmin.UpdateTenantConfig", tenantOnly: true,
			check: func(a []any) error {
				return want("patched mission", patched(a[1], domain.BankConfig{}).RetainMission, "m")
			},
		},
		"memory.v1.BankService.CreateBank": {
			req: &pb.CreateBankRequest{Name: "n"}, mock: "BankAdmin.CreateBank",
			check: func(a []any) error { return want("name", a[0].(domain.Bank).Name, "n") },
		},
		"memory.v1.BankService.GetBank":       {mock: "BankAdmin.GetBank"},
		"memory.v1.BankService.GetBankConfig": {mock: "ConfigResolver.Effective"},
		"memory.v1.BankService.UpdateBankConfig": {
			req:  &pb.UpdateBankConfigRequest{Config: &pb.BankConfig{ReflectMission: "r"}},
			mock: "BankAdmin.UpdateBankConfig",
			check: func(a []any) error {
				return want("patched mission", patched(a[1], domain.BankConfig{}).ReflectMission, "r")
			},
		},
		"memory.v1.BankService.ResetBankConfig": {
			req: &pb.ResetBankConfigRequest{Paths: []string{"retain_mission"}}, mock: "BankAdmin.UpdateBankConfig",
			check: func(a []any) error {
				got := patched(a[1], domain.BankConfig{RetainMission: "a", ReflectMission: "b"})
				return want("reset", []string{got.RetainMission, got.ReflectMission}, []string{"", "b"})
			},
		},
		"memory.v1.BankService.GetBankStats": {mock: "StatsReader.Stats"},
		"memory.v1.BankService.ListTags": {
			req:  &pb.ListTagsRequest{Prefix: "pre", Source: pb.TagSource_TAG_SOURCE_DOCUMENTS},
			mock: "StatsReader.Tags",
			check: func(a []any) error {
				r := a[0].(bank.ListTagsRequest)
				return want("prefix/source", []any{r.Prefix, r.Source}, []any{"pre", domain.TagSourceDocuments})
			},
		},
		"memory.v1.BankService.PreviewPrompt": {
			req:   &pb.PreviewPromptRequest{Operation: pb.PromptOperation_PROMPT_OPERATION_REFLECT},
			mock:  "PromptPreviewer.Preview",
			check: func(a []any) error { return want("operation", a[1], domain.PromptOperationReflect) },
		},
		"memory.v1.BankService.DeleteBank":         {mock: "BankDeleter.Tombstone"},
		"memory.v1.BankService.ExportBankTemplate": {mock: "TemplateCodec.Export"},
		"memory.v1.BankService.CreateBankFromTemplate": {
			req: &pb.CreateBankFromTemplateRequest{Name: "n", Template: &pb.BankTemplate{
				Config: &pb.BankConfig{RetainMission: "t"},
			}},
			mock: "TemplateCodec.CreateNamed",
			check: func(a []any) error {
				if err := want("name", a[1], "n"); err != nil {
					return err
				}
				return want("template mission", a[2].(domain.BankTemplate).Config.RetainMission, "t")
			},
		},
		"memory.v1.BankService.UpsertDirective": {
			req: &pb.UpsertDirectiveRequest{Directive: &pb.Directive{Name: "d"}}, mock: "DirectiveAdmin.UpsertDirective",
			check: func(a []any) error { return want("name", a[0].(domain.Directive).Name, "d") },
		},
		"memory.v1.BankService.ListDirectives": {
			req: &pb.ListDirectivesRequest{Tags: []string{"a"}, ActiveOnly: true}, mock: "DirectiveAdmin.ListDirectives",
			check: func(a []any) error { return want("tags/active_only", a[1:], []any{[]string{"a"}, true}) },
		},
		"memory.v1.BankService.DeleteDirective": {
			mock:  "DirectiveAdmin.DeleteDirective",
			check: func(a []any) error { return want("id", a[1], port.DirectiveID("x")) },
		},
		"memory.v1.DocumentService.ListDocuments": {
			req: &pb.ListDocumentsRequest{Q: "doc"}, mock: "DocumentAdmin.ListDocuments",
			check: func(a []any) error { return want("q", a[0].(server.ListDocumentsRequest).Q, "doc") },
		},
		"memory.v1.DocumentService.GetDocument": {
			req: &pb.GetDocumentRequest{IncludeText: true}, mock: "DocumentAdmin.GetDocument",
			check: func(a []any) error { return want("id/include_text", a[1:], []any{port.DocumentID("x"), true}) },
		},
		"memory.v1.DocumentService.UpdateDocument": {
			req: &pb.UpdateDocumentRequest{Tags: &pb.TagSet{Tags: []string{"t"}}}, mock: "Retagger.Retag",
			check: func(a []any) error { return want("doc/tags", a[1:], []any{port.DocumentID("x"), []string{"t"}}) },
		},
		"memory.v1.DocumentService.ReprocessDocument": {
			req:   &pb.ReprocessDocumentRequest{Overrides: &pb.BankConfig{RetainMission: "o"}},
			mock:  "DocumentAdmin.ReprocessDocument",
			check: func(a []any) error { return want("overrides", a[2].(*domain.BankConfig).RetainMission, "o") },
		},
		"memory.v1.ObservationService.Consolidate": {
			req: &pb.ConsolidateRequest{Scopes: []*pb.TagSet{{Tags: []string{"s"}}}}, mock: "ObservationAdmin.Consolidate",
			check: func(a []any) error { return want("scopes", a[1], []domain.TagSet{{Tags: []string{"s"}}}) },
		},
		"memory.v1.ObservationService.ListScopes": {
			req: &pb.ListScopesRequest{PageSize: 3, PageToken: "tok"}, mock: "ObservationAdmin.ListScopes",
			check: func(a []any) error { return want("page", a[1:], []any{3, "tok"}) },
		},
		"memory.v1.ObservationService.ClearObservations": {
			req: &pb.ClearObservationsRequest{Reconsolidate: true}, mock: "ObservationAdmin.ClearObservations",
			check: func(a []any) error { return want("reconsolidate", a[2], true) },
		},
		"memory.v1.ObservationService.ClearMemoryObservations": {
			req:   &pb.ClearMemoryObservationsRequest{Reconsolidate: true},
			mock:  "ObservationAdmin.ClearMemoryObservations",
			check: func(a []any) error { return want("id/reconsolidate", a[1:], []any{port.UnitID("x"), true}) },
		},
		"memory.v1.ObservationService.PreviewConsolidationStrategies": {
			req: &pb.PreviewConsolidationStrategiesRequest{
				Strategies: []*pb.ConsolidationStrategy{{Name: "s"}},
			},
			mock:  "StrategyMatcher.Preview",
			check: func(a []any) error { return want("draft", a[1].([]domain.ConsolidationStrategy)[0].Name, "s") },
		},
		"memory.v1.MentalModelService.CreateMentalModel": {
			req: &pb.CreateMentalModelRequest{Spec: &pb.MentalModelSpec{Name: "n"}}, mock: "MentalModelAdmin.CreateMentalModel",
			check: func(a []any) error { return want("name", a[1].(domain.MentalModelSpec).Name, "n") },
		},
		"memory.v1.MentalModelService.GetMentalModel": {
			req: &pb.GetMentalModelRequest{Detail: pb.Detail_DETAIL_CONTENT}, mock: "MentalModelAdmin.GetMentalModel",
			check: func(a []any) error {
				return want("id/detail", a[1:], []any{port.MentalModelID("x"), domain.DetailContent})
			},
		},
		"memory.v1.MentalModelService.ListMentalModels": {
			req: &pb.ListMentalModelsRequest{FolderPrefix: "f"}, mock: "MentalModelAdmin.ListMentalModels",
			check: func(a []any) error {
				r := a[0].(server.ListMentalModelsRequest)
				return want("prefix/detail", []any{r.FolderPrefix, r.Detail}, []any{"f", domain.DetailMetadata})
			},
		},
		"memory.v1.MentalModelService.UpdateMentalModel": {
			req:  &pb.UpdateMentalModelRequest{Spec: &pb.MentalModelSpec{Name: "n2"}},
			mock: "MentalModelAdmin.UpdateMentalModel",
			check: func(a []any) error {
				out, err := a[2].(server.SpecPatch)(domain.MentalModelSpec{ID: "x"})
				if err != nil {
					return err
				}
				return want("patched name", out.Name, "n2")
			},
		},
		"memory.v1.MentalModelService.RefreshMentalModel": {
			req: &pb.RefreshMentalModelRequest{ForceFull: true}, mock: "MentalModelAdmin.RefreshMentalModel",
			check: func(a []any) error {
				r := a[0].(server.RefreshRequest)
				return want("id/force_full", []any{r.ID, r.ForceFull}, []any{port.MentalModelID("x"), true})
			},
		},
		"memory.v1.MentalModelService.DryRunRefresh": {
			req:  &pb.DryRunRefreshRequest{Strategy: pb.RefreshStrategy_REFRESH_STRATEGY_SINGLE_SHOT},
			mock: "RefreshRunner.Run",
			check: func(a []any) error {
				o := a[2].(mentalmodel.RefreshOptions)
				return want("dry run/strategy", []any{o.DryRun, o.Strategy}, []any{true, domain.RefreshStrategySingleShot})
			},
		},
		"memory.v1.MentalModelService.ClearMentalModel": {
			mock:  "MentalModelAdmin.ClearMentalModel",
			check: func(a []any) error { return want("id", a[1], port.MentalModelID("x")) },
		},
		"memory.v1.MentalModelService.GetMentalModelHistory": {
			req:   &pb.GetMentalModelHistoryRequest{Kind: pb.HistoryKind_HISTORY_KIND_VERSION},
			mock:  "MentalModelAdmin.MentalModelHistory",
			check: func(a []any) error { return want("kind", a[0].(server.HistoryRequest).Kind, domain.HistoryKindVersion) },
		},
		"memory.v1.MentalModelService.DeleteMentalModel": {
			mock:  "MentalModelAdmin.DeleteMentalModel",
			check: func(a []any) error { return want("id", a[1], port.MentalModelID("x")) },
		},
		"memory.v1.MentalModelService.DeleteFolder": {
			req: &pb.DeleteFolderRequest{FolderPath: "a/b"}, mock: "MentalModelAdmin.DeleteFolder",
			check: func(a []any) error { return want("path", a[1], "a/b") },
		},
		"memory.v1.MentalModelService.SearchPages": {
			req: &pb.SearchPagesRequest{Query: "q", Limit: 4}, mock: "PageSearcher.Search",
			check: func(a []any) error {
				r := a[0].(mentalmodel.SearchPagesRequest)
				return want("query/limit", []any{r.Query, r.Limit}, []any{"q", 4})
			},
		},
		"memory.v1.OperationService.GetOperation": {
			mock:  "OperationReader.GetOperation",
			check: func(a []any) error { return want("id", a[1], port.OperationID("x")) },
		},
		"memory.v1.OperationService.ListOperations": {
			req:  &pb.ListOperationsRequest{Kind: pb.OperationKind_OPERATION_KIND_CONSOLIDATE, PageSize: 2},
			mock: "OperationReader.ListOperations",
			check: func(a []any) error {
				f := a[1].(port.OperationFilter)
				return want("kind/page_size", []any{f.Kind, f.PageSize}, []any{domain.OperationKindConsolidate, 2})
			},
		},
		"memory.v1.OperationService.CancelOperation": {
			mock:  "OperationControl.Cancel",
			check: func(a []any) error { return want("id", a[1], port.OperationID("x")) },
		},
		"memory.v1.OperationService.RetryOperation": {
			mock:  "OperationControl.Retry",
			check: func(a []any) error { return want("id", a[1], port.OperationID("x")) },
		},
		"memory.v1.OperationService.ListEvents": {
			req: &pb.ListEventsRequest{AfterSeq: 9, Limit: 3}, mock: "OperationReader.ListEvents",
			check: func(a []any) error { return want("after_seq/limit", a[1:3], []any{int64(9), 3}) },
		},
	}
}

// TestEveryRPCForwards calls every RPC with the x-tenant-id header and checks it reaches the expected mock
// method with BankRef {acme, x} (or the tenant) and one representative field.
func TestEveryRPCForwards(t *testing.T) {
	h := newHarness(t)
	cases := fwdCases()
	seen := 0
	protoregistry.GlobalFiles.RangeFilesByPackage("memory.v1", func(fd protoreflect.FileDescriptor) bool {
		for i := range fd.Services().Len() {
			sd := fd.Services().Get(i)
			for j := range sd.Methods().Len() {
				md := sd.Methods().Get(j)
				c, ok := cases[md.FullName()]
				if !ok {
					t.Errorf("%s has no forwarding case", md.FullName())
					continue
				}
				seen++
				t.Run(string(md.Name()), func(t *testing.T) { checkForward(t, h, sd, md, c) })
			}
		}
		return true
	})
	if seen != len(cases) {
		t.Fatalf("checked %d RPCs, table has %d", seen, len(cases))
	}
}

// checkForward runs one forwarding case.
func checkForward(t *testing.T, h *harness, sd protoreflect.ServiceDescriptor, md protoreflect.MethodDescriptor,
	c fwdCase,
) {
	in, _ := protoregistry.GlobalTypes.FindMessageByName(md.Input().FullName())
	out, _ := protoregistry.GlobalTypes.FindMessageByName(md.Output().FullName())
	req := in.New()
	fillRequest(req)
	if c.req != nil {
		proto.Merge(req.Interface(), c.req)
	}
	h.calls.reset()
	method := "/" + string(sd.FullName()) + "/" + string(md.Name())
	if err := h.conn.Invoke(tenantCtx(t), method, req.Interface(), out.New().Interface()); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	args, ok := h.calls.args(c.mock)
	if !ok {
		t.Fatalf("%s did not call %s", method, c.mock)
	}
	if c.tenantOnly {
		if args[0] != "acme" {
			t.Fatalf("tenant = %v, want acme", args[0])
		}
	} else if ref, ok := refOf(args); !ok || ref != (port.BankRef{Tenant: "acme", Bank: "x"}) {
		t.Fatalf("bank ref = %+v (found %v), want {acme x}", ref, ok)
	}
	if c.check != nil {
		if err := c.check(args); err != nil {
			t.Fatal(err)
		}
	}
}

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

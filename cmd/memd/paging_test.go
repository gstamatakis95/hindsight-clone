package main

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	pb "memd/gen/memory/v1"
)

// pagedBank is the bank TestListPaging seeds and lists.
const pagedBank = "paged"

// pagedMethod is one RPC that takes page_size.
type pagedMethod struct {
	// path is the method's full path, "/memory.v1.MemoryService/ListMemories".
	path string
	// md describes it.
	md protoreflect.MethodDescriptor
}

// TestListPaging: every RPC that takes page_size (enumerated from the descriptors, so a new list RPC is covered
// without editing this test) follows the paging convention on the wired stack. A negative page_size is
// INVALID_ARGUMENT naming page_size, a page_size over 1000 is served, a malformed page_token is INVALID_ARGUMENT
// naming page_token, and a walk one item per page ends, repeats nothing and lists exactly what one default page
// lists, in the same order.
func TestListPaging(t *testing.T) {
	w := startWired(t)
	ctx := w.ctx(t)
	obs := w.seedPaging(t, ctx)
	methods := pagedMethods()
	if len(methods) < 10 {
		t.Fatalf("found %d RPCs with page_size, want every list RPC", len(methods))
	}
	for _, m := range methods {
		t.Run(string(m.md.Name()), func(t *testing.T) {
			newReq := func(size int32) protoreflect.Message { return pagingRequest(m.md, obs, size) }
			w.checkPageBounds(t, ctx, m, newReq)
			if m.md.Input().Fields().ByName("page_token") != nil {
				w.checkPageWalk(t, ctx, m, newReq)
			}
		})
	}
}

// pagedMethods returns every memory.v1 RPC whose request has a page_size field.
func pagedMethods() []pagedMethod {
	var out []pagedMethod
	protoregistry.GlobalFiles.RangeFilesByPackage("memory.v1", func(fd protoreflect.FileDescriptor) bool {
		for i := range fd.Services().Len() {
			sd := fd.Services().Get(i)
			for j := range sd.Methods().Len() {
				md := sd.Methods().Get(j)
				if md.Input().Fields().ByName("page_size") != nil {
					out = append(out, pagedMethod{path: "/" + string(sd.FullName()) + "/" + string(md.Name()), md: md})
				}
			}
		}
		return true
	})
	return out
}

// seedPaging fills pagedBank (and the tenant's orgs and testOrg's banks) so that every listing has items: three
// orgs and banks, three tagged documents with their memories, observations and scopes, directives, two mental
// models with a refreshed version, and the operations of all that. It returns an observation id.
func (w *wiredStack) seedPaging(t *testing.T, ctx context.Context) string {
	t.Helper()
	for _, org := range []string{"paging-a", "paging-b"} {
		if _, err := pb.NewOrgServiceClient(w.conn).CreateOrg(ctx, &pb.CreateOrgRequest{OrgId: org}); err != nil {
			t.Fatal(err)
		}
	}
	w.bank(t, "paged-2")
	w.bank(t, "paged-3")
	w.retainTagged(t, ctx, "first", []string{"a"}, []string{"b"}, []string{"a", "b"})
	cfg := pb.NewConfigServiceClient(w.conn)
	for i := range 3 {
		if _, err := cfg.CreateDirective(ctx, &pb.CreateDirectiveRequest{OrgId: testOrg, BankId: pagedBank,
			Directive: &pb.Directive{Name: fmt.Sprintf("d%d", i), Rules: []string{"be brief"}, Active: true}},
		); err != nil {
			t.Fatal(err)
		}
	}
	mms := pb.NewMentalModelServiceClient(w.conn)
	for _, id := range []string{"pm1", "pm2"} {
		spec := &pb.MentalModelSpec{Id: id, Name: id, SourceQuery: "who works where?", Tags: []string{"a"}}
		if _, err := mms.CreateMentalModel(ctx, &pb.CreateMentalModelRequest{
			OrgId: testOrg, BankId: pagedBank, Spec: spec,
		}); err != nil {
			t.Fatal(err)
		}
	}
	w.idle(t, pagedBank)
	w.retainTagged(t, ctx, "second", []string{"a"}) // in pm1's scope, so its refresh writes a version
	if _, err := mms.RefreshMentalModel(ctx, &pb.RefreshMentalModelRequest{
		OrgId: testOrg, BankId: pagedBank, MentalModelId: "pm1",
	}); err != nil {
		t.Fatal(err)
	}
	w.idle(t, pagedBank)
	resp, err := pb.NewMemoryServiceClient(w.conn).ListMemories(ctx, &pb.ListMemoriesRequest{
		OrgId: testOrg, BankId: pagedBank, FactTypes: []pb.FactType{pb.FactType_FACT_TYPE_OBSERVATION},
	})
	if err != nil || len(resp.GetMemories()) == 0 {
		t.Fatalf("no observation after consolidation: %v", err)
	}
	return resp.GetMemories()[0].GetId()
}

// retainTagged retains one document per tag set into pagedBank, synchronously; batch names the call, so its
// document ids are new.
func (w *wiredStack) retainTagged(t *testing.T, ctx context.Context, batch string, tagSets ...[]string) {
	t.Helper()
	items := make([]*pb.RetainItem, len(tagSets))
	for i, tags := range tagSets {
		id := fmt.Sprintf("%s-%d", batch, i)
		items[i] = &pb.RetainItem{DocumentId: id, Content: "Person " + id + " works at the site.", Tags: tags}
	}
	if _, err := pb.NewMemoryServiceClient(w.conn).Retain(ctx, &pb.RetainRequest{
		OrgId: testOrg, BankId: pagedBank, Items: items,
	}); err != nil {
		t.Fatal(err)
	}
}

// pagingRequest builds md's request with page_size size, naming testOrg, pagedBank, mental model pm1 and
// observation obs wherever the request has those fields; a strategy preview gets two draft strategies.
func pagingRequest(md protoreflect.MethodDescriptor, obs string, size int32) protoreflect.Message {
	mt, err := protoregistry.GlobalTypes.FindMessageByName(md.Input().FullName())
	if err != nil {
		panic(err)
	}
	m := mt.New()
	set := func(name string, v protoreflect.Value) {
		if fd := m.Descriptor().Fields().ByName(protoreflect.Name(name)); fd != nil {
			m.Set(fd, v)
		}
	}
	set("org_id", protoreflect.ValueOfString(testOrg))
	set("bank_id", protoreflect.ValueOfString(pagedBank))
	set("mental_model_id", protoreflect.ValueOfString("pm1"))
	set("observation_id", protoreflect.ValueOfString(obs))
	set("page_size", protoreflect.ValueOfInt32(size))
	if r, ok := m.Interface().(*pb.PreviewConsolidationStrategiesRequest); ok {
		r.Strategies = []*pb.ConsolidationStrategy{
			{Name: "a", Scopes: []*pb.TagFilter{{Tags: []string{"a"}}}},
			{Name: "b", Scopes: []*pb.TagFilter{{Tags: []string{"b"}}}},
		}
	}
	return m
}

// call invokes m with req and returns the response.
func (w *wiredStack) call(ctx context.Context, m pagedMethod, req protoreflect.Message) (proto.Message, error) {
	mt, err := protoregistry.GlobalTypes.FindMessageByName(m.md.Output().FullName())
	if err != nil {
		return nil, err //nolint:wrapcheck // test helper
	}
	resp := mt.New().Interface()
	return resp, w.conn.Invoke(ctx, m.path, req.Interface(), resp) //nolint:wrapcheck // the status is the result
}

// checkPageBounds checks a negative page_size is INVALID_ARGUMENT naming page_size, a page_size over the maximum
// is served, and (when the RPC takes one) a malformed page_token is INVALID_ARGUMENT naming page_token.
func (w *wiredStack) checkPageBounds(
	t *testing.T, ctx context.Context, m pagedMethod, newReq func(int32) protoreflect.Message,
) {
	t.Helper()
	_, err := w.call(ctx, m, newReq(-1))
	wantField(t, "page_size -1", err, "page_size")
	if _, err := w.call(ctx, m, newReq(5000)); err != nil {
		t.Errorf("page_size 5000: %v, want served (capped at 1000)", err)
	}
	if fd := m.md.Input().Fields().ByName("page_token"); fd != nil {
		req := newReq(0)
		req.Set(fd, protoreflect.ValueOfString("!!not-a-token"))
		_, err := w.call(ctx, m, req)
		wantField(t, "malformed page_token", err, "page_token")
	}
}

// checkPageWalk walks the listing one item per page and compares it with one default page.
func (w *wiredStack) checkPageWalk(
	t *testing.T, ctx context.Context, m pagedMethod, newReq func(int32) protoreflect.Message,
) {
	t.Helper()
	resp, err := w.call(ctx, m, newReq(0))
	if err != nil {
		t.Fatalf("default page: %v", err)
	}
	want, next := pageItems(resp)
	if next != "" || len(want) == 0 {
		t.Fatalf("default page: %d items, next_page_token %q; want every seeded item on one page", len(want), next)
	}
	var got []string
	token := m.md.Input().Fields().ByName("page_token")
	for pages := 0; pages == 0 || next != ""; pages++ {
		if pages > len(want) {
			t.Fatalf("walk did not end after %d pages", pages)
		}
		req := newReq(1)
		req.Set(token, protoreflect.ValueOfString(next))
		if resp, err = w.call(ctx, m, req); err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		var items []string
		items, next = pageItems(resp)
		got = append(got, items...)
	}
	if _, ok := resp.(*pb.PreviewConsolidationStrategiesResponse); ok { // grouped per strategy, not per page
		slices.Sort(got)
		slices.Sort(want)
	}
	if !slices.Equal(got, want) {
		t.Errorf("walked %d items, want the default page's %d in its order:\n got %q\nwant %q", len(got), len(want),
			got, want)
	}
}

// pageItems returns a response's items, each as its deterministic encoding, and its next_page_token. The items
// are the elements of the response's first field; a strategy preview's are its (strategy, matched or shadowed
// scope) pairs, which accumulate over the pages.
func pageItems(resp proto.Message) ([]string, string) {
	if p, ok := resp.(*pb.PreviewConsolidationStrategiesResponse); ok {
		var out []string
		for _, m := range p.GetMatches() {
			for _, s := range m.GetMatchedScopes() {
				out = append(out, m.GetStrategy()+" matched "+s)
			}
			for _, s := range m.GetShadowedScopes() {
				out = append(out, m.GetStrategy()+" shadowed "+s)
			}
		}
		return out, p.GetNextPageToken()
	}
	m := resp.ProtoReflect()
	list := m.Get(m.Descriptor().Fields().ByNumber(1)).List()
	out := make([]string, list.Len())
	for i := range list.Len() {
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(list.Get(i).Message().Interface())
		if err != nil {
			panic(err)
		}
		out[i] = string(b)
	}
	next := m.Descriptor().Fields().ByName("next_page_token")
	return out, m.Get(next).String()
}

// wantField fails unless err is INVALID_ARGUMENT whose BadRequest names field.
func wantField(t *testing.T, what string, err error, field string) {
	t.Helper()
	st := status.Convert(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("%s: %v, want INVALID_ARGUMENT", what, err)
		return
	}
	for _, d := range st.Details() {
		if br, ok := d.(*errdetails.BadRequest); ok {
			for _, v := range br.GetFieldViolations() {
				if v.GetField() == field {
					return
				}
			}
		}
	}
	t.Errorf("%s: %v names no %s field violation", what, err, field)
}

package server_test

import (
	"context"
	"memd/internal/domain"
	"memd/internal/engine/recall"
	"memd/internal/server"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	mockbank "memd/internal/mocks/engine/bank"
	mockconsolidate "memd/internal/mocks/engine/consolidate"
	mockcurate "memd/internal/mocks/engine/curate"
	mockdocs "memd/internal/mocks/engine/docs"
	mockmentalmodel "memd/internal/mocks/engine/mentalmodel"
	mockrecall "memd/internal/mocks/engine/recall"
	mockreflect "memd/internal/mocks/engine/reflect"
	mockretain "memd/internal/mocks/engine/retain"
	mockport "memd/internal/mocks/port"
	mockserver "memd/internal/mocks/server"
	mockworker "memd/internal/mocks/worker"

	pb "memd/gen/memory/v1"
)

// counter records how often each mocked method ran and the arguments of its last call.
type counter struct {
	// mu guards n and last.
	mu sync.Mutex
	// n counts calls per "Mock.Method".
	n map[string]int
	// last holds the arguments (after ctx) of the latest call per "Mock.Method".
	last map[string][]any
}

// reset forgets every recorded call.
func (c *counter) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n = map[string]int{}
	c.last = map[string][]any{}
}

// args returns the arguments of the latest call of key and whether it was called.
func (c *counter) args(key string) ([]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.last[key]
	return a, ok
}

// total returns the number of calls so far.
func (c *counter) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	sum := 0
	for _, v := range c.n {
		sum += v
	}
	return sum
}

// stub sets every nil XxxFunc field of the mock struct m to a function that counts the call and returns
// zero values, so any handler can run against it.
func (c *counter) stub(m any) {
	v := reflect.ValueOf(m).Elem()
	for i := range v.NumField() {
		f := v.Field(i)
		name := v.Type().Field(i).Name
		if f.Kind() != reflect.Func || !f.IsNil() || !strings.HasSuffix(name, "Func") {
			continue
		}
		key := v.Type().Name() + "." + strings.TrimSuffix(name, "Func")
		ft := f.Type()
		f.Set(reflect.MakeFunc(ft, func(in []reflect.Value) []reflect.Value {
			args := make([]any, 0, len(in))
			for _, a := range in[1:] {
				args = append(args, a.Interface())
			}
			c.mu.Lock()
			c.n[key]++
			c.last[key] = args
			c.mu.Unlock()
			out := make([]reflect.Value, ft.NumOut())
			for j := range out {
				out[j] = reflect.Zero(ft.Out(j))
			}
			return out
		}))
	}
}

// harness is a memd server on bufconn with every dependency mocked.
type harness struct {
	// conn is the client connection.
	conn *grpc.ClientConn
	// calls counts mock calls.
	calls *counter
	// srv is the server, for GetServiceInfo.
	srv *grpc.Server
	// recaller is the Recall mock, for argument checks.
	recaller *mockrecall.Recaller
	// probe is the readiness probe mock.
	probe *mockserver.ReadinessProbe
}

// newHarness starts the server with zero-value mocks for every dependency.
func newHarness(t *testing.T, opts ...func(*server.Deps)) *harness {
	t.Helper()
	h := &harness{calls: &counter{n: map[string]int{}, last: map[string][]any{}}}
	h.recaller = &mockrecall.Recaller{}
	h.probe = &mockserver.ReadinessProbe{}
	d := server.Deps{
		Ingestor: &mockretain.Ingestor{}, Recaller: h.recaller, Reflector: &mockreflect.Reflector{},
		Lister: &mockcurate.MemoryLister{}, Curator: &mockcurate.Curator{}, Stats: &mockbank.StatsReader{},
		Previewer: &mockbank.PromptPreviewer{}, Deleter: &mockbank.BankDeleter{}, Templates: &mockbank.TemplateCodec{},
		Retagger: &mockdocs.Retagger{}, Strategies: &mockconsolidate.StrategyMatcher{},
		Refresher: &mockmentalmodel.RefreshRunner{}, Pages: &mockmentalmodel.PageSearcher{},
		Control: &mockworker.OperationControl{}, Config: &mockport.ConfigResolver{},
		Tenants: &mockserver.TenantAdmin{}, Banks: &mockserver.BankAdmin{}, Directives: &mockserver.DirectiveAdmin{},
		Documents: &mockserver.DocumentAdmin{}, Observations: &mockserver.ObservationAdmin{},
		MentalModels: &mockserver.MentalModelAdmin{}, Operations: &mockserver.OperationReader{},
		Probes: []server.ReadinessProbe{h.probe},
	}
	for _, m := range []any{
		d.Ingestor, d.Recaller, d.Reflector, d.Lister, d.Curator, d.Stats, d.Previewer, d.Deleter, d.Templates,
		d.Retagger, d.Strategies, d.Refresher, d.Pages, d.Control, d.Config, d.Tenants, d.Banks, d.Directives,
		d.Documents, d.Observations, d.MentalModels, d.Operations, h.probe,
	} {
		h.calls.stub(m)
	}
	for _, o := range opts {
		o(&d)
	}
	h.srv = server.New(d)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = h.srv.Serve(lis) }()
	t.Cleanup(h.srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	h.conn = conn
	return h
}

// tenantCtx returns a context carrying the x-tenant-id header.
func tenantCtx(t *testing.T) context.Context {
	return metadata.AppendToOutgoingContext(t.Context(), server.TenantHeader, "acme")
}

// fillRequest sets every id-like string field to "x" and every singular message field to an empty
// message, so required-field checks pass with otherwise zero requests.
func fillRequest(m protoreflect.Message) {
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		switch {
		case fd.IsList() || fd.IsMap():
		case fd.Kind() == protoreflect.StringKind && (strings.HasSuffix(string(fd.Name()), "id") ||
			strings.HasSuffix(string(fd.Name()), "_path")):
			m.Set(fd, protoreflect.ValueOfString("x"))
		case fd.Message() != nil && fd.ContainingOneof() == nil:
			m.Set(fd, protoreflect.ValueOfMessage(m.NewField(fd).Message()))
		}
	}
}

// TestEveryRPCReachable calls every memory.v1 RPC (enumerated from the service descriptors) over bufconn
// and checks it succeeds and reaches a mock.
func TestEveryRPCReachable(t *testing.T) {
	h := newHarness(t)
	n := 0
	protoregistry.GlobalFiles.RangeFilesByPackage("memory.v1", func(fd protoreflect.FileDescriptor) bool {
		for i := range fd.Services().Len() {
			sd := fd.Services().Get(i)
			for j := range sd.Methods().Len() {
				md := sd.Methods().Get(j)
				n++
				t.Run(string(md.FullName()), func(t *testing.T) { callRPC(t, h, sd, md) })
			}
		}
		return true
	})
	if n != 48 {
		t.Fatalf("found %d RPCs, want 48", n)
	}
}

// callRPC invokes one method with a filled request and checks the outcome.
func callRPC(t *testing.T, h *harness, sd protoreflect.ServiceDescriptor, md protoreflect.MethodDescriptor) {
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
	before := h.calls.total()
	method := "/" + string(sd.FullName()) + "/" + string(md.Name())
	err = h.conn.Invoke(tenantCtx(t), method, req.Interface(), out.New().Interface())
	if code := status.Code(err); code != codes.OK {
		t.Fatalf("%s: %v", method, err)
	}
	if h.calls.total() == before {
		t.Fatalf("%s reached no mock", method)
	}
}

// TestTenantReachesEngine checks the header's tenant and the request's bank reach the engine.
func TestTenantReachesEngine(t *testing.T) {
	h := newHarness(t)
	var got recall.RecallRequest
	h.recaller.RecallFunc = func(_ context.Context, req recall.RecallRequest) (recall.RecallResult, error) {
		got = req
		return recall.RecallResult{}, domain.ErrNotFound
	}
	_, err := pb.NewMemoryServiceClient(h.conn).Recall(tenantCtx(t), &pb.RecallRequest{BankId: "main", Query: "q"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("err = %v, want NOT_FOUND", err)
	}
	if got.Bank.Tenant != "acme" || got.Bank.Bank != "main" || got.Query != "q" {
		t.Fatalf("engine got %+v", got)
	}
	_, err = pb.NewMemoryServiceClient(h.conn).Recall(t.Context(), &pb.RecallRequest{BankId: "main"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no tenant: err = %v, want INVALID_ARGUMENT", err)
	}
}

// TestHealthWithoutTenant checks health answers without a tenant header and reflects the probes.
func TestHealthWithoutTenant(t *testing.T) {
	h := newHarness(t)
	client := healthpb.NewHealthClient(h.conn)
	resp, err := client.Check(t.Context(), &healthpb.HealthCheckRequest{})
	if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("check = %v, %v", resp, err)
	}
	h.probe.ReadyFunc = func(context.Context) error { return domain.ErrUnavailable }
	resp, err = client.Check(t.Context(), &healthpb.HealthCheckRequest{})
	if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("failing probe: check = %v, %v", resp, err)
	}
}

// TestServiceInfo checks the eight memory.v1 services and health are registered.
func TestServiceInfo(t *testing.T) {
	h := newHarness(t)
	info := h.srv.GetServiceInfo()
	want := []string{
		"memory.v1.MemoryService", "memory.v1.MemoryUnitService", "memory.v1.TenantService",
		"memory.v1.BankService", "memory.v1.DocumentService", "memory.v1.ObservationService",
		"memory.v1.MentalModelService", "memory.v1.OperationService", "grpc.health.v1.Health",
	}
	for _, s := range want {
		if _, ok := info[s]; !ok {
			t.Errorf("service %s not registered", s)
		}
	}
	if len(info) != len(want) {
		t.Errorf("registered %d services, want %d", len(info), len(want))
	}
}

// TestUnwiredDependency checks a nil dependency is UNIMPLEMENTED, not a panic.
func TestUnwiredDependency(t *testing.T) {
	srv := server.New(server.Deps{})
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_, err = pb.NewMemoryServiceClient(conn).Recall(tenantCtx(t), &pb.RecallRequest{BankId: "main"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("err = %v, want UNIMPLEMENTED", err)
	}
}

package grpcapi

import (
	"context"
	"memd/internal/domain"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "memd/gen/memory/v1"
)

// TestTenantInterceptor checks header handling: exactly one valid x-tenant-id, health exempt.
func TestTenantInterceptor(t *testing.T) {
	icpt := TenantInterceptor()
	tests := []struct {
		name   string
		method string
		md     metadata.MD
		want   codes.Code
		tenant string
	}{
		{"ok", "/memory.v1.MemoryService/Recall", metadata.Pairs(TenantHeader, "acme"), codes.OK, "acme"},
		{"missing", "/memory.v1.MemoryService/Recall", nil, codes.InvalidArgument, ""},
		{"empty", "/memory.v1.MemoryService/Recall", metadata.Pairs(TenantHeader, ""), codes.InvalidArgument, ""},
		{
			"repeated", "/memory.v1.MemoryService/Recall",
			metadata.Pairs(TenantHeader, "a", TenantHeader, "b"), codes.InvalidArgument, "",
		},
		{"slash", "/memory.v1.MemoryService/Recall", metadata.Pairs(TenantHeader, "a/b"), codes.InvalidArgument, ""},
		{
			"control char", "/memory.v1.MemoryService/Recall",
			metadata.Pairs(TenantHeader, "a\nb"), codes.InvalidArgument, "",
		},
		{"DEL", "/memory.v1.MemoryService/Recall", metadata.Pairs(TenantHeader, "a\x7fb"), codes.InvalidArgument, ""},
		{
			"exactly 256 bytes", "/memory.v1.MemoryService/Recall",
			metadata.Pairs(TenantHeader, strings.Repeat("x", 256)), codes.OK, strings.Repeat("x", 256),
		},
		{
			"too long", "/memory.v1.MemoryService/Recall",
			metadata.Pairs(TenantHeader, strings.Repeat("x", 257)), codes.InvalidArgument, "",
		},
		{"health needs none", "/grpc.health.v1.Health/Check", nil, codes.OK, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.md != nil {
				ctx = metadata.NewIncomingContext(ctx, tt.md)
			}
			var seen string
			_, err := icpt(ctx, nil, &grpc.UnaryServerInfo{FullMethod: tt.method},
				func(ctx context.Context, _ any) (any, error) {
					seen = TenantFrom(ctx)
					return nil, nil
				})
			if got := status.Code(err); got != tt.want {
				t.Fatalf("code = %v, want %v (%v)", got, tt.want, err)
			}
			if seen != tt.tenant {
				t.Fatalf("tenant = %q, want %q", seen, tt.tenant)
			}
		})
	}
}

// TestDeadlineInterceptor checks the default applies only to calls without a deadline.
func TestDeadlineInterceptor(t *testing.T) {
	info := &grpc.UnaryServerInfo{FullMethod: "/memory.v1.MemoryService/Recall"}
	remaining := func(ctx context.Context, def time.Duration) (time.Duration, bool) {
		var left time.Duration
		var has bool
		_, _ = DeadlineInterceptor(def)(ctx, nil, info, func(ctx context.Context, _ any) (any, error) {
			var dl time.Time
			dl, has = ctx.Deadline()
			left = time.Until(dl)
			return nil, nil
		})
		return left, has
	}
	if left, ok := remaining(context.Background(), time.Minute); !ok || left > time.Minute || left < 50*time.Second {
		t.Fatalf("default not applied: %v %v", left, ok)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	if left, ok := remaining(ctx, time.Minute); !ok || left < 59*time.Minute {
		t.Fatalf("caller deadline replaced: %v %v", left, ok)
	}
	if _, ok := remaining(context.Background(), 0); ok {
		t.Fatal("zero default must not set a deadline")
	}
}

// TestPagingInterceptor checks a negative page_size or max_results is INVALID_ARGUMENT naming the field, and 0
// (the default) passes.
func TestPagingInterceptor(t *testing.T) {
	info := &grpc.UnaryServerInfo{FullMethod: "/memory.v1.X/Y"}
	tests := []struct {
		req   proto.Message
		field string
	}{
		{&pb.ListObservationScopesRequest{PageSize: -1}, "page_size"},
		{&pb.ListEventsRequest{PageSize: -1}, "page_size"},
		{&pb.SearchMentalModelsRequest{MaxResults: -1}, "max_results"},
		{&pb.ListObservationScopesRequest{}, ""},
		{&pb.ListEventsRequest{PageSize: 5}, ""},
	}
	for _, tc := range tests {
		_, err := pagingInterceptor()(context.Background(), tc.req, info,
			func(context.Context, any) (any, error) { return nil, nil })
		if tc.field == "" {
			if err != nil {
				t.Errorf("%v: %v, want OK", tc.req, err)
			}
			continue
		}
		if got := status.Code(err); got != codes.InvalidArgument {
			t.Errorf("%v: code = %v, want INVALID_ARGUMENT", tc.req, got)
		}
		if got := fieldsOf(err); len(got) != 1 || got[0] != tc.field {
			t.Errorf("%v: field violations %v, want [%s]", tc.req, got, tc.field)
		}
	}
}

// TestResponseSizeInterceptor: a read whose response passes the limit fails with RESOURCE_EXHAUSTED
// (RESPONSE_TOO_LARGE); a write with the same response, or a read within the limit, passes untouched.
func TestResponseSizeInterceptor(t *testing.T) {
	big := &pb.ExportBankTemplateResponse{Template: &pb.BankTemplate{
		Directives: []*pb.Directive{{Name: "d", Rules: []string{strings.Repeat("r", 2000)}}},
	}}
	tests := []struct {
		method string
		limit  int
		want   codes.Code
	}{
		{"/memory.v1.BankService/ExportBankTemplate", 1000, codes.ResourceExhausted},
		{"/memory.v1.MemoryService/Recall", 1000, codes.ResourceExhausted},
		{"/memory.v1.BankService/ExportBankTemplate", 4000, codes.OK},
		{"/memory.v1.BankService/ExportBankTemplate", 0, codes.OK}, // no limit
		{"/memory.v1.ConfigService/CreateDirective", 1000, codes.OK},
	}
	for _, tc := range tests {
		resp, err := responseSizeInterceptor(tc.limit)(context.Background(), nil,
			&grpc.UnaryServerInfo{FullMethod: tc.method}, func(context.Context, any) (any, error) { return big, nil })
		if got := status.Code(err); got != tc.want || (err == nil) != (resp != nil) {
			t.Errorf("%s limit %d: code %v, response %v; want %v", tc.method, tc.limit, got, resp != nil, tc.want)
		}
		if tc.want == codes.ResourceExhausted {
			if r := errorInfo(t, status.Convert(err)).GetReason(); r != "RESPONSE_TOO_LARGE" {
				t.Errorf("%s: reason %q, want RESPONSE_TOO_LARGE", tc.method, r)
			}
		}
	}
}

// ref is a bank-scoped request for bankRef tests.
type ref struct{ org, bank string }

// GetOrgId returns the org id.
func (r ref) GetOrgId() string { return r.org }

// GetBankId returns the bank id.
func (r ref) GetBankId() string { return r.bank }

// TestBankRef checks the org and bank id rules: both are required and follow checkName, and the violation names
// the field.
func TestBankRef(t *testing.T) {
	ctx := WithTenant(context.Background(), "acme")
	if b, err := bankRef(ctx, ref{"o", "main"}); err != nil || b != domain.NewBankRef("acme", "o", "main") {
		t.Fatalf("bankRef = %v, %v", b, err)
	}
	for _, id := range []string{"", "a/b", "a\x00", "a\x7f", strings.Repeat("b", 257)} {
		_, err := bankRef(ctx, ref{"o", id})
		if got := fieldsOf(toStatus(err)); status.Code(toStatus(err)) != codes.InvalidArgument || len(got) != 1 ||
			got[0] != "bank_id" {
			t.Fatalf("bank %q: err = %v, want INVALID_ARGUMENT on bank_id", id, err)
		}
		_, err = bankRef(ctx, ref{id, "main"})
		if got := fieldsOf(toStatus(err)); status.Code(toStatus(err)) != codes.InvalidArgument || len(got) != 1 ||
			got[0] != "org_id" {
			t.Fatalf("org %q: err = %v, want INVALID_ARGUMENT on org_id", id, err)
		}
	}
	if _, err := bankRef(context.Background(), ref{"o", "main"}); status.Code(toStatus(err)) != codes.InvalidArgument {
		t.Fatalf("no tenant: err = %v", err)
	}
}

// TestCheckNameRejectsDotIDs: "." and ".." are rejected as tenant (header) and bank ids;
// names that merely contain dots are fine.
func TestCheckNameRejectsDotIDs(t *testing.T) {
	acme := WithTenant(context.Background(), "acme")
	for _, id := range []string{".", ".."} {
		if _, err := bankRef(acme, ref{"o", id}); status.Code(toStatus(err)) != codes.InvalidArgument {
			t.Errorf("bank %q: err = %v, want INVALID_ARGUMENT", id, err)
		}
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(TenantHeader, id))
		_, err := TenantInterceptor()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/memory.v1.X/Y"},
			func(context.Context, any) (any, error) { return nil, nil })
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("tenant header %q: err = %v, want INVALID_ARGUMENT", id, err)
		}
	}
	for _, id := range []string{"...", ".a", "a.b", "a.."} {
		if _, err := bankRef(acme, ref{"o", id}); err != nil {
			t.Errorf("bank %q rejected: %v", id, err)
		}
	}
}

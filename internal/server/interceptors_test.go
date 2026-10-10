package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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

// TestBankRef checks the bank id rules.
func TestBankRef(t *testing.T) {
	ctx := WithTenant(context.Background(), "acme")
	if b, err := bankRef(ctx, "main"); err != nil || b.Tenant != "acme" || b.Bank != "main" {
		t.Fatalf("bankRef = %v, %v", b, err)
	}
	for _, id := range []string{"", "a/b", "a\x00", "a\x7f", strings.Repeat("b", 257)} {
		if _, err := bankRef(ctx, id); status.Code(toStatus(err)) != codes.InvalidArgument {
			t.Fatalf("bank %q: err = %v, want INVALID_ARGUMENT", id, err)
		}
	}
	if _, err := bankRef(context.Background(), "main"); status.Code(toStatus(err)) != codes.InvalidArgument {
		t.Fatalf("no tenant: err = %v", err)
	}
}

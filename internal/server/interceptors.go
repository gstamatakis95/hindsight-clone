package server

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// TenantHeader is the metadata key that carries the tenant id. Auth is out of scope: the tenant is never
// read from the request body.
const TenantHeader = "x-tenant-id"

// healthPrefix is the method prefix of grpc.health.v1, which k8s probes call without metadata.
const healthPrefix = "/grpc.health.v1.Health/"

// tenantKey is the context key holding the tenant id.
type tenantKey struct{}

// TenantFrom returns the tenant id TenantInterceptor stored in ctx ("" when there is none).
func TenantFrom(ctx context.Context) string {
	t, _ := ctx.Value(tenantKey{}).(string)
	return t
}

// WithTenant returns ctx carrying tenant, as TenantInterceptor does.
func WithTenant(ctx context.Context, tenant string) context.Context {
	return context.WithValue(ctx, tenantKey{}, tenant)
}

// TenantInterceptor reads the tenant from the x-tenant-id header into the context. A missing, empty,
// repeated or malformed header fails with INVALID_ARGUMENT (decision 600). Health checks need no tenant.
func TenantInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		if strings.HasPrefix(info.FullMethod, healthPrefix) {
			return handler(ctx, req)
		}
		md, _ := metadata.FromIncomingContext(ctx)
		vals := md.Get(TenantHeader)
		if len(vals) != 1 {
			return nil, status.Errorf(codes.InvalidArgument, "exactly one %s header is required", TenantHeader)
		}
		if err := checkName("tenant", vals[0]); err != nil {
			return nil, toStatus(err)
		}
		return handler(WithTenant(ctx, vals[0]), req)
	}
}

// DeadlineInterceptor gives a call that arrives without a deadline the default one; def <= 0 = none.
func DeadlineInterceptor(def time.Duration) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		if _, ok := ctx.Deadline(); ok || def <= 0 {
			return handler(ctx, req)
		}
		ctx, cancel := context.WithTimeout(ctx, def)
		defer cancel()
		return handler(ctx, req)
	}
}

// maxNameLen bounds tenant and bank ids.
const maxNameLen = 256

// checkName validates a tenant or bank id: non-empty, at most 256 bytes, and free of '/' and control
// characters. Blob keys are "{tenant}/{bank}/...", so a '/' would let one bank's prefix cover another's.
func checkName(what, v string) error {
	if v == "" {
		return fmt.Errorf("%w: %s is required", domain.ErrInvalidArgument, what)
	}
	if len(v) > maxNameLen {
		return fmt.Errorf("%w: %s is longer than %d bytes", domain.ErrInvalidArgument, what, maxNameLen)
	}
	if strings.ContainsFunc(v, func(r rune) bool { return r == '/' || r < 0x20 || r == 0x7f }) {
		return fmt.Errorf("%w: %s %q contains '/' or a control character", domain.ErrInvalidArgument, what, v)
	}
	return nil
}

// bankRef builds the BankRef of a request from the context's tenant and the request's bank id.
func bankRef(ctx context.Context, bankID string) (port.BankRef, error) {
	tenant := TenantFrom(ctx)
	if err := checkName("tenant", tenant); err != nil {
		return port.BankRef{}, err
	}
	if err := checkName("bank_id", bankID); err != nil {
		return port.BankRef{}, err
	}
	return port.BankRef{Tenant: tenant, Bank: bankID}, nil
}

// requireID rejects an empty id field.
func requireID(field, v string) error {
	if v == "" {
		return fmt.Errorf("%w: %s is required", domain.ErrInvalidArgument, field)
	}
	return nil
}

// pagingInterceptor rejects a request whose page_size or limit is negative (INVALID_ARGUMENT); 0 keeps
// meaning "the server default".
func pagingInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		if m, ok := req.(proto.Message); ok {
			if err := checkPaging(m.ProtoReflect()); err != nil {
				return nil, toStatus(err)
			}
		}
		return handler(ctx, req)
	}
}

// checkPaging returns ErrInvalidArgument when m's page_size or limit field is negative.
func checkPaging(m protoreflect.Message) error {
	for _, name := range []protoreflect.Name{"page_size", "limit"} {
		fd := m.Descriptor().Fields().ByName(name)
		if fd != nil && fd.Kind() == protoreflect.Int32Kind && m.Get(fd).Int() < 0 {
			return fmt.Errorf("%w: %s must not be negative", domain.ErrInvalidArgument, name)
		}
	}
	return nil
}

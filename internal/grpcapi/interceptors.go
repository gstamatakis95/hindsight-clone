package grpcapi

import (
	"context"
	"memd/internal/config"
	"memd/internal/domain"
	"strings"
	"time"

	pb "memd/gen/memory/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protopath"
	"google.golang.org/protobuf/reflect/protorange"
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
// repeated or malformed header fails with INVALID_ARGUMENT naming the x-tenant-id field.
// Health checks need no tenant.
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
			return nil, toStatus(domain.InvalidField(TenantHeader, "exactly one %s header is required", TenantHeader))
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

// maxNameLen bounds tenant, org and bank ids.
const maxNameLen = 256

// checkName validates a tenant, org or bank id: non-empty, not "." or "..", at most 256 bytes, and free of '/'
// and control characters. Blob keys are "{tenant}/{org}/{bank}/...", so a '/' would let one org's or bank's prefix
// cover another's, and a dot segment would make the key a relative path on stores or tools that normalize it. The
// error names the request field: org_id, bank_id, or x-tenant-id for the tenant.
func checkName(what, v string) error {
	field := what
	if what == "tenant" {
		field = TenantHeader
	}
	switch {
	case v == "":
		return domain.InvalidField(field, "%s is required", what)
	case v == "." || v == "..":
		return domain.InvalidField(field, "%s must not be %q", what, v)
	case len(v) > maxNameLen:
		return domain.InvalidField(field, "%s is longer than %d bytes", what, maxNameLen)
	case strings.ContainsFunc(v, func(r rune) bool { return r == '/' || r < 0x20 || r == 0x7f }):
		return domain.InvalidField(field, "%s %q contains '/' or a control character", what, v)
	}
	return nil
}

// bankScoped is a request that names a bank: every request with org_id and bank_id fields.
type bankScoped interface {
	GetOrgId() string
	GetBankId() string
}

// orgScoped is a request that names an org.
type orgScoped interface {
	GetOrgId() string
}

// orgRef builds the OrgRef of a request from the context's tenant and the request's org id.
func orgRef(ctx context.Context, r orgScoped) (domain.OrgRef, error) {
	tenant := TenantFrom(ctx)
	if err := checkName("tenant", tenant); err != nil {
		return domain.OrgRef{}, err
	}
	if err := checkName("org_id", r.GetOrgId()); err != nil {
		return domain.OrgRef{}, err
	}
	return domain.OrgRef{Tenant: tenant, Org: r.GetOrgId()}, nil
}

// bankRef builds the BankRef of a request from the context's tenant and the request's org and bank ids.
func bankRef(ctx context.Context, r bankScoped) (domain.BankRef, error) {
	o, err := orgRef(ctx, r)
	if err != nil {
		return domain.BankRef{}, err
	}
	if err := checkName("bank_id", r.GetBankId()); err != nil {
		return domain.BankRef{}, err
	}
	return domain.NewBankRef(o.Tenant, o.Org, r.GetBankId()), nil
}

// requireID rejects an empty id field.
func requireID(field, v string) error {
	if v == "" {
		return domain.InvalidField(field, "%s is required", field)
	}
	return nil
}

// pagingInterceptor rejects a request whose page_size or max_results is negative (INVALID_ARGUMENT naming the
// field); 0 keeps meaning "the default" (domain.ListPage, domain.SearchResults). The services check again, so
// this is the API's guard, not the only one.
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

// checkPaging returns an ErrInvalidArgument naming the field when m's page_size or max_results is negative.
func checkPaging(m protoreflect.Message) error {
	for _, name := range []protoreflect.Name{"page_size", "max_results"} {
		fd := m.Descriptor().Fields().ByName(name)
		if fd != nil && fd.Kind() == protoreflect.Int32Kind && m.Get(fd).Int() < 0 {
			return domain.InvalidField(string(name), "%s must not be negative", name)
		}
	}
	return nil
}

// armNamesInterceptor rejects a request holding a RecallConfig anywhere (a config level or patch, a bank
// template, a mental model's overrides, a request's overrides) whose recall.arms or recall.arm_boosts name an arm
// outside registered (config.CheckArms; INVALID_ARGUMENT). No registered arms turns the check off. The
// request-level arms and min_scores.arms are checked by the recall engine.
func armNamesInterceptor(registered []string) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		if m, ok := req.(proto.Message); ok && len(registered) > 0 {
			if err := checkArmNames(m.ProtoReflect(), registered); err != nil {
				return nil, toStatus(err)
			}
		}
		return handler(ctx, req)
	}
}

// checkArmNames runs config.CheckArms on every RecallConfig inside m.
func checkArmNames(m protoreflect.Message, registered []string) error {
	check := func(v protopath.Values) error {
		msg, ok := v.Index(-1).Value.Interface().(protoreflect.Message)
		if !ok {
			return nil
		}
		rc, ok := msg.Interface().(*pb.RecallConfig)
		if !ok {
			return nil
		}
		cfg := domain.BankConfig{Recall: &domain.RecallConfig{Arms: rc.GetArms(), ArmBoosts: rc.GetArmBoosts()}}
		return config.CheckArms(cfg, registered)
	}
	return protorange.Range(m, check) //nolint:wrapcheck // check returns config.CheckArms's domain error class
}

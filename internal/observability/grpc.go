package observability

import (
	"context"

	grpctrace "github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"google.golang.org/grpc"
)

// untracedMethods are probes and tooling that would flood traces: k8s health checks and grpcurl's
// reflection.
var untracedMethods = []string{
	"/grpc.health.v1.Health/Check",
	"/grpc.health.v1.Health/Watch",
	"/grpc.health.v1.Health/List",
	"/grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
	"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo",
}

// GRPCServerOptions returns the server options that trace every RPC except health and reflection: one
// "grpc.server" span per call (resource = the full method, error = any non-OK status but CANCELLED), the
// outermost interceptor so calls the tenant check rejects are traced too, plus an inner interceptor that
// tags the span with the tenant (from tenant, e.g. server.TenantFrom) and the request's bank_id. Request
// bodies and metadata are never tagged. Disabled, it returns nil.
//
// The options set grpc.UnaryInterceptor and grpc.StreamInterceptor, so the server must not set those
// itself (server.New only chains).
func (t *Telemetry) GRPCServerOptions(tenant func(context.Context) string) []grpc.ServerOption {
	if t == nil {
		return nil
	}
	untraced := grpctrace.WithUntracedMethods(untracedMethods...)
	return []grpc.ServerOption{
		grpc.UnaryInterceptor(grpctrace.UnaryServerInterceptor(untraced)),
		grpc.StreamInterceptor(grpctrace.StreamServerInterceptor(untraced, grpctrace.WithStreamMessages(false))),
		grpc.ChainUnaryInterceptor(tagInterceptor(tenant)),
	}
}

// bankRequest is any request with a bank_id field.
type bankRequest interface {
	// GetBankId returns the request's bank id.
	GetBankId() string
}

// tagInterceptor tags the RPC's span with the tenant and bank id. It runs after the tenant interceptor,
// so the tenant is the validated one.
func tagInterceptor(tenant func(context.Context) string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if span, ok := tracer.SpanFromContext(ctx); ok {
			if tenant != nil {
				if v := tenant(ctx); v != "" {
					span.SetTag(TagTenant, v)
				}
			}
			if r, ok := req.(bankRequest); ok && r.GetBankId() != "" {
				span.SetTag(TagBank, r.GetBankId())
			}
		}
		return handler(ctx, req)
	}
}

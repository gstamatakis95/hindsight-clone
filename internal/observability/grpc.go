package observability

import (
	"context"

	grpctrace "github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
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
// "grpc.server" span per call (resource = the full method; error = any non-OK status but CANCELLED, with only
// the status code as message, since status messages can quote gateway bodies or model output), the
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
		grpc.UnaryInterceptor(codeOnlyErrors(grpctrace.UnaryServerInterceptor(untraced))),
		grpc.StreamInterceptor(grpctrace.StreamServerInterceptor(untraced, grpctrace.WithStreamMessages(false))),
		grpc.ChainUnaryInterceptor(tagInterceptor(tenant)),
	}
}

// codeOnlyErrors wraps the tracing interceptor dd so the span records only a failed call's status code
// (its message can quote a gateway response body or model output), while the client still receives the
// handler's full status.
func codeOnlyErrors(dd grpc.UnaryServerInterceptor) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		var real error
		called := false
		resp, err := dd(ctx, req, info, func(ctx context.Context, req any) (any, error) {
			resp, err := handler(ctx, req)
			called, real = true, err
			if err == nil {
				return resp, nil
			}
			c := status.Code(err)
			return resp, status.Error(c, c.String())
		})
		if !called { // the interceptor answered itself (e.g. Datadog AppSec blocking the call)
			return resp, err
		}
		return resp, real
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

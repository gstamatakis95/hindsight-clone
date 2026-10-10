package server

import (
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	pb "memd/gen/memory/v1"
)

// New returns a gRPC server with all eight memory.v1 services and grpc.health.v1 registered over d. The
// tenant and default-deadline interceptors run first, in that order; opts are appended (more interceptors
// chain after them).
func New(d Deps, opts ...grpc.ServerOption) *grpc.Server {
	base := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(TenantInterceptor(), DeadlineInterceptor(d.DefaultDeadline)),
	}
	s := grpc.NewServer(append(base, opts...)...)
	pb.RegisterMemoryServiceServer(s, newMemoryServer(d))
	pb.RegisterMemoryUnitServiceServer(s, newMemoryUnitServer(d))
	pb.RegisterTenantServiceServer(s, newTenantServer(d))
	pb.RegisterBankServiceServer(s, newBankServer(d))
	pb.RegisterDocumentServiceServer(s, newDocumentServer(d))
	pb.RegisterObservationServiceServer(s, newObservationServer(d))
	pb.RegisterMentalModelServiceServer(s, newMentalModelServer(d))
	pb.RegisterOperationServiceServer(s, newOperationServer(d))
	healthpb.RegisterHealthServer(s, newHealthServer(d.Probes))
	return s
}

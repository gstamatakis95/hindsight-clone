package grpcapi

import (
	"time"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	pb "memd/gen/memory/v1"
)

// Handlers are the memory.v1 service handlers New registers, one per service. A nil handler leaves its service
// unregistered, so its RPCs answer UNIMPLEMENTED.
type Handlers struct {
	// Memory serves MemoryService.
	Memory *MemoryHandler
	// MemoryUnit serves MemoryUnitService.
	MemoryUnit *MemoryUnitHandler
	// Tenant serves TenantService.
	Tenant *TenantHandler
	// Bank serves BankService.
	Bank *BankHandler
	// Document serves DocumentService.
	Document *DocumentHandler
	// Observation serves ObservationService.
	Observation *ObservationHandler
	// MentalModel serves MentalModelService.
	MentalModel *MentalModelHandler
	// Operation serves OperationService.
	Operation *OperationHandler
}

// Options are the server-wide settings.
type Options struct {
	// DefaultDeadline applies to calls that arrive without a deadline; 0 = none.
	DefaultDeadline time.Duration
	// Probes are the readiness checks (Postgres, gateway) behind grpc.health.v1 Check.
	Probes []ReadinessProbe
	// RecallArms are the registered recall arm names (recall.Engine.Arms): a RecallConfig in a request may name
	// only these. Empty = unchecked.
	RecallArms []string
}

// New returns a gRPC server with the memory.v1 services of h and grpc.health.v1 registered. The tenant,
// default-deadline, paging (negative page_size / limit) and arm-name (RecallConfig names outside o.RecallArms)
// interceptors run first, in that order; opts are appended (more interceptors chain after them).
func New(h Handlers, o Options, opts ...grpc.ServerOption) *grpc.Server {
	base := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(
			TenantInterceptor(), DeadlineInterceptor(o.DefaultDeadline), pagingInterceptor(),
			armNamesInterceptor(o.RecallArms),
		),
	}
	s := grpc.NewServer(append(base, opts...)...)
	if h.Memory != nil {
		pb.RegisterMemoryServiceServer(s, h.Memory)
	}
	if h.MemoryUnit != nil {
		pb.RegisterMemoryUnitServiceServer(s, h.MemoryUnit)
	}
	if h.Tenant != nil {
		pb.RegisterTenantServiceServer(s, h.Tenant)
	}
	if h.Bank != nil {
		pb.RegisterBankServiceServer(s, h.Bank)
	}
	if h.Document != nil {
		pb.RegisterDocumentServiceServer(s, h.Document)
	}
	if h.Observation != nil {
		pb.RegisterObservationServiceServer(s, h.Observation)
	}
	if h.MentalModel != nil {
		pb.RegisterMentalModelServiceServer(s, h.MentalModel)
	}
	if h.Operation != nil {
		pb.RegisterOperationServiceServer(s, h.Operation)
	}
	healthpb.RegisterHealthServer(s, newHealthServer(o.Probes))
	return s
}

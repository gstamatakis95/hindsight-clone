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
	// Org serves OrgService.
	Org *OrgHandler
	// Bank serves BankService.
	Bank *BankHandler
	// Config serves ConfigService.
	Config *ConfigHandler
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
	// MaxRecvBytes is the largest request message the server accepts; 0 = gRPC's default (4 MiB).
	MaxRecvBytes int
	// MaxResponseBytes is the largest response a read may return (api.max_response_bytes); a larger one fails with
	// RESOURCE_EXHAUSTED (RESPONSE_TOO_LARGE). 0 = unchecked.
	MaxResponseBytes int
}

// New returns a gRPC server with the memory.v1 services of h and grpc.health.v1 registered, receiving messages of
// up to o.MaxRecvBytes. The tenant, default-deadline, paging (negative page_size / limit), arm-name
// (RecallConfig names outside o.RecallArms) and response-size (a read's response over o.MaxResponseBytes)
// interceptors run first, in that order; opts are appended (more interceptors chain after them).
func New(h Handlers, o Options, opts ...grpc.ServerOption) *grpc.Server {
	base := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(
			TenantInterceptor(), DeadlineInterceptor(o.DefaultDeadline), pagingInterceptor(),
			armNamesInterceptor(o.RecallArms), responseSizeInterceptor(o.MaxResponseBytes),
		),
	}
	if o.MaxRecvBytes > 0 {
		base = append(base, grpc.MaxRecvMsgSize(o.MaxRecvBytes))
	}
	s := grpc.NewServer(append(base, opts...)...)
	if h.Memory != nil {
		pb.RegisterMemoryServiceServer(s, h.Memory)
	}
	if h.Org != nil {
		pb.RegisterOrgServiceServer(s, h.Org)
	}
	if h.Bank != nil {
		pb.RegisterBankServiceServer(s, h.Bank)
	}
	if h.Config != nil {
		pb.RegisterConfigServiceServer(s, h.Config)
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

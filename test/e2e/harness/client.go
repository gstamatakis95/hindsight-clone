package harness

import (
	"context"
	"fmt"
	"maps"
	"memd/internal/grpcapi"
	"sort"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	memoryv1 "memd/gen/memory/v1"
)

// Client holds a client for every memd gRPC service; every call carries Tenant in the x-tenant-id header.
type Client struct {
	// Conn is the underlying connection (calls on it directly carry no tenant).
	Conn *grpc.ClientConn
	// Tenant is the tenant id every call sends.
	Tenant string
	// Memory is MemoryService (Retain, Recall, Reflect, memory listing and curation).
	Memory memoryv1.MemoryServiceClient
	// Documents is DocumentService.
	Documents memoryv1.DocumentServiceClient
	// Banks is BankService.
	Banks memoryv1.BankServiceClient
	// MentalModels is MentalModelService.
	MentalModels memoryv1.MentalModelServiceClient
	// Observations is ObservationService.
	Observations memoryv1.ObservationServiceClient
	// Operations is OperationService.
	Operations memoryv1.OperationServiceClient
	// Config is ConfigService (tenant and bank config, prompt preview, directives).
	Config memoryv1.ConfigServiceClient
}

// Dial connects to a memd api at addr (plaintext) on behalf of tenant; the connection closes with the test.
func Dial(t testing.TB, addr, tenant string) *Client {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("harness: dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return newClient(conn, tenant)
}

// newClient builds the service clients over a tenant-stamping connection.
func newClient(conn *grpc.ClientConn, tenant string) *Client {
	cc := tenantConn{cc: conn, tenant: tenant}
	return &Client{
		Conn: conn, Tenant: tenant,
		Memory: memoryv1.NewMemoryServiceClient(cc), Documents: memoryv1.NewDocumentServiceClient(cc),
		Banks:        memoryv1.NewBankServiceClient(cc),
		MentalModels: memoryv1.NewMentalModelServiceClient(cc), Observations: memoryv1.NewObservationServiceClient(cc),
		Operations: memoryv1.NewOperationServiceClient(cc), Config: memoryv1.NewConfigServiceClient(cc),
	}
}

// tenantConn adds the tenant header to every call on cc.
type tenantConn struct {
	cc     *grpc.ClientConn
	tenant string
}

var _ grpc.ClientConnInterface = tenantConn{}

// Invoke implements grpc.ClientConnInterface.
func (c tenantConn) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	return c.cc.Invoke(c.out(ctx), method, args, reply, opts...) //nolint:wrapcheck // the RPC status is the result
}

// NewStream implements grpc.ClientConnInterface.
func (c tenantConn) NewStream(
	ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption,
) (grpc.ClientStream, error) {
	return c.cc.NewStream(c.out(ctx), desc, method, opts...) //nolint:wrapcheck // the RPC status is the result
}

// out adds the tenant header.
func (c tenantConn) out(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, grpcapi.TenantHeader, c.tenant)
}

// Pending returns the bank's BankStats.pending_operations_by_kind without zero counts.
func (c *Client) Pending(ctx context.Context, bank string) (map[string]int64, error) {
	resp, err := c.Banks.GetBankStats(ctx, &memoryv1.GetBankStatsRequest{BankId: bank})
	if err != nil {
		return nil, fmt.Errorf("bank stats %s: %w", bank, err)
	}
	pending := map[string]int64{}
	for kind, n := range resp.GetStats().GetPendingOperationsByKind() {
		if n != 0 {
			pending[kind] = n
		}
	}
	return pending, nil
}

// WaitIdle polls the bank every poll until pending_operations_by_kind is empty on two consecutive polls
// (one idle poll can fall between a job finishing and its follow-up being enqueued). It returns
// an error naming what was still pending when ctx ends.
func (c *Client) WaitIdle(ctx context.Context, bank string, poll time.Duration) error {
	idle := 0
	var last map[string]int64
	for {
		pending, err := c.Pending(ctx, bank)
		if err != nil {
			return err
		}
		last = pending
		if len(pending) == 0 {
			idle++
			if idle == 2 {
				return nil
			}
		} else {
			idle = 0
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("bank %s not idle: pending %s: %w", bank, describe(last), ctx.Err())
		case <-time.After(poll):
		}
	}
}

// describe renders a pending map in key order.
func describe(pending map[string]int64) string {
	keys := make([]string, 0, len(pending))
	for k := range maps.Keys(pending) {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := "{"
	for i, k := range keys {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%s: %d", k, pending[k])
	}
	return out + "}"
}

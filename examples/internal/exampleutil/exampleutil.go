package exampleutil

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	pb "memd/gen/memory/v1"
)

// Client holds a client for every memd service; every call carries the tenant in the x-tenant-id header.
type Client struct {
	// Tenant is the tenant id every call sends.
	Tenant string
	// Memory is MemoryService: retain, recall, reflect and memory curation.
	Memory pb.MemoryServiceClient
	// Documents is DocumentService.
	Documents pb.DocumentServiceClient
	// Observations is ObservationService.
	Observations pb.ObservationServiceClient
	// MentalModels is MentalModelService.
	MentalModels pb.MentalModelServiceClient
	// Banks is BankService.
	Banks pb.BankServiceClient
	// Config is ConfigService: tenant and bank config and directives.
	Config pb.ConfigServiceClient
	// Operations is OperationService: background work and the event feed.
	Operations pb.OperationServiceClient

	conn *grpc.ClientConn
}

// Dial connects to a memd at addr (plaintext) on behalf of tenant.
func Dial(addr, tenant string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return newClient(conn, tenant), nil
}

// As returns a client over the same connection that acts for another tenant.
func (c *Client) As(tenant string) *Client { return newClient(c.conn, tenant) }

// Close closes the connection.
func (c *Client) Close() error {
	if err := c.conn.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return nil
}

func newClient(conn *grpc.ClientConn, tenant string) *Client {
	cc := tenantConn{conn: conn, tenant: tenant}
	return &Client{
		Tenant: tenant, conn: conn,
		Memory: pb.NewMemoryServiceClient(cc), Documents: pb.NewDocumentServiceClient(cc),
		Observations: pb.NewObservationServiceClient(cc), MentalModels: pb.NewMentalModelServiceClient(cc),
		Banks: pb.NewBankServiceClient(cc), Config: pb.NewConfigServiceClient(cc),
		Operations: pb.NewOperationServiceClient(cc),
	}
}

// tenantConn adds the x-tenant-id header to every call.
type tenantConn struct {
	conn   *grpc.ClientConn
	tenant string
}

// Invoke implements grpc.ClientConnInterface.
func (t tenantConn) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	return t.conn.Invoke(t.out(ctx), method, args, reply, opts...) //nolint:wrapcheck // the RPC status is the result
}

// NewStream implements grpc.ClientConnInterface.
func (t tenantConn) NewStream(ctx context.Context, d *grpc.StreamDesc, method string, opts ...grpc.CallOption,
) (grpc.ClientStream, error) {
	return t.conn.NewStream(t.out(ctx), d, method, opts...) //nolint:wrapcheck // the RPC status is the result
}

func (t tenantConn) out(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "x-tenant-id", t.tenant)
}

// Run is the whole main of an example: it reads the -addr, -tenant and -timeout flags (defaults come from the
// MEMD_ADDR and MEMD_TENANT environment variables, then localhost:50051 and "examples"), connects, runs
// scenario and exits 1 with the error on failure. Flags an example defines itself must be registered before Run.
func Run(scenario func(ctx context.Context, c *Client) error) {
	addr := flag.String("addr", env("MEMD_ADDR", "localhost:50051"), "memd gRPC address")
	tenant := flag.String("tenant", env("MEMD_TENANT", "examples"), "tenant id sent as x-tenant-id")
	timeout := flag.Duration("timeout", 5*time.Minute, "give up after this long")
	flag.Parse()
	if err := run(*addr, *tenant, *timeout, scenario); err != nil {
		fmt.Fprintln(os.Stderr, "FAILED:", err)
		os.Exit(1)
	}
}

func run(addr, tenant string, timeout time.Duration, scenario func(ctx context.Context, c *Client) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c, err := Dial(addr, tenant)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	return scenario(ctx, c)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// UniqueID returns prefix plus a time-based suffix, so a rerun against the same server gets fresh banks and
// models instead of ALREADY_EXISTS.
func UniqueID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixMilli()%100000000)
}

// Steps runs the steps of an example in order and stops at the first error.
func Steps(ctx context.Context, steps ...func(context.Context) error) error {
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Section prints a heading for the next step of an example.
func Section(title string) { fmt.Printf("\n== %s ==\n", title) }

// Expect returns an error describing what an example claims but the server did not show; examples use it so a
// broken behaviour fails the run instead of printing a misleading success.
func Expect(ok bool, format string, args ...any) error {
	if ok {
		return nil
	}
	return errors.New("expected " + fmt.Sprintf(format, args...))
}

// WaitIdle waits until the bank has no pending or processing operation, i.e. until the consolidation that
// follows a retain is done. Operations of the ignored kinds (lowercase, such as "refresh_mental_model") do not count.
func WaitIdle(ctx context.Context, c *Client, bank string, ignoreKinds ...string) error {
	quiet := 0 // a job can enqueue its successor as it finishes, so two quiet polls in a row count as idle
	return Until(ctx, func() (bool, error) {
		resp, err := c.Banks.GetBankStats(ctx, &pb.GetBankStatsRequest{BankId: bank})
		if err != nil {
			return false, fmt.Errorf("get bank stats: %w", err)
		}
		quiet++
		for kind := range resp.GetStats().GetPendingOperationsByKind() {
			if !slices.Contains(ignoreKinds, kind) {
				quiet = 0
			}
		}
		return quiet >= 2, nil
	})
}

// WaitOperation polls an operation until it is COMPLETED, FAILED or CANCELLED (or one of the extra statuses)
// and returns it. A FAILED operation is returned with its error as a Go error too.
func WaitOperation(ctx context.Context, c *Client, bank, id string, extra ...pb.OperationStatus,
) (*pb.Operation, error) {
	var op *pb.Operation
	err := Until(ctx, func() (bool, error) {
		resp, err := c.Operations.GetOperation(ctx, &pb.GetOperationRequest{BankId: bank, OperationId: id})
		if err != nil {
			return false, fmt.Errorf("get operation %s: %w", id, err)
		}
		op = resp.GetOperation()
		return isTerminal(op.GetStatus(), extra), nil
	})
	if err == nil && op.GetStatus() == pb.OperationStatus_OPERATION_STATUS_FAILED {
		err = fmt.Errorf("operation %s failed: %s", id, op.GetError())
	}
	return op, err
}

func isTerminal(s pb.OperationStatus, extra []pb.OperationStatus) bool {
	switch s {
	case pb.OperationStatus_OPERATION_STATUS_COMPLETED, pb.OperationStatus_OPERATION_STATUS_FAILED,
		pb.OperationStatus_OPERATION_STATUS_CANCELLED:
		return true
	default:
		for _, e := range extra {
			if s == e {
				return true
			}
		}
		return false
	}
}

// Until calls done every 200ms until it reports true, fails, or ctx ends.
func Until(ctx context.Context, done func() (bool, error)) error {
	for {
		ok, err := done()
		if err != nil || ok {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting: %w", ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// PrintUsage prints token usage per slot and model, the way an operator would audit which model served which step.
func PrintUsage(label string, usage []*pb.SlotUsage) {
	fmt.Printf("%s usage:\n", label)
	if len(usage) == 0 {
		fmt.Println("  (none)")
	}
	for _, u := range usage {
		fmt.Printf("  %-18s model=%-22s in=%d out=%d\n",
			strings.TrimPrefix(u.GetSlot().String(), "MODEL_SLOT_"), u.GetModel(), u.GetInputTokens(), u.GetOutputTokens())
	}
}

// UsedModel reports the model that served slot in usage ("" if the slot made no call).
func UsedModel(usage []*pb.SlotUsage, slot pb.SlotUsage_ModelSlot) string {
	for _, u := range usage {
		if u.GetSlot() == slot {
			return u.GetModel()
		}
	}
	return ""
}

// Fact formats a memory as "[TYPE] text" for one-line printing.
func Fact(m *pb.Memory, n int) string {
	return fmt.Sprintf("[%s] %s", strings.TrimPrefix(m.GetFactType().String(), "FACT_TYPE_"), ShortText(m.GetText(), n))
}

// Model builds a model binding that names only the gateway model; every other parameter keeps the gateway default.
func Model(name string) *pb.ModelBinding { return &pb.ModelBinding{Model: name} }

// Paths builds the FieldMask of a patch request from config paths such as "models.default".
func Paths(paths ...string) *fieldmaskpb.FieldMask { return &fieldmaskpb.FieldMask{Paths: paths} }

// ShortText cuts s to at most n runes for one-line printing.
func ShortText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

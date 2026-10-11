package target

import (
	"context"
	"fmt"
	"math"
	"memd/eval/harness"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	memoryv1 "memd/gen/memory/v1"
)

// tenantHeader is the gRPC metadata key memd reads the tenant id from.
const tenantHeader = "x-tenant-id"

// operationsPageSize is the page size Background lists operations with.
const operationsPageSize = 100

var (
	_ harness.Target        = (*Memd)(nil)
	_ harness.ModelReporter = (*Memd)(nil)
)

// Memd is the harness.Target for memd's gRPC API. Every call carries the tenant in the x-tenant-id header.
type Memd struct {
	memory memoryv1.MemoryServiceClient
	banks  memoryv1.BankServiceClient
	ops    memoryv1.OperationServiceClient
	tenant string
}

// NewMemd returns a Memd target that sends every call over conn on behalf of tenant.
func NewMemd(conn grpc.ClientConnInterface, tenant string) *Memd {
	return &Memd{
		memory: memoryv1.NewMemoryServiceClient(conn),
		banks:  memoryv1.NewBankServiceClient(conn),
		ops:    memoryv1.NewOperationServiceClient(conn),
		tenant: tenant,
	}
}

// Name returns "memd".
func (m *Memd) Name() string { return "memd" }

// Retain stores s as one document (document id = session id) with a synchronous Retain and returns the
// per-slot usage the response reports (extraction, time parsing, embedding).
func (m *Memd) Retain(ctx context.Context, bank string, s harness.Session) ([]harness.Usage, error) {
	req := &memoryv1.RetainRequest{BankId: bank, Items: []*memoryv1.RetainItem{{
		Content:    s.Content,
		Context:    s.Context,
		DocumentId: s.ID,
		Timestamp:  timestamp(s.Time),
	}}}
	resp, err := m.memory.Retain(m.out(ctx), req)
	if err != nil {
		return nil, fmt.Errorf("memd: retain %s/%s: %w", bank, s.ID, err)
	}
	var acc usageSum
	acc.add(resp.GetUsage())
	return acc.usage, nil
}

// PendingOperations returns BankStats.pending_operations_by_kind without zero counts.
func (m *Memd) PendingOperations(ctx context.Context, bank string) (map[string]int64, error) {
	resp, err := m.banks.GetBankStats(m.out(ctx), &memoryv1.GetBankStatsRequest{BankId: bank})
	if err != nil {
		return nil, fmt.Errorf("memd: bank stats %s: %w", bank, err)
	}
	pending := map[string]int64{}
	for kind, n := range resp.GetStats().GetPendingOperationsByKind() {
		if n != 0 {
			pending[kind] = n
		}
	}
	return pending, nil
}

// budgets maps RecallOptions.Budget to memd's Budget.
var budgets = map[string]memoryv1.Budget{
	"": memoryv1.Budget_BUDGET_UNSPECIFIED, "low": memoryv1.Budget_BUDGET_LOW,
	"mid": memoryv1.Budget_BUDGET_MID, "high": memoryv1.Budget_BUDGET_HIGH,
}

// Recall returns the recalled memories in result order, with their source chunks when opts asks for them.
func (m *Memd) Recall(
	ctx context.Context, bank, query string, at time.Time, opts harness.RecallOptions,
) (harness.Recalled, error) {
	budget, ok := budgets[strings.ToLower(opts.Budget)]
	if !ok {
		return harness.Recalled{}, fmt.Errorf("memd: unknown recall budget %q (want low, mid or high)", opts.Budget)
	}
	req := &memoryv1.RecallRequest{
		BankId: bank, Query: query, QueryTime: timestamp(at), Budget: budget, MaxTokens: clampInt32(opts.MaxTokens),
	}
	if opts.ChunkTokens > 0 {
		chunks, limit := true, clampInt32(opts.ChunkTokens)
		req.Include = &memoryv1.RecallInclude{Chunks: &chunks, ChunksMaxTokens: &limit}
	}
	resp, err := m.memory.Recall(m.out(ctx), req)
	if err != nil {
		return harness.Recalled{}, fmt.Errorf("memd: recall %s: %w", bank, err)
	}
	out := harness.Recalled{Results: make([]harness.Memory, 0, len(resp.GetResults()))}
	for _, r := range resp.GetResults() {
		out.Results = append(out.Results, harness.Memory{
			ID: r.GetId(), Text: r.GetText(),
			Type:     strings.ToLower(strings.TrimPrefix(r.GetType().String(), "FACT_TYPE_")),
			Entities: r.GetEntities(), Context: r.GetContext(),
			OccurredStart: protoTime(r.GetOccurredStart()), OccurredEnd: protoTime(r.GetOccurredEnd()),
			MentionedAt: protoTime(r.GetMentionedAt()), DocumentID: r.GetDocumentId(), ChunkID: r.GetChunkId(),
		})
	}
	if len(resp.GetChunks()) > 0 {
		out.Chunks = make(map[string]harness.Chunk, len(resp.GetChunks()))
		for id, c := range resp.GetChunks() {
			out.Chunks[id] = harness.Chunk{
				ID: c.GetId(), Text: c.GetText(), ChunkIndex: int(c.GetChunkIndex()), Truncated: c.GetTruncated(),
			}
		}
	}
	return out, nil
}

// Models returns the model of every slot bound in bank's resolved config (server < tenant < bank).
func (m *Memd) Models(ctx context.Context, bank string) (map[string]string, error) {
	resp, err := m.banks.GetBankConfig(m.out(ctx), &memoryv1.GetBankConfigRequest{BankId: bank})
	if err != nil {
		return nil, fmt.Errorf("memd: bank config %s: %w", bank, err)
	}
	models := map[string]string{}
	resp.GetResolved().GetModels().ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if b, ok := v.Message().Interface().(*memoryv1.ModelBinding); ok && fd.Name() != "default" {
			models[strings.ToUpper(string(fd.Name()))] = b.GetModel()
		}
		return true
	})
	return models, nil
}

// protoTime formats a proto timestamp as RFC 3339; nil becomes "".
func protoTime(t *timestamppb.Timestamp) string {
	if t == nil {
		return ""
	}
	return t.AsTime().UTC().Format(time.RFC3339)
}

// clampInt32 converts a configured token budget to int32, clamped to [0, MaxInt32].
func clampInt32(n int) int32 {
	switch {
	case n <= 0:
		return 0
	case n >= math.MaxInt32:
		return math.MaxInt32
	default:
		return int32(n)
	}
}

// Reflect returns the reflect answer and its per-slot usage.
func (m *Memd) Reflect(ctx context.Context, bank, query string, at time.Time) (string, []harness.Usage, error) {
	resp, err := m.memory.Reflect(m.out(ctx), &memoryv1.ReflectRequest{
		BankId: bank, Query: query, QueryTime: timestamp(at),
	})
	if err != nil {
		return "", nil, fmt.Errorf("memd: reflect %s: %w", bank, err)
	}
	var acc usageSum
	acc.add(resp.GetUsage())
	return resp.GetText(), acc.usage, nil
}

// Background pages through every operation of the bank (parents excluded, so a retain_batch's usage is
// not counted twice), sums their usage per slot and counts the FAILED ones.
func (m *Memd) Background(ctx context.Context, bank string) (harness.BackgroundStats, error) {
	var (
		acc    usageSum
		failed int
		token  string
	)
	ctx = m.out(ctx)
	for {
		resp, err := m.ops.ListOperations(ctx, &memoryv1.ListOperationsRequest{
			BankId: bank, ExcludeParents: true, PageSize: operationsPageSize, PageToken: token,
		})
		if err != nil {
			return harness.BackgroundStats{}, fmt.Errorf("memd: list operations %s: %w", bank, err)
		}
		for _, op := range resp.GetOperations() {
			if op.GetStatus() == memoryv1.OperationStatus_OPERATION_STATUS_FAILED {
				failed++
			}
			acc.add(op.GetUsage())
		}
		next := resp.GetNextPageToken()
		if next == "" {
			break
		}
		if next == token {
			return harness.BackgroundStats{}, fmt.Errorf("memd: list operations %s: page token %q repeats", bank, next)
		}
		token = next
	}
	return harness.BackgroundStats{Usage: acc.usage, Failed: failed}, nil
}

// out adds the tenant header to ctx.
func (m *Memd) out(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, tenantHeader, m.tenant)
}

// timestamp converts t to a proto timestamp; the zero time becomes nil (unset).
func timestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// usageSum sums usage per slot, keeping slots in first-seen order.
type usageSum struct {
	usage []harness.Usage
}

// add merges us into the sum; the slot name drops memd's MODEL_SLOT_ prefix.
func (s *usageSum) add(us []*memoryv1.SlotUsage) {
	for _, u := range us {
		slot := strings.TrimPrefix(u.GetSlot().String(), "MODEL_SLOT_")
		i := 0
		for i < len(s.usage) && s.usage[i].Slot != slot {
			i++
		}
		if i == len(s.usage) {
			s.usage = append(s.usage, harness.Usage{Slot: slot})
		}
		s.usage[i].InputTokens += u.GetInputTokens()
		s.usage[i].OutputTokens += u.GetOutputTokens()
	}
}

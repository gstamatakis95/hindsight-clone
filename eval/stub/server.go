package stub

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	memoryv1 "memd/gen/memory/v1"
)

// TenantHeader is the gRPC metadata key that carries the tenant id.
const TenantHeader = "x-tenant-id"

const (
	// recallLimit caps the number of lines Recall returns.
	recallLimit = 10
	// reflectLines is how many recalled lines Reflect joins into its answer.
	reflectLines = 3
	// minWordLen is the shortest query word that counts towards a line's score.
	minWordLen = 3
	// stubModel is the model name the stub reports in usage.
	stubModel = "stub"
	// consolidateKind is the PendingOperationsByKind key the stub reports.
	consolidateKind = "consolidate"
	// unknownAnswer is Reflect's answer when nothing matches.
	unknownAnswer = "I don't know."
)

var (
	_ memoryv1.MemoryServiceServer    = (*Server)(nil)
	_ memoryv1.BankServiceServer      = (*Server)(nil)
	_ memoryv1.OperationServiceServer = (*Server)(nil)
)

// Options tunes the stub's behaviour.
type Options struct {
	// PendingPolls is how many GetBankStats calls report a pending consolidate after each Retain; 0 means
	// the bank is idle as soon as Retain returns.
	PendingPolls int
	// FailedOps is how many FAILED consolidate operations a bank starts with when it is created.
	FailedOps int
}

// document is one retained document.
type document struct {
	id      string
	content string
	time    *timestamppb.Timestamp
}

// bank is the state of one (tenant, bank) pair.
type bank struct {
	docs    []*document
	index   map[string]int
	pending int
	ops     []*memoryv1.Operation
}

// bankKey identifies a bank across tenants.
type bankKey struct {
	tenant string
	bank   string
}

// Server implements the subset of memd's MemoryService, BankService and OperationService the eval uses.
type Server struct {
	memoryv1.UnimplementedMemoryServiceServer
	memoryv1.UnimplementedBankServiceServer
	memoryv1.UnimplementedOperationServiceServer

	opts    Options
	mu      sync.Mutex
	banks   map[bankKey]*bank
	retains int
	nextDoc int
	nextOp  int
}

// New returns an empty stub server.
func New(o Options) *Server {
	return &Server{opts: o, banks: map[bankKey]*bank{}}
}

// Register registers the stub's three services on g.
func (s *Server) Register(g *grpc.Server) {
	memoryv1.RegisterMemoryServiceServer(g, s)
	memoryv1.RegisterBankServiceServer(g, s)
	memoryv1.RegisterOperationServiceServer(g, s)
}

// Start serves a new stub on a loopback port. It returns the address to dial and a stop function that
// shuts the server down and waits for it to exit.
func Start(o Options) (addr string, stop func(), err error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("stub: listen: %w", err)
	}
	g := grpc.NewServer()
	New(o).Register(g)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = g.Serve(lis) // Serve returns once Stop closes the listener.
	}()
	return lis.Addr().String(), func() { g.Stop(); <-done }, nil
}

// Retains returns how many items have been retained across all banks.
func (s *Server) Retains() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retains
}

// Retain upserts each item as a document (by document_id), creating the bank when it is unknown. It arms
// the bank's pending polls, records one completed consolidate operation per call and, like a sync retain,
// reports EXTRACT usage (20 in, 4 out per item).
func (s *Server) Retain(ctx context.Context, req *memoryv1.RetainRequest) (*memoryv1.RetainResponse, error) {
	tenant, err := tenantOf(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetBankId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "bank_id is required for tenant %q", tenant)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.bankOrCreate(bankKey{tenant, req.GetBankId()})
	resp := &memoryv1.RetainResponse{}
	for _, it := range req.GetItems() {
		id := it.GetDocumentId()
		if id == "" {
			s.nextDoc++
			id = fmt.Sprintf("doc-%d", s.nextDoc)
		}
		b.upsert(&document{id: id, content: it.GetContent(), time: it.GetTimestamp()})
		s.retains++
		resp.Documents = append(resp.Documents, &memoryv1.RetainedDocument{DocumentId: id, UnitsCreated: 1})
		resp.Usage = append(resp.Usage, &memoryv1.SlotUsage{
			Slot: memoryv1.ModelSlot_MODEL_SLOT_EXTRACT, Model: stubModel, InputTokens: 20, OutputTokens: 4,
		})
	}
	b.pending = s.opts.PendingPolls
	b.ops = append(b.ops, s.newOp(memoryv1.OperationStatus_OPERATION_STATUS_COMPLETED, []*memoryv1.SlotUsage{{
		Slot: memoryv1.ModelSlot_MODEL_SLOT_CONSOLIDATE, Model: stubModel, InputTokens: 10, OutputTokens: 5,
	}}))
	return resp, nil
}

// GetBankStats reports the bank's document count and, while pending polls remain, one pending consolidate.
func (s *Server) GetBankStats(
	ctx context.Context, req *memoryv1.GetBankStatsRequest,
) (*memoryv1.GetBankStatsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.lookup(ctx, req.GetBankId())
	if err != nil {
		return nil, err
	}
	pending := map[string]int64{}
	if b.pending > 0 {
		b.pending--
		pending[consolidateKind] = 1
	}
	return &memoryv1.GetBankStatsResponse{Stats: &memoryv1.BankStats{
		Documents:               int64(len(b.docs)),
		PendingOperationsByKind: pending,
	}}, nil
}

// GetBankConfig reports a resolved config that binds every LLM slot the stub reports usage for to the
// stub's model name.
func (s *Server) GetBankConfig(
	ctx context.Context, req *memoryv1.GetBankConfigRequest,
) (*memoryv1.GetBankConfigResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.lookup(ctx, req.GetBankId()); err != nil {
		return nil, err
	}
	resolved := &memoryv1.BankConfig{}
	for _, slot := range []memoryv1.ModelSlot{
		memoryv1.ModelSlot_MODEL_SLOT_EXTRACT,
		memoryv1.ModelSlot_MODEL_SLOT_CONSOLIDATE,
		memoryv1.ModelSlot_MODEL_SLOT_REFLECT,
	} {
		resolved.Models = append(resolved.Models, &memoryv1.SlotBinding{
			Slot: slot, Binding: &memoryv1.ModelBinding{Model: stubModel},
		})
	}
	return &memoryv1.GetBankConfigResponse{Resolved: resolved}, nil
}

// Recall returns the stored lines that share the most query words, best first.
func (s *Server) Recall(ctx context.Context, req *memoryv1.RecallRequest) (*memoryv1.RecallResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.lookup(ctx, req.GetBankId())
	if err != nil {
		return nil, err
	}
	return &memoryv1.RecallResponse{Results: b.recall(req.GetQuery(), recallLimit)}, nil
}

// Reflect answers with the top recalled lines joined by newlines, or "I don't know." when none match.
func (s *Server) Reflect(ctx context.Context, req *memoryv1.ReflectRequest) (*memoryv1.ReflectResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.lookup(ctx, req.GetBankId())
	if err != nil {
		return nil, err
	}
	mems := b.recall(req.GetQuery(), reflectLines)
	text := unknownAnswer
	if len(mems) > 0 {
		lines := make([]string, len(mems))
		for i, m := range mems {
			lines[i] = m.GetText()
		}
		text = strings.Join(lines, "\n")
	}
	return &memoryv1.ReflectResponse{Text: text, Usage: []*memoryv1.SlotUsage{{
		Slot: memoryv1.ModelSlot_MODEL_SLOT_REFLECT, Model: stubModel, InputTokens: 100, OutputTokens: 20,
	}}}, nil
}

// ListOperations returns the bank's recorded operations, filtered by status and kind when set, in one page.
func (s *Server) ListOperations(
	ctx context.Context, req *memoryv1.ListOperationsRequest,
) (*memoryv1.ListOperationsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.lookup(ctx, req.GetBankId())
	if err != nil {
		return nil, err
	}
	resp := &memoryv1.ListOperationsResponse{}
	for _, op := range b.ops {
		if req.GetStatus() != memoryv1.OperationStatus_OPERATION_STATUS_UNSPECIFIED && op.GetStatus() != req.GetStatus() {
			continue
		}
		if req.GetKind() != memoryv1.OperationKind_OPERATION_KIND_UNSPECIFIED && op.GetKind() != req.GetKind() {
			continue
		}
		resp.Operations = append(resp.Operations, op)
	}
	return resp, nil
}

// lookup resolves the caller's tenant and an existing bank. The caller holds s.mu.
func (s *Server) lookup(ctx context.Context, bankID string) (*bank, error) {
	tenant, err := tenantOf(ctx)
	if err != nil {
		return nil, err
	}
	b, ok := s.banks[bankKey{tenant, bankID}]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "bank %q not found", bankID)
	}
	return b, nil
}

// bankOrCreate returns the bank for k, creating it (with its seeded failed operations) when unknown. The
// caller holds s.mu.
func (s *Server) bankOrCreate(k bankKey) *bank {
	if b, ok := s.banks[k]; ok {
		return b
	}
	b := &bank{index: map[string]int{}}
	for range s.opts.FailedOps {
		op := s.newOp(memoryv1.OperationStatus_OPERATION_STATUS_FAILED, nil)
		op.Error = "stub: seeded failure"
		b.ops = append(b.ops, op)
	}
	s.banks[k] = b
	return b
}

// newOp returns a consolidate operation with the next id. The caller holds s.mu.
func (s *Server) newOp(st memoryv1.OperationStatus, usage []*memoryv1.SlotUsage) *memoryv1.Operation {
	s.nextOp++
	return &memoryv1.Operation{
		Id:     fmt.Sprintf("op-%d", s.nextOp),
		Kind:   memoryv1.OperationKind_OPERATION_KIND_CONSOLIDATE,
		Status: st,
		Usage:  usage,
	}
}

// upsert stores d, replacing a document with the same id in place.
func (b *bank) upsert(d *document) {
	if i, ok := b.index[d.id]; ok {
		b.docs[i] = d
		return
	}
	b.index[d.id] = len(b.docs)
	b.docs = append(b.docs, d)
}

// candidate is one scored line.
type candidate struct {
	mem   *memoryv1.Memory
	score int
}

// recall scores every stored line by the number of distinct query words it contains and returns the best
// limit lines with a positive score; ties keep document and line order.
func (b *bank) recall(query string, limit int) []*memoryv1.Memory {
	words := wordSet(query)
	var cands []candidate
	for _, d := range b.docs {
		for n, line := range strings.Split(d.content, "\n") {
			line = strings.TrimSpace(line)
			score := overlap(words, wordSet(line))
			if score == 0 {
				continue
			}
			cands = append(cands, candidate{score: score, mem: &memoryv1.Memory{
				Id:          fmt.Sprintf("%s#%d", d.id, n),
				Type:        memoryv1.FactType_FACT_TYPE_WORLD,
				Text:        line,
				DocumentId:  d.id,
				MentionedAt: d.time,
			}})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	out := make([]*memoryv1.Memory, 0, min(limit, len(cands)))
	for i := 0; i < len(cands) && i < limit; i++ {
		out = append(out, cands[i].mem)
	}
	return out
}

// wordSet returns the distinct lowercase letter/digit tokens of text that are at least minWordLen long.
func wordSet(text string) map[string]struct{} {
	set := map[string]struct{}{}
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range fields {
		if utf8.RuneCountInString(w) >= minWordLen {
			set[w] = struct{}{}
		}
	}
	return set
}

// overlap counts the words of query that also occur in line.
func overlap(query, line map[string]struct{}) int {
	n := 0
	for w := range query {
		if _, ok := line[w]; ok {
			n++
		}
	}
	return n
}

// tenantOf reads the tenant id from the incoming metadata; a missing or empty one is INVALID_ARGUMENT.
func tenantOf(ctx context.Context) (string, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if v := md.Get(TenantHeader); len(v) > 0 && v[0] != "" {
		return v[0], nil
	}
	return "", status.Errorf(codes.InvalidArgument, "missing %s metadata", TenantHeader)
}

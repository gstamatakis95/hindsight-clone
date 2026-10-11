// The memd API: agent memory over gRPC.
//
// This one file is the whole contract. It is ordered as follows:
//   1. this header: the conventions every service shares;
//   2. all service blocks;
//   3. one section per service, in the same order, with each RPC's request, response and helper messages,
//      then the service's resource messages;
//   4. shared types used by more than one service;
//   5. configuration: BankConfig, its sub-messages and the model bindings;
//   6. shared enums.
//
// Tenant. The tenant comes from the x-tenant-id metadata header, never from the request body.
//
// Banks. A bank is an isolated memory store within a tenant. A write to an unknown bank creates it with
// tenant defaults, and every read of an unknown bank returns NOT_FOUND.
//
// Configuration. BankConfig is the one config shape used at every stored level: server (the defaults block of
// the server config file) < tenant < bank, then a consolidation strategy and the request (< item, for retain).
// It is grouped by phase: retain, observations, recall, reflect, mental_models, models. Scalars: a set value
// replaces an unset one. Groups merge field by field. A ModelBinding is atomic. Lists: a set list replaces the
// inherited one (no append). Patch RPCs take a FieldMask, which is also how a list is cleared. Each request
// that does work takes its own *Overrides message holding only what that RPC can change; the server turns it
// into a BankConfig level. Model routing: see ModelBindings.
//
// Deadlines. A call that arrives without a deadline gets the server's default deadline (a server setting, so
// set your own for anything slower). A call that runs out of time fails with DEADLINE_EXCEEDED; work already
// committed stays. Recall, Reflect and PreviewMentalModelRefresh run within the call's deadline.
//
// Recall arms. A recall retrieves candidates with several named arms (built in: vector, text, graph, temporal)
// and fuses them. Arms are open-ended, so anything per arm is keyed by arm name, never a field per arm;
// ListRecallArms lists the names a server accepts. An unknown arm name is INVALID_ARGUMENT.
//
// Async work. Background work runs as operations. An RPC whose requested work runs in the background returns
// the Operation it enqueued, in a field named operation (or operations, batch_operation,
// mental_model_operations), never a bare id; OperationService reports its status and progress. Follow-up jobs
// an RPC triggers on its own (such as consolidation after a retain) are not returned. Retain is synchronous by
// default, returning once the memories are committed; run_async=true makes it return the enqueued operations instead.
// Recall and Reflect are synchronous.
//
// Events. State changes are recorded as lifecycle events numbered by the bank's change sequence; clients
// poll OperationService.ListEvents with after_seq, passing the previous response's next_after_seq.
//
// Paging. Every List RPC takes page_size and page_token and returns next_page_token (empty = no more pages).
// page_size 0 = 100, larger than 1000 = 1000, negative = INVALID_ARGUMENT. A page token is opaque and only
// valid for the same request; a malformed one is INVALID_ARGUMENT. ListEvents pages by sequence instead
// (page_size + after_seq / next_after_seq), and SearchMentalModels takes max_results (0 = 10, larger than
// 100 = 100, negative = INVALID_ARGUMENT).
//
// Tag filters. Every request that filters by tag takes one TagFilter: flat tags under a match mode, or
// boolean tag groups (setting both = INVALID_ARGUMENT). Each TagFilter field states its default match mode.
// List RPCs take only the flat form for now: groups there are INVALID_ARGUMENT.
//
// Errors. Every error carries a canonical gRPC code and a google.rpc.ErrorInfo detail with domain "memd" and
// a stable reason; INVALID_ARGUMENT also carries a google.rpc.BadRequest detail whose field violations name
// the offending request fields (for a config, the BankConfig path, for example reflect.disposition.empathy)
// when the server knows them. Clients branch on the code, then the reason; the message is for humans. The
// reasons, by code:
//   INVALID_ARGUMENT     INVALID_ARGUMENT (malformed request), TOO_MANY_SCOPES (ALL_COMBINATIONS fan-out
//                        over max_combinations), MEMORY_IS_OBSERVATION (curating an observation)
//   NOT_FOUND            NOT_FOUND (the named object does not exist), BANK_NOT_FOUND (unknown bank, or any
//                        read of a bank being deleted except GetOperation of its delete_bank operations)
//   ALREADY_EXISTS       ALREADY_EXISTS (a create collided with an existing id)
//   FAILED_PRECONDITION  FAILED_PRECONDITION (the state forbids the call), BANK_DELETING (a write to a bank
//                        being deleted), MODEL_UNBOUND (a model slot the call needs has no binding),
//                        OPERATION_NOT_CANCELLABLE, OPERATION_NOT_RETRYABLE (the operation's status or kind
//                        forbids it)
//   UNAVAILABLE          UNAVAILABLE (a dependency failed; retry with backoff), RETRIEVAL_UNAVAILABLE (a
//                        Reflect retrieval tool failed), CONTEXT_OVERFLOW (a prompt exceeded the model's context)
//   RESOURCE_EXHAUSTED   RESOURCE_EXHAUSTED, MAX_TOTAL_TOKENS_EXCEEDED (Reflect went over max_total_tokens)
//   ABORTED              OPERATION_CANCELLED (the operation was cancelled or its bank deleted), CONFLICT (a
//                        concurrent writer won; retry)
//   DEADLINE_EXCEEDED    DEADLINE_EXCEEDED
//   CANCELLED            CANCELLED (the client cancelled the call)
//   INTERNAL             INTERNAL (a server bug)
// The x-tenant-id header is checked first: missing or invalid = INVALID_ARGUMENT with field x-tenant-id.
//
// Naming. Every RPC has its own <Rpc>Request and <Rpc>Response. Every enum value is prefixed with its enum
// name, and value 0 is *_UNSPECIFIED. Patch RPCs take a google.protobuf.FieldMask update_mask.

// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.6.2
// - protoc             (unknown)
// source: memory/v1/memory.proto

package memoryv1

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.64.0 or later.
const _ = grpc.SupportPackageIsVersion9

const (
	MemoryService_Retain_FullMethodName           = "/memory.v1.MemoryService/Retain"
	MemoryService_Recall_FullMethodName           = "/memory.v1.MemoryService/Recall"
	MemoryService_Reflect_FullMethodName          = "/memory.v1.MemoryService/Reflect"
	MemoryService_ListRecallArms_FullMethodName   = "/memory.v1.MemoryService/ListRecallArms"
	MemoryService_ListMemories_FullMethodName     = "/memory.v1.MemoryService/ListMemories"
	MemoryService_GetMemory_FullMethodName        = "/memory.v1.MemoryService/GetMemory"
	MemoryService_UpdateMemory_FullMethodName     = "/memory.v1.MemoryService/UpdateMemory"
	MemoryService_InvalidateMemory_FullMethodName = "/memory.v1.MemoryService/InvalidateMemory"
	MemoryService_RestoreMemory_FullMethodName    = "/memory.v1.MemoryService/RestoreMemory"
)

// MemoryServiceClient is the client API for MemoryService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
//
// MemoryService stores content as memories, answers questions from them, and lists and curates them.
type MemoryServiceClient interface {
	// Retain stores content and extracts memories from it. Synchronous by default (returns after the memories
	// commit); run_async=true enqueues retain_document jobs and returns their operations. A write to an unknown
	// bank creates it with tenant defaults.
	Retain(ctx context.Context, in *RetainRequest, opts ...grpc.CallOption) (*RetainResponse, error)
	// Recall retrieves memories with the recall arms (built in: vector, text, graph, temporal), RRF fusion,
	// rerank, boosts and token packing. Unknown bank = NOT_FOUND.
	Recall(ctx context.Context, in *RecallRequest, opts ...grpc.CallOption) (*RecallResponse, error)
	// Reflect answers a query with an agentic tool loop over mental models, observations and raw facts.
	// Strict: a failing retrieval dependency fails the call with UNAVAILABLE; exceeding max_total_tokens
	// fails it with RESOURCE_EXHAUSTED. Unknown bank = NOT_FOUND.
	Reflect(ctx context.Context, in *ReflectRequest, opts ...grpc.CallOption) (*ReflectResponse, error)
	// ListRecallArms lists the recall arms this server has registered, with a one-line description each: the
	// names RecallRequest.arms, ReflectRequest.arms, RecallConfig.arms, RecallConfig.arm_boosts and
	// MinScores.arms accept.
	ListRecallArms(ctx context.Context, in *ListRecallArmsRequest, opts ...grpc.CallOption) (*ListRecallArmsResponse, error)
	// ListMemories lists live and invalidated memories (raw facts and observations) with filters and keyset
	// pagination. Returns no total.
	ListMemories(ctx context.Context, in *ListMemoriesRequest, opts ...grpc.CallOption) (*ListMemoriesResponse, error)
	// GetMemory returns one memory, live or invalidated.
	GetMemory(ctx context.Context, in *GetMemoryRequest, opts ...grpc.CallOption) (*GetMemoryResponse, error)
	// UpdateMemory edits a live raw fact; observations are rejected. It enqueues a rederive_memory job.
	UpdateMemory(ctx context.Context, in *UpdateMemoryRequest, opts ...grpc.CallOption) (*UpdateMemoryResponse, error)
	// InvalidateMemory archives a live raw fact: recall and consolidation stop seeing it, and RestoreMemory
	// brings it back. Observations are rejected. Everything happens in the call; no operation is returned.
	InvalidateMemory(ctx context.Context, in *InvalidateMemoryRequest, opts ...grpc.CallOption) (*InvalidateMemoryResponse, error)
	// RestoreMemory brings an invalidated raw fact back and enqueues a rederive_memory job. Restoring a live
	// fact changes nothing.
	RestoreMemory(ctx context.Context, in *RestoreMemoryRequest, opts ...grpc.CallOption) (*RestoreMemoryResponse, error)
}

type memoryServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewMemoryServiceClient(cc grpc.ClientConnInterface) MemoryServiceClient {
	return &memoryServiceClient{cc}
}

func (c *memoryServiceClient) Retain(ctx context.Context, in *RetainRequest, opts ...grpc.CallOption) (*RetainResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(RetainResponse)
	err := c.cc.Invoke(ctx, MemoryService_Retain_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *memoryServiceClient) Recall(ctx context.Context, in *RecallRequest, opts ...grpc.CallOption) (*RecallResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(RecallResponse)
	err := c.cc.Invoke(ctx, MemoryService_Recall_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *memoryServiceClient) Reflect(ctx context.Context, in *ReflectRequest, opts ...grpc.CallOption) (*ReflectResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ReflectResponse)
	err := c.cc.Invoke(ctx, MemoryService_Reflect_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *memoryServiceClient) ListRecallArms(ctx context.Context, in *ListRecallArmsRequest, opts ...grpc.CallOption) (*ListRecallArmsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListRecallArmsResponse)
	err := c.cc.Invoke(ctx, MemoryService_ListRecallArms_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *memoryServiceClient) ListMemories(ctx context.Context, in *ListMemoriesRequest, opts ...grpc.CallOption) (*ListMemoriesResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListMemoriesResponse)
	err := c.cc.Invoke(ctx, MemoryService_ListMemories_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *memoryServiceClient) GetMemory(ctx context.Context, in *GetMemoryRequest, opts ...grpc.CallOption) (*GetMemoryResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetMemoryResponse)
	err := c.cc.Invoke(ctx, MemoryService_GetMemory_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *memoryServiceClient) UpdateMemory(ctx context.Context, in *UpdateMemoryRequest, opts ...grpc.CallOption) (*UpdateMemoryResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(UpdateMemoryResponse)
	err := c.cc.Invoke(ctx, MemoryService_UpdateMemory_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *memoryServiceClient) InvalidateMemory(ctx context.Context, in *InvalidateMemoryRequest, opts ...grpc.CallOption) (*InvalidateMemoryResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(InvalidateMemoryResponse)
	err := c.cc.Invoke(ctx, MemoryService_InvalidateMemory_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *memoryServiceClient) RestoreMemory(ctx context.Context, in *RestoreMemoryRequest, opts ...grpc.CallOption) (*RestoreMemoryResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(RestoreMemoryResponse)
	err := c.cc.Invoke(ctx, MemoryService_RestoreMemory_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MemoryServiceServer is the server API for MemoryService service.
// All implementations must embed UnimplementedMemoryServiceServer
// for forward compatibility.
//
// MemoryService stores content as memories, answers questions from them, and lists and curates them.
type MemoryServiceServer interface {
	// Retain stores content and extracts memories from it. Synchronous by default (returns after the memories
	// commit); run_async=true enqueues retain_document jobs and returns their operations. A write to an unknown
	// bank creates it with tenant defaults.
	Retain(context.Context, *RetainRequest) (*RetainResponse, error)
	// Recall retrieves memories with the recall arms (built in: vector, text, graph, temporal), RRF fusion,
	// rerank, boosts and token packing. Unknown bank = NOT_FOUND.
	Recall(context.Context, *RecallRequest) (*RecallResponse, error)
	// Reflect answers a query with an agentic tool loop over mental models, observations and raw facts.
	// Strict: a failing retrieval dependency fails the call with UNAVAILABLE; exceeding max_total_tokens
	// fails it with RESOURCE_EXHAUSTED. Unknown bank = NOT_FOUND.
	Reflect(context.Context, *ReflectRequest) (*ReflectResponse, error)
	// ListRecallArms lists the recall arms this server has registered, with a one-line description each: the
	// names RecallRequest.arms, ReflectRequest.arms, RecallConfig.arms, RecallConfig.arm_boosts and
	// MinScores.arms accept.
	ListRecallArms(context.Context, *ListRecallArmsRequest) (*ListRecallArmsResponse, error)
	// ListMemories lists live and invalidated memories (raw facts and observations) with filters and keyset
	// pagination. Returns no total.
	ListMemories(context.Context, *ListMemoriesRequest) (*ListMemoriesResponse, error)
	// GetMemory returns one memory, live or invalidated.
	GetMemory(context.Context, *GetMemoryRequest) (*GetMemoryResponse, error)
	// UpdateMemory edits a live raw fact; observations are rejected. It enqueues a rederive_memory job.
	UpdateMemory(context.Context, *UpdateMemoryRequest) (*UpdateMemoryResponse, error)
	// InvalidateMemory archives a live raw fact: recall and consolidation stop seeing it, and RestoreMemory
	// brings it back. Observations are rejected. Everything happens in the call; no operation is returned.
	InvalidateMemory(context.Context, *InvalidateMemoryRequest) (*InvalidateMemoryResponse, error)
	// RestoreMemory brings an invalidated raw fact back and enqueues a rederive_memory job. Restoring a live
	// fact changes nothing.
	RestoreMemory(context.Context, *RestoreMemoryRequest) (*RestoreMemoryResponse, error)
	mustEmbedUnimplementedMemoryServiceServer()
}

// UnimplementedMemoryServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedMemoryServiceServer struct{}

func (UnimplementedMemoryServiceServer) Retain(context.Context, *RetainRequest) (*RetainResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method Retain not implemented")
}
func (UnimplementedMemoryServiceServer) Recall(context.Context, *RecallRequest) (*RecallResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method Recall not implemented")
}
func (UnimplementedMemoryServiceServer) Reflect(context.Context, *ReflectRequest) (*ReflectResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method Reflect not implemented")
}
func (UnimplementedMemoryServiceServer) ListRecallArms(context.Context, *ListRecallArmsRequest) (*ListRecallArmsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListRecallArms not implemented")
}
func (UnimplementedMemoryServiceServer) ListMemories(context.Context, *ListMemoriesRequest) (*ListMemoriesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListMemories not implemented")
}
func (UnimplementedMemoryServiceServer) GetMemory(context.Context, *GetMemoryRequest) (*GetMemoryResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetMemory not implemented")
}
func (UnimplementedMemoryServiceServer) UpdateMemory(context.Context, *UpdateMemoryRequest) (*UpdateMemoryResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method UpdateMemory not implemented")
}
func (UnimplementedMemoryServiceServer) InvalidateMemory(context.Context, *InvalidateMemoryRequest) (*InvalidateMemoryResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method InvalidateMemory not implemented")
}
func (UnimplementedMemoryServiceServer) RestoreMemory(context.Context, *RestoreMemoryRequest) (*RestoreMemoryResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method RestoreMemory not implemented")
}
func (UnimplementedMemoryServiceServer) mustEmbedUnimplementedMemoryServiceServer() {}
func (UnimplementedMemoryServiceServer) testEmbeddedByValue()                       {}

// UnsafeMemoryServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to MemoryServiceServer will
// result in compilation errors.
type UnsafeMemoryServiceServer interface {
	mustEmbedUnimplementedMemoryServiceServer()
}

func RegisterMemoryServiceServer(s grpc.ServiceRegistrar, srv MemoryServiceServer) {
	// If the following call panics, it indicates UnimplementedMemoryServiceServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&MemoryService_ServiceDesc, srv)
}

func _MemoryService_Retain_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RetainRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MemoryServiceServer).Retain(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MemoryService_Retain_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MemoryServiceServer).Retain(ctx, req.(*RetainRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MemoryService_Recall_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RecallRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MemoryServiceServer).Recall(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MemoryService_Recall_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MemoryServiceServer).Recall(ctx, req.(*RecallRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MemoryService_Reflect_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ReflectRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MemoryServiceServer).Reflect(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MemoryService_Reflect_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MemoryServiceServer).Reflect(ctx, req.(*ReflectRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MemoryService_ListRecallArms_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListRecallArmsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MemoryServiceServer).ListRecallArms(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MemoryService_ListRecallArms_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MemoryServiceServer).ListRecallArms(ctx, req.(*ListRecallArmsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MemoryService_ListMemories_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListMemoriesRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MemoryServiceServer).ListMemories(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MemoryService_ListMemories_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MemoryServiceServer).ListMemories(ctx, req.(*ListMemoriesRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MemoryService_GetMemory_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetMemoryRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MemoryServiceServer).GetMemory(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MemoryService_GetMemory_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MemoryServiceServer).GetMemory(ctx, req.(*GetMemoryRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MemoryService_UpdateMemory_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateMemoryRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MemoryServiceServer).UpdateMemory(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MemoryService_UpdateMemory_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MemoryServiceServer).UpdateMemory(ctx, req.(*UpdateMemoryRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MemoryService_InvalidateMemory_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(InvalidateMemoryRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MemoryServiceServer).InvalidateMemory(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MemoryService_InvalidateMemory_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MemoryServiceServer).InvalidateMemory(ctx, req.(*InvalidateMemoryRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MemoryService_RestoreMemory_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RestoreMemoryRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MemoryServiceServer).RestoreMemory(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MemoryService_RestoreMemory_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MemoryServiceServer).RestoreMemory(ctx, req.(*RestoreMemoryRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// MemoryService_ServiceDesc is the grpc.ServiceDesc for MemoryService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var MemoryService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "memory.v1.MemoryService",
	HandlerType: (*MemoryServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Retain",
			Handler:    _MemoryService_Retain_Handler,
		},
		{
			MethodName: "Recall",
			Handler:    _MemoryService_Recall_Handler,
		},
		{
			MethodName: "Reflect",
			Handler:    _MemoryService_Reflect_Handler,
		},
		{
			MethodName: "ListRecallArms",
			Handler:    _MemoryService_ListRecallArms_Handler,
		},
		{
			MethodName: "ListMemories",
			Handler:    _MemoryService_ListMemories_Handler,
		},
		{
			MethodName: "GetMemory",
			Handler:    _MemoryService_GetMemory_Handler,
		},
		{
			MethodName: "UpdateMemory",
			Handler:    _MemoryService_UpdateMemory_Handler,
		},
		{
			MethodName: "InvalidateMemory",
			Handler:    _MemoryService_InvalidateMemory_Handler,
		},
		{
			MethodName: "RestoreMemory",
			Handler:    _MemoryService_RestoreMemory_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "memory/v1/memory.proto",
}

const (
	DocumentService_ListDocuments_FullMethodName     = "/memory.v1.DocumentService/ListDocuments"
	DocumentService_GetDocument_FullMethodName       = "/memory.v1.DocumentService/GetDocument"
	DocumentService_RetagDocument_FullMethodName     = "/memory.v1.DocumentService/RetagDocument"
	DocumentService_ReprocessDocument_FullMethodName = "/memory.v1.DocumentService/ReprocessDocument"
)

// DocumentServiceClient is the client API for DocumentService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
//
// DocumentService lists, reads, retags and reprocesses retained documents.
type DocumentServiceClient interface {
	// ListDocuments pages through the bank's documents with filters.
	ListDocuments(ctx context.Context, in *ListDocumentsRequest, opts ...grpc.CallOption) (*ListDocumentsResponse, error)
	// GetDocument returns one document, optionally with its original text from the blob store.
	GetDocument(ctx context.Context, in *GetDocumentRequest, opts ...grpc.CallOption) (*GetDocumentResponse, error)
	// RetagDocument replaces the document's tag set. A set-equal retag is a no-op. A real change rewrites the
	// tags of the document's memories at once and, as a side effect, enqueues a retag_document job that clears
	// every observation citing those memories and re-consolidates them under the new tags.
	RetagDocument(ctx context.Context, in *RetagDocumentRequest, opts ...grpc.CallOption) (*RetagDocumentResponse, error)
	// ReprocessDocument re-extracts the document from its original text, replacing its memories and resetting
	// their curation. Replays the stored overrides unless new ones are given.
	ReprocessDocument(ctx context.Context, in *ReprocessDocumentRequest, opts ...grpc.CallOption) (*ReprocessDocumentResponse, error)
}

type documentServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewDocumentServiceClient(cc grpc.ClientConnInterface) DocumentServiceClient {
	return &documentServiceClient{cc}
}

func (c *documentServiceClient) ListDocuments(ctx context.Context, in *ListDocumentsRequest, opts ...grpc.CallOption) (*ListDocumentsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListDocumentsResponse)
	err := c.cc.Invoke(ctx, DocumentService_ListDocuments_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *documentServiceClient) GetDocument(ctx context.Context, in *GetDocumentRequest, opts ...grpc.CallOption) (*GetDocumentResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetDocumentResponse)
	err := c.cc.Invoke(ctx, DocumentService_GetDocument_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *documentServiceClient) RetagDocument(ctx context.Context, in *RetagDocumentRequest, opts ...grpc.CallOption) (*RetagDocumentResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(RetagDocumentResponse)
	err := c.cc.Invoke(ctx, DocumentService_RetagDocument_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *documentServiceClient) ReprocessDocument(ctx context.Context, in *ReprocessDocumentRequest, opts ...grpc.CallOption) (*ReprocessDocumentResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ReprocessDocumentResponse)
	err := c.cc.Invoke(ctx, DocumentService_ReprocessDocument_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DocumentServiceServer is the server API for DocumentService service.
// All implementations must embed UnimplementedDocumentServiceServer
// for forward compatibility.
//
// DocumentService lists, reads, retags and reprocesses retained documents.
type DocumentServiceServer interface {
	// ListDocuments pages through the bank's documents with filters.
	ListDocuments(context.Context, *ListDocumentsRequest) (*ListDocumentsResponse, error)
	// GetDocument returns one document, optionally with its original text from the blob store.
	GetDocument(context.Context, *GetDocumentRequest) (*GetDocumentResponse, error)
	// RetagDocument replaces the document's tag set. A set-equal retag is a no-op. A real change rewrites the
	// tags of the document's memories at once and, as a side effect, enqueues a retag_document job that clears
	// every observation citing those memories and re-consolidates them under the new tags.
	RetagDocument(context.Context, *RetagDocumentRequest) (*RetagDocumentResponse, error)
	// ReprocessDocument re-extracts the document from its original text, replacing its memories and resetting
	// their curation. Replays the stored overrides unless new ones are given.
	ReprocessDocument(context.Context, *ReprocessDocumentRequest) (*ReprocessDocumentResponse, error)
	mustEmbedUnimplementedDocumentServiceServer()
}

// UnimplementedDocumentServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedDocumentServiceServer struct{}

func (UnimplementedDocumentServiceServer) ListDocuments(context.Context, *ListDocumentsRequest) (*ListDocumentsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListDocuments not implemented")
}
func (UnimplementedDocumentServiceServer) GetDocument(context.Context, *GetDocumentRequest) (*GetDocumentResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetDocument not implemented")
}
func (UnimplementedDocumentServiceServer) RetagDocument(context.Context, *RetagDocumentRequest) (*RetagDocumentResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method RetagDocument not implemented")
}
func (UnimplementedDocumentServiceServer) ReprocessDocument(context.Context, *ReprocessDocumentRequest) (*ReprocessDocumentResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ReprocessDocument not implemented")
}
func (UnimplementedDocumentServiceServer) mustEmbedUnimplementedDocumentServiceServer() {}
func (UnimplementedDocumentServiceServer) testEmbeddedByValue()                         {}

// UnsafeDocumentServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to DocumentServiceServer will
// result in compilation errors.
type UnsafeDocumentServiceServer interface {
	mustEmbedUnimplementedDocumentServiceServer()
}

func RegisterDocumentServiceServer(s grpc.ServiceRegistrar, srv DocumentServiceServer) {
	// If the following call panics, it indicates UnimplementedDocumentServiceServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&DocumentService_ServiceDesc, srv)
}

func _DocumentService_ListDocuments_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListDocumentsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(DocumentServiceServer).ListDocuments(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: DocumentService_ListDocuments_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(DocumentServiceServer).ListDocuments(ctx, req.(*ListDocumentsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _DocumentService_GetDocument_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetDocumentRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(DocumentServiceServer).GetDocument(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: DocumentService_GetDocument_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(DocumentServiceServer).GetDocument(ctx, req.(*GetDocumentRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _DocumentService_RetagDocument_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RetagDocumentRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(DocumentServiceServer).RetagDocument(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: DocumentService_RetagDocument_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(DocumentServiceServer).RetagDocument(ctx, req.(*RetagDocumentRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _DocumentService_ReprocessDocument_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ReprocessDocumentRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(DocumentServiceServer).ReprocessDocument(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: DocumentService_ReprocessDocument_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(DocumentServiceServer).ReprocessDocument(ctx, req.(*ReprocessDocumentRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// DocumentService_ServiceDesc is the grpc.ServiceDesc for DocumentService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var DocumentService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "memory.v1.DocumentService",
	HandlerType: (*DocumentServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "ListDocuments",
			Handler:    _DocumentService_ListDocuments_Handler,
		},
		{
			MethodName: "GetDocument",
			Handler:    _DocumentService_GetDocument_Handler,
		},
		{
			MethodName: "RetagDocument",
			Handler:    _DocumentService_RetagDocument_Handler,
		},
		{
			MethodName: "ReprocessDocument",
			Handler:    _DocumentService_ReprocessDocument_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "memory/v1/memory.proto",
}

const (
	ObservationService_Consolidate_FullMethodName                    = "/memory.v1.ObservationService/Consolidate"
	ObservationService_ListObservationScopes_FullMethodName          = "/memory.v1.ObservationService/ListObservationScopes"
	ObservationService_ListObservationHistory_FullMethodName         = "/memory.v1.ObservationService/ListObservationHistory"
	ObservationService_ClearObservations_FullMethodName              = "/memory.v1.ObservationService/ClearObservations"
	ObservationService_PreviewConsolidationStrategies_FullMethodName = "/memory.v1.ObservationService/PreviewConsolidationStrategies"
)

// ObservationServiceClient is the client API for ObservationService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
//
// ObservationService runs consolidation and lists, inspects and clears observations.
type ObservationServiceClient interface {
	// Consolidate enqueues a manual consolidation, optionally limited to scopes and with its own overrides.
	// A job carrying overrides or scopes is never merged into another queued job.
	Consolidate(ctx context.Context, in *ConsolidateRequest, opts ...grpc.CallOption) (*ConsolidateResponse, error)
	// ListObservationScopes pages through the bank's observation scopes with counts.
	ListObservationScopes(ctx context.Context, in *ListObservationScopesRequest, opts ...grpc.CallOption) (*ListObservationScopesResponse, error)
	// ListObservationHistory pages through the versions of one observation, newest first.
	ListObservationHistory(ctx context.Context, in *ListObservationHistoryRequest, opts ...grpc.CallOption) (*ListObservationHistoryResponse, error)
	// ClearObservations deletes the observations of exact scopes, of one memory or of the whole bank, resets
	// their sources for re-consolidation, and optionally re-consolidates at once.
	ClearObservations(ctx context.Context, in *ClearObservationsRequest, opts ...grpc.CallOption) (*ClearObservationsResponse, error)
	// PreviewConsolidationStrategies evaluates draft strategies against the bank's existing scopes without
	// saving them.
	PreviewConsolidationStrategies(ctx context.Context, in *PreviewConsolidationStrategiesRequest, opts ...grpc.CallOption) (*PreviewConsolidationStrategiesResponse, error)
}

type observationServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewObservationServiceClient(cc grpc.ClientConnInterface) ObservationServiceClient {
	return &observationServiceClient{cc}
}

func (c *observationServiceClient) Consolidate(ctx context.Context, in *ConsolidateRequest, opts ...grpc.CallOption) (*ConsolidateResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ConsolidateResponse)
	err := c.cc.Invoke(ctx, ObservationService_Consolidate_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *observationServiceClient) ListObservationScopes(ctx context.Context, in *ListObservationScopesRequest, opts ...grpc.CallOption) (*ListObservationScopesResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListObservationScopesResponse)
	err := c.cc.Invoke(ctx, ObservationService_ListObservationScopes_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *observationServiceClient) ListObservationHistory(ctx context.Context, in *ListObservationHistoryRequest, opts ...grpc.CallOption) (*ListObservationHistoryResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListObservationHistoryResponse)
	err := c.cc.Invoke(ctx, ObservationService_ListObservationHistory_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *observationServiceClient) ClearObservations(ctx context.Context, in *ClearObservationsRequest, opts ...grpc.CallOption) (*ClearObservationsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ClearObservationsResponse)
	err := c.cc.Invoke(ctx, ObservationService_ClearObservations_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *observationServiceClient) PreviewConsolidationStrategies(ctx context.Context, in *PreviewConsolidationStrategiesRequest, opts ...grpc.CallOption) (*PreviewConsolidationStrategiesResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(PreviewConsolidationStrategiesResponse)
	err := c.cc.Invoke(ctx, ObservationService_PreviewConsolidationStrategies_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ObservationServiceServer is the server API for ObservationService service.
// All implementations must embed UnimplementedObservationServiceServer
// for forward compatibility.
//
// ObservationService runs consolidation and lists, inspects and clears observations.
type ObservationServiceServer interface {
	// Consolidate enqueues a manual consolidation, optionally limited to scopes and with its own overrides.
	// A job carrying overrides or scopes is never merged into another queued job.
	Consolidate(context.Context, *ConsolidateRequest) (*ConsolidateResponse, error)
	// ListObservationScopes pages through the bank's observation scopes with counts.
	ListObservationScopes(context.Context, *ListObservationScopesRequest) (*ListObservationScopesResponse, error)
	// ListObservationHistory pages through the versions of one observation, newest first.
	ListObservationHistory(context.Context, *ListObservationHistoryRequest) (*ListObservationHistoryResponse, error)
	// ClearObservations deletes the observations of exact scopes, of one memory or of the whole bank, resets
	// their sources for re-consolidation, and optionally re-consolidates at once.
	ClearObservations(context.Context, *ClearObservationsRequest) (*ClearObservationsResponse, error)
	// PreviewConsolidationStrategies evaluates draft strategies against the bank's existing scopes without
	// saving them.
	PreviewConsolidationStrategies(context.Context, *PreviewConsolidationStrategiesRequest) (*PreviewConsolidationStrategiesResponse, error)
	mustEmbedUnimplementedObservationServiceServer()
}

// UnimplementedObservationServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedObservationServiceServer struct{}

func (UnimplementedObservationServiceServer) Consolidate(context.Context, *ConsolidateRequest) (*ConsolidateResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method Consolidate not implemented")
}
func (UnimplementedObservationServiceServer) ListObservationScopes(context.Context, *ListObservationScopesRequest) (*ListObservationScopesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListObservationScopes not implemented")
}
func (UnimplementedObservationServiceServer) ListObservationHistory(context.Context, *ListObservationHistoryRequest) (*ListObservationHistoryResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListObservationHistory not implemented")
}
func (UnimplementedObservationServiceServer) ClearObservations(context.Context, *ClearObservationsRequest) (*ClearObservationsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ClearObservations not implemented")
}
func (UnimplementedObservationServiceServer) PreviewConsolidationStrategies(context.Context, *PreviewConsolidationStrategiesRequest) (*PreviewConsolidationStrategiesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method PreviewConsolidationStrategies not implemented")
}
func (UnimplementedObservationServiceServer) mustEmbedUnimplementedObservationServiceServer() {}
func (UnimplementedObservationServiceServer) testEmbeddedByValue()                            {}

// UnsafeObservationServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to ObservationServiceServer will
// result in compilation errors.
type UnsafeObservationServiceServer interface {
	mustEmbedUnimplementedObservationServiceServer()
}

func RegisterObservationServiceServer(s grpc.ServiceRegistrar, srv ObservationServiceServer) {
	// If the following call panics, it indicates UnimplementedObservationServiceServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&ObservationService_ServiceDesc, srv)
}

func _ObservationService_Consolidate_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ConsolidateRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ObservationServiceServer).Consolidate(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ObservationService_Consolidate_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ObservationServiceServer).Consolidate(ctx, req.(*ConsolidateRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ObservationService_ListObservationScopes_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListObservationScopesRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ObservationServiceServer).ListObservationScopes(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ObservationService_ListObservationScopes_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ObservationServiceServer).ListObservationScopes(ctx, req.(*ListObservationScopesRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ObservationService_ListObservationHistory_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListObservationHistoryRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ObservationServiceServer).ListObservationHistory(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ObservationService_ListObservationHistory_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ObservationServiceServer).ListObservationHistory(ctx, req.(*ListObservationHistoryRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ObservationService_ClearObservations_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ClearObservationsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ObservationServiceServer).ClearObservations(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ObservationService_ClearObservations_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ObservationServiceServer).ClearObservations(ctx, req.(*ClearObservationsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ObservationService_PreviewConsolidationStrategies_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PreviewConsolidationStrategiesRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ObservationServiceServer).PreviewConsolidationStrategies(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ObservationService_PreviewConsolidationStrategies_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ObservationServiceServer).PreviewConsolidationStrategies(ctx, req.(*PreviewConsolidationStrategiesRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// ObservationService_ServiceDesc is the grpc.ServiceDesc for ObservationService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var ObservationService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "memory.v1.ObservationService",
	HandlerType: (*ObservationServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Consolidate",
			Handler:    _ObservationService_Consolidate_Handler,
		},
		{
			MethodName: "ListObservationScopes",
			Handler:    _ObservationService_ListObservationScopes_Handler,
		},
		{
			MethodName: "ListObservationHistory",
			Handler:    _ObservationService_ListObservationHistory_Handler,
		},
		{
			MethodName: "ClearObservations",
			Handler:    _ObservationService_ClearObservations_Handler,
		},
		{
			MethodName: "PreviewConsolidationStrategies",
			Handler:    _ObservationService_PreviewConsolidationStrategies_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "memory/v1/memory.proto",
}

const (
	MentalModelService_CreateMentalModel_FullMethodName         = "/memory.v1.MentalModelService/CreateMentalModel"
	MentalModelService_GetMentalModel_FullMethodName            = "/memory.v1.MentalModelService/GetMentalModel"
	MentalModelService_ListMentalModels_FullMethodName          = "/memory.v1.MentalModelService/ListMentalModels"
	MentalModelService_UpdateMentalModel_FullMethodName         = "/memory.v1.MentalModelService/UpdateMentalModel"
	MentalModelService_RefreshMentalModel_FullMethodName        = "/memory.v1.MentalModelService/RefreshMentalModel"
	MentalModelService_PreviewMentalModelRefresh_FullMethodName = "/memory.v1.MentalModelService/PreviewMentalModelRefresh"
	MentalModelService_ListMentalModelHistory_FullMethodName    = "/memory.v1.MentalModelService/ListMentalModelHistory"
	MentalModelService_DeleteMentalModel_FullMethodName         = "/memory.v1.MentalModelService/DeleteMentalModel"
	MentalModelService_DeleteMentalModelFolder_FullMethodName   = "/memory.v1.MentalModelService/DeleteMentalModelFolder"
	MentalModelService_SearchMentalModels_FullMethodName        = "/memory.v1.MentalModelService/SearchMentalModels"
)

// MentalModelServiceClient is the client API for MentalModelService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
//
// MentalModelService creates, reads, refreshes and searches mental models.
type MentalModelServiceClient interface {
	// CreateMentalModel stores a model and enqueues its first build asynchronously.
	CreateMentalModel(ctx context.Context, in *CreateMentalModelRequest, opts ...grpc.CallOption) (*CreateMentalModelResponse, error)
	// GetMentalModel returns one model in the requested view, default MENTAL_MODEL_VIEW_FULL.
	GetMentalModel(ctx context.Context, in *GetMentalModelRequest, opts ...grpc.CallOption) (*GetMentalModelResponse, error)
	// ListMentalModels pages through models with filters, default MENTAL_MODEL_VIEW_BASIC.
	ListMentalModels(ctx context.Context, in *ListMentalModelsRequest, opts ...grpc.CallOption) (*ListMentalModelsResponse, error)
	// UpdateMentalModel patches a model's spec with a FieldMask.
	UpdateMentalModel(ctx context.Context, in *UpdateMentalModelRequest, opts ...grpc.CallOption) (*UpdateMentalModelResponse, error)
	// RefreshMentalModel enqueues a manual refresh. It ignores the interval floor, releases a parked
	// refresh, joins and upgrades a queued one, and on success clears auto_refresh_paused.
	RefreshMentalModel(ctx context.Context, in *RefreshMentalModelRequest, opts ...grpc.CallOption) (*RefreshMentalModelResponse, error)
	// PreviewMentalModelRefresh runs the full refresh pipeline synchronously within the RPC deadline and writes
	// nothing, not even usage.
	PreviewMentalModelRefresh(ctx context.Context, in *PreviewMentalModelRefreshRequest, opts ...grpc.CallOption) (*PreviewMentalModelRefreshResponse, error)
	// ListMentalModelHistory pages through a model's versions and refresh failures, newest first.
	ListMentalModelHistory(ctx context.Context, in *ListMentalModelHistoryRequest, opts ...grpc.CallOption) (*ListMentalModelHistoryResponse, error)
	// DeleteMentalModel deletes one model and its history.
	DeleteMentalModel(ctx context.Context, in *DeleteMentalModelRequest, opts ...grpc.CallOption) (*DeleteMentalModelResponse, error)
	// DeleteMentalModelFolder deletes every model under a folder path, recursively.
	DeleteMentalModelFolder(ctx context.Context, in *DeleteMentalModelFolderRequest, opts ...grpc.CallOption) (*DeleteMentalModelFolderResponse, error)
	// SearchMentalModels ranks whole models by fused full-text and semantic scores, without rerank.
	SearchMentalModels(ctx context.Context, in *SearchMentalModelsRequest, opts ...grpc.CallOption) (*SearchMentalModelsResponse, error)
}

type mentalModelServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewMentalModelServiceClient(cc grpc.ClientConnInterface) MentalModelServiceClient {
	return &mentalModelServiceClient{cc}
}

func (c *mentalModelServiceClient) CreateMentalModel(ctx context.Context, in *CreateMentalModelRequest, opts ...grpc.CallOption) (*CreateMentalModelResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(CreateMentalModelResponse)
	err := c.cc.Invoke(ctx, MentalModelService_CreateMentalModel_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *mentalModelServiceClient) GetMentalModel(ctx context.Context, in *GetMentalModelRequest, opts ...grpc.CallOption) (*GetMentalModelResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetMentalModelResponse)
	err := c.cc.Invoke(ctx, MentalModelService_GetMentalModel_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *mentalModelServiceClient) ListMentalModels(ctx context.Context, in *ListMentalModelsRequest, opts ...grpc.CallOption) (*ListMentalModelsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListMentalModelsResponse)
	err := c.cc.Invoke(ctx, MentalModelService_ListMentalModels_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *mentalModelServiceClient) UpdateMentalModel(ctx context.Context, in *UpdateMentalModelRequest, opts ...grpc.CallOption) (*UpdateMentalModelResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(UpdateMentalModelResponse)
	err := c.cc.Invoke(ctx, MentalModelService_UpdateMentalModel_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *mentalModelServiceClient) RefreshMentalModel(ctx context.Context, in *RefreshMentalModelRequest, opts ...grpc.CallOption) (*RefreshMentalModelResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(RefreshMentalModelResponse)
	err := c.cc.Invoke(ctx, MentalModelService_RefreshMentalModel_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *mentalModelServiceClient) PreviewMentalModelRefresh(ctx context.Context, in *PreviewMentalModelRefreshRequest, opts ...grpc.CallOption) (*PreviewMentalModelRefreshResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(PreviewMentalModelRefreshResponse)
	err := c.cc.Invoke(ctx, MentalModelService_PreviewMentalModelRefresh_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *mentalModelServiceClient) ListMentalModelHistory(ctx context.Context, in *ListMentalModelHistoryRequest, opts ...grpc.CallOption) (*ListMentalModelHistoryResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListMentalModelHistoryResponse)
	err := c.cc.Invoke(ctx, MentalModelService_ListMentalModelHistory_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *mentalModelServiceClient) DeleteMentalModel(ctx context.Context, in *DeleteMentalModelRequest, opts ...grpc.CallOption) (*DeleteMentalModelResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(DeleteMentalModelResponse)
	err := c.cc.Invoke(ctx, MentalModelService_DeleteMentalModel_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *mentalModelServiceClient) DeleteMentalModelFolder(ctx context.Context, in *DeleteMentalModelFolderRequest, opts ...grpc.CallOption) (*DeleteMentalModelFolderResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(DeleteMentalModelFolderResponse)
	err := c.cc.Invoke(ctx, MentalModelService_DeleteMentalModelFolder_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *mentalModelServiceClient) SearchMentalModels(ctx context.Context, in *SearchMentalModelsRequest, opts ...grpc.CallOption) (*SearchMentalModelsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(SearchMentalModelsResponse)
	err := c.cc.Invoke(ctx, MentalModelService_SearchMentalModels_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MentalModelServiceServer is the server API for MentalModelService service.
// All implementations must embed UnimplementedMentalModelServiceServer
// for forward compatibility.
//
// MentalModelService creates, reads, refreshes and searches mental models.
type MentalModelServiceServer interface {
	// CreateMentalModel stores a model and enqueues its first build asynchronously.
	CreateMentalModel(context.Context, *CreateMentalModelRequest) (*CreateMentalModelResponse, error)
	// GetMentalModel returns one model in the requested view, default MENTAL_MODEL_VIEW_FULL.
	GetMentalModel(context.Context, *GetMentalModelRequest) (*GetMentalModelResponse, error)
	// ListMentalModels pages through models with filters, default MENTAL_MODEL_VIEW_BASIC.
	ListMentalModels(context.Context, *ListMentalModelsRequest) (*ListMentalModelsResponse, error)
	// UpdateMentalModel patches a model's spec with a FieldMask.
	UpdateMentalModel(context.Context, *UpdateMentalModelRequest) (*UpdateMentalModelResponse, error)
	// RefreshMentalModel enqueues a manual refresh. It ignores the interval floor, releases a parked
	// refresh, joins and upgrades a queued one, and on success clears auto_refresh_paused.
	RefreshMentalModel(context.Context, *RefreshMentalModelRequest) (*RefreshMentalModelResponse, error)
	// PreviewMentalModelRefresh runs the full refresh pipeline synchronously within the RPC deadline and writes
	// nothing, not even usage.
	PreviewMentalModelRefresh(context.Context, *PreviewMentalModelRefreshRequest) (*PreviewMentalModelRefreshResponse, error)
	// ListMentalModelHistory pages through a model's versions and refresh failures, newest first.
	ListMentalModelHistory(context.Context, *ListMentalModelHistoryRequest) (*ListMentalModelHistoryResponse, error)
	// DeleteMentalModel deletes one model and its history.
	DeleteMentalModel(context.Context, *DeleteMentalModelRequest) (*DeleteMentalModelResponse, error)
	// DeleteMentalModelFolder deletes every model under a folder path, recursively.
	DeleteMentalModelFolder(context.Context, *DeleteMentalModelFolderRequest) (*DeleteMentalModelFolderResponse, error)
	// SearchMentalModels ranks whole models by fused full-text and semantic scores, without rerank.
	SearchMentalModels(context.Context, *SearchMentalModelsRequest) (*SearchMentalModelsResponse, error)
	mustEmbedUnimplementedMentalModelServiceServer()
}

// UnimplementedMentalModelServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedMentalModelServiceServer struct{}

func (UnimplementedMentalModelServiceServer) CreateMentalModel(context.Context, *CreateMentalModelRequest) (*CreateMentalModelResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method CreateMentalModel not implemented")
}
func (UnimplementedMentalModelServiceServer) GetMentalModel(context.Context, *GetMentalModelRequest) (*GetMentalModelResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetMentalModel not implemented")
}
func (UnimplementedMentalModelServiceServer) ListMentalModels(context.Context, *ListMentalModelsRequest) (*ListMentalModelsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListMentalModels not implemented")
}
func (UnimplementedMentalModelServiceServer) UpdateMentalModel(context.Context, *UpdateMentalModelRequest) (*UpdateMentalModelResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method UpdateMentalModel not implemented")
}
func (UnimplementedMentalModelServiceServer) RefreshMentalModel(context.Context, *RefreshMentalModelRequest) (*RefreshMentalModelResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method RefreshMentalModel not implemented")
}
func (UnimplementedMentalModelServiceServer) PreviewMentalModelRefresh(context.Context, *PreviewMentalModelRefreshRequest) (*PreviewMentalModelRefreshResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method PreviewMentalModelRefresh not implemented")
}
func (UnimplementedMentalModelServiceServer) ListMentalModelHistory(context.Context, *ListMentalModelHistoryRequest) (*ListMentalModelHistoryResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListMentalModelHistory not implemented")
}
func (UnimplementedMentalModelServiceServer) DeleteMentalModel(context.Context, *DeleteMentalModelRequest) (*DeleteMentalModelResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method DeleteMentalModel not implemented")
}
func (UnimplementedMentalModelServiceServer) DeleteMentalModelFolder(context.Context, *DeleteMentalModelFolderRequest) (*DeleteMentalModelFolderResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method DeleteMentalModelFolder not implemented")
}
func (UnimplementedMentalModelServiceServer) SearchMentalModels(context.Context, *SearchMentalModelsRequest) (*SearchMentalModelsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method SearchMentalModels not implemented")
}
func (UnimplementedMentalModelServiceServer) mustEmbedUnimplementedMentalModelServiceServer() {}
func (UnimplementedMentalModelServiceServer) testEmbeddedByValue()                            {}

// UnsafeMentalModelServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to MentalModelServiceServer will
// result in compilation errors.
type UnsafeMentalModelServiceServer interface {
	mustEmbedUnimplementedMentalModelServiceServer()
}

func RegisterMentalModelServiceServer(s grpc.ServiceRegistrar, srv MentalModelServiceServer) {
	// If the following call panics, it indicates UnimplementedMentalModelServiceServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&MentalModelService_ServiceDesc, srv)
}

func _MentalModelService_CreateMentalModel_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateMentalModelRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MentalModelServiceServer).CreateMentalModel(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MentalModelService_CreateMentalModel_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MentalModelServiceServer).CreateMentalModel(ctx, req.(*CreateMentalModelRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MentalModelService_GetMentalModel_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetMentalModelRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MentalModelServiceServer).GetMentalModel(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MentalModelService_GetMentalModel_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MentalModelServiceServer).GetMentalModel(ctx, req.(*GetMentalModelRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MentalModelService_ListMentalModels_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListMentalModelsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MentalModelServiceServer).ListMentalModels(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MentalModelService_ListMentalModels_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MentalModelServiceServer).ListMentalModels(ctx, req.(*ListMentalModelsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MentalModelService_UpdateMentalModel_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateMentalModelRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MentalModelServiceServer).UpdateMentalModel(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MentalModelService_UpdateMentalModel_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MentalModelServiceServer).UpdateMentalModel(ctx, req.(*UpdateMentalModelRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MentalModelService_RefreshMentalModel_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RefreshMentalModelRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MentalModelServiceServer).RefreshMentalModel(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MentalModelService_RefreshMentalModel_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MentalModelServiceServer).RefreshMentalModel(ctx, req.(*RefreshMentalModelRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MentalModelService_PreviewMentalModelRefresh_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PreviewMentalModelRefreshRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MentalModelServiceServer).PreviewMentalModelRefresh(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MentalModelService_PreviewMentalModelRefresh_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MentalModelServiceServer).PreviewMentalModelRefresh(ctx, req.(*PreviewMentalModelRefreshRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MentalModelService_ListMentalModelHistory_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListMentalModelHistoryRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MentalModelServiceServer).ListMentalModelHistory(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MentalModelService_ListMentalModelHistory_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MentalModelServiceServer).ListMentalModelHistory(ctx, req.(*ListMentalModelHistoryRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MentalModelService_DeleteMentalModel_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(DeleteMentalModelRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MentalModelServiceServer).DeleteMentalModel(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MentalModelService_DeleteMentalModel_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MentalModelServiceServer).DeleteMentalModel(ctx, req.(*DeleteMentalModelRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MentalModelService_DeleteMentalModelFolder_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(DeleteMentalModelFolderRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MentalModelServiceServer).DeleteMentalModelFolder(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MentalModelService_DeleteMentalModelFolder_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MentalModelServiceServer).DeleteMentalModelFolder(ctx, req.(*DeleteMentalModelFolderRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _MentalModelService_SearchMentalModels_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(SearchMentalModelsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MentalModelServiceServer).SearchMentalModels(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MentalModelService_SearchMentalModels_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MentalModelServiceServer).SearchMentalModels(ctx, req.(*SearchMentalModelsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// MentalModelService_ServiceDesc is the grpc.ServiceDesc for MentalModelService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var MentalModelService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "memory.v1.MentalModelService",
	HandlerType: (*MentalModelServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "CreateMentalModel",
			Handler:    _MentalModelService_CreateMentalModel_Handler,
		},
		{
			MethodName: "GetMentalModel",
			Handler:    _MentalModelService_GetMentalModel_Handler,
		},
		{
			MethodName: "ListMentalModels",
			Handler:    _MentalModelService_ListMentalModels_Handler,
		},
		{
			MethodName: "UpdateMentalModel",
			Handler:    _MentalModelService_UpdateMentalModel_Handler,
		},
		{
			MethodName: "RefreshMentalModel",
			Handler:    _MentalModelService_RefreshMentalModel_Handler,
		},
		{
			MethodName: "PreviewMentalModelRefresh",
			Handler:    _MentalModelService_PreviewMentalModelRefresh_Handler,
		},
		{
			MethodName: "ListMentalModelHistory",
			Handler:    _MentalModelService_ListMentalModelHistory_Handler,
		},
		{
			MethodName: "DeleteMentalModel",
			Handler:    _MentalModelService_DeleteMentalModel_Handler,
		},
		{
			MethodName: "DeleteMentalModelFolder",
			Handler:    _MentalModelService_DeleteMentalModelFolder_Handler,
		},
		{
			MethodName: "SearchMentalModels",
			Handler:    _MentalModelService_SearchMentalModels_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "memory/v1/memory.proto",
}

const (
	BankService_CreateBank_FullMethodName         = "/memory.v1.BankService/CreateBank"
	BankService_GetBank_FullMethodName            = "/memory.v1.BankService/GetBank"
	BankService_ListBanks_FullMethodName          = "/memory.v1.BankService/ListBanks"
	BankService_DeleteBank_FullMethodName         = "/memory.v1.BankService/DeleteBank"
	BankService_GetBankStats_FullMethodName       = "/memory.v1.BankService/GetBankStats"
	BankService_ListTags_FullMethodName           = "/memory.v1.BankService/ListTags"
	BankService_ExportBankTemplate_FullMethodName = "/memory.v1.BankService/ExportBankTemplate"
)

// BankServiceClient is the client API for BankService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
//
// BankService manages the calling tenant's banks: create (optionally from a template), get, list, delete,
// stats, tags and template export.
type BankServiceClient interface {
	// CreateBank creates a bank with initial config overrides, or from a template (its config, directives and
	// mental models, whose first builds it enqueues). An existing bank = ALREADY_EXISTS; a bank being deleted =
	// FAILED_PRECONDITION (BANK_DELETING).
	CreateBank(ctx context.Context, in *CreateBankRequest, opts ...grpc.CallOption) (*CreateBankResponse, error)
	// GetBank returns one bank. Unknown, or being deleted = NOT_FOUND (BANK_NOT_FOUND).
	GetBank(ctx context.Context, in *GetBankRequest, opts ...grpc.CallOption) (*GetBankResponse, error)
	// ListBanks pages through the tenant's banks in id order; banks being deleted are left out.
	ListBanks(ctx context.Context, in *ListBanksRequest, opts ...grpc.CallOption) (*ListBanksResponse, error)
	// DeleteBank marks the bank as being deleted, cancels its pending operations and enqueues delete_bank, all
	// at once, and returns that operation. The only bulk hard delete: while it runs, writes fail FAILED_PRECONDITION
	// (BANK_DELETING) and reads NOT_FOUND (BANK_NOT_FOUND), except GetOperation of the bank's delete_bank
	// operations. To observe completion, poll GetOperation with the returned operation id: it answers while the
	// delete runs, and NOT_FOUND once the bank is gone, because the delete's last step removes the bank with all
	// its operations and events. A large delete runs in bounded steps, so the returned operation may end
	// COMPLETED while a later step finishes the work. Once the bank is gone, a write creates a new, empty bank.
	DeleteBank(ctx context.Context, in *DeleteBankRequest, opts ...grpc.CallOption) (*DeleteBankResponse, error)
	// GetBankStats returns counts of the bank's contents; poll pending_operations_by_kind as an eval
	// barrier.
	GetBankStats(ctx context.Context, in *GetBankStatsRequest, opts ...grpc.CallOption) (*GetBankStatsResponse, error)
	// ListTags pages through the bank's tags with usage counts.
	ListTags(ctx context.Context, in *ListTagsRequest, opts ...grpc.CallOption) (*ListTagsResponse, error)
	// ExportBankTemplate exports the bank's own config, directives and mental-model specs.
	ExportBankTemplate(ctx context.Context, in *ExportBankTemplateRequest, opts ...grpc.CallOption) (*ExportBankTemplateResponse, error)
}

type bankServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewBankServiceClient(cc grpc.ClientConnInterface) BankServiceClient {
	return &bankServiceClient{cc}
}

func (c *bankServiceClient) CreateBank(ctx context.Context, in *CreateBankRequest, opts ...grpc.CallOption) (*CreateBankResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(CreateBankResponse)
	err := c.cc.Invoke(ctx, BankService_CreateBank_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *bankServiceClient) GetBank(ctx context.Context, in *GetBankRequest, opts ...grpc.CallOption) (*GetBankResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetBankResponse)
	err := c.cc.Invoke(ctx, BankService_GetBank_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *bankServiceClient) ListBanks(ctx context.Context, in *ListBanksRequest, opts ...grpc.CallOption) (*ListBanksResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListBanksResponse)
	err := c.cc.Invoke(ctx, BankService_ListBanks_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *bankServiceClient) DeleteBank(ctx context.Context, in *DeleteBankRequest, opts ...grpc.CallOption) (*DeleteBankResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(DeleteBankResponse)
	err := c.cc.Invoke(ctx, BankService_DeleteBank_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *bankServiceClient) GetBankStats(ctx context.Context, in *GetBankStatsRequest, opts ...grpc.CallOption) (*GetBankStatsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetBankStatsResponse)
	err := c.cc.Invoke(ctx, BankService_GetBankStats_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *bankServiceClient) ListTags(ctx context.Context, in *ListTagsRequest, opts ...grpc.CallOption) (*ListTagsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListTagsResponse)
	err := c.cc.Invoke(ctx, BankService_ListTags_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *bankServiceClient) ExportBankTemplate(ctx context.Context, in *ExportBankTemplateRequest, opts ...grpc.CallOption) (*ExportBankTemplateResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ExportBankTemplateResponse)
	err := c.cc.Invoke(ctx, BankService_ExportBankTemplate_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// BankServiceServer is the server API for BankService service.
// All implementations must embed UnimplementedBankServiceServer
// for forward compatibility.
//
// BankService manages the calling tenant's banks: create (optionally from a template), get, list, delete,
// stats, tags and template export.
type BankServiceServer interface {
	// CreateBank creates a bank with initial config overrides, or from a template (its config, directives and
	// mental models, whose first builds it enqueues). An existing bank = ALREADY_EXISTS; a bank being deleted =
	// FAILED_PRECONDITION (BANK_DELETING).
	CreateBank(context.Context, *CreateBankRequest) (*CreateBankResponse, error)
	// GetBank returns one bank. Unknown, or being deleted = NOT_FOUND (BANK_NOT_FOUND).
	GetBank(context.Context, *GetBankRequest) (*GetBankResponse, error)
	// ListBanks pages through the tenant's banks in id order; banks being deleted are left out.
	ListBanks(context.Context, *ListBanksRequest) (*ListBanksResponse, error)
	// DeleteBank marks the bank as being deleted, cancels its pending operations and enqueues delete_bank, all
	// at once, and returns that operation. The only bulk hard delete: while it runs, writes fail FAILED_PRECONDITION
	// (BANK_DELETING) and reads NOT_FOUND (BANK_NOT_FOUND), except GetOperation of the bank's delete_bank
	// operations. To observe completion, poll GetOperation with the returned operation id: it answers while the
	// delete runs, and NOT_FOUND once the bank is gone, because the delete's last step removes the bank with all
	// its operations and events. A large delete runs in bounded steps, so the returned operation may end
	// COMPLETED while a later step finishes the work. Once the bank is gone, a write creates a new, empty bank.
	DeleteBank(context.Context, *DeleteBankRequest) (*DeleteBankResponse, error)
	// GetBankStats returns counts of the bank's contents; poll pending_operations_by_kind as an eval
	// barrier.
	GetBankStats(context.Context, *GetBankStatsRequest) (*GetBankStatsResponse, error)
	// ListTags pages through the bank's tags with usage counts.
	ListTags(context.Context, *ListTagsRequest) (*ListTagsResponse, error)
	// ExportBankTemplate exports the bank's own config, directives and mental-model specs.
	ExportBankTemplate(context.Context, *ExportBankTemplateRequest) (*ExportBankTemplateResponse, error)
	mustEmbedUnimplementedBankServiceServer()
}

// UnimplementedBankServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedBankServiceServer struct{}

func (UnimplementedBankServiceServer) CreateBank(context.Context, *CreateBankRequest) (*CreateBankResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method CreateBank not implemented")
}
func (UnimplementedBankServiceServer) GetBank(context.Context, *GetBankRequest) (*GetBankResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetBank not implemented")
}
func (UnimplementedBankServiceServer) ListBanks(context.Context, *ListBanksRequest) (*ListBanksResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListBanks not implemented")
}
func (UnimplementedBankServiceServer) DeleteBank(context.Context, *DeleteBankRequest) (*DeleteBankResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method DeleteBank not implemented")
}
func (UnimplementedBankServiceServer) GetBankStats(context.Context, *GetBankStatsRequest) (*GetBankStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetBankStats not implemented")
}
func (UnimplementedBankServiceServer) ListTags(context.Context, *ListTagsRequest) (*ListTagsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListTags not implemented")
}
func (UnimplementedBankServiceServer) ExportBankTemplate(context.Context, *ExportBankTemplateRequest) (*ExportBankTemplateResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ExportBankTemplate not implemented")
}
func (UnimplementedBankServiceServer) mustEmbedUnimplementedBankServiceServer() {}
func (UnimplementedBankServiceServer) testEmbeddedByValue()                     {}

// UnsafeBankServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to BankServiceServer will
// result in compilation errors.
type UnsafeBankServiceServer interface {
	mustEmbedUnimplementedBankServiceServer()
}

func RegisterBankServiceServer(s grpc.ServiceRegistrar, srv BankServiceServer) {
	// If the following call panics, it indicates UnimplementedBankServiceServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&BankService_ServiceDesc, srv)
}

func _BankService_CreateBank_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateBankRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankServiceServer).CreateBank(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankService_CreateBank_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankServiceServer).CreateBank(ctx, req.(*CreateBankRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BankService_GetBank_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetBankRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankServiceServer).GetBank(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankService_GetBank_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankServiceServer).GetBank(ctx, req.(*GetBankRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BankService_ListBanks_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListBanksRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankServiceServer).ListBanks(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankService_ListBanks_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankServiceServer).ListBanks(ctx, req.(*ListBanksRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BankService_DeleteBank_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(DeleteBankRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankServiceServer).DeleteBank(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankService_DeleteBank_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankServiceServer).DeleteBank(ctx, req.(*DeleteBankRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BankService_GetBankStats_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetBankStatsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankServiceServer).GetBankStats(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankService_GetBankStats_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankServiceServer).GetBankStats(ctx, req.(*GetBankStatsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BankService_ListTags_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListTagsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankServiceServer).ListTags(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankService_ListTags_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankServiceServer).ListTags(ctx, req.(*ListTagsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BankService_ExportBankTemplate_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ExportBankTemplateRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankServiceServer).ExportBankTemplate(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankService_ExportBankTemplate_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankServiceServer).ExportBankTemplate(ctx, req.(*ExportBankTemplateRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// BankService_ServiceDesc is the grpc.ServiceDesc for BankService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var BankService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "memory.v1.BankService",
	HandlerType: (*BankServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "CreateBank",
			Handler:    _BankService_CreateBank_Handler,
		},
		{
			MethodName: "GetBank",
			Handler:    _BankService_GetBank_Handler,
		},
		{
			MethodName: "ListBanks",
			Handler:    _BankService_ListBanks_Handler,
		},
		{
			MethodName: "DeleteBank",
			Handler:    _BankService_DeleteBank_Handler,
		},
		{
			MethodName: "GetBankStats",
			Handler:    _BankService_GetBankStats_Handler,
		},
		{
			MethodName: "ListTags",
			Handler:    _BankService_ListTags_Handler,
		},
		{
			MethodName: "ExportBankTemplate",
			Handler:    _BankService_ExportBankTemplate_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "memory/v1/memory.proto",
}

const (
	ConfigService_GetTenantConfig_FullMethodName    = "/memory.v1.ConfigService/GetTenantConfig"
	ConfigService_UpdateTenantConfig_FullMethodName = "/memory.v1.ConfigService/UpdateTenantConfig"
	ConfigService_ResetTenantConfig_FullMethodName  = "/memory.v1.ConfigService/ResetTenantConfig"
	ConfigService_GetBankConfig_FullMethodName      = "/memory.v1.ConfigService/GetBankConfig"
	ConfigService_UpdateBankConfig_FullMethodName   = "/memory.v1.ConfigService/UpdateBankConfig"
	ConfigService_ResetBankConfig_FullMethodName    = "/memory.v1.ConfigService/ResetBankConfig"
	ConfigService_PreviewPrompt_FullMethodName      = "/memory.v1.ConfigService/PreviewPrompt"
	ConfigService_CreateDirective_FullMethodName    = "/memory.v1.ConfigService/CreateDirective"
	ConfigService_UpdateDirective_FullMethodName    = "/memory.v1.ConfigService/UpdateDirective"
	ConfigService_ListDirectives_FullMethodName     = "/memory.v1.ConfigService/ListDirectives"
	ConfigService_DeleteDirective_FullMethodName    = "/memory.v1.ConfigService/DeleteDirective"
)

// ConfigServiceClient is the client API for ConfigService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
//
// ConfigService reads and writes how the tenant's banks behave: the tenant and bank config levels, the prompt
// preview, and each bank's directives. Every config RPC returns a ConfigView: the level's own overrides and
// the effective config they resolve to.
type ConfigServiceClient interface {
	// GetTenantConfig returns the tenant's own config and the effective tenant defaults (server < tenant).
	GetTenantConfig(ctx context.Context, in *GetTenantConfigRequest, opts ...grpc.CallOption) (*GetTenantConfigResponse, error)
	// UpdateTenantConfig patches the tenant's own config with a FieldMask.
	UpdateTenantConfig(ctx context.Context, in *UpdateTenantConfigRequest, opts ...grpc.CallOption) (*UpdateTenantConfigResponse, error)
	// ResetTenantConfig removes the tenant's own config at the given paths, so they inherit the server defaults
	// again.
	ResetTenantConfig(ctx context.Context, in *ResetTenantConfigRequest, opts ...grpc.CallOption) (*ResetTenantConfigResponse, error)
	// GetBankConfig returns the bank's own overrides and its effective config (server < tenant < bank).
	GetBankConfig(ctx context.Context, in *GetBankConfigRequest, opts ...grpc.CallOption) (*GetBankConfigResponse, error)
	// UpdateBankConfig patches the bank's own overrides with a FieldMask.
	UpdateBankConfig(ctx context.Context, in *UpdateBankConfigRequest, opts ...grpc.CallOption) (*UpdateBankConfigResponse, error)
	// ResetBankConfig removes the bank's own overrides at the given paths, so they inherit again.
	ResetBankConfig(ctx context.Context, in *ResetBankConfigRequest, opts ...grpc.CallOption) (*ResetBankConfigResponse, error)
	// PreviewPrompt renders the exact messages an operation would send; no LLM call is made.
	PreviewPrompt(ctx context.Context, in *PreviewPromptRequest, opts ...grpc.CallOption) (*PreviewPromptResponse, error)
	// CreateDirective adds a directive to the bank; its name must be new in the bank.
	CreateDirective(ctx context.Context, in *CreateDirectiveRequest, opts ...grpc.CallOption) (*CreateDirectiveResponse, error)
	// UpdateDirective patches a directive with a FieldMask.
	UpdateDirective(ctx context.Context, in *UpdateDirectiveRequest, opts ...grpc.CallOption) (*UpdateDirectiveResponse, error)
	// ListDirectives pages through the bank's directives, oldest first, optionally by tag and active state.
	ListDirectives(ctx context.Context, in *ListDirectivesRequest, opts ...grpc.CallOption) (*ListDirectivesResponse, error)
	// DeleteDirective deletes one directive.
	DeleteDirective(ctx context.Context, in *DeleteDirectiveRequest, opts ...grpc.CallOption) (*DeleteDirectiveResponse, error)
}

type configServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewConfigServiceClient(cc grpc.ClientConnInterface) ConfigServiceClient {
	return &configServiceClient{cc}
}

func (c *configServiceClient) GetTenantConfig(ctx context.Context, in *GetTenantConfigRequest, opts ...grpc.CallOption) (*GetTenantConfigResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetTenantConfigResponse)
	err := c.cc.Invoke(ctx, ConfigService_GetTenantConfig_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *configServiceClient) UpdateTenantConfig(ctx context.Context, in *UpdateTenantConfigRequest, opts ...grpc.CallOption) (*UpdateTenantConfigResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(UpdateTenantConfigResponse)
	err := c.cc.Invoke(ctx, ConfigService_UpdateTenantConfig_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *configServiceClient) ResetTenantConfig(ctx context.Context, in *ResetTenantConfigRequest, opts ...grpc.CallOption) (*ResetTenantConfigResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ResetTenantConfigResponse)
	err := c.cc.Invoke(ctx, ConfigService_ResetTenantConfig_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *configServiceClient) GetBankConfig(ctx context.Context, in *GetBankConfigRequest, opts ...grpc.CallOption) (*GetBankConfigResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetBankConfigResponse)
	err := c.cc.Invoke(ctx, ConfigService_GetBankConfig_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *configServiceClient) UpdateBankConfig(ctx context.Context, in *UpdateBankConfigRequest, opts ...grpc.CallOption) (*UpdateBankConfigResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(UpdateBankConfigResponse)
	err := c.cc.Invoke(ctx, ConfigService_UpdateBankConfig_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *configServiceClient) ResetBankConfig(ctx context.Context, in *ResetBankConfigRequest, opts ...grpc.CallOption) (*ResetBankConfigResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ResetBankConfigResponse)
	err := c.cc.Invoke(ctx, ConfigService_ResetBankConfig_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *configServiceClient) PreviewPrompt(ctx context.Context, in *PreviewPromptRequest, opts ...grpc.CallOption) (*PreviewPromptResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(PreviewPromptResponse)
	err := c.cc.Invoke(ctx, ConfigService_PreviewPrompt_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *configServiceClient) CreateDirective(ctx context.Context, in *CreateDirectiveRequest, opts ...grpc.CallOption) (*CreateDirectiveResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(CreateDirectiveResponse)
	err := c.cc.Invoke(ctx, ConfigService_CreateDirective_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *configServiceClient) UpdateDirective(ctx context.Context, in *UpdateDirectiveRequest, opts ...grpc.CallOption) (*UpdateDirectiveResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(UpdateDirectiveResponse)
	err := c.cc.Invoke(ctx, ConfigService_UpdateDirective_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *configServiceClient) ListDirectives(ctx context.Context, in *ListDirectivesRequest, opts ...grpc.CallOption) (*ListDirectivesResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListDirectivesResponse)
	err := c.cc.Invoke(ctx, ConfigService_ListDirectives_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *configServiceClient) DeleteDirective(ctx context.Context, in *DeleteDirectiveRequest, opts ...grpc.CallOption) (*DeleteDirectiveResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(DeleteDirectiveResponse)
	err := c.cc.Invoke(ctx, ConfigService_DeleteDirective_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ConfigServiceServer is the server API for ConfigService service.
// All implementations must embed UnimplementedConfigServiceServer
// for forward compatibility.
//
// ConfigService reads and writes how the tenant's banks behave: the tenant and bank config levels, the prompt
// preview, and each bank's directives. Every config RPC returns a ConfigView: the level's own overrides and
// the effective config they resolve to.
type ConfigServiceServer interface {
	// GetTenantConfig returns the tenant's own config and the effective tenant defaults (server < tenant).
	GetTenantConfig(context.Context, *GetTenantConfigRequest) (*GetTenantConfigResponse, error)
	// UpdateTenantConfig patches the tenant's own config with a FieldMask.
	UpdateTenantConfig(context.Context, *UpdateTenantConfigRequest) (*UpdateTenantConfigResponse, error)
	// ResetTenantConfig removes the tenant's own config at the given paths, so they inherit the server defaults
	// again.
	ResetTenantConfig(context.Context, *ResetTenantConfigRequest) (*ResetTenantConfigResponse, error)
	// GetBankConfig returns the bank's own overrides and its effective config (server < tenant < bank).
	GetBankConfig(context.Context, *GetBankConfigRequest) (*GetBankConfigResponse, error)
	// UpdateBankConfig patches the bank's own overrides with a FieldMask.
	UpdateBankConfig(context.Context, *UpdateBankConfigRequest) (*UpdateBankConfigResponse, error)
	// ResetBankConfig removes the bank's own overrides at the given paths, so they inherit again.
	ResetBankConfig(context.Context, *ResetBankConfigRequest) (*ResetBankConfigResponse, error)
	// PreviewPrompt renders the exact messages an operation would send; no LLM call is made.
	PreviewPrompt(context.Context, *PreviewPromptRequest) (*PreviewPromptResponse, error)
	// CreateDirective adds a directive to the bank; its name must be new in the bank.
	CreateDirective(context.Context, *CreateDirectiveRequest) (*CreateDirectiveResponse, error)
	// UpdateDirective patches a directive with a FieldMask.
	UpdateDirective(context.Context, *UpdateDirectiveRequest) (*UpdateDirectiveResponse, error)
	// ListDirectives pages through the bank's directives, oldest first, optionally by tag and active state.
	ListDirectives(context.Context, *ListDirectivesRequest) (*ListDirectivesResponse, error)
	// DeleteDirective deletes one directive.
	DeleteDirective(context.Context, *DeleteDirectiveRequest) (*DeleteDirectiveResponse, error)
	mustEmbedUnimplementedConfigServiceServer()
}

// UnimplementedConfigServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedConfigServiceServer struct{}

func (UnimplementedConfigServiceServer) GetTenantConfig(context.Context, *GetTenantConfigRequest) (*GetTenantConfigResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetTenantConfig not implemented")
}
func (UnimplementedConfigServiceServer) UpdateTenantConfig(context.Context, *UpdateTenantConfigRequest) (*UpdateTenantConfigResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method UpdateTenantConfig not implemented")
}
func (UnimplementedConfigServiceServer) ResetTenantConfig(context.Context, *ResetTenantConfigRequest) (*ResetTenantConfigResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ResetTenantConfig not implemented")
}
func (UnimplementedConfigServiceServer) GetBankConfig(context.Context, *GetBankConfigRequest) (*GetBankConfigResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetBankConfig not implemented")
}
func (UnimplementedConfigServiceServer) UpdateBankConfig(context.Context, *UpdateBankConfigRequest) (*UpdateBankConfigResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method UpdateBankConfig not implemented")
}
func (UnimplementedConfigServiceServer) ResetBankConfig(context.Context, *ResetBankConfigRequest) (*ResetBankConfigResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ResetBankConfig not implemented")
}
func (UnimplementedConfigServiceServer) PreviewPrompt(context.Context, *PreviewPromptRequest) (*PreviewPromptResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method PreviewPrompt not implemented")
}
func (UnimplementedConfigServiceServer) CreateDirective(context.Context, *CreateDirectiveRequest) (*CreateDirectiveResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method CreateDirective not implemented")
}
func (UnimplementedConfigServiceServer) UpdateDirective(context.Context, *UpdateDirectiveRequest) (*UpdateDirectiveResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method UpdateDirective not implemented")
}
func (UnimplementedConfigServiceServer) ListDirectives(context.Context, *ListDirectivesRequest) (*ListDirectivesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListDirectives not implemented")
}
func (UnimplementedConfigServiceServer) DeleteDirective(context.Context, *DeleteDirectiveRequest) (*DeleteDirectiveResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method DeleteDirective not implemented")
}
func (UnimplementedConfigServiceServer) mustEmbedUnimplementedConfigServiceServer() {}
func (UnimplementedConfigServiceServer) testEmbeddedByValue()                       {}

// UnsafeConfigServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to ConfigServiceServer will
// result in compilation errors.
type UnsafeConfigServiceServer interface {
	mustEmbedUnimplementedConfigServiceServer()
}

func RegisterConfigServiceServer(s grpc.ServiceRegistrar, srv ConfigServiceServer) {
	// If the following call panics, it indicates UnimplementedConfigServiceServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&ConfigService_ServiceDesc, srv)
}

func _ConfigService_GetTenantConfig_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetTenantConfigRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ConfigServiceServer).GetTenantConfig(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ConfigService_GetTenantConfig_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ConfigServiceServer).GetTenantConfig(ctx, req.(*GetTenantConfigRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ConfigService_UpdateTenantConfig_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateTenantConfigRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ConfigServiceServer).UpdateTenantConfig(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ConfigService_UpdateTenantConfig_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ConfigServiceServer).UpdateTenantConfig(ctx, req.(*UpdateTenantConfigRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ConfigService_ResetTenantConfig_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ResetTenantConfigRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ConfigServiceServer).ResetTenantConfig(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ConfigService_ResetTenantConfig_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ConfigServiceServer).ResetTenantConfig(ctx, req.(*ResetTenantConfigRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ConfigService_GetBankConfig_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetBankConfigRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ConfigServiceServer).GetBankConfig(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ConfigService_GetBankConfig_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ConfigServiceServer).GetBankConfig(ctx, req.(*GetBankConfigRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ConfigService_UpdateBankConfig_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateBankConfigRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ConfigServiceServer).UpdateBankConfig(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ConfigService_UpdateBankConfig_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ConfigServiceServer).UpdateBankConfig(ctx, req.(*UpdateBankConfigRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ConfigService_ResetBankConfig_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ResetBankConfigRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ConfigServiceServer).ResetBankConfig(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ConfigService_ResetBankConfig_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ConfigServiceServer).ResetBankConfig(ctx, req.(*ResetBankConfigRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ConfigService_PreviewPrompt_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PreviewPromptRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ConfigServiceServer).PreviewPrompt(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ConfigService_PreviewPrompt_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ConfigServiceServer).PreviewPrompt(ctx, req.(*PreviewPromptRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ConfigService_CreateDirective_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateDirectiveRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ConfigServiceServer).CreateDirective(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ConfigService_CreateDirective_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ConfigServiceServer).CreateDirective(ctx, req.(*CreateDirectiveRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ConfigService_UpdateDirective_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateDirectiveRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ConfigServiceServer).UpdateDirective(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ConfigService_UpdateDirective_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ConfigServiceServer).UpdateDirective(ctx, req.(*UpdateDirectiveRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ConfigService_ListDirectives_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListDirectivesRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ConfigServiceServer).ListDirectives(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ConfigService_ListDirectives_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ConfigServiceServer).ListDirectives(ctx, req.(*ListDirectivesRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ConfigService_DeleteDirective_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(DeleteDirectiveRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ConfigServiceServer).DeleteDirective(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ConfigService_DeleteDirective_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ConfigServiceServer).DeleteDirective(ctx, req.(*DeleteDirectiveRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// ConfigService_ServiceDesc is the grpc.ServiceDesc for ConfigService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var ConfigService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "memory.v1.ConfigService",
	HandlerType: (*ConfigServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetTenantConfig",
			Handler:    _ConfigService_GetTenantConfig_Handler,
		},
		{
			MethodName: "UpdateTenantConfig",
			Handler:    _ConfigService_UpdateTenantConfig_Handler,
		},
		{
			MethodName: "ResetTenantConfig",
			Handler:    _ConfigService_ResetTenantConfig_Handler,
		},
		{
			MethodName: "GetBankConfig",
			Handler:    _ConfigService_GetBankConfig_Handler,
		},
		{
			MethodName: "UpdateBankConfig",
			Handler:    _ConfigService_UpdateBankConfig_Handler,
		},
		{
			MethodName: "ResetBankConfig",
			Handler:    _ConfigService_ResetBankConfig_Handler,
		},
		{
			MethodName: "PreviewPrompt",
			Handler:    _ConfigService_PreviewPrompt_Handler,
		},
		{
			MethodName: "CreateDirective",
			Handler:    _ConfigService_CreateDirective_Handler,
		},
		{
			MethodName: "UpdateDirective",
			Handler:    _ConfigService_UpdateDirective_Handler,
		},
		{
			MethodName: "ListDirectives",
			Handler:    _ConfigService_ListDirectives_Handler,
		},
		{
			MethodName: "DeleteDirective",
			Handler:    _ConfigService_DeleteDirective_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "memory/v1/memory.proto",
}

const (
	OperationService_GetOperation_FullMethodName    = "/memory.v1.OperationService/GetOperation"
	OperationService_ListOperations_FullMethodName  = "/memory.v1.OperationService/ListOperations"
	OperationService_CancelOperation_FullMethodName = "/memory.v1.OperationService/CancelOperation"
	OperationService_RetryOperation_FullMethodName  = "/memory.v1.OperationService/RetryOperation"
	OperationService_ListEvents_FullMethodName      = "/memory.v1.OperationService/ListEvents"
)

// OperationServiceClient is the client API for OperationService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
//
// OperationService reads and controls background operations and serves the lifecycle event feed.
type OperationServiceClient interface {
	// GetOperation returns one operation of the bank. For a bank being deleted it answers only for the bank's
	// delete_bank operations (any other id = NOT_FOUND, BANK_NOT_FOUND); once the delete is done they are
	// NOT_FOUND too.
	GetOperation(ctx context.Context, in *GetOperationRequest, opts ...grpc.CallOption) (*GetOperationResponse, error)
	// ListOperations pages through the bank's operations, optionally filtered by status and kind.
	ListOperations(ctx context.Context, in *ListOperationsRequest, opts ...grpc.CallOption) (*ListOperationsResponse, error)
	// CancelOperation cancels a PENDING or PROCESSING operation. A pending one becomes CANCELLED at once.
	// A processing one stays PROCESSING with the cancel recorded until it
	// stops at its next checkpoint, then becomes CANCELLED; committed work stays.
	CancelOperation(ctx context.Context, in *CancelOperationRequest, opts ...grpc.CallOption) (*CancelOperationResponse, error)
	// RetryOperation re-queues a FAILED or CANCELLED operation: it sets it back to PENDING to run
	// again at once. Other states are rejected.
	RetryOperation(ctx context.Context, in *RetryOperationRequest, opts ...grpc.CallOption) (*RetryOperationResponse, error)
	// ListEvents polls the bank's lifecycle events by sequence number.
	ListEvents(ctx context.Context, in *ListEventsRequest, opts ...grpc.CallOption) (*ListEventsResponse, error)
}

type operationServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewOperationServiceClient(cc grpc.ClientConnInterface) OperationServiceClient {
	return &operationServiceClient{cc}
}

func (c *operationServiceClient) GetOperation(ctx context.Context, in *GetOperationRequest, opts ...grpc.CallOption) (*GetOperationResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetOperationResponse)
	err := c.cc.Invoke(ctx, OperationService_GetOperation_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *operationServiceClient) ListOperations(ctx context.Context, in *ListOperationsRequest, opts ...grpc.CallOption) (*ListOperationsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListOperationsResponse)
	err := c.cc.Invoke(ctx, OperationService_ListOperations_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *operationServiceClient) CancelOperation(ctx context.Context, in *CancelOperationRequest, opts ...grpc.CallOption) (*CancelOperationResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(CancelOperationResponse)
	err := c.cc.Invoke(ctx, OperationService_CancelOperation_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *operationServiceClient) RetryOperation(ctx context.Context, in *RetryOperationRequest, opts ...grpc.CallOption) (*RetryOperationResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(RetryOperationResponse)
	err := c.cc.Invoke(ctx, OperationService_RetryOperation_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *operationServiceClient) ListEvents(ctx context.Context, in *ListEventsRequest, opts ...grpc.CallOption) (*ListEventsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListEventsResponse)
	err := c.cc.Invoke(ctx, OperationService_ListEvents_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// OperationServiceServer is the server API for OperationService service.
// All implementations must embed UnimplementedOperationServiceServer
// for forward compatibility.
//
// OperationService reads and controls background operations and serves the lifecycle event feed.
type OperationServiceServer interface {
	// GetOperation returns one operation of the bank. For a bank being deleted it answers only for the bank's
	// delete_bank operations (any other id = NOT_FOUND, BANK_NOT_FOUND); once the delete is done they are
	// NOT_FOUND too.
	GetOperation(context.Context, *GetOperationRequest) (*GetOperationResponse, error)
	// ListOperations pages through the bank's operations, optionally filtered by status and kind.
	ListOperations(context.Context, *ListOperationsRequest) (*ListOperationsResponse, error)
	// CancelOperation cancels a PENDING or PROCESSING operation. A pending one becomes CANCELLED at once.
	// A processing one stays PROCESSING with the cancel recorded until it
	// stops at its next checkpoint, then becomes CANCELLED; committed work stays.
	CancelOperation(context.Context, *CancelOperationRequest) (*CancelOperationResponse, error)
	// RetryOperation re-queues a FAILED or CANCELLED operation: it sets it back to PENDING to run
	// again at once. Other states are rejected.
	RetryOperation(context.Context, *RetryOperationRequest) (*RetryOperationResponse, error)
	// ListEvents polls the bank's lifecycle events by sequence number.
	ListEvents(context.Context, *ListEventsRequest) (*ListEventsResponse, error)
	mustEmbedUnimplementedOperationServiceServer()
}

// UnimplementedOperationServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedOperationServiceServer struct{}

func (UnimplementedOperationServiceServer) GetOperation(context.Context, *GetOperationRequest) (*GetOperationResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetOperation not implemented")
}
func (UnimplementedOperationServiceServer) ListOperations(context.Context, *ListOperationsRequest) (*ListOperationsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListOperations not implemented")
}
func (UnimplementedOperationServiceServer) CancelOperation(context.Context, *CancelOperationRequest) (*CancelOperationResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method CancelOperation not implemented")
}
func (UnimplementedOperationServiceServer) RetryOperation(context.Context, *RetryOperationRequest) (*RetryOperationResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method RetryOperation not implemented")
}
func (UnimplementedOperationServiceServer) ListEvents(context.Context, *ListEventsRequest) (*ListEventsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListEvents not implemented")
}
func (UnimplementedOperationServiceServer) mustEmbedUnimplementedOperationServiceServer() {}
func (UnimplementedOperationServiceServer) testEmbeddedByValue()                          {}

// UnsafeOperationServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to OperationServiceServer will
// result in compilation errors.
type UnsafeOperationServiceServer interface {
	mustEmbedUnimplementedOperationServiceServer()
}

func RegisterOperationServiceServer(s grpc.ServiceRegistrar, srv OperationServiceServer) {
	// If the following call panics, it indicates UnimplementedOperationServiceServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&OperationService_ServiceDesc, srv)
}

func _OperationService_GetOperation_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetOperationRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(OperationServiceServer).GetOperation(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: OperationService_GetOperation_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(OperationServiceServer).GetOperation(ctx, req.(*GetOperationRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _OperationService_ListOperations_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListOperationsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(OperationServiceServer).ListOperations(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: OperationService_ListOperations_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(OperationServiceServer).ListOperations(ctx, req.(*ListOperationsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _OperationService_CancelOperation_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CancelOperationRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(OperationServiceServer).CancelOperation(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: OperationService_CancelOperation_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(OperationServiceServer).CancelOperation(ctx, req.(*CancelOperationRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _OperationService_RetryOperation_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RetryOperationRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(OperationServiceServer).RetryOperation(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: OperationService_RetryOperation_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(OperationServiceServer).RetryOperation(ctx, req.(*RetryOperationRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _OperationService_ListEvents_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListEventsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(OperationServiceServer).ListEvents(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: OperationService_ListEvents_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(OperationServiceServer).ListEvents(ctx, req.(*ListEventsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// OperationService_ServiceDesc is the grpc.ServiceDesc for OperationService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var OperationService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "memory.v1.OperationService",
	HandlerType: (*OperationServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetOperation",
			Handler:    _OperationService_GetOperation_Handler,
		},
		{
			MethodName: "ListOperations",
			Handler:    _OperationService_ListOperations_Handler,
		},
		{
			MethodName: "CancelOperation",
			Handler:    _OperationService_CancelOperation_Handler,
		},
		{
			MethodName: "RetryOperation",
			Handler:    _OperationService_RetryOperation_Handler,
		},
		{
			MethodName: "ListEvents",
			Handler:    _OperationService_ListEvents_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "memory/v1/memory.proto",
}

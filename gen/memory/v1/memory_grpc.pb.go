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
// Async work. Background work runs as operations. An RPC whose requested work runs in the background returns
// the Operation it enqueued, in a field named operation (or operations, batch_operation,
// mental_model_operations), never a bare id; OperationService reports its status and progress. Follow-up jobs
// an RPC triggers on its own (such as consolidation after a retain) are not returned. Retain is synchronous by
// default, returning once units are committed; run_async=true makes it return the enqueued operations instead.
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
// when the server knows them. Clients branch on the code, then the reason; the message is for humans. The reasons, by code:
//   INVALID_ARGUMENT     INVALID_ARGUMENT (malformed request), TOO_MANY_SCOPES (ALL_COMBINATIONS fan-out
//                        over max_combinations), MEMORY_IS_OBSERVATION (curating an observation)
//   NOT_FOUND            NOT_FOUND (the named object does not exist), BANK_NOT_FOUND (unknown bank, or any
//                        read of a bank being deleted)
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
	MemoryUnitService_GetObservationHistory_FullMethodName = "/memory.v1.MemoryUnitService/GetObservationHistory"
)

// MemoryUnitServiceClient is the client API for MemoryUnitService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
//
// MemoryUnitService reads the version history of observations.
type MemoryUnitServiceClient interface {
	// GetObservationHistory returns every version of an observation, oldest first.
	GetObservationHistory(ctx context.Context, in *GetObservationHistoryRequest, opts ...grpc.CallOption) (*GetObservationHistoryResponse, error)
}

type memoryUnitServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewMemoryUnitServiceClient(cc grpc.ClientConnInterface) MemoryUnitServiceClient {
	return &memoryUnitServiceClient{cc}
}

func (c *memoryUnitServiceClient) GetObservationHistory(ctx context.Context, in *GetObservationHistoryRequest, opts ...grpc.CallOption) (*GetObservationHistoryResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetObservationHistoryResponse)
	err := c.cc.Invoke(ctx, MemoryUnitService_GetObservationHistory_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MemoryUnitServiceServer is the server API for MemoryUnitService service.
// All implementations must embed UnimplementedMemoryUnitServiceServer
// for forward compatibility.
//
// MemoryUnitService reads the version history of observations.
type MemoryUnitServiceServer interface {
	// GetObservationHistory returns every version of an observation, oldest first.
	GetObservationHistory(context.Context, *GetObservationHistoryRequest) (*GetObservationHistoryResponse, error)
	mustEmbedUnimplementedMemoryUnitServiceServer()
}

// UnimplementedMemoryUnitServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedMemoryUnitServiceServer struct{}

func (UnimplementedMemoryUnitServiceServer) GetObservationHistory(context.Context, *GetObservationHistoryRequest) (*GetObservationHistoryResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetObservationHistory not implemented")
}
func (UnimplementedMemoryUnitServiceServer) mustEmbedUnimplementedMemoryUnitServiceServer() {}
func (UnimplementedMemoryUnitServiceServer) testEmbeddedByValue()                           {}

// UnsafeMemoryUnitServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to MemoryUnitServiceServer will
// result in compilation errors.
type UnsafeMemoryUnitServiceServer interface {
	mustEmbedUnimplementedMemoryUnitServiceServer()
}

func RegisterMemoryUnitServiceServer(s grpc.ServiceRegistrar, srv MemoryUnitServiceServer) {
	// If the following call panics, it indicates UnimplementedMemoryUnitServiceServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&MemoryUnitService_ServiceDesc, srv)
}

func _MemoryUnitService_GetObservationHistory_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetObservationHistoryRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MemoryUnitServiceServer).GetObservationHistory(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MemoryUnitService_GetObservationHistory_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MemoryUnitServiceServer).GetObservationHistory(ctx, req.(*GetObservationHistoryRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// MemoryUnitService_ServiceDesc is the grpc.ServiceDesc for MemoryUnitService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var MemoryUnitService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "memory.v1.MemoryUnitService",
	HandlerType: (*MemoryUnitServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetObservationHistory",
			Handler:    _MemoryUnitService_GetObservationHistory_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "memory/v1/memory.proto",
}

const (
	DocumentService_ListDocuments_FullMethodName     = "/memory.v1.DocumentService/ListDocuments"
	DocumentService_GetDocument_FullMethodName       = "/memory.v1.DocumentService/GetDocument"
	DocumentService_UpdateDocument_FullMethodName    = "/memory.v1.DocumentService/UpdateDocument"
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
	// UpdateDocument replaces the document's tag set. A set-equal update is a no-op; a real change clears
	// the document's observations and enqueues a retag_document job that re-consolidates under the new tags.
	UpdateDocument(ctx context.Context, in *UpdateDocumentRequest, opts ...grpc.CallOption) (*UpdateDocumentResponse, error)
	// ReprocessDocument re-extracts the document from its original text, replacing its units and resetting
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

func (c *documentServiceClient) UpdateDocument(ctx context.Context, in *UpdateDocumentRequest, opts ...grpc.CallOption) (*UpdateDocumentResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(UpdateDocumentResponse)
	err := c.cc.Invoke(ctx, DocumentService_UpdateDocument_FullMethodName, in, out, cOpts...)
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
	// UpdateDocument replaces the document's tag set. A set-equal update is a no-op; a real change clears
	// the document's observations and enqueues a retag_document job that re-consolidates under the new tags.
	UpdateDocument(context.Context, *UpdateDocumentRequest) (*UpdateDocumentResponse, error)
	// ReprocessDocument re-extracts the document from its original text, replacing its units and resetting
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
func (UnimplementedDocumentServiceServer) UpdateDocument(context.Context, *UpdateDocumentRequest) (*UpdateDocumentResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method UpdateDocument not implemented")
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

func _DocumentService_UpdateDocument_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateDocumentRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(DocumentServiceServer).UpdateDocument(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: DocumentService_UpdateDocument_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(DocumentServiceServer).UpdateDocument(ctx, req.(*UpdateDocumentRequest))
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
			MethodName: "UpdateDocument",
			Handler:    _DocumentService_UpdateDocument_Handler,
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
	ObservationService_ListScopes_FullMethodName                     = "/memory.v1.ObservationService/ListScopes"
	ObservationService_ClearObservations_FullMethodName              = "/memory.v1.ObservationService/ClearObservations"
	ObservationService_ClearMemoryObservations_FullMethodName        = "/memory.v1.ObservationService/ClearMemoryObservations"
	ObservationService_PreviewConsolidationStrategies_FullMethodName = "/memory.v1.ObservationService/PreviewConsolidationStrategies"
)

// ObservationServiceClient is the client API for ObservationService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
//
// ObservationService runs and inspects consolidation.
type ObservationServiceClient interface {
	// Consolidate enqueues a manual consolidation, optionally limited to scopes and with its own overrides.
	// A job carrying overrides or scopes is never merged into another queued job.
	Consolidate(ctx context.Context, in *ConsolidateRequest, opts ...grpc.CallOption) (*ConsolidateResponse, error)
	// ListScopes pages through the bank's observation scopes with counts.
	ListScopes(ctx context.Context, in *ListScopesRequest, opts ...grpc.CallOption) (*ListScopesResponse, error)
	// ClearObservations deletes observations in the given scopes (or all) and resets their sources,
	// optionally re-consolidating.
	ClearObservations(ctx context.Context, in *ClearObservationsRequest, opts ...grpc.CallOption) (*ClearObservationsResponse, error)
	// ClearMemoryObservations deletes the observations citing one unit and resets their co-sources,
	// optionally re-consolidating.
	ClearMemoryObservations(ctx context.Context, in *ClearMemoryObservationsRequest, opts ...grpc.CallOption) (*ClearMemoryObservationsResponse, error)
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

func (c *observationServiceClient) ListScopes(ctx context.Context, in *ListScopesRequest, opts ...grpc.CallOption) (*ListScopesResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListScopesResponse)
	err := c.cc.Invoke(ctx, ObservationService_ListScopes_FullMethodName, in, out, cOpts...)
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

func (c *observationServiceClient) ClearMemoryObservations(ctx context.Context, in *ClearMemoryObservationsRequest, opts ...grpc.CallOption) (*ClearMemoryObservationsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ClearMemoryObservationsResponse)
	err := c.cc.Invoke(ctx, ObservationService_ClearMemoryObservations_FullMethodName, in, out, cOpts...)
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
// ObservationService runs and inspects consolidation.
type ObservationServiceServer interface {
	// Consolidate enqueues a manual consolidation, optionally limited to scopes and with its own overrides.
	// A job carrying overrides or scopes is never merged into another queued job.
	Consolidate(context.Context, *ConsolidateRequest) (*ConsolidateResponse, error)
	// ListScopes pages through the bank's observation scopes with counts.
	ListScopes(context.Context, *ListScopesRequest) (*ListScopesResponse, error)
	// ClearObservations deletes observations in the given scopes (or all) and resets their sources,
	// optionally re-consolidating.
	ClearObservations(context.Context, *ClearObservationsRequest) (*ClearObservationsResponse, error)
	// ClearMemoryObservations deletes the observations citing one unit and resets their co-sources,
	// optionally re-consolidating.
	ClearMemoryObservations(context.Context, *ClearMemoryObservationsRequest) (*ClearMemoryObservationsResponse, error)
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
func (UnimplementedObservationServiceServer) ListScopes(context.Context, *ListScopesRequest) (*ListScopesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListScopes not implemented")
}
func (UnimplementedObservationServiceServer) ClearObservations(context.Context, *ClearObservationsRequest) (*ClearObservationsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ClearObservations not implemented")
}
func (UnimplementedObservationServiceServer) ClearMemoryObservations(context.Context, *ClearMemoryObservationsRequest) (*ClearMemoryObservationsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ClearMemoryObservations not implemented")
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

func _ObservationService_ListScopes_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListScopesRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ObservationServiceServer).ListScopes(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ObservationService_ListScopes_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ObservationServiceServer).ListScopes(ctx, req.(*ListScopesRequest))
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

func _ObservationService_ClearMemoryObservations_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ClearMemoryObservationsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ObservationServiceServer).ClearMemoryObservations(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ObservationService_ClearMemoryObservations_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ObservationServiceServer).ClearMemoryObservations(ctx, req.(*ClearMemoryObservationsRequest))
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
			MethodName: "ListScopes",
			Handler:    _ObservationService_ListScopes_Handler,
		},
		{
			MethodName: "ClearObservations",
			Handler:    _ObservationService_ClearObservations_Handler,
		},
		{
			MethodName: "ClearMemoryObservations",
			Handler:    _ObservationService_ClearMemoryObservations_Handler,
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
	BankService_CreateBank_FullMethodName             = "/memory.v1.BankService/CreateBank"
	BankService_GetBank_FullMethodName                = "/memory.v1.BankService/GetBank"
	BankService_GetBankConfig_FullMethodName          = "/memory.v1.BankService/GetBankConfig"
	BankService_UpdateBankConfig_FullMethodName       = "/memory.v1.BankService/UpdateBankConfig"
	BankService_ResetBankConfig_FullMethodName        = "/memory.v1.BankService/ResetBankConfig"
	BankService_GetBankStats_FullMethodName           = "/memory.v1.BankService/GetBankStats"
	BankService_ListTags_FullMethodName               = "/memory.v1.BankService/ListTags"
	BankService_PreviewPrompt_FullMethodName          = "/memory.v1.BankService/PreviewPrompt"
	BankService_DeleteBank_FullMethodName             = "/memory.v1.BankService/DeleteBank"
	BankService_ExportBankTemplate_FullMethodName     = "/memory.v1.BankService/ExportBankTemplate"
	BankService_CreateBankFromTemplate_FullMethodName = "/memory.v1.BankService/CreateBankFromTemplate"
	BankService_UpsertDirective_FullMethodName        = "/memory.v1.BankService/UpsertDirective"
	BankService_ListDirectives_FullMethodName         = "/memory.v1.BankService/ListDirectives"
	BankService_DeleteDirective_FullMethodName        = "/memory.v1.BankService/DeleteDirective"
)

// BankServiceClient is the client API for BankService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
//
// BankService manages banks, their config, directives and templates.
type BankServiceClient interface {
	// CreateBank creates a bank with optional config overrides.
	CreateBank(ctx context.Context, in *CreateBankRequest, opts ...grpc.CallOption) (*CreateBankResponse, error)
	// GetBank returns a bank and its own config overrides.
	GetBank(ctx context.Context, in *GetBankRequest, opts ...grpc.CallOption) (*GetBankResponse, error)
	// GetBankConfig returns the resolved config (server < tenant < bank) and the bank's own overrides.
	GetBankConfig(ctx context.Context, in *GetBankConfigRequest, opts ...grpc.CallOption) (*GetBankConfigResponse, error)
	// UpdateBankConfig patches the bank's own overrides with a FieldMask.
	UpdateBankConfig(ctx context.Context, in *UpdateBankConfigRequest, opts ...grpc.CallOption) (*UpdateBankConfigResponse, error)
	// ResetBankConfig removes the bank's own overrides at the given paths, so they inherit again.
	ResetBankConfig(ctx context.Context, in *ResetBankConfigRequest, opts ...grpc.CallOption) (*ResetBankConfigResponse, error)
	// GetBankStats returns counts from indexed COUNT queries; poll pending_operations_by_kind as an eval
	// barrier.
	GetBankStats(ctx context.Context, in *GetBankStatsRequest, opts ...grpc.CallOption) (*GetBankStatsResponse, error)
	// ListTags pages through the bank's tags with usage counts.
	ListTags(ctx context.Context, in *ListTagsRequest, opts ...grpc.CallOption) (*ListTagsResponse, error)
	// PreviewPrompt renders the exact messages an operation would send; no LLM call is made.
	PreviewPrompt(ctx context.Context, in *PreviewPromptRequest, opts ...grpc.CallOption) (*PreviewPromptResponse, error)
	// DeleteBank tombstones the bank, cancels its pending jobs and enqueues delete_bank, in one
	// transaction. The only bulk hard delete: while it runs, writes fail FAILED_PRECONDITION and reads
	// NOT_FOUND. Once delete_bank has removed the bank row, a write creates a new, empty bank with no old
	// events or operations.
	DeleteBank(ctx context.Context, in *DeleteBankRequest, opts ...grpc.CallOption) (*DeleteBankResponse, error)
	// ExportBankTemplate exports the bank's config, directives and mental-model specs.
	ExportBankTemplate(ctx context.Context, in *ExportBankTemplateRequest, opts ...grpc.CallOption) (*ExportBankTemplateResponse, error)
	// CreateBankFromTemplate creates a bank from a template and enqueues builds of its mental models.
	CreateBankFromTemplate(ctx context.Context, in *CreateBankFromTemplateRequest, opts ...grpc.CallOption) (*CreateBankFromTemplateResponse, error)
	// UpsertDirective creates (empty id) or replaces a directive.
	UpsertDirective(ctx context.Context, in *UpsertDirectiveRequest, opts ...grpc.CallOption) (*UpsertDirectiveResponse, error)
	// ListDirectives pages through the bank's directives, oldest first, optionally by tag and active state.
	ListDirectives(ctx context.Context, in *ListDirectivesRequest, opts ...grpc.CallOption) (*ListDirectivesResponse, error)
	// DeleteDirective deletes one directive.
	DeleteDirective(ctx context.Context, in *DeleteDirectiveRequest, opts ...grpc.CallOption) (*DeleteDirectiveResponse, error)
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

func (c *bankServiceClient) GetBankConfig(ctx context.Context, in *GetBankConfigRequest, opts ...grpc.CallOption) (*GetBankConfigResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetBankConfigResponse)
	err := c.cc.Invoke(ctx, BankService_GetBankConfig_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *bankServiceClient) UpdateBankConfig(ctx context.Context, in *UpdateBankConfigRequest, opts ...grpc.CallOption) (*UpdateBankConfigResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(UpdateBankConfigResponse)
	err := c.cc.Invoke(ctx, BankService_UpdateBankConfig_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *bankServiceClient) ResetBankConfig(ctx context.Context, in *ResetBankConfigRequest, opts ...grpc.CallOption) (*ResetBankConfigResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ResetBankConfigResponse)
	err := c.cc.Invoke(ctx, BankService_ResetBankConfig_FullMethodName, in, out, cOpts...)
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

func (c *bankServiceClient) PreviewPrompt(ctx context.Context, in *PreviewPromptRequest, opts ...grpc.CallOption) (*PreviewPromptResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(PreviewPromptResponse)
	err := c.cc.Invoke(ctx, BankService_PreviewPrompt_FullMethodName, in, out, cOpts...)
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

func (c *bankServiceClient) ExportBankTemplate(ctx context.Context, in *ExportBankTemplateRequest, opts ...grpc.CallOption) (*ExportBankTemplateResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ExportBankTemplateResponse)
	err := c.cc.Invoke(ctx, BankService_ExportBankTemplate_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *bankServiceClient) CreateBankFromTemplate(ctx context.Context, in *CreateBankFromTemplateRequest, opts ...grpc.CallOption) (*CreateBankFromTemplateResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(CreateBankFromTemplateResponse)
	err := c.cc.Invoke(ctx, BankService_CreateBankFromTemplate_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *bankServiceClient) UpsertDirective(ctx context.Context, in *UpsertDirectiveRequest, opts ...grpc.CallOption) (*UpsertDirectiveResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(UpsertDirectiveResponse)
	err := c.cc.Invoke(ctx, BankService_UpsertDirective_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *bankServiceClient) ListDirectives(ctx context.Context, in *ListDirectivesRequest, opts ...grpc.CallOption) (*ListDirectivesResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListDirectivesResponse)
	err := c.cc.Invoke(ctx, BankService_ListDirectives_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *bankServiceClient) DeleteDirective(ctx context.Context, in *DeleteDirectiveRequest, opts ...grpc.CallOption) (*DeleteDirectiveResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(DeleteDirectiveResponse)
	err := c.cc.Invoke(ctx, BankService_DeleteDirective_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// BankServiceServer is the server API for BankService service.
// All implementations must embed UnimplementedBankServiceServer
// for forward compatibility.
//
// BankService manages banks, their config, directives and templates.
type BankServiceServer interface {
	// CreateBank creates a bank with optional config overrides.
	CreateBank(context.Context, *CreateBankRequest) (*CreateBankResponse, error)
	// GetBank returns a bank and its own config overrides.
	GetBank(context.Context, *GetBankRequest) (*GetBankResponse, error)
	// GetBankConfig returns the resolved config (server < tenant < bank) and the bank's own overrides.
	GetBankConfig(context.Context, *GetBankConfigRequest) (*GetBankConfigResponse, error)
	// UpdateBankConfig patches the bank's own overrides with a FieldMask.
	UpdateBankConfig(context.Context, *UpdateBankConfigRequest) (*UpdateBankConfigResponse, error)
	// ResetBankConfig removes the bank's own overrides at the given paths, so they inherit again.
	ResetBankConfig(context.Context, *ResetBankConfigRequest) (*ResetBankConfigResponse, error)
	// GetBankStats returns counts from indexed COUNT queries; poll pending_operations_by_kind as an eval
	// barrier.
	GetBankStats(context.Context, *GetBankStatsRequest) (*GetBankStatsResponse, error)
	// ListTags pages through the bank's tags with usage counts.
	ListTags(context.Context, *ListTagsRequest) (*ListTagsResponse, error)
	// PreviewPrompt renders the exact messages an operation would send; no LLM call is made.
	PreviewPrompt(context.Context, *PreviewPromptRequest) (*PreviewPromptResponse, error)
	// DeleteBank tombstones the bank, cancels its pending jobs and enqueues delete_bank, in one
	// transaction. The only bulk hard delete: while it runs, writes fail FAILED_PRECONDITION and reads
	// NOT_FOUND. Once delete_bank has removed the bank row, a write creates a new, empty bank with no old
	// events or operations.
	DeleteBank(context.Context, *DeleteBankRequest) (*DeleteBankResponse, error)
	// ExportBankTemplate exports the bank's config, directives and mental-model specs.
	ExportBankTemplate(context.Context, *ExportBankTemplateRequest) (*ExportBankTemplateResponse, error)
	// CreateBankFromTemplate creates a bank from a template and enqueues builds of its mental models.
	CreateBankFromTemplate(context.Context, *CreateBankFromTemplateRequest) (*CreateBankFromTemplateResponse, error)
	// UpsertDirective creates (empty id) or replaces a directive.
	UpsertDirective(context.Context, *UpsertDirectiveRequest) (*UpsertDirectiveResponse, error)
	// ListDirectives pages through the bank's directives, oldest first, optionally by tag and active state.
	ListDirectives(context.Context, *ListDirectivesRequest) (*ListDirectivesResponse, error)
	// DeleteDirective deletes one directive.
	DeleteDirective(context.Context, *DeleteDirectiveRequest) (*DeleteDirectiveResponse, error)
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
func (UnimplementedBankServiceServer) GetBankConfig(context.Context, *GetBankConfigRequest) (*GetBankConfigResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetBankConfig not implemented")
}
func (UnimplementedBankServiceServer) UpdateBankConfig(context.Context, *UpdateBankConfigRequest) (*UpdateBankConfigResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method UpdateBankConfig not implemented")
}
func (UnimplementedBankServiceServer) ResetBankConfig(context.Context, *ResetBankConfigRequest) (*ResetBankConfigResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ResetBankConfig not implemented")
}
func (UnimplementedBankServiceServer) GetBankStats(context.Context, *GetBankStatsRequest) (*GetBankStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetBankStats not implemented")
}
func (UnimplementedBankServiceServer) ListTags(context.Context, *ListTagsRequest) (*ListTagsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListTags not implemented")
}
func (UnimplementedBankServiceServer) PreviewPrompt(context.Context, *PreviewPromptRequest) (*PreviewPromptResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method PreviewPrompt not implemented")
}
func (UnimplementedBankServiceServer) DeleteBank(context.Context, *DeleteBankRequest) (*DeleteBankResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method DeleteBank not implemented")
}
func (UnimplementedBankServiceServer) ExportBankTemplate(context.Context, *ExportBankTemplateRequest) (*ExportBankTemplateResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ExportBankTemplate not implemented")
}
func (UnimplementedBankServiceServer) CreateBankFromTemplate(context.Context, *CreateBankFromTemplateRequest) (*CreateBankFromTemplateResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method CreateBankFromTemplate not implemented")
}
func (UnimplementedBankServiceServer) UpsertDirective(context.Context, *UpsertDirectiveRequest) (*UpsertDirectiveResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method UpsertDirective not implemented")
}
func (UnimplementedBankServiceServer) ListDirectives(context.Context, *ListDirectivesRequest) (*ListDirectivesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListDirectives not implemented")
}
func (UnimplementedBankServiceServer) DeleteDirective(context.Context, *DeleteDirectiveRequest) (*DeleteDirectiveResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method DeleteDirective not implemented")
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

func _BankService_GetBankConfig_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetBankConfigRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankServiceServer).GetBankConfig(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankService_GetBankConfig_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankServiceServer).GetBankConfig(ctx, req.(*GetBankConfigRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BankService_UpdateBankConfig_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateBankConfigRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankServiceServer).UpdateBankConfig(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankService_UpdateBankConfig_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankServiceServer).UpdateBankConfig(ctx, req.(*UpdateBankConfigRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BankService_ResetBankConfig_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ResetBankConfigRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankServiceServer).ResetBankConfig(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankService_ResetBankConfig_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankServiceServer).ResetBankConfig(ctx, req.(*ResetBankConfigRequest))
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

func _BankService_PreviewPrompt_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PreviewPromptRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankServiceServer).PreviewPrompt(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankService_PreviewPrompt_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankServiceServer).PreviewPrompt(ctx, req.(*PreviewPromptRequest))
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

func _BankService_CreateBankFromTemplate_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateBankFromTemplateRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankServiceServer).CreateBankFromTemplate(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankService_CreateBankFromTemplate_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankServiceServer).CreateBankFromTemplate(ctx, req.(*CreateBankFromTemplateRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BankService_UpsertDirective_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpsertDirectiveRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankServiceServer).UpsertDirective(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankService_UpsertDirective_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankServiceServer).UpsertDirective(ctx, req.(*UpsertDirectiveRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BankService_ListDirectives_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListDirectivesRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankServiceServer).ListDirectives(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankService_ListDirectives_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankServiceServer).ListDirectives(ctx, req.(*ListDirectivesRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BankService_DeleteDirective_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(DeleteDirectiveRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BankServiceServer).DeleteDirective(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BankService_DeleteDirective_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BankServiceServer).DeleteDirective(ctx, req.(*DeleteDirectiveRequest))
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
			MethodName: "GetBankConfig",
			Handler:    _BankService_GetBankConfig_Handler,
		},
		{
			MethodName: "UpdateBankConfig",
			Handler:    _BankService_UpdateBankConfig_Handler,
		},
		{
			MethodName: "ResetBankConfig",
			Handler:    _BankService_ResetBankConfig_Handler,
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
			MethodName: "PreviewPrompt",
			Handler:    _BankService_PreviewPrompt_Handler,
		},
		{
			MethodName: "DeleteBank",
			Handler:    _BankService_DeleteBank_Handler,
		},
		{
			MethodName: "ExportBankTemplate",
			Handler:    _BankService_ExportBankTemplate_Handler,
		},
		{
			MethodName: "CreateBankFromTemplate",
			Handler:    _BankService_CreateBankFromTemplate_Handler,
		},
		{
			MethodName: "UpsertDirective",
			Handler:    _BankService_UpsertDirective_Handler,
		},
		{
			MethodName: "ListDirectives",
			Handler:    _BankService_ListDirectives_Handler,
		},
		{
			MethodName: "DeleteDirective",
			Handler:    _BankService_DeleteDirective_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "memory/v1/memory.proto",
}

const (
	TenantService_GetTenantConfig_FullMethodName    = "/memory.v1.TenantService/GetTenantConfig"
	TenantService_UpdateTenantConfig_FullMethodName = "/memory.v1.TenantService/UpdateTenantConfig"
)

// TenantServiceClient is the client API for TenantService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
//
// TenantService reads and patches the calling tenant's config defaults.
type TenantServiceClient interface {
	// GetTenantConfig returns the tenant-level BankConfig overrides.
	GetTenantConfig(ctx context.Context, in *GetTenantConfigRequest, opts ...grpc.CallOption) (*GetTenantConfigResponse, error)
	// UpdateTenantConfig patches the tenant-level BankConfig with a FieldMask.
	UpdateTenantConfig(ctx context.Context, in *UpdateTenantConfigRequest, opts ...grpc.CallOption) (*UpdateTenantConfigResponse, error)
}

type tenantServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewTenantServiceClient(cc grpc.ClientConnInterface) TenantServiceClient {
	return &tenantServiceClient{cc}
}

func (c *tenantServiceClient) GetTenantConfig(ctx context.Context, in *GetTenantConfigRequest, opts ...grpc.CallOption) (*GetTenantConfigResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetTenantConfigResponse)
	err := c.cc.Invoke(ctx, TenantService_GetTenantConfig_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *tenantServiceClient) UpdateTenantConfig(ctx context.Context, in *UpdateTenantConfigRequest, opts ...grpc.CallOption) (*UpdateTenantConfigResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(UpdateTenantConfigResponse)
	err := c.cc.Invoke(ctx, TenantService_UpdateTenantConfig_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// TenantServiceServer is the server API for TenantService service.
// All implementations must embed UnimplementedTenantServiceServer
// for forward compatibility.
//
// TenantService reads and patches the calling tenant's config defaults.
type TenantServiceServer interface {
	// GetTenantConfig returns the tenant-level BankConfig overrides.
	GetTenantConfig(context.Context, *GetTenantConfigRequest) (*GetTenantConfigResponse, error)
	// UpdateTenantConfig patches the tenant-level BankConfig with a FieldMask.
	UpdateTenantConfig(context.Context, *UpdateTenantConfigRequest) (*UpdateTenantConfigResponse, error)
	mustEmbedUnimplementedTenantServiceServer()
}

// UnimplementedTenantServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedTenantServiceServer struct{}

func (UnimplementedTenantServiceServer) GetTenantConfig(context.Context, *GetTenantConfigRequest) (*GetTenantConfigResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetTenantConfig not implemented")
}
func (UnimplementedTenantServiceServer) UpdateTenantConfig(context.Context, *UpdateTenantConfigRequest) (*UpdateTenantConfigResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method UpdateTenantConfig not implemented")
}
func (UnimplementedTenantServiceServer) mustEmbedUnimplementedTenantServiceServer() {}
func (UnimplementedTenantServiceServer) testEmbeddedByValue()                       {}

// UnsafeTenantServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to TenantServiceServer will
// result in compilation errors.
type UnsafeTenantServiceServer interface {
	mustEmbedUnimplementedTenantServiceServer()
}

func RegisterTenantServiceServer(s grpc.ServiceRegistrar, srv TenantServiceServer) {
	// If the following call panics, it indicates UnimplementedTenantServiceServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&TenantService_ServiceDesc, srv)
}

func _TenantService_GetTenantConfig_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetTenantConfigRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TenantServiceServer).GetTenantConfig(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: TenantService_GetTenantConfig_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TenantServiceServer).GetTenantConfig(ctx, req.(*GetTenantConfigRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _TenantService_UpdateTenantConfig_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateTenantConfigRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TenantServiceServer).UpdateTenantConfig(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: TenantService_UpdateTenantConfig_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TenantServiceServer).UpdateTenantConfig(ctx, req.(*UpdateTenantConfigRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// TenantService_ServiceDesc is the grpc.ServiceDesc for TenantService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var TenantService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "memory.v1.TenantService",
	HandlerType: (*TenantServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetTenantConfig",
			Handler:    _TenantService_GetTenantConfig_Handler,
		},
		{
			MethodName: "UpdateTenantConfig",
			Handler:    _TenantService_UpdateTenantConfig_Handler,
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
	// GetOperation returns one operation of the bank.
	GetOperation(ctx context.Context, in *GetOperationRequest, opts ...grpc.CallOption) (*GetOperationResponse, error)
	// ListOperations pages through the bank's operations, optionally filtered by status and kind.
	ListOperations(ctx context.Context, in *ListOperationsRequest, opts ...grpc.CallOption) (*ListOperationsResponse, error)
	// CancelOperation cancels a PENDING or PROCESSING operation. A pending one becomes CANCELLED at once.
	// A processing one stays PROCESSING with the cancel recorded (its serial key stays held) until its
	// handler stops at the next checkpoint, then becomes CANCELLED; committed work stays.
	CancelOperation(ctx context.Context, in *CancelOperationRequest, opts ...grpc.CallOption) (*CancelOperationResponse, error)
	// RetryOperation re-queues a FAILED or CANCELLED operation: it resets the row to PENDING with
	// next_run_at = now. Other states are rejected.
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
	// GetOperation returns one operation of the bank.
	GetOperation(context.Context, *GetOperationRequest) (*GetOperationResponse, error)
	// ListOperations pages through the bank's operations, optionally filtered by status and kind.
	ListOperations(context.Context, *ListOperationsRequest) (*ListOperationsResponse, error)
	// CancelOperation cancels a PENDING or PROCESSING operation. A pending one becomes CANCELLED at once.
	// A processing one stays PROCESSING with the cancel recorded (its serial key stays held) until its
	// handler stops at the next checkpoint, then becomes CANCELLED; committed work stays.
	CancelOperation(context.Context, *CancelOperationRequest) (*CancelOperationResponse, error)
	// RetryOperation re-queues a FAILED or CANCELLED operation: it resets the row to PENDING with
	// next_run_at = now. Other states are rejected.
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

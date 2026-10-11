// Package server is the gRPC surface of memd: one handler type per memory.v1 service, the tenant, deadline
// and paging interceptors, grpc.health.v1 with readiness probes, the proto <-> domain conversion, FieldMask
// patches, and the mapping from domain errors to gRPC status codes.
//
// # Where it sits
//
// server sits above the engines (domain -> port -> engine -> server/worker -> cmd/memd/wire.go). It imports
// domain, port, config, gen/memory/v1, the engine packages whose request and result types it converts, and
// worker (for OperationControl); depguard forbids it to import adapters. Handlers are thin: validate the
// tenant and ids, convert the proto request to domain types, check request overrides, call the engine, and
// convert the result back. Business rules live in the engines.
//
// # Usage
//
// cmd/memd/wire.go fills [Deps] and calls [New]:
//
//	s := server.New(deps, grpc.ChainUnaryInterceptor(extra...))
//	err := s.Serve(lis)
//
// [New] registers MemoryService, MemoryUnitService, TenantService, BankService, DocumentService,
// ObservationService, MentalModelService and OperationService, plus grpc.health.v1. A nil dependency in
// [Deps] makes its RPCs return UNIMPLEMENTED ("not wired") instead of panicking.
//
// # Interceptors
//
// Three unary interceptors run first, in this order; options passed to New chain after them:
//
//   - [TenantInterceptor] reads the tenant from the x-tenant-id metadata header ([TenantHeader]); exactly one
//     non-empty value is required, else INVALID_ARGUMENT. The tenant is never read from the request body
//     (auth is out of scope). Health checks need no tenant. [TenantFrom] / [WithTenant] read and set it.
//   - [DeadlineInterceptor] gives a call without a deadline the default one (Deps.DefaultDeadline, from
//     MEMD_RPC_DEADLINE in cmd/memd); 0 = none.
//   - The paging interceptor rejects a negative page_size or limit field with INVALID_ARGUMENT; 0 keeps
//     meaning "the server default".
//
// Tenant and bank ids must be non-empty, at most 256 bytes, not "." or "..", and free of '/' and control
// characters, because blob keys are "{tenant}/{bank}/..." and one bank's prefix must never cover another's.
//
// # Request overrides
//
// Every RPC that takes a BankConfig runs Deps.ValidateOverrides (config.ValidateOverrides) with its method
// name before calling the engine, so a field the RPC cannot use is INVALID_ARGUMENT instead of being silently
// ignored (SPEC "gRPC surface", Per-request overrides). Retain checks the request and every item's
// overrides; mental-model specs are checked as config.RPCMentalModelSpec; whole-level writes (CreateBank,
// CreateBankFromTemplate, UpdateBankConfig, UpdateTenantConfig) are checked as config.RPCBankConfig, against
// the patched level inside the patch. ResetBankConfig is not checked: it only unsets fields.
//
// # FieldMask patches
//
// UpdateBankConfig, UpdateTenantConfig and UpdateMentalModel do not read and then write. The server builds
// a pure [ConfigPatch] or [SpecPatch] function from the request and the engine applies it inside its own write
// transaction, where the read-modify-write is serialized (the bank row's Seq lock for bank configs and
// models, a per-tenant lock for tenant configs), so concurrent patches of different fields both survive.
// Paths are proto field names, validated against the message, and applied to the domain struct (whose json
// tags are the proto names), so explicit clears stored at other paths survive:
//
//   - a masked path takes the request's value; an unset value clears it (inherit), and an empty list or map
//     becomes an explicit clear;
//   - an absent or empty mask means every populated leaf of the request (set nested messages are recursed
//     into; lists, maps and Structs are leaves), so such a request never clears anything;
//   - under an implicit mask, models (and a spec's overrides.models) merge per slot into the stored list, so
//     binding one slot never drops another; an explicit models path replaces the whole list;
//   - ResetBankConfig resets the given paths, or the whole bank override when none are given;
//   - a mental model's id cannot be patched.
//
// # Error mapping
//
// Every handler returns toStatus(err), which maps domain error classes with errors.Is, checked in order:
//
//	context.DeadlineExceeded      DEADLINE_EXCEEDED
//	context.Canceled              CANCELLED
//	domain.ErrNotFound            NOT_FOUND
//	domain.ErrAlreadyExists       ALREADY_EXISTS
//	domain.ErrInvalidArgument     INVALID_ARGUMENT  (includes ErrTooManyScopes)
//	domain.ErrFailedPrecondition  FAILED_PRECONDITION  (includes ErrBankDeleting)
//	domain.ErrUnavailable         UNAVAILABLE
//	domain.ErrResourceExhausted   RESOURCE_EXHAUSTED
//	domain.ErrCancelled           ABORTED
//	domain.ErrConflict            ABORTED
//	anything else                 INTERNAL
//
// An error that already carries a gRPC status passes through. ABORTED covers two classes: domain.ErrConflict,
// a concurrent writer won (for example a superseded sync Retain, or an entity pruned under a retain or
// curation on every transaction attempt), and the client may retry; and domain.ErrCancelled, the work was
// cancelled.
//
// # Conversion
//
// The convert_*.go files are structural only: no tag normalization, defaults or business validation (the
// engines own those). proto -> domain fails only on an undefined enum number or an invalid timestamp
// (INVALID_ARGUMENT); domain -> proto never fails, and an unknown domain enum becomes *_UNSPECIFIED. Enum
// strings are the lowercase value names without the prefix; a zero time is a nil Timestamp; presence is kept
// where it matters (UpdateMemoryRequest.entities unset = unchanged, set and empty = clear). Mental-model detail
// is trimmed here: METADATA is the spec plus status, CONTENT adds content and structured output, FULL adds
// source ids and the last trace; Get defaults to FULL, List to METADATA, and Create, Update and Clear return
// FULL.
//
// # Consumer interfaces
//
// [Deps] holds engine interfaces for the RPCs that have an engine (retain, recall, reflect, curate, bank,
// docs, consolidate, mentalmodel, worker.OperationControl) and small server-declared interfaces for the rest:
// [TenantAdmin], [BankAdmin], [DirectiveAdmin], [DocumentAdmin], [ObservationAdmin], [MentalModelAdmin],
// [OperationReader] and [ReadinessProbe]. The engine implementations satisfy them directly (several request
// and page types are aliases of engine types). [OperationReader] is implemented in cmd/memd: ListEvents with
// limit 0 returns 100 events and any limit is capped at 1000 there.
//
// # Health
//
// grpc.health.v1 Check runs every [ReadinessProbe] (Postgres, gateway) and reports NOT_SERVING when one fails;
// service names are ignored. Watch and List are grpc-go's standard implementation and run no probe (Watch on
// "" is a liveness signal). [NewHealthServer] serves the same health service in worker-only processes.
//
// # Tests
//
// roundtrip_*_test.go round-trip every memory.v1 message proto -> domain -> proto with random values
// (TestRoundTripEveryMessage fails on a message with neither a round trip nor a listed exemption; FuzzRoundTrip
// fuzzes the same), golden tests pin the Retain, Recall and Reflect request conversions, and bufconn tests run
// every RPC through a real gRPC server over generated mocks (forwarding, unwired dependencies, tenant
// handling). Other tests cover the interceptors, error mapping, enum mapping, FieldMask patches, config-write
// validation and mental-model detail. SPEC "gRPC surface", "Code standards" and "Behavioural rules".
package server

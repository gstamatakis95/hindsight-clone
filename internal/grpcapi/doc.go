// Package grpcapi is the gRPC surface of memd: one handler type per memory.v1 service, the tenant, deadline and
// paging interceptors, grpc.health.v1 with its readiness probes, the proto <-> domain conversion, FieldMask
// patches, and the mapping from domain errors to gRPC status codes.
//
// # Where it sits
//
// grpcapi sits above the engines and below cmd/memd. It imports domain, store (for request types), config,
// gen/memory/v1, the engine packages whose request and result types it converts, and jobs (for OperationControl);
// depguard forbids it to import adapters, pgx or maintenance. Handlers are thin: validate the tenant and ids,
// convert the proto request to domain types, check request overrides, call the engine, and convert the result
// back. Business rules live in the engines.
//
// # Usage
//
// Each cmd/memd/wire_<area>.go builds its area's handlers with exactly the engines they call, and wire.go calls
// [New]:
//
//	h := grpcapi.Handlers{Memory: &grpcapi.MemoryHandler{Memory: mem}, ...}
//	s := grpcapi.New(h, grpcapi.Options{DefaultDeadline: d, Probes: probes}, grpc.ChainUnaryInterceptor(extra...))
//	err := s.Serve(lis)
//
// [New] registers each non-nil handler of [Handlers] ([MemoryHandler], [MemoryUnitHandler], [TenantHandler],
// [BankHandler], [DocumentHandler], [ObservationHandler], [MentalModelHandler], [OperationHandler]) plus
// grpc.health.v1. A nil handler leaves its service unregistered, so its RPCs answer UNIMPLEMENTED.
//
// # Interceptors
//
// Four unary interceptors run first, in this order; options passed to New chain after them:
//
//   - [TenantInterceptor] reads the tenant from the x-tenant-id metadata header ([TenantHeader]); exactly one
//     non-empty value is required, else INVALID_ARGUMENT. The tenant is never read from the request body
//     (auth is out of scope). Health checks need no tenant. [TenantFrom] / [WithTenant] read and set it.
//   - [DeadlineInterceptor] gives a call without a deadline the default one (Options.DefaultDeadline, from
//     api.rpc_deadline in the server config file); 0 = none.
//   - The paging interceptor rejects a negative page_size or limit field with INVALID_ARGUMENT; 0 keeps
//     meaning "the server default".
//   - The arm-name interceptor finds every RecallConfig in a request (config levels and patches, templates, spec
//     and request overrides) and rejects recall.arms or recall.arm_boosts names outside Options.RecallArms, the
//     registered recall arms (config.CheckArms), with INVALID_ARGUMENT. A request's own arms and min_scores.arms
//     are checked by the recall engine; overrides.recall.arms on Recall and Reflect is rejected outright, since
//     the request's arms field picks the arms there. ListRecallArms lists the registered arms.
//
// Tenant and bank ids must be non-empty, at most 256 bytes, not "." or "..", and free of '/' and control
// characters, because blob keys are "{tenant}/{bank}/..." and one bank's prefix must never cover another's.
//
// # Request overrides
//
// Each RPC that does work takes its own overrides message (RetainOverrides, RecallOverrides, ReflectOverrides,
// ConsolidateOverrides, RederiveOverrides, RefreshOverrides), so the schema says what it can change;
// convert_overrides.go turns each into the BankConfig level the engines merge, and reads stored levels back (a
// document's retain_overrides, a model's spec overrides). PreviewPrompt takes one of them, matching its
// operation. Every config the server is given passes config.Validate before the engine sees it: request and
// item overrides, mental-model specs, and whole levels (CreateBank, CreateBankFromTemplate, UpdateBankConfig,
// UpdateTenantConfig), the latter against the patched level inside the patch. ResetBankConfig is not checked:
// it only unsets fields; it needs at least one path, and "*" resets everything.
//
// # FieldMask patches
//
// UpdateBankConfig, UpdateTenantConfig and UpdateMentalModel do not read and then write. The handler builds a
// pure patch function from the request and the engine applies it inside its own write transaction, where the
// read-modify-write is serialized (the bank row's Seq lock for bank configs and models, a per-tenant lock for
// tenant configs), so concurrent patches of different fields both survive. Paths are proto field names,
// validated against the message, and applied to the domain struct (whose json tags are the proto names), so
// explicit clears stored at other paths survive:
//
//   - a masked path takes the request's value; an unset value clears it (inherit), and an empty list or map
//     becomes an explicit clear;
//   - an absent or empty mask means every populated leaf of the request (set nested messages are recursed
//     into; lists, maps and Structs are leaves), so such a request never clears anything;
//   - under an implicit mask, models (and a spec's overrides.models) merge per slot into the stored list, so
//     binding one slot never drops another; an explicit models path replaces the whole list;
//   - ResetBankConfig resets the given paths ("*" = the whole bank override);
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
// engines own those). They convert requests proto -> domain and results domain -> proto, plus the stored
// levels that travel both ways (configs, overrides, specs). proto -> domain fails only on an undefined enum
// number or an invalid timestamp (INVALID_ARGUMENT); domain -> proto never fails, and an unknown domain enum
// becomes *_UNSPECIFIED. Enum strings are the lowercase value names without the prefix; a zero time is a nil
// Timestamp; presence is kept where it matters (UpdateMemoryRequest.entities unset = unchanged, set and empty =
// clear). Mental-model detail is trimmed here: METADATA is the spec plus status, CONTENT adds content and
// structured output, FULL adds source ids and the last trace; Get defaults to FULL, List to METADATA, and
// Create, Update and Clear return FULL.
//
// # Health
//
// grpc.health.v1 Check runs every [ReadinessProbe] ([PostgresProbe], [GatewayProbe]) and reports NOT_SERVING
// when one fails; service names are ignored. Watch and List are grpc-go's standard implementation and run no
// probe (Watch on "" is a liveness signal). [ServeHealth] serves the same health service in worker-only
// processes.
//
// # Tests
//
// The tests here need no database: golden tests pin the Retain, Recall and Reflect request conversions, and
// others cover the interceptors, error mapping, enum mapping, FieldMask patches, override lists, mental-model
// detail and health. The API's behaviour through the real engines (every RPC wired, config-write validation,
// spec overrides, document retags, reflect's based_on) is tested in cmd/memd over the wired stack. SPEC "gRPC
// surface", "Code standards" and "Behavioural rules".
package grpcapi

# 600 — Tenant header and id checks

## Context
SPEC "Process model / Tenancy": an interceptor reads the tenant from the `x-tenant-id` metadata header,
never from the request body, and every store call takes `(tenant, bank)`. Auth is out of scope. The spec
doesn't say what happens when the header is missing. The WP-06 card asks for INVALID_ARGUMENT.

The blob layout is `{tenant}/{bank}/...` (`domain.BankBlobPrefix`). Bank delete calls
`Blob.DeletePrefix("{tenant}/{bank}/")`. If ids could contain `/`, deleting bank `b` would also delete bank
`b/x`, and tenant `a` with bank `b/c` would collide with tenant `a/b` with bank `c`. That is a cross-bank
data-loss path.

## Decision
- `TenantInterceptor` requires exactly one `x-tenant-id` value. A missing, empty or repeated header
  fails with **INVALID_ARGUMENT**, as does a malformed value.
- **Validation rules.** Tenant and bank ids must be non-empty and at most 256 bytes, and must contain no
  `/` and no control characters. Every handler checks `bank_id` this way through `bankRef`, which also
  re-checks the tenant.
- **Health exemption.** `/grpc.health.v1.Health/*` needs no tenant, because k8s probes send no metadata.
- **Deadline.** `DeadlineInterceptor` applies its default only when the call has no deadline. A shorter
  client deadline is kept, and so is a longer one: sync Retain and Reflect can legitimately run long.

## Alternatives
- **UNAUTHENTICATED for a missing tenant.** Rejected: there is no authentication in v1, and the request
  is malformed rather than unauthenticated.
- **A default tenant.** Rejected: it silently mixes callers' data.
- **Escaping `/` in blob keys instead of rejecting it.** Rejected: blob keys are a frozen domain helper,
  and rejecting is simpler.

## Spec lines involved
Process model (Tenancy), Ports (Blob key layout), Behavioural rules (bank delete).

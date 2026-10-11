# 2004 — cmd/memd hardening and API error surface (final review A items)

## Context
Final review A raised these items (docs/STATUS.md, 2026-10-11, FINAL REVIEW A):
- A duplicate `MEMD_WORKER_ID` defeats the heartbeat fencing.
- The S3 keys are not checked at startup.
- An unbound EXTRACT or REFLECT slot surfaces only on the first call, and as INVALID_ARGUMENT.
- There is no per-slot gateway concurrency setting.
- Worker mode has no health endpoint.
- CreateBank on a deleting bank leaks SQL text.
- "." and ".." are accepted as ids.
- The semantics of health `Watch` are undocumented.

## Decision
- **Worker id.** The effective id is `<MEMD_WORKER_ID or hostname>-<uuid>`, with a fresh uuid per process
  (`effectiveWorkerID`, logged at start). If there is no hostname either, the prefix is `worker`. Two
  processes given the same `MEMD_WORKER_ID` therefore never share an id.
- **S3 credentials.** `MEMD_S3_ACCESS_KEY` and `MEMD_S3_SECRET_KEY` are both required.
  - The S3 adapter signs with static credentials only. Supporting the AWS default credential chain would
    mean changing the adapter, which WP-20 doesn't own.
  - "Both required" is the option that can be implemented in cmd/memd. Validate rejects either one missing.
- **Required slots.** Validate fails unless EXTRACT and REFLECT have a model at the server level
  (`MEMD_MODEL` or `MEMD_MODEL_<SLOT>`, read through `config.ServerDefaults`). The other slots may stay
  unbound.
- **Unbound slot.** cmd/memd wraps the engine router (`slotRouter`). A slot with no model bound returns an
  error wrapping domain.ErrFailedPrecondition and worker.ErrPermanent.
  - The RPC therefore maps to FAILED_PRECONDITION: the deployment or bank config lacks a binding, and the
    request itself is fine.
  - A job on that slot still fails at once without retrying, as it did with INVALID_ARGUMENT.
  - EMBED and the unspecified slot keep the router's INVALID_ARGUMENT.
  - The engine router (internal/engine/routing) is unchanged.
- **Per-slot concurrency.** `MEMD_GATEWAY_MAX_CONCURRENT_<SLOT>` (EXTRACT, ..., RERANK, EMBED) fills
  `gateway.Config.SlotConcurrency`. These are environment variables only, like `MEMD_MODEL_<SLOT>`, and
  values must be positive integers.
- **Worker health.** When `MEMD_WORKER_HEALTH_ADDR` is set, `runWorker` serves grpc.health.v1 there through
  `server.NewHealthServer`, with the API's probes (Postgres ping, gateway reachability). This applies in
  both worker and all modes. When it is unset, nothing is served.
- **Health semantics** (documented on `healthServer`).
  - `Check` runs the probes and ignores the service name.
  - `Watch` and `List` are grpc-go's standard implementations and run no probe. `Watch("")` streams
    SERVING once, so it is a liveness signal. Other names stream SERVICE_UNKNOWN. `List` reports
    `{"": SERVING}`.
- **CreateBank.** CreateBank reads the bank first.
  - A deleting bank is domain.ErrBankDeleting (FAILED_PRECONDITION).
  - An existing bank is ALREADY_EXISTS with memd's own message.
  - A unique violation from a concurrent create is replaced by the same message, not wrapped, so no SQL
    text reaches the client.
- **Dot ids.** Tenant (header) and bank ids equal to "." or ".." are INVALID_ARGUMENT. Ids that only
  contain dots are fine.

## Spec lines involved
SPEC "Process model" (modes), "Jobs" (heartbeats and reclaim), "Code standards" (errors), "Behavioural
rules" (DeleteBank: writes return FAILED_PRECONDITION); decisions 600, 602 and 1411.

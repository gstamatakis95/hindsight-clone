# Several tenants on one memd

A SaaS product serving several customers. Each customer is a tenant (the `x-tenant-id` header): tenants never see each other's banks, each has its own default behaviour, and a bank can be stamped out from a template with its own mission and disposition.

## What it does

1. `UpdateTenantConfig` / `GetTenantConfig`: one tenant sets a skeptical disposition, another keeps the server defaults (`own` versus `effective`).
2. `CreateBank` with its own mission and disposition, `CreateDirective`, then `ExportBankTemplate`.
3. `CreateBank` from that template in the other tenant, then `UpdateBankConfig` for a per-bank mission.
4. Isolation: the same bank id holds different documents per tenant, an unknown bank is `NOT_FOUND`, and a call without `x-tenant-id` is `INVALID_ARGUMENT`.

## Setup

Start memd with the gateway emulator as described in [../README.md](../README.md#shared-setup). Nothing else is needed: the example creates its own bank (or tenants) with a fresh name on every run, so it can be run again.

## Run

```sh
go run ./examples/multi-tenant            # -addr localhost:50051 -tenant examples by default
```

`-addr` and `-tenant` (or `MEMD_ADDR` and `MEMD_TENANT`) choose the server and tenant; `-timeout` bounds the run (default 5m). The two tenants are derived from a per-run suffix, so `-tenant` is only used to connect.

The example exits 1 with `FAILED: ...` when the behaviour it demonstrates does not hold.

## Expected output

From a run against the emulator (ids, counts and tokens differ between runs; the emulator's answers quote the retained text, a real gateway writes real answers):

```text

== tenant config ==
acme-51975405: own skepticism=5, effective skepticism=5
globex-51975405: own skepticism=0, effective skepticism=3

== a bank with its own mission, exported as a template ==
template: mission="You help support agents prepare for a call.", 1 directive(s)

== the same template in another tenant ==
acme-51975405/support: mission="You help support agents prepare for a call." empathy=5 skepticism=5
globex-51975405/support: mission="You help support agents of a logistics company." empathy=5 skepticism=3

== isolation ==
acme-51975405/support: 1 documents
globex-51975405/support: 0 documents
a bank the tenant does not have: NotFound
a call without a tenant: InvalidArgument
```

## With grpcurl

[commands.md](commands.md) has the same calls as grpcurl commands.

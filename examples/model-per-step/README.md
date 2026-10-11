# A model for each step

Spend model budget where it matters: a cheap model for the high-volume steps (extraction, consolidation), a strong model for the steps that talk to the user (reflect, mental models) and a cross-encoder for reranking. The routing is set once on the bank, then overridden for single requests, and the usage that every call reports shows which model served which step.

## What it does

1. `UpdateBankConfig` binds `models.default` (cheap), `models.reflect` and `models.mental_model` (strong) and `models.rerank`.
2. `Retain` twice: with the bank's routing, then with `overrides.extract_model` for one request. `SlotUsage` names the model of the `EXTRACT` slot.
3. `Consolidate` with `overrides.consolidate_model`; the finished `Operation` carries the usage.
4. `Recall` with the bank's reranker (`trace.reranked`), then `Reflect` on the bank's model and with `overrides.reflect_model`.
5. `CreateMentalModel` with the single-shot strategy; its build uses the `MENTAL_MODEL` slot.

## Setup

Start memd with the gateway emulator as described in [../README.md](../README.md#shared-setup). Nothing else is needed: the example creates its own bank (or tenants) with a fresh name on every run, so it can be run again.

## Run

```sh
go run ./examples/model-per-step            # -addr localhost:50051 -tenant examples by default
```

`-addr` and `-tenant` (or `MEMD_ADDR` and `MEMD_TENANT`) choose the server and tenant; `-timeout` bounds the run (default 5m). `-cheap`, `-strong`, `-reranker` and `-one-off` name the gateway models (defaults are placeholder names that the emulator accepts). Pass the names your gateway serves when you run against a real one.

The example exits 1 with `FAILED: ...` when the behaviour it demonstrates does not hold.

## Expected output

From a run against the emulator (ids, counts and tokens differ between runs; the emulator's answers quote the retained text, a real gateway writes real answers):

```text

== bind models on the bank ==
effective: default=cheap-model reflect=strong-model mental_model=strong-model rerank=rerank-model

== retain: bank routing, then a per-request override ==
notes-1 usage:
  EXTRACT            model=cheap-model            in=2739 out=136
notes-2 usage:
  EXTRACT            model=one-off-model          in=2736 out=132

== consolidate with a one-off model ==
consolidate operation usage:
  CONSOLIDATE        model=one-off-model          in=2797 out=154

== recall with the reranker ==
4 results, 4 candidates sent to the cross-encoder

== reflect: bank model, then a per-request model ==
reflect usage:
  REFLECT            model=strong-model           in=5276 out=49
reflect usage:
  REFLECT            model=one-off-model          in=5276 out=49

== mental model build on the strong model ==
mental model build usage:
  MENTAL_MODEL       model=strong-model           in=939 out=7
```

## With grpcurl

[commands.md](commands.md) has the same calls as grpcurl commands.

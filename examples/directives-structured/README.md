# Directives and structured output

Steer Reflect. Directives are standing rules the bank applies to every answer (and can be switched off or added for a single call); `response_schema_json` makes Reflect return JSON that matches a schema, so the answer can feed code instead of a chat window.

## What it does

1. `CreateDirective`, then `Reflect`: `based_on.directive_ids` lists the directives that shaped the answer, and `PreviewPrompt` shows how the rules reach the model, without calling it.
2. `UpdateDirective` with `update_mask: ["active"]` switches it off; `extra_directives` adds a rule for one call only.
3. `Reflect` with `response_schema_json`; the example decodes `structured_output_json` into a Go struct.

## Setup

Start memd with the gateway emulator as described in [../README.md](../README.md#shared-setup). Nothing else is needed: the example creates its own bank (or tenants) with a fresh name on every run, so it can be run again.

## Run

```sh
go run ./examples/directives-structured            # -addr localhost:50051 -tenant examples by default
```

`-addr` and `-tenant` (or `MEMD_ADDR` and `MEMD_TENANT`) choose the server and tenant; `-timeout` bounds the run (default 5m). 

The example exits 1 with `FAILED: ...` when the behaviour it demonstrates does not hold.

## Expected output

From a run against the emulator (ids, counts and tokens differ between runs; the emulator's answers quote the retained text, a real gateway writes real answers):

```text

== a standing directive ==
applied directives: [3bc93c17-4633-4801-8b28-9f8d013e6f58]
prompt block (PROMPT_SOURCE_CONFIG): blameless: Describe what happened to systems, never blame a person.; Mention the rollback if there w...

== switch the directive off, add one for a single call ==
applied directives: []
one-off answer: - Incident 77: the payments service returned errors for 20 minutes.

== structured output ==
severity=low services=[- Incident 77: the payments service returned errors for 20 minutes.]
summary=- Incident 77: the payments service returned errors for 20 minutes.
structured reflect usage:
  REFLECT            model=emu-reflect            in=5319 out=66
  STRUCTURED_OUTPUT  model=emu-structured_output  in=383 out=46
```

## With grpcurl

[commands.md](commands.md) has the same calls as grpcurl commands.

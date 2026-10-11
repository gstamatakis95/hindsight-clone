# Recall filters and tuning

Narrow and tune `Recall`: boolean tag groups, a time window, budgets, a preference for consolidated observations with the evidence attached, and the choice of retrieval arms.

## What it does

1. `TagFilter.groups` with `all_of` / `none_of`: engineering memories that are not about the legacy project.
2. `time_window_start` / `time_window_end` for the temporal arm.
3. `budget` and `max_tokens`: how many candidates each arm returns and how much is packed.
4. `prefer_observations` with `include.source_facts`, and `include.chunks` for raw facts.
5. `ListRecallArms`, then `arms: ["graph", "vector"]` to run only those two (`trace` lists the arms that ran).

## Setup

Start memd with the gateway emulator as described in [../README.md](../README.md#shared-setup). Nothing else is needed: the example creates its own bank (or tenants) with a fresh name on every run, so it can be run again.

## Run

```sh
go run ./examples/recall-filters            # -addr localhost:50051 -tenant examples by default
```

`-addr` and `-tenant` (or `MEMD_ADDR` and `MEMD_TENANT`) choose the server and tenant; `-timeout` bounds the run (default 5m). 

The example exits 1 with `FAILED: ...` when the behaviour it demonstrates does not hold.

## Expected output

From a run against the emulator (ids, counts and tokens differ between runs; the emulator's answers quote the retained text, a real gateway writes real answers):

```text

== tag groups ==
team:eng but not project:legacy: 2 results
    [WORLD] Engineering shipped the Atlas ledg... [project:atlas team:eng]
    [OBSERVATION] Engineering shipped the Atlas ledg... [project:atlas team:eng]

== time window ==
20-30 September: 8 results
    [OBSERVATION] Operations upgraded the ledger dat... [team:ops]
    [WORLD] Operations upgraded the ledger dat... [team:ops]
    [WORLD] Engineering shipped the Atlas ledg... [project:atlas team:eng]
    [OBSERVATION] Engineering shipped the Atlas ledg... [project:atlas team:eng]
    [WORLD] Engineering retired the legacy led... [project:legacy team:eng]
    [OBSERVATION] Engineering retired the legacy led... [project:legacy team:eng]
    [OBSERVATION] Sales signed Acme as the first Atl... [project:atlas team:sales]
    [WORLD] Sales signed Acme as the first Atl... [project:atlas team:sales]
  window used: 2026-09-20 to 2026-09-30

== budgets ==
BUDGET_LOW, 24 tokens: 3 results
    [OBSERVATION] Operations upgraded the ledger dat... [team:ops]
    [WORLD] Operations upgraded the ledger dat... [team:ops]
    [WORLD] Engineering retired the legacy led... [project:legacy team:eng]
  packed 3 of 8 fused candidates in 22/24 tokens
BUDGET_HIGH, 4096 tokens: 8 results
    [OBSERVATION] Operations upgraded the ledger dat... [team:ops]
    [WORLD] Operations upgraded the ledger dat... [team:ops]
    [WORLD] Engineering retired the legacy led... [project:legacy team:eng]
    [OBSERVATION] Engineering retired the legacy led... [project:legacy team:eng]
    [WORLD] Engineering shipped the Atlas ledg... [project:atlas team:eng]
    [OBSERVATION] Engineering shipped the Atlas ledg... [project:atlas team:eng]
    [OBSERVATION] Sales signed Acme as the first Atl... [project:atlas team:sales]
    [WORLD] Sales signed Acme as the first Atl... [project:atlas team:sales]
  packed 8 of 8 fused candidates in 64/4096 tokens

== prefer observations and attach sources ==
prefer_observations: 4 results
    [OBSERVATION] Operations upgraded the ledger dat... [team:ops]
    [OBSERVATION] Engineering retired the legacy led... [project:legacy team:eng]
    [OBSERVATION] Engineering shipped the Atlas ledg... [project:atlas team:eng]
    [OBSERVATION] Sales signed Acme as the first Atl... [project:atlas team:sales]
  4 source facts attached
raw facts with chunks: 4 results
    [WORLD] Operations upgraded the ledger dat... [team:ops]
    [WORLD] Engineering retired the legacy led... [project:legacy team:eng]
    [WORLD] Engineering shipped the Atlas ledg... [project:atlas team:eng]
    [WORLD] Sales signed Acme as the first Atl... [project:atlas team:sales]
  4 chunks attached

== recall arms ==
  vector    Semantic similarity: cosine between the query embedding and each memor...
  text      Full-text search: BM25 keyword relevance over each memory's text.
  graph     Link expansion: memories linked (semantic and causal links) to the que...
  temporal  Time window: memories that happened within the query's explicit or der...
arms graph+vector: 4 results
    [OBSERVATION] Operations upgraded the ledger dat... [team:ops]
    [WORLD] Operations upgraded the ledger dat... [team:ops]
    [WORLD] Engineering shipped the Atlas ledg... [project:atlas team:eng]
    [OBSERVATION] Engineering shipped the Atlas ledg... [project:atlas team:eng]
  arms that ran: [graph vector]
  an unknown arm: InvalidArgument
```

## With grpcurl

[commands.md](commands.md) has the same calls as grpcurl commands.

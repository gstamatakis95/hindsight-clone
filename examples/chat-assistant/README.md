# Chat assistant

An assistant that remembers its users. It retains a conversation, recalls what matters for the next message, and reflects: memd's agent searches the memories, writes the answer and cites the memories it used (`based_on`).

## What it does

1. `Retain` a conversation as one document; the bank is created by the first write.
2. `Recall` the memories for "Where should I take Dana for dinner?" (raw facts and observations).
3. `Reflect` on a broader question and print the answer with its `based_on` citations and the token usage.

## Setup

Start memd with the gateway emulator as described in [../README.md](../README.md#shared-setup). Nothing else is needed: the example creates its own bank (or tenants) with a fresh name on every run, so it can be run again.

## Run

```sh
go run ./examples/chat-assistant            # -addr localhost:50051 -tenant examples by default
```

`-addr` and `-tenant` (or `MEMD_ADDR` and `MEMD_TENANT`) choose the server and tenant; `-timeout` bounds the run (default 5m). 

The example exits 1 with `FAILED: ...` when the behaviour it demonstrates does not hold.

## Expected output

From a run against the emulator (ids, counts and tokens differ between runs; the emulator's answers quote the retained text, a real gateway writes real answers):

```text

== retain the conversation ==
document chat-2026-10-04: 8 memories created
retain usage:
  EXTRACT            model=emu-extract            in=2829 out=597

== recall ==
  [WORLD] Anything else I should remember?
  [OBSERVATION] Anything else I should remember?
  [WORLD] Dana: I just moved from Lisbon to Berlin for a job at a robotics startup.
  [WORLD] Dana: Motion planning for warehouse robots.
  [OBSERVATION] Dana: I just moved from Lisbon to Berlin for a job at a robotics startup.
  [OBSERVATION] Dana: Motion planning for warehouse robots.
  [WORLD] I'm vegetarian, so I'm looking for good restaurants near Kreuzberg.
  [WORLD] Dana: My sister Marta visits on the first weekend of every month, and she is allergic to peanuts.
  [OBSERVATION] Dana: My sister Marta visits on the first weekend of every month, and she is allergic to peanuts.
  [WORLD] Assistant: Noted.
  [WORLD] Assistant: Congratulations!
  [WORLD] What will you work on?
  [OBSERVATION] I'm vegetarian, so I'm looking for good restaurants near Kreuzberg.

== reflect ==
answer: - Anything else I should remember? - Dana: My sister Marta visits on the first weekend of every month, and she is allergic to peanuts. - Dana: I just moved from Lisbon to Berlin for a job at a robotics startup. - What will you work on? - Dana: Motion planning for warehouse robots. - I'm vegetarian, ...
based on:
  ecfe9b64 [WORLD] Anything else I should remember?
  71786c37 [WORLD] Dana: My sister Marta visits on the first weekend of every month, and she is all...
  97b35e3c [WORLD] Dana: I just moved from Lisbon to Berlin for a job at a robotics startup.
  eb6aa711 [WORLD] What will you work on?
  8f1d558d [WORLD] Dana: Motion planning for warehouse robots.
  1b98fa76 [WORLD] I'm vegetarian, so I'm looking for good restaurants near Kreuzberg.
  9c96ed72 [WORLD] Assistant: Noted.
  a6a7ef44 [WORLD] Assistant: Congratulations!
reflect usage:
  REFLECT            model=emu-reflect            in=5851 out=229
```

## With grpcurl

[commands.md](commands.md) has the same calls as grpcurl commands.

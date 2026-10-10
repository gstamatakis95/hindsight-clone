# 1101 — Context-window overflow in reflect

## Context
SPEC: "A context-window overflow does not [fail]: the agent synthesises from what it has." The gateway
surfaces an overflow as a non-retried `ErrUnavailable` whose message carries the provider's error body
(decision 301), so the port can't type it.

## Decision
- File CCR WP-11-1 for `domain.ErrContextOverflow` (wrapping `ErrUnavailable`).
- Meanwhile `isContextOverflow` matches the lowercased error text against Hindsight's markers
  (`context_length_exceeded`, `context length exceeded`, `maximum context length`, `prompt_too_long`,
  `prompt is too long`, `input is too long`) plus `context window`. Hindsight's `resource_exhausted` and
  `too many tokens` markers are left out: both can appear in rate-limit (429) errors, and memd's own
  `ErrResourceExhausted` must never be read as an overflow.
- On an overflow in a loop turn or the closing turn, reflect makes one tool-less `reflect_final` call with
  every successful tool output so far (stop reason `context_full`). If that call overflows too, it drops the
  older half of the outputs, sets the "omitted" note and tries again, until no output is left; then it fails.
- Any other chat error fails the call (decision 1100).

## Alternatives
- Treating every `ErrUnavailable` from Chat as an overflow: rejected, it would answer from partial evidence on
  a gateway outage, which "strict" forbids.
- Hindsight's proactive token count against `max_context_tokens`: no such knob in ReflectConfig; skipped.

## Spec lines involved
Behavioural rules > Reflect; ReflectTrace.stop_reason (`context_full`); decision 301 (Known gap).

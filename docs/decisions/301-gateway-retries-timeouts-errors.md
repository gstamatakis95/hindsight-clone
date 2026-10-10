# 301 — Gateway retries, timeouts and error mapping

## Context
The card asks Structured to "retry with the error on invalid output (bounded by the binding's
`max_retries`), and honour `timeout_ms`". The binding says `max_retries` "bounds retries on invalid output or
transient errors" and `timeout_ms` "bounds one call". Prompt text must live in `internal/prompts/templates`,
which WP-03 does not own.

## Decision
- **Retry feedback is the error itself.** After an invalid structured output, the next attempt sends four
  messages: system, user, assistant (the invalid output verbatim) and user (the validator's error text,
  e.g. `response does not match the JSON schema: $.facts[0]: missing required property "text"`). That last
  message is data, not prompt prose, so no template is needed. If a later WP wants a templated repair
  prompt, it can add one in `internal/prompts` and a hook here.
- **One budget.** All of these retry and draw on the same budget: invalid output (bad JSON, a schema
  mismatch, or JSON that doesn't decode into `out`), attempt timeouts, transport errors, 408, 429, 5xx, an
  undecodable response, and a response with no choices.
  - A call makes at most `1 + max_retries` attempts. `max_retries` is the binding's value, else
    `Config.MaxRetries` (default 2).
  - Other 4xx responses are not retried.
  - Embeddings use the config default.
- **Backoff.** The wait before retry n is `RetryBackoff << n` (default 500 ms), capped at 8 s. The caller's
  ctx cuts it short. There is no jitter: gosec G404 flags `math/rand`, and the Clock port has no sleep.
- **Timeout per attempt.** `timeout_ms` (else `Config.Timeout`, default 60 s) bounds one HTTP exchange. The
  wait for a semaphore permit doesn't count toward it. A call's worst case is therefore
  `(1 + max_retries) x timeout` plus backoff, and callers bound the whole call with their ctx (the RPC
  deadline).
- **Errors.**
  - Gateway failures wrap `domain.ErrUnavailable` with `%w`, plus the cause: `context.DeadlineExceeded` for
    an attempt timeout, or the HTTP status in the message.
  - The caller's own cancellation or deadline is returned as the ctx error. It is not wrapped in
    `ErrUnavailable` and never retried.
  - A missing or malformed schema is `domain.ErrInvalidArgument`, and no call is made.
- **Usage on failure.** Structured and Chat return the usage summed over every attempt, failed ones included
  (those tokens were spent), alongside the error.

## Alternatives
- `timeout_ms` for the whole call, retries included: rejected. A single slow attempt would use up the time
  the retries need, and the caller's ctx already bounds the whole call.
- Separate budgets for invalid output and transport errors: rejected, because the binding has only one
  `max_retries` knob.

## Known gap
WP-11 needs to recognise a context-window overflow ("a context-window overflow does not fail; the agent
synthesises from what it has"), but the ports can't tell it apart from other 400s. It surfaces as a
non-retried `ErrUnavailable` whose message carries the gateway's error body. Distinguishing it needs a typed
error in `domain` (a CCR), if WP-11 wants it.

## Spec lines involved
domain.ModelBinding (`TimeoutMS`, `MaxRetries`); port.LLM.Structured; AGENTS.md (prompts in templates;
adapters map errors to domain classes); Behavioural rules (Reflect).

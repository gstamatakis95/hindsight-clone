# 202 — Error classes of the scripted LLM fake (WP-02)

## Context
The card asks for an LLM fake that "can be told to fail, time out or return invalid JSON". The port says
the adapter validates Structured output against the schema and retries invalid output up to the
binding's max_retries, but not which domain error class it reports once retries run out. The spec's
retry rule ("deterministic errors (schema violation, integrity error) fail at once") doesn't settle it
either: a model returning bad JSON is not deterministic.

## Decision
- `InvalidJSON(slot, name)` scripts a body that isn't JSON. Structured returns an error wrapping
  domain.ErrUnavailable whose message includes the raw body: the reading is "the gateway could not
  produce valid output after its retries", a dependency failure, which the runner retries with backoff.
- `Timeout(slot, name)` returns `fake.ErrTimeout` at once (it never blocks); it wraps both
  domain.ErrUnavailable and context.DeadlineExceeded.
- An unscripted call returns `fake.ErrNotScripted`, so a missing script fails loudly.
- `Fail(slot, name, err)` returns any error the test chooses, so an engine can be tested against
  whatever class WP-03 settles on.

If WP-03 classifies exhausted schema retries differently, align `InvalidJSON` in a follow-up; engines
should test their handling of each class with `Fail`.

## Alternatives
- domain.ErrInvalidArgument: it maps to INVALID_ARGUMENT for synchronous RPCs, which blames the caller.
- Simulate the adapter's retries inside the fake: invisible to engines and more code.

## Spec lines involved
Ports (LLM.Structured), Jobs (Retries), WP-02 and WP-03 cards.

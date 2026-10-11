# 1604 — Reflect on the batch ports

This supersedes the text-marker bullet of decision 1101 and the `ChunkReader` stand-in.

## Context
Decision 1101 matched provider error text in the engine because the port could not type a context
overflow. CCR WP-11-1 added `domain.ErrContextOverflow`, which wraps `ErrUnavailable`, and WP-15 made the
gateway classify a 400 or 413 overflow reply as that error. CCR WP-11-2 added `Reader.GetChunks`.

## Decision
- **Overflow detection.** `isContextOverflow(err)` is `errors.Is(err, domain.ErrContextOverflow)`. The
  engine's own marker list is gone.
  - The gateway matches its markers against the full response body, capped at maxResponseBytes, not the
    512-byte message snippet.
  - Overflow handling itself (synthesise from what was retrieved) is unchanged from 1101.
- **Chunk reads.** `ChunkReader` is deleted. The expand tool reads chunks with `port.Reader.GetChunks`.
  `DefaultTools(rc, pages, r)` no longer takes a chunk reader.

## Alternatives
- Keeping the text match as a fallback: it would mask a gateway that fails to classify, and the gateway is
  the single place that sees the provider's raw reply.

## Spec lines involved
SPEC 1990 (context overflow synthesises), 1421 (expand tool); CCR batch #1 and #26.

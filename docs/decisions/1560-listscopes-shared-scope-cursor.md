# 1560 — ListScopes: the shared scope and the `after` cursor

Status: accepted (WP-15; proposed by the observation-admin porttest, accepted by the WP-15 owner and folded into 1500)

## Context

`Reader.ListScopes(b, after, limit)` returns scopes "starting strictly after after ("" = from the start)",
ordered by `ScopeKey.String()` in byte order. Decision 1500 pins the filter as `scope_key > after`.

The shared scope (no tags) has the key `""`. Read literally, `scope_key > ''` never matches it, so the
shared scope is never listed, although it is an observation_scopes row like any other and the port says
`""` means "from the start". Both first implementations (fake `listScopes`, Postgres query in
`queries/reads_scopes.sql`) read 1500 literally and drop the shared scope.

There is a second edge. When the shared scope is the last row of a page, the next cursor would be `""`,
which restarts the listing.

## Decision

- `after == ""` applies no filter, so the first page starts with the shared scope when the bank has one.
  Any other `after` filters with `scope_key > after` (byte order).
- To continue after a page whose last row is the shared scope, callers pass `after = "0"`. Every other
  key starts with a tag byte-length of at least 1, so `"0"` sorts after `""` and before every non-shared
  key. No special case is needed in the adapters.
- The observation-admin porttest (`list_scopes`) asserts both points: the shared scope is listed first from
  `""`, keyset paging with limit 1 and 2 returns every scope once (using `"0"` after the shared scope), and
  `ListScopes(b, "0", n)` skips only the shared scope.

## Alternatives

- Keep the literal `scope_key > after` and never list the shared scope. Rejected: it contradicts the port
  doc ("" = from the start; every observation_scopes row is listed). It would also hide the shared scope
  from ClearObservations and the scope admin views.
- Change the port to take `*string` or a separate "started" flag. Rejected: it is a frozen-contract change
  for an edge that a sentinel cursor solves.

## Spec lines

CCR WP-15-1 §2 (`Reader.ListScopes` doc), §0 (keyset paging lives in engines, byte order); decision 1500
(ListScopes).

# 1102 — Reflect evidence guard, citations and directive selection

## Context
SPEC: reflect "must retrieve before done; drops citations it never retrieved"; directives are "selected by
tags (apply_all, extra_directives)"; a request can never remove the bank's stored directives.

## Decision
- **Evidence guard.** With `require_evidence` (nil = true) a `done` is rejected (tool error back to the model,
  costing an iteration) until a retrieval tool has run successfully. A retrieval that found nothing counts:
  Hindsight requires a retrieved id, but on an empty bank that burns every iteration, and SPEC says "a
  retrieval that returns nothing is not a failure". `expand` does not count (it reads ids found earlier), and
  read_mental_models counts only when it actually read a page. The closing `done` turn is guarded as well
  (decision 1100: one forced retrieval turn, else synthesis), unlike Hindsight's last iteration.
- **What is retrievable.** recall and search_observations register the units they return; search_mental_models
  (snippet hits included, as in Hindsight) and read_mental_models register the pages they return. `expand`
  registers nothing, so its source facts can't be cited unless a search returned them (Hindsight).
- **Citations.** `done`'s `memory_ids`, `observation_ids` and `mental_model_ids` are kept only when retrieved,
  in citation order, de-duplicated. `BasedOn.Memories` holds the cited units (facts and observations),
  `BasedOn.MentalModelIDs` the cited pages.
- **Answers without a done call** (closing turn ignored, synthesis after max_iterations or an overflow):
  `BasedOn` is everything retrieved, sorted by id, since the synthesis saw every tool output. (Hindsight does
  the same when the used set is empty.)
- **include.facts = false** leaves `BasedOn.Memories` and `MentalModelIDs` empty; `DirectiveIDs` stay.
- **Directive selection** (Hindsight's `list_directives` with `isolation_mode`): read active directives with
  `tx.Directives().List` in a short read-only transaction. `apply_all_directives` applies every active one.
  Otherwise untagged directives always apply; a tagged one applies only when the request has a tag filter
  and its tags satisfy it (flat tags with the request's `tags_match`, and every tag group). An untagged
  request gets only untagged directives. `extra_directives` are appended after the stored ones and never
  replace or filter them; they are applied whatever their `active` flag (proto bool defaults to false).
  `BasedOn.DirectiveIDs` / `Trace.DirectiveIDs` list the applied stored directives only; extras never appear
  there, even when the caller gave them an id.
- The directive-aware `done` variant is offered when some applied directive has rules (the reflect_system
  predicate); `directive_compliance` is not checked (Hindsight doesn't either).

## Alternatives
- Consuming `bank.DirectiveSelector` (WP-13): its `Select(tags []string, all bool)` can't carry `tags_match`
  or tag groups, and reflect owns the selection per its card; reading through `port.Tx` needs no new port.

## Spec lines involved
Behavioural rules > Reflect; Per-request overrides (extra directives); Directive (domain); BasedOn,
ReflectInclude.facts; Engines > Reflector.

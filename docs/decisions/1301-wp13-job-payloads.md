# 1301 — Job payloads, serial keys and dedupe for the jobs WP-13 enqueues

## Context
Every job's payload is a kind-specific JSON object (port.Job.Payload) and the handler that decodes it is a
thin wrapper (WP-14) around the engine. SPEC "Jobs" fixes the serial keys and dedupe policies but not the
payloads. WP-13 enqueues rederive_memory, retag_document, graph_maintenance, delete_bank and consolidate,
and runs rederive_memory, retag_document, graph_maintenance and delete_bank.

## Decision
| Kind | Payload | Serial key | Dedupe | Overrides |
|---|---|---|---|---|
| rederive_memory | `{"unit_id": "...", "entities": [...]?, "resolve_entities": bool?}` (`curate.RederivePayload`; `entities` absent = re-resolve the unit's current names, `resolve_entities` absent = true) | `doc:{unit's document}` | None | the UpdateMemory request overrides |
| retag_document | `{"document_id": "..."}` (`docs.RetagPayload`). The tags are **not** in the payload: the API commits them on the document row and the job re-reads them, so a later retag that joins a pending one is never lost. | `doc:{document_id}` | Pending | none |
| graph_maintenance | `{}` | `graph` | PendingOrRunning when a removal triggers it; Pending for the job's own continuation | none |
| delete_bank | `{}` | `bank` | PendingOrRunning | none |
| consolidate (automatic: invalidate, retag, rederive, clears) | `{}` = every scope, bank defaults (decision 502) | `consolidate` | Pending | none |
| consolidate (manual Consolidate with scopes or overrides) | `{"scopes": [{"tags": [...]}, ...]}` (`curate.ConsolidatePayload`; absent = every scope) | `consolidate` | None (SPEC: overrides or scopes are never merged) | the request overrides |

Refresh (refresh_mental_model) and retain (retain_document) payloads are owned by WP-12 and WP-08, so WP-13
never builds them (decision 1303). WP-10's consolidate handler must accept the manual payload above; if
WP-10 chose another shape, WP-14 aligns `curate.ConsolidatePayload` with it (one struct).

## Alternatives
- Putting the new tags in the retag payload: rejected, a joined DedupePending enqueue would drop the
  newer tag set.
- A shared payload package: rejected for now; each engine owns the payload of the jobs it runs, and WP-14
  wires handlers to them.

## Spec lines involved
SPEC "Jobs" table (serial keys, dedupe), "Curation" table, decision 502 (empty consolidate payload).

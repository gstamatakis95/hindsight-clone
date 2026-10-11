# 1205 — Mental-model admin types, folders, clear, history and page search

## Context
The server declares `MentalModelAdmin` with its own request types (`ListMentalModelsRequest`,
`SpecPatch`, `RefreshRequest`, `HistoryRequest`, ...). The server imports `mentalmodel`, so the engine
can't import the server to name them, and Go interface satisfaction needs identical named types.
SearchPages must rank whole pages "by fused full-text and semantic scores, no rerank", and the ports have
no page queries.

## Decision
- `mentalmodel.Admin` has the same methods with package-local mirror types whose fields match the
  server's one for one (`ListRequest`, `ModelPage`, `HistoryRequest`, `HistoryPage`, `SpecPatch`, and the
  engine's `RefreshRequest`). WP-14 adapts with plain conversions for `ListRequest`, `ModelPage`,
  `HistoryRequest`, `HistoryPage` and `SpecPatch`; `RefreshRequest` needs a field copy (the engine's adds `Manual` and `RunAt`, which Admin
  forces / ignores). Simpler: the server redeclares those six types as aliases of the engine's, and `Admin`
  then satisfies `server.MentalModelAdmin` directly.
- **Create**: an empty id gets a UUID; ids must be lowercase alphanumerics and hyphens; cron and
  `refresh_after_consolidation` are exclusive; the cron expression must parse; tags are normalized and the
  folder path is trimmed of spaces and slashes. The bank is ensured (a write creates it).
- **Update** runs the pure patch in the write transaction; the id can't change; a changed source query or
  read scope resets `last_seq_seen` (decision 1202). "Changed" compares tags and fact types as sets and tag
  groups by value, nil equal to empty, so a no-op edit doesn't force a full refresh. Update and Clear take
  `tx.Seq` before reading the model: the bank row lock serializes every read-modify-write of a model row
  (runner write, failure reconcile, Update, Clear). Delete and DeleteFolder only delete, so they don't.
- **Clear** keeps the cleared content as a HISTORY_KIND_VERSION entry, then empties content, structured
  output, sources, embedding, trace and the watermark, so the next refresh is full and nothing is lost.
- **History** pages with an offset token over `MentalModelRepo.History` (newest first).
- **Folders** are the `folder_path` prefix tree: listing and DeleteFolder match the path itself and
  everything under `path/`; DeleteFolder with an empty path is INVALID_ARGUMENT (no accidental
  delete-all). The `mental_model_folders` table is not used in v1.
- **is_stale** is computed through the ChangeDetector on reads at DETAIL_FULL only (Get defaults to full;
  List defaults to metadata); at metadata and content detail it is false, so listing a folder runs no
  per-model scope queries.
- **Page search** lists the folder's pages (ModelLister) and scores them in Go: term overlap (name hits
  doubled, length damped) and cosine similarity of the stored page embedding to the query embedding, fused
  by RRF (k = 60). A bank holds tens to hundreds of pages, so this avoids new search queries in the ports.
  The runner stores the embedding of `name + content` on every version.

## Alternatives
- Implementing the server interface directly: impossible without an import cycle.
- BM25 / HNSW page queries in Postgres: a bigger CCR for no v1 need; revisit if banks grow large.

## Spec lines involved
gRPC surface (MentalModelService, Detail, SearchPages, DeleteFolder), decision 601.

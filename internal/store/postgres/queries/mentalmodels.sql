-- MentalModelGet returns one mental model of a bank.
-- name: MentalModelGet :one
SELECT * FROM mental_models WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND id = @id;

-- MentalModelCreate inserts a new mental model.
-- name: MentalModelCreate :exec
INSERT INTO mental_models (
    tenant_id, bank_id, id, name, source_query, folder_path, tags, max_tokens, trigger, overrides, content_md,
    structured_output_json, version, last_refreshed_at, last_seq_seen, last_refresh_failed_at,
    auto_refresh_paused, source_ids, last_trace, embedding, refresh_cron
) VALUES (
    @tenant_id, @bank_id, @id, @name, @source_query, @folder_path, @tags, @max_tokens, @trigger, @overrides,
    @content_md, @structured_output_json, @version, @last_refreshed_at, @last_seq_seen, @last_refresh_failed_at,
    @auto_refresh_paused, @source_ids, @last_trace, @embedding, @refresh_cron
);

-- MentalModelUpdate replaces a mental model's spec, content and refresh state.
-- name: MentalModelUpdate :execrows
UPDATE mental_models SET
    name = @name, source_query = @source_query, folder_path = @folder_path, tags = @tags,
    max_tokens = @max_tokens, trigger = @trigger, overrides = @overrides, content_md = @content_md,
    structured_output_json = @structured_output_json, version = @version, last_refreshed_at = @last_refreshed_at,
    last_seq_seen = @last_seq_seen, last_refresh_failed_at = @last_refresh_failed_at,
    auto_refresh_paused = @auto_refresh_paused, source_ids = @source_ids, last_trace = @last_trace,
    embedding = @embedding, refresh_cron = @refresh_cron, updated_at = now()
WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND id = @id;

-- MentalModelDelete removes a mental model; its history cascades.
-- name: MentalModelDelete :execrows
DELETE FROM mental_models WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND id = @id;

-- MentalModelAppendHistory adds one history entry; a NULL changed_at takes the database's now().
-- name: MentalModelAppendHistory :exec
INSERT INTO mental_model_history (
    tenant_id, bank_id, mental_model_id, kind, version, previous_content, outcome, failure_reason, error_message,
    changed_at
) VALUES (
    @tenant_id, @bank_id, @mental_model_id, @kind, @version, @previous_content, @outcome, @failure_reason,
    @error_message, COALESCE(sqlc.narg(changed_at)::timestamptz, now())
);

-- MentalModelTrimHistory keeps only the newest keep entries of one kind of a model.
-- name: MentalModelTrimHistory :exec
DELETE FROM mental_model_history h
WHERE h.tenant_id = @tenant_id AND h.bank_id = @bank_id AND h.mental_model_id = @mental_model_id AND h.kind = @kind
  AND h.id NOT IN (
    SELECT k.id FROM mental_model_history k
    WHERE k.tenant_id = @tenant_id AND k.bank_id = @bank_id AND k.mental_model_id = @mental_model_id
      AND k.kind = @kind
    ORDER BY k.changed_at DESC, k.id DESC
    LIMIT @keep::int
  );

-- MentalModelHistory returns a model's history entries (one kind, or all when kind is ''), newest first;
-- lim <= 0 returns all.
-- name: MentalModelHistory :many
SELECT * FROM mental_model_history
WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND mental_model_id = @mental_model_id
  AND (@kind::text = '' OR kind = @kind::text)
ORDER BY changed_at DESC, id DESC
LIMIT CASE WHEN @lim::int > 0 THEN @lim::int END;
